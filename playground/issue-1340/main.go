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

// Package main is a Docker Compose reproduction for issue #1340: a node that
// is killed abruptly must be reported as gone within seconds, and the work it
// hosted must come back on a survivor.
//
// Three containers run the same binary and join one cluster through DNS
// discovery. The oldest node hosts a relocatable singleton; the two survivors
// keep asking the singleton and thirty grains every 200ms and record, with
// wall-clock timestamps, when those requests started failing, when NodeLeft
// arrived, and when the requests started succeeding again.
//
// The cluster configuration keeps the framework defaults the issue was
// reported against: the replica count, the state sync interval, the balancer
// interval, the network profile and the convergence timeout are all left
// untouched, so the measurement describes what a stock cluster does.
//
// The HTTP surface (see server.go) makes the reproduction self-validating:
//
//	GET /health  answers once the node joined the cluster
//	GET /ready   answers once the driver saw a streak of successful requests
//	GET /report  the recorded events and the measured timeline, as JSON
package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	goakt "github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery/dnssd"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
)

const (
	// actorSystemName identifies the actor system every node joins.
	actorSystemName = "issue1340"

	// singletonName is the name of the relocatable cluster singleton the
	// survivors keep asking for. It runs on the oldest node, which is the node
	// the demo kills.
	singletonName = "matchmaker"

	// clusterSize is the number of nodes the compose file starts. The
	// singleton host waits for the whole cluster before spawning the
	// singleton, and the driver waits for it before sending requests.
	clusterSize = 3

	// environment variables read by every node. The compose file sets them
	// all; the defaults below only keep a bare "go run" usable.
	envNodeName       = "NODE_NAME"
	envDomainName     = "DOMAIN_NAME"
	envDiscoveryPort  = "DISCOVERY_PORT"
	envPeersPort      = "PEERS_PORT"
	envRemotingPort   = "REMOTING_PORT"
	envHTTPPort       = "HTTP_PORT"
	envSpawnSingleton = "SPAWN_SINGLETON"
	envLogLevel       = "LOG_LEVEL"

	// defaults applied when the environment variables above are not set.
	defaultNodeName      = "node"
	defaultDomainName    = "nodes.issue1340.local"
	defaultDiscoveryPort = 3322
	defaultPeersPort     = 3320
	defaultRemotingPort  = 50052
	defaultHTTPPort      = 8080

	// bootstrapTimeout bounds the cluster bootstrap of a single node.
	bootstrapTimeout = 10 * time.Second

	// membershipPollInterval and membershipTimeout bound the wait for the
	// whole cluster to be visible from this node.
	membershipPollInterval = 500 * time.Millisecond
	membershipTimeout      = 3 * time.Minute

	// membershipReadTimeout bounds one membership lookup.
	membershipReadTimeout = 2 * time.Second

	// shutdownTimeout bounds the graceful shutdown on SIGTERM.
	shutdownTimeout = 30 * time.Second
)

