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

package bench

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"

	"github.com/tochemey/goakt/v3/actor"
	"github.com/tochemey/goakt/v3/discovery"
	"github.com/tochemey/goakt/v3/discovery/nats"
	"github.com/tochemey/goakt/v3/internal/pause"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/remote"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

type noopActor struct{}

func (x *noopActor) PreStart(*actor.Context) error { return nil }
func (x *noopActor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
	default:
		ctx.Unhandled()
	}
}
func (x *noopActor) PostStop(*actor.Context) error { return nil }

func BenchmarkActorMemoryFootprint(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping memory benchmark in short mode")
	}

	ctx := context.Background()
	system, err := actor.NewActorSystem("mem-bench",
		actor.WithLogger(log.DiscardLogger),
		actor.WithActorInitMaxRetries(1))
	require.NoError(b, err)
	require.NoError(b, system.Start(ctx))
	defer func() { _ = system.Stop(ctx) }()

	const actorCount = 2_000

	pids := make([]*actor.PID, 0, actorCount)
	for i := range actorCount {
		pid, err := system.Spawn(ctx, benchActorName(i), new(noopActor))
		require.NoError(b, err)
		pids = append(pids, pid)
	}

	runtime.GC()

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	bytesPerActor := float64(mem.Alloc) / float64(actorCount)
	b.ReportMetric(bytesPerActor, "bytes/actor")

	fmt.Printf("\nActors=%d Alloc=%s bytes/actor=%.0f (~%.2f KB)\n", actorCount, humanReadableBytes(mem.Alloc), bytesPerActor, bytesPerActor/1024)

	for _, pid := range pids {
		_ = pid.Shutdown(ctx)
	}
}

func benchActorName(i int) string {
	return "bench-noop-" + strconv.Itoa(i)
}

func humanReadableBytes(b uint64) string {
	const (
		kilobyte = 1024
		megabyte = 1024 * kilobyte
		gigabyte = 1024 * megabyte
	)

	switch {
	case b >= gigabyte:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(gigabyte))
	case b >= megabyte:
		return fmt.Sprintf("%.2f MB", float64(b)/float64(megabyte))
	case b >= kilobyte:
		return fmt.Sprintf("%.2f KB", float64(b)/float64(kilobyte))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// BenchmarkGCAllocationsDuringShutdown_100Actors measures GC allocations during shutdown with 100 actors
func BenchmarkGCAllocationsDuringShutdown_100Actors(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping GC allocation benchmark in short mode")
	}
	benchmarkGCAllocationsDuringShutdown(b, 100, 0)
}

// BenchmarkGCAllocationsDuringShutdown_1000Actors measures GC allocations during shutdown with 1,000 actors
func BenchmarkGCAllocationsDuringShutdown_1000Actors(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping GC allocation benchmark in short mode")
	}
	benchmarkGCAllocationsDuringShutdown(b, 1000, 0)
}

// BenchmarkGCAllocationsDuringShutdown_100Grains measures GC allocations during shutdown with 100 grains
func BenchmarkGCAllocationsDuringShutdown_100Grains(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping GC allocation benchmark in short mode")
	}
	benchmarkGCAllocationsDuringShutdown(b, 0, 100)
}

// BenchmarkGCAllocationsDuringShutdown_1000Grains measures GC allocations during shutdown with 1,000 grains
func BenchmarkGCAllocationsDuringShutdown_1000Grains(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping GC allocation benchmark in short mode")
	}
	benchmarkGCAllocationsDuringShutdown(b, 0, 1000)
}

// BenchmarkGCAllocationsDuringShutdown_Mixed measures GC allocations during shutdown with mixed actors and grains
func BenchmarkGCAllocationsDuringShutdown_Mixed(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping GC allocation benchmark in short mode")
	}
	benchmarkGCAllocationsDuringShutdown(b, 500, 500)
}

