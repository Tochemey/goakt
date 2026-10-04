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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/actor"
	natsdisc "github.com/tochemey/goakt/v4/discovery/nats"
	dynaport "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/stream"
)

const (
	// slowDownstreamTotal is the length of the sources of the backpressure
	// tests: long enough that a stream that ignores demand pulls far past
	// heldBackLimit.
	slowDownstreamTotal = 20_000
	// heldBackLimit is the most elements a source held back by the merged
	// stream may emit: one window per stage of each open substream, the
	// merged window and the elements waiting in the splitter.
	heldBackLimit = 5_000
	// heldBackPeriod is how long a test watches a held back source.
	heldBackPeriod = time.Second
	// demandWindow is the demand a stage requests at once
	// (defaultInitialDemand), so the most elements a substream runs ahead of
	// what the merged stream has taken.
	demandWindow = 224
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

// requireNoStreamActorsLeft waits until no stream actor (coordinator or
// stage) is left running in sys.
func requireNoStreamActorsLeft(t *testing.T, sys actor.ActorSystem) {
	t.Helper()
	require.Eventually(t, func() bool {
		pids, err := sys.Actors(context.Background(), time.Second)
		if err != nil {
			return false
		}

		for _, pid := range pids {
			if strings.HasPrefix(pid.Name(), "stream-") {
				return false
			}
		}

		return true
	}, 5*time.Second, 10*time.Millisecond, "stream actors still running")
}

// countingSource returns a source of the ints 0 to n-1 that adds every
// element it emits to pulled, so a test can tell how far the source has been
// pulled.
func countingSource(n int, pulled *atomic.Int64) stream.Source[int] {
	return stream.Via(stream.Range(0, int64(n)), stream.Map(func(v int64) int {
		pulled.Add(1)
		return int(v)
	}))
}

// gatedSink stands for a downstream that stops asking for elements: it
// records the elements it receives, in order, and blocks on the first one
// until release is called.
type gatedSink[T any] struct {
	mu    sync.Mutex
	items []T
	// first is closed when the first element arrives.
	first chan types.Unit
	// gate is closed by release; the first element waits on it.
	gate chan types.Unit
}

// newGatedSink returns a gatedSink that has received nothing.
func newGatedSink[T any]() *gatedSink[T] {
	return &gatedSink[T]{first: make(chan types.Unit), gate: make(chan types.Unit)}
}

// sink returns the stream sink backed by x.
func (x *gatedSink[T]) sink() stream.Sink[T] {
	return stream.ForEach(func(v T) {
		x.mu.Lock()
		x.items = append(x.items, v)
		first := len(x.items) == 1
		x.mu.Unlock()

		if first {
			close(x.first)
			<-x.gate
		}
	})
}

// waitFirst waits until the first element has arrived.
func (x *gatedSink[T]) waitFirst(t *testing.T) {
	t.Helper()
	select {
	case <-x.first:
	case <-time.After(5 * time.Second):
		t.Fatal("the sink received no element")
	}
}

// release unblocks the sink, which then takes every element as it comes.
func (x *gatedSink[T]) release() {
	close(x.gate)
}

// received returns a copy of the elements received so far, in order.
func (x *gatedSink[T]) received() []T {
	x.mu.Lock()
	items := append([]T(nil), x.items...)
	x.mu.Unlock()
	return items
}

// requireOrderedPerKey verifies that the elements of every key, as keyOf maps
// them, arrived in increasing order.
func requireOrderedPerKey(t *testing.T, items []int, keyOf func(int) int) {
	t.Helper()
	last := make(map[int]int)
	for _, item := range items {
		key := keyOf(item)
		if previous, seen := last[key]; seen {
			require.Greater(t, item, previous, "key %d out of order", key)
		}

		last[key] = item
	}
}

// waitStream waits for handle to end and fails the test after timeout.
func waitStream(t *testing.T, handle stream.StreamHandle, timeout time.Duration) {
	t.Helper()
	select {
	case <-handle.Done():
	case <-time.After(timeout):
		t.Fatal("stream did not end in time")
	}
}

// newGate returns a channel that blocks its readers until open is called.
// The test's cleanup opens it as well, so a stage blocked on it does not
// hold a dispatcher worker while the actor system stops after a failure.
func newGate(t *testing.T) (<-chan types.Unit, func()) {
	t.Helper()
	gate := make(chan types.Unit)
	var once sync.Once
	open := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(open)
	return gate, open
}

// requireHeldBack verifies that the source counted by pulled stays below
// heldBackLimit for heldBackPeriod.
func requireHeldBack(t *testing.T, pulled *atomic.Int64) {
	t.Helper()
	require.Never(t, func() bool { return pulled.Load() > heldBackLimit }, heldBackPeriod, 10*time.Millisecond,
		"the source was not held back")
}

// holdKey returns a per-substream flow that blocks on the elements of key
// until gate is closed, which makes that substream slower than its feed.
func holdKey(gate <-chan types.Unit, key int) stream.Flow[int, int] {
	return stream.Map(func(n int) int {
		if n%4 == key {
			<-gate
		}

		return n
	})
}

// failKeyOnRelease returns a per-substream flow that blocks on the first
// element of key until gate is closed and then fails on it.
func failKeyOnRelease(gate <-chan types.Unit, key int) stream.Flow[int, int] {
	return stream.TryMap(func(n int) (int, error) {
		if n%4 == key {
			<-gate
			return 0, errInjected
		}

		return n, nil
	})
}
