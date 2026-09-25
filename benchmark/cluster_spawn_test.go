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

package benchmark

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery/static"
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
)

const (
	// clusterSpawnNodeCount is the size of the cluster the spawns are measured
	// on. With three nodes most registry keys are owned by a node other than
	// the spawning one, so a spawn pays the network round trips of a real
	// cluster rather than local map writes.
	clusterSpawnNodeCount = 3

	// clusterSpawnPortsPerNode is the number of ports one node binds:
	// discovery, peers and remoting.
	clusterSpawnPortsPerNode = 3

	// clusterSpawnHost is the loopback address every node binds to.
	clusterSpawnHost = "127.0.0.1"

	// clusterSpawnSystemName is the actor system name shared by the nodes.
	clusterSpawnSystemName = "bench-cluster"

	// clusterSpawnBootstrapTimeout bounds how long a node waits for its peers
	// while joining. The nodes start together, so a short wait is enough.
	clusterSpawnBootstrapTimeout = time.Second

	// clusterSpawnPeersTimeout bounds the wait for every node to see the
	// others before the measurement starts.
	clusterSpawnPeersTimeout = 30 * time.Second

	// clusterSpawnPeersQueryTimeout bounds one peers query during that wait.
	clusterSpawnPeersQueryTimeout = time.Second

	// clusterSpawnPeersPoll is the interval between two peers queries.
	clusterSpawnPeersPoll = 100 * time.Millisecond

	// clusterSpawnActorNamePrefix prefixes the name of every spawned actor.
	clusterSpawnActorNamePrefix = "bench-cluster-actor-"
)

// benchCluster is a cluster of nodes started in one process for a benchmark.
// The first node spawns; the others only hold their share of the registry.
type benchCluster struct {
	nodes []actor.ActorSystem
	// spawned counts the names handed out so far, so every spawn of the
	// benchmark claims a name the registry has not seen, whichever
	// sub-benchmark or iteration it belongs to.
	spawned atomic.Int64
}

// startBenchCluster starts clusterSpawnNodeCount nodes on loopback that
// discover each other through a static host list, waits until every node sees
// the others and stops them all when the benchmark ends.
func startBenchCluster(b *testing.B) *benchCluster {
	b.Helper()

	ctx := context.Background()
	ports := inet.Get(clusterSpawnPortsPerNode * clusterSpawnNodeCount)
	hosts := make([]string, clusterSpawnNodeCount)

	for i := range clusterSpawnNodeCount {
		hosts[i] = fmt.Sprintf("%s:%d", clusterSpawnHost, ports[clusterSpawnPortsPerNode*i])
	}

	nodes := make([]actor.ActorSystem, clusterSpawnNodeCount)

	for i := range clusterSpawnNodeCount {
		discoveryPort := ports[clusterSpawnPortsPerNode*i]
		peersPort := ports[clusterSpawnPortsPerNode*i+1]
		remotingPort := ports[clusterSpawnPortsPerNode*i+2]

		clusterConfig := actor.NewClusterConfig().
			WithDiscovery(static.NewDiscovery(&static.Config{Hosts: hosts})).
			WithDiscoveryPort(discoveryPort).
			WithPeersPort(peersPort).
			WithBootstrapTimeout(clusterSpawnBootstrapTimeout).
			WithKinds(new(noopActor))

		node, err := actor.NewActorSystem(clusterSpawnSystemName,
			actor.WithLogger(log.DiscardLogger),
			actor.WithActorInitMaxRetries(1),
			actor.WithRemote(remote.NewConfig(clusterSpawnHost, remotingPort)),
			actor.WithCluster(clusterConfig))
		if err != nil {
			b.Fatalf("failed to create node %d: %v", i, err)
		}

		nodes[i] = node
	}

	errs := make([]error, clusterSpawnNodeCount)
	var wg sync.WaitGroup

	for i, node := range nodes {
		wg.Go(func() {
			errs[i] = node.Start(ctx)
		})
	}

	wg.Wait()

	b.Cleanup(func() {
		for _, node := range nodes {
			_ = node.Stop(ctx)
		}
	})

	for i, err := range errs {
		if err != nil {
			b.Fatalf("failed to start node %d: %v", i, err)
		}
	}

	deadline := time.Now().Add(clusterSpawnPeersTimeout)

	for _, node := range nodes {
		for {
			peers, err := node.Peers(ctx, clusterSpawnPeersQueryTimeout)
			if err == nil && len(peers) == clusterSpawnNodeCount-1 {
				break
			}

			if time.Now().After(deadline) {
				b.Fatalf("the %d nodes did not see each other within %s", clusterSpawnNodeCount, clusterSpawnPeersTimeout)
			}

			time.Sleep(clusterSpawnPeersPoll)
		}
	}

	return &benchCluster{nodes: nodes}
}

// spawnNext spawns one actor under a name no earlier spawn of this cluster
// used, from the first node, and fails the benchmark when the spawn does. The
// actor is not relocatable so that stopping the cluster at the end does not
// move the population to the surviving nodes; its registry record is written
// the same way as a relocatable one.
func (x *benchCluster) spawnNext(b *testing.B, ctx context.Context) {
	name := clusterSpawnActorNamePrefix + strconv.FormatInt(x.spawned.Add(1), 10)
	if _, err := x.nodes[0].Spawn(ctx, name, new(noopActor), actor.WithRelocationDisabled()); err != nil {
		b.Fatalf("failed to spawn %s: %v", name, err)
	}
}

// BenchmarkClusterSpawn measures what a named spawn costs in cluster mode. A
// spawn in a cluster claims the actor name in the registry before it returns,
// so on top of the local activation it pays registry round trips over the
// network. The benchmark starts clusterSpawnNodeCount nodes in one process on
// loopback and spawns from one of them under names no earlier spawn used, so
// every spawn is a first claim. It reports spawns/sec next to the time and the
// allocations per spawn.
//
// The sequential variant spawns one actor at a time: the latency of one spawn.
// The parallel variant spawns from GOMAXPROCS goroutines at once: its
// aggregate rate shows how far spawns of distinct names overlap in the
// registry. SpawnOn ends in the same path on the node it picks, so its cost is
// this plus one remoting hop when the name lands on another node.
//
// The cluster is started once and shared by both variants and every repeat,
// so the registry holds every actor spawned so far. Run the benchmark with a
// fixed iteration count to keep that population bounded.
func BenchmarkClusterSpawn(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping cluster benchmark in short mode")
	}

	ctx := context.Background()
	cluster := startBenchCluster(b)

	b.Run("sequential", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			cluster.spawnNext(b, ctx)
		}

		b.StopTimer()
		b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "spawns/sec")
	})

	b.Run("parallel", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				cluster.spawnNext(b, ctx)
			}
		})
		b.StopTimer()
		b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "spawns/sec")
	})
}