// benchmarkGCAllocationsDuringShutdown measures GC allocations during shutdown
func benchmarkGCAllocationsDuringShutdown(b *testing.B, actorCount, grainCount int) {
	ctx := context.Background()

	// Create and start the actor system
	system, err := actor.NewActorSystem("gc-shutdown-bench",
		actor.WithLogger(log.DiscardLogger),
		actor.WithActorInitMaxRetries(1))
	require.NoError(b, err)
	require.NoError(b, system.Start(ctx))

	// Create actors
	pids := make([]*actor.PID, 0, actorCount)
	for i := 0; i < actorCount; i++ {
		pid, err := system.Spawn(ctx, benchActorName(i), new(noopActor))
		require.NoError(b, err)
		pids = append(pids, pid)
	}

	// Create grains
	identities := make([]*actor.GrainIdentity, 0, grainCount)
	for i := 0; i < grainCount; i++ {
		identity, err := system.GrainIdentity(ctx, benchGrainName(i), func(_ context.Context) (actor.Grain, error) {
			return newMockGrain(), nil
		})
		require.NoError(b, err)
		identities = append(identities, identity)

		// Activate the grain by sending a message
		_, err = system.AskGrain(ctx, identity, new(testpb.TestReply), time.Second)
		require.NoError(b, err)
	}

	// Force GC before measuring
	runtime.GC()
	runtime.GC()

	// Get baseline memory stats before shutdown
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	// Reset timer before measuring shutdown
	b.ResetTimer()

	// Measure shutdown and GC allocations
	err = system.Stop(ctx)
	require.NoError(b, err)

	// Force GC after shutdown to measure allocations
	runtime.GC()
	runtime.GC()

	// Get memory stats after shutdown
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)

	// Calculate allocations during shutdown
	// TotalAlloc represents cumulative allocations
	// Mallocs represents number of allocations
	totalAllocs := memAfter.TotalAlloc - memBefore.TotalAlloc
	mallocs := memAfter.Mallocs - memBefore.Mallocs
	heapAlloc := memAfter.HeapAlloc

	// Report metrics
	b.ReportMetric(float64(totalAllocs), "bytes/alloc")
	b.ReportMetric(float64(totalAllocs)/1024/1024, "MB/alloc")
	b.ReportMetric(float64(mallocs), "count/alloc")
	b.ReportMetric(float64(heapAlloc)/1024/1024, "MB/heap")

	// Log summary
	entityCount := actorCount + grainCount
	if entityCount > 0 {
		bytesPerEntity := float64(totalAllocs) / float64(entityCount)
		fmt.Printf("\nShutdown GC: Actors=%d Grains=%d TotalAlloc=%s (%.2f MB) Mallocs=%d bytes/entity=%.0f\n",
			actorCount, grainCount, humanReadableBytes(totalAllocs), float64(totalAllocs)/1024/1024, mallocs, bytesPerEntity)
	} else {
		fmt.Printf("\nShutdown GC: TotalAlloc=%s (%.2f MB) Mallocs=%d\n",
			humanReadableBytes(totalAllocs), float64(totalAllocs)/1024/1024, mallocs)
	}
}

// BenchmarkMemoryFootprintDuringRebalancing measures memory footprint during rebalancing
func BenchmarkMemoryFootprintDuringRebalancing(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping rebalancing memory benchmark in short mode")
	}

	ctx := context.Background()

	// Start NATS server for cluster discovery
	srv := startNatsServerForBench(b)
	defer func() {
		srv.Shutdown()
		srv.WaitForShutdown()
	}()

	// Create 3-node cluster
	node1, sd1 := testNATsForBench(b, srv.Addr().String())
	require.NotNil(b, node1)
	require.NotNil(b, sd1)
	defer func() { _ = node1.Stop(ctx); _ = sd1.Close() }()

	node2, sd2 := testNATsForBench(b, srv.Addr().String())
	require.NotNil(b, node2)
	require.NotNil(b, sd2)
	defer func() { _ = node2.Stop(ctx); _ = sd2.Close() }()

	node3, sd3 := testNATsForBench(b, srv.Addr().String())
	require.NotNil(b, node3)
	require.NotNil(b, sd3)
	defer func() { _ = node3.Stop(ctx); _ = sd3.Close() }()

	// Wait for cluster to stabilize
	pause.For(2 * time.Second)

	// Create actors on each node (30 per node, 90 total)
	// Scale reduced to ensure queries complete within cluster readTimeout (1s default)
	const actorsPerNode = 30
	actorNames := make([]string, 0, actorsPerNode*3)

	// Create actors on node1
	for i := 0; i < actorsPerNode; i++ {
		name := fmt.Sprintf("rebal-actor-node1-%d", i)
		_, err := node1.Spawn(ctx, name, new(noopActor))
		require.NoError(b, err)
		actorNames = append(actorNames, name)
	}

	// Create actors on node2
	for i := 0; i < actorsPerNode; i++ {
		name := fmt.Sprintf("rebal-actor-node2-%d", i)
		_, err := node2.Spawn(ctx, name, new(noopActor))
		require.NoError(b, err)
		actorNames = append(actorNames, name)
	}

	// Create actors on node3
	for i := 0; i < actorsPerNode; i++ {
		name := fmt.Sprintf("rebal-actor-node3-%d", i)
		_, err := node3.Spawn(ctx, name, new(noopActor))
		require.NoError(b, err)
		actorNames = append(actorNames, name)
	}

	// Wait for actors to be replicated
	pause.For(3 * time.Second)

	// Force GC before measuring
	runtime.GC()
	runtime.GC()

	// Get baseline memory stats before rebalancing
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	// Reset timer before measuring rebalancing
	b.ResetTimer()

	// Trigger rebalancing by stopping node3
	err := node3.Stop(ctx)
	require.NoError(b, err)
	_ = sd3.Close()

	// Wait a short time for rebalancing to start (but not complete)
	// We measure memory footprint during the rebalancing process, not after completion
	pause.For(2 * time.Second)

	// Force GC after rebalancing starts
	runtime.GC()
	runtime.GC()

	// Get memory stats after rebalancing
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)

	// Calculate memory footprint during rebalancing
	totalAllocs := memAfter.TotalAlloc - memBefore.TotalAlloc
	mallocs := memAfter.Mallocs - memBefore.Mallocs
	heapAlloc := memAfter.HeapAlloc
	heapInuse := memAfter.HeapInuse

	// Report metrics
	b.ReportMetric(float64(totalAllocs), "bytes/alloc")
	b.ReportMetric(float64(totalAllocs)/1024/1024, "MB/alloc")
	b.ReportMetric(float64(mallocs), "count/alloc")
	b.ReportMetric(float64(heapAlloc)/1024/1024, "MB/heap")
	b.ReportMetric(float64(heapInuse)/1024/1024, "MB/heap-inuse")

	// Log summary
	fmt.Printf("\nRebalancing Memory: Actors=%d TotalAlloc=%s (%.2f MB) Mallocs=%d HeapInuse=%s (%.2f MB)\n",
		actorsPerNode*3, humanReadableBytes(totalAllocs), float64(totalAllocs)/1024/1024, mallocs,
		humanReadableBytes(heapInuse), float64(heapInuse)/1024/1024)
}

