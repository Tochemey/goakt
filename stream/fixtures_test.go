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

package stream_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/actor"
	natsdisc "github.com/tochemey/goakt/v4/discovery/nats"
	dynaport "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/stream"
)

// newTestSystem creates and starts a fresh ActorSystem for a test,
// registering a cleanup to stop it when the test ends. Each test gets a
// unique system name so multiple tests in the same process do not race
// on the system-name registry during Start/Stop.
func newTestSystem(t *testing.T) actor.ActorSystem {
	t.Helper()
	name := fmt.Sprintf("stream-test-%d", time.Now().UnixNano())
	sys, err := actor.NewActorSystem(name, actor.WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(context.Background()))
	t.Cleanup(func() { _ = sys.Stop(context.Background()) })
	return sys
}

// dummyActorA / dummyActorB satisfy the cluster's WithKinds requirement
// (at least two kinds must be registered). They never receive messages —
// only their type identities matter at registration time.
type dummyActorA struct{}

func (*dummyActorA) PreStart(*actor.Context) error { return nil }
func (*dummyActorA) Receive(*actor.ReceiveContext) {}
func (*dummyActorA) PostStop(*actor.Context) error { return nil }

type dummyActorB struct{}

func (*dummyActorB) PreStart(*actor.Context) error { return nil }
func (*dummyActorB) Receive(*actor.ReceiveContext) {}
func (*dummyActorB) PostStop(*actor.Context) error { return nil }

// startNatsServer starts an embedded NATS server on a random loopback port.
// The cluster discovery providers connect to it for peer announcement.
func startNatsServer(t *testing.T) *natsserver.Server {
	t.Helper()
	srv, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1",
		Port: -1, // random
	})
	require.NoError(t, err)

	ready := make(chan struct{})
	go func() {
		close(ready)
		srv.Start()
	}()
	<-ready

	require.True(t, srv.ReadyForConnections(2*time.Second), "embedded nats failed to start")
	t.Cleanup(srv.Shutdown)
	return srv
}

// newClusterPair starts two cluster-enabled actor systems on dynamic ports
// and returns them once they have settled. Discovery uses an embedded NATS
// server (reliable on every CI host), and the stream wire protocol is
// registered automatically.
//
// extraOpts are appended to each system's remote.NewConfig — use them to
// register user element types via remote.WithSerializables.
func newClusterPair(t *testing.T, extraOpts ...remote.Option) (actor.ActorSystem, actor.ActorSystem) {
	t.Helper()
	ctx := context.Background()

	const host = "127.0.0.1"
	natsSrv := startNatsServer(t)
	natsURL := fmt.Sprintf("nats://%s", natsSrv.Addr().String())
	// Per-test cluster identity prevents leftover olric state from one
	// test influencing the next when several tests in the same process
	// build separate cluster pairs.
	natsSubject := fmt.Sprintf("stream-test-%d", time.Now().UnixNano())

	mkNode := func(name string) actor.ActorSystem {
		ports := dynaport.Get(3)
		discoveryPort, remotingPort, peersPort := ports[0], ports[1], ports[2]

		discoCfg := &natsdisc.Config{
			NatsServer:    natsURL,
			NatsSubject:   natsSubject,
			Host:          host,
			DiscoveryPort: discoveryPort,
		}
		provider := natsdisc.NewDiscovery(discoCfg, natsdisc.WithLogger(log.DiscardLogger))

		clusterCfg := actor.NewClusterConfig().
			WithKinds(new(dummyActorA), new(dummyActorB)).
			WithPartitionCount(7).
			WithReplicaCount(1).
			WithPeersPort(peersPort).
			WithMinimumPeersQuorum(1).
			WithDiscoveryPort(discoveryPort).
			WithBootstrapTimeout(10 * time.Second).
			WithClusterStateSyncInterval(300 * time.Millisecond).
			WithClusterBalancerInterval(100 * time.Millisecond).
			WithDiscovery(provider)

		remoteOpts := append([]remote.Option{stream.RemoteOptions()}, extraOpts...)
		remoteCfg := remote.NewConfig(host, remotingPort, remoteOpts...)

		sys, err := actor.NewActorSystem(name,
			actor.WithLogger(log.DiscardLogger),
			actor.WithCluster(clusterCfg),
			actor.WithRemote(remoteCfg),
			actor.WithShutdownTimeout(30*time.Second),
		)
		require.NoError(t, err)
		require.NoError(t, sys.Start(ctx))
		return sys
	}

	// Both nodes share the actor system name — they form one cluster.
	// The name is randomized per cluster pair so leftover in-process state
	// from a prior pair (olric storage keyed by cluster name, etc.) cannot
	// influence a fresh pair built later in the same test binary.
	systemName := fmt.Sprintf("stream-test-cluster-%d", time.Now().UnixNano())
	sysA := mkNode(systemName)
	sysB := mkNode(systemName)

	t.Cleanup(func() {
		_ = sysA.Stop(context.Background())
		_ = sysB.Stop(context.Background())
	})

	// Wait for the two nodes to discover each other before returning. A fixed
	// pause is unreliable when the test binary is loaded (full package run):
	// olric peer-list propagation can lag well past one second. Polling Peers()
	// gives a deterministic readiness signal so cross-node lookups inside the
	// test body don't race the cluster.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		pa, _ := sysA.Peers(ctx, 500*time.Millisecond)
		pb, _ := sysB.Peers(ctx, 500*time.Millisecond)
		if len(pa) >= 1 && len(pb) >= 1 {
			break
		}
		pause.For(100 * time.Millisecond)
	}
	return sysA, sysB
}
