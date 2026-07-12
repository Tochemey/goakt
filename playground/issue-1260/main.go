// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

// Package main is a Kubernetes reproduction for issue #1260: registry-derived
// crash recovery must be reliable on the default cluster configuration.
//
// The cluster configuration below deliberately sets no replica count and no
// quorums, so every node runs whatever NewClusterConfig defaults to. Before
// the fix the default was a replica count of 1: crashing a pod (kill -9, no
// graceful shutdown snapshot) silently lost the registry records whose
// partitions the crashed pod owned, so a share of its actors never came back
// and nothing reported the loss. With the fix the default is a replica count
// of 2: every registry partition has a backup, the surviving copy is promoted
// before the recovery scan runs, and the leader announces the reconstruction
// with a RelocationDerived event.
//
// The HTTP surface (see server.go) makes the reproduction self-validating:
//
//	POST /spawn?count=N  spawns N relocatable workers spread round-robin
//	GET  /verify?count=N reports how many workers exist in the cluster registry
//	GET  /events         dumps the observed cluster and relocation events
//	GET  /health         readiness and liveness probe
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	goakt "github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery/kubernetes"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
)

const (
	// actorSystemName identifies the actor system every pod joins.
	actorSystemName = "issue1260"

	// discoveryPortName, remotingPortName and peersPortName are the container
	// port names the kubernetes discovery provider resolves on each pod. They
	// must match the port names declared in deploy/k8s.yaml.
	discoveryPortName = "discovery-port"
	remotingPortName  = "remoting-port"
	peersPortName     = "peers-port"

	// podLabelName is the pod label the discovery provider selects on. It must
	// match the pod template labels declared in deploy/k8s.yaml.
	podLabelName = "app.kubernetes.io/name"

	// envNamespace, envServiceName and the port variables are the environment
	// variables injected by deploy/k8s.yaml.
	envNamespace     = "NAMESPACE"
	envServiceName   = "SERVICE_NAME"
	envHTTPPort      = "HTTP_PORT"
	envRemotingPort  = "REMOTING_PORT"
	envDiscoveryPort = "DISCOVERY_PORT"
	envPeersPort     = "PEERS_PORT"
	envLogLevel      = "LOG_LEVEL"
	logLevelDebug    = "debug"

	// defaults applied when the environment variables above are not set.
	defaultNamespace     = "default"
	defaultServiceName   = "issue1260"
	defaultHTTPPort      = 8080
	defaultRemotingPort  = 50052
	defaultDiscoveryPort = 3322
	defaultPeersPort     = 3320

	// partitionCount keeps the registry spread over enough partitions that a
	// crashed pod necessarily owns a share of them.
	partitionCount = 19

	// shutdownTimeout bounds the graceful shutdown on SIGTERM.
	shutdownTimeout = 30 * time.Second
)

func main() {
	ctx := context.Background()

	logLevel := log.InfoLevel
	if envOr(envLogLevel, "") == logLevelDebug {
		// debug also raises the embedded olric engine's verbosity, which is
		// what the crash-recovery investigation needs
		logLevel = log.DebugLevel
	}
	logger := log.NewZap(logLevel, os.Stdout)

	namespace := envOr(envNamespace, defaultNamespace)
	serviceName := envOr(envServiceName, defaultServiceName)
	httpPort := envIntOr(envHTTPPort, defaultHTTPPort)
	remotingPort := envIntOr(envRemotingPort, defaultRemotingPort)
	discoveryPort := envIntOr(envDiscoveryPort, defaultDiscoveryPort)
	peersPort := envIntOr(envPeersPort, defaultPeersPort)

	provider := kubernetes.NewDiscovery(&kubernetes.Config{
		Namespace:         namespace,
		DiscoveryPortName: discoveryPortName,
		RemotingPortName:  remotingPortName,
		PeersPortName:     peersPortName,
		PodLabels: map[string]string{
			podLabelName: serviceName,
		},
	})

	hostname, err := os.Hostname()
	if err != nil {
		logger.Fatal("failed to get the host name: ", err)
	}
	host := fmt.Sprintf("%s.%s.%s.svc.cluster.local", hostname, serviceName, namespace)

	// the whole point of the reproduction: no WithReplicaCount and no quorums;
	// the node must be safe on the untouched defaults
	clusterConfig := goakt.NewClusterConfig().
		WithDiscovery(provider).
		WithKinds(new(Worker)).
		WithPartitionCount(partitionCount).
		WithDiscoveryPort(discoveryPort).
		WithPeersPort(peersPort)

	actorSystem, err := goakt.NewActorSystem(
		actorSystemName,
		goakt.WithLogger(logger),
		goakt.WithRemote(remote.NewConfig(host, remotingPort)),
		goakt.WithCluster(clusterConfig),
	)
	if err != nil {
		logger.Fatal(err)
	}

	// The HTTP server comes up before the actor system so the liveness probe
	// answers while the cluster bootstraps: killing a pod mid-bootstrap only
	// prolongs cluster churn. Readiness (/ready) still gates on the actor
	// system running, which keeps OrderedReady StatefulSets sequential.
	recorder := newEventRecorder()
	server := newHTTPServer(actorSystem, recorder, httpPort, logger)

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal(err)
		}
	}()

	if err := actorSystem.Start(ctx); err != nil {
		logger.Fatal(err)
	}

	consumer, err := actorSystem.Subscribe()
	if err != nil {
		logger.Fatal(err)
	}

	go recorder.consume(consumer, logger)

	logger.Infof("issue-1260 node %s is up (http=%d)", hostname, httpPort)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs

	shutdownCtx, cancel := context.WithTimeout(ctx, shutdownTimeout)
	defer cancel()

	_ = server.Shutdown(shutdownCtx)

	if err := actorSystem.Stop(shutdownCtx); err != nil {
		logger.Error(err)
	}
}

// envOr returns the value of the environment variable key, or fallback when it
// is not set.
func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