// main starts one node of the reproduction cluster: the actor system, the
// event recorder, the HTTP surface, and either the singleton (on the node the
// demo kills) or the request driver (on the survivors).
func main() {
	ctx := context.Background()

	nodeName := envOr(envNodeName, defaultNodeName)
	logger := log.NewZap(logLevel(envOr(envLogLevel, "")), os.Stdout)

	hostname, err := os.Hostname()
	if err != nil {
		logger.Fatal("failed to read the host name: ", err)
	}

	host, err := localAddress(hostname)
	if err != nil {
		logger.Fatal("failed to resolve the node address: ", err)
	}

	discoveryPort := envIntOr(envDiscoveryPort, defaultDiscoveryPort)
	peersPort := envIntOr(envPeersPort, defaultPeersPort)
	remotingPort := envIntOr(envRemotingPort, defaultRemotingPort)
	httpPort := envIntOr(envHTTPPort, defaultHTTPPort)

	runScenario, err := newScenario()
	if err != nil {
		logger.Fatal(err)
	}

	provider := dnssd.NewDiscovery(&dnssd.Config{
		DomainName: envOr(envDomainName, defaultDomainName),
	})

	// nothing here tunes failure detection, replication or convergence: the
	// issue is about what the defaults deliver
	clusterConfig := goakt.NewClusterConfig().
		WithDiscovery(provider).
		WithDiscoveryPort(discoveryPort).
		WithPeersPort(peersPort).
		WithKinds(new(Matchmaker)).
		WithGrains(new(Worker)).
		WithBootstrapTimeout(bootstrapTimeout).
		WithMinimumPeersQuorum(1)

	// the scenario adds the convergence timeout and the network profile only
	// when the run asks for them, so the default scenario measures the
	// untouched defaults
	runScenario.apply(clusterConfig)

	actorSystem, err := goakt.NewActorSystem(
		actorSystemName,
		goakt.WithLogger(logger),
		goakt.WithRemote(remote.NewConfig(host, remotingPort)),
		goakt.WithCluster(clusterConfig),
	)
	if err != nil {
		logger.Fatal(err)
	}

	recorder := newEventRecorder(nodeName)

	var runner *driver
	if !envBool(envSpawnSingleton) {
		runner = newDriver(actorSystem, nodeName, logger)
	}

	server := newHTTPServer(actorSystem, recorder, runner, runScenario, httpPort)

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

	logger.Infof("%s is up on %s (http=%d, remoting=%d, peers=%d, discovery=%d)", nodeName, host, httpPort, remotingPort, peersPort, discoveryPort)
	logger.Infof("cluster settings: convergence timeout=%s, network profile=%s", runScenario.convergenceTimeoutLabel(), runScenario.networkProfileLabel())

	go start(ctx, actorSystem, runner, logger)

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

// start waits for the whole cluster to be visible, then spawns the singleton
// on the node that hosts it, or runs the request driver on the survivors.
func start(ctx context.Context, actorSystem goakt.ActorSystem, runner *driver, logger log.Logger) {
	if err := awaitCluster(ctx, actorSystem, logger); err != nil {
		// the scenario needs the whole cluster; a node that never sees it
		// would silently report an outage that means nothing
		logger.Errorf("the cluster did not reach %d nodes: %v", clusterSize, err)
		os.Exit(1)
	}

	if runner != nil {
		logger.Infof("cluster is complete: sending requests to %s and to %d grains every %s", singletonName, grainCount, probeInterval)
		runner.run(ctx)
		return
	}

	pid, err := actorSystem.SpawnSingleton(ctx, singletonName, new(Matchmaker))
	if err != nil {
		logger.Errorf("failed to spawn the singleton: %v", err)
		os.Exit(1)
	}

	logger.Infof("singleton %s spawned on this node", pid.Name())
}

// awaitCluster blocks until this node sees every other node of the cluster, so
// the singleton is spawned and the requests start on a complete cluster.
func awaitCluster(ctx context.Context, actorSystem goakt.ActorSystem, logger log.Logger) error {
	deadline := time.Now().Add(membershipTimeout)

	for time.Now().Before(deadline) {
		peers, err := actorSystem.Peers(ctx, membershipReadTimeout)
		if err != nil {
			logger.Warnf("membership lookup failed: %v", err)
		}

		if len(peers)+1 >= clusterSize {
			return nil
		}

		time.Sleep(membershipPollInterval)
	}

	return context.DeadlineExceeded
}

// localAddress returns the address this node advertises to its peers. The
// container's own name resolves to its address on the compose network through
// /etc/hosts, which does not depend on the cluster DNS server.
func localAddress(hostname string) (string, error) {
	addresses, err := net.LookupHost(hostname)
	if err != nil {
		return "", err
	}

	for _, address := range addresses {
		if ip := net.ParseIP(address); ip != nil && ip.To4() != nil {
			return address, nil
		}
	}

	return "", net.UnknownNetworkError(hostname)
}

// logLevel maps the LOG_LEVEL value to a logger level, defaulting to info.
func logLevel(level string) log.Level {
	switch strings.ToLower(level) {
	case "debug":
		return log.DebugLevel
	case "warn":
		return log.WarningLevel
	case "error":
		return log.ErrorLevel
	default:
		return log.InfoLevel
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

// envIntOr returns the integer value of the environment variable key, or
// fallback when it is not set or not a number.
func envIntOr(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}

	return parsed
}

// envBool reports whether the environment variable key is set to true.
func envBool(key string) bool {
	parsed, err := strconv.ParseBool(os.Getenv(key))
	if err != nil {
		return false
	}

	return parsed
}