// Helper functions for cluster setup in benchmarks
func startNatsServerForBench(b *testing.B) *natsserver.Server {
	opts := &natsserver.Options{
		Host:           "127.0.0.1",
		Port:           -1, // Random port
		MaxControlLine: 4096,
	}
	srv, err := natsserver.NewServer(opts)
	require.NoError(b, err)

	ready := make(chan bool)
	go func() {
		ready <- true
		srv.Start()
	}()
	<-ready

	if !srv.ReadyForConnections(2 * time.Second) {
		b.Fatalf("nats-io server failed to start")
	}
	return srv
}

func testNATsForBench(b *testing.B, serverAddr string) (actor.ActorSystem, discovery.Provider) {
	ctx := context.Background()
	logger := log.DiscardLogger

	// Use dynamic ports
	ports := dynaport.Get(3)
	discoveryPort := ports[0]
	remotingPort := ports[1]
	peersPort := ports[2]

	host := "127.0.0.1"
	actorSystemName := "rebal-bench"

	// Create NATS provider using the nats discovery package
	natsSubject := "bench-subject"
	natsConfig := &nats.Config{
		NatsServer:    fmt.Sprintf("nats://%s", serverAddr),
		NatsSubject:   natsSubject,
		Host:          host,
		DiscoveryPort: discoveryPort,
	}

	// Create NATS discovery provider
	provider := nats.NewDiscovery(natsConfig, nats.WithLogger(log.DiscardLogger))
	require.NoError(b, provider.Initialize())

	// Create actor system with cluster
	system, err := actor.NewActorSystem(actorSystemName,
		actor.WithLogger(logger),
		actor.WithShutdownTimeout(3*time.Minute),
		actor.WithCluster(
			actor.NewClusterConfig().
				WithKinds(new(noopActor)).
				WithPartitionCount(7).
				WithReplicaCount(3).
				WithWriteQuorum(1).
				WithReadQuorum(1).
				WithPeersPort(peersPort).
				WithReadTimeout(1*time.Second).
				WithWriteTimeout(1*time.Second).
				WithMinimumPeersQuorum(1).
				WithDiscoveryPort(discoveryPort).
				WithBootstrapTimeout(time.Second).
				WithClusterStateSyncInterval(300*time.Millisecond).
				WithClusterBalancerInterval(100*time.Millisecond).
				WithDiscovery(provider)),
		actor.WithRemote(remote.NewConfig(host, remotingPort)))
	require.NoError(b, err)
	require.NoError(b, system.Start(ctx))

	return system, provider
}
