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
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
)

// spawnChildDepths are the ancestor counts BenchmarkSpawnChild sweeps. A
// child spawn validates and registers one address per ancestor, so the slope
// across these depths is the per-ancestor cost of a spawn.
var spawnChildDepths = []int{1, 10, 20}

// BenchmarkSpawnTopLevel measures the latency of one ActorSystem.Spawn: an
// empty actor under a name no earlier spawn used, spawned into a started
// system from one goroutine. Spawn returns once the actor is registered and
// its PostStart is in flight, so the number is the framework's own cost of
// creating an actor: address validation, PID construction, tree and
// passivation registration, and the first dispatch.
//
// Every iteration leaves an actor alive, so the reported cost is averaged
// over a population that grows with the iteration count. Run the benchmark
// with a fixed iteration count to compare runs.
func BenchmarkSpawnTopLevel(b *testing.B) {
	ctx, system := startSpawnBenchSystem(b)
	names := spawnBenchNames("spawn-", b.N)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := system.Spawn(ctx, names[i], new(noopActor))
		require.NoError(b, err)
	}

	b.StopTimer()
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "spawns/sec")
}

// BenchmarkSpawnChild measures the latency of one PID.SpawnChild under a
// parent with the given number of ancestors: the parent of depth 1 is a
// top-level actor, the parent of depth 20 sits at the bottom of a chain of
// twenty actors. A child spawn validates its address once per ancestor, so
// the difference between depths is what a deep hierarchy adds to a spawn.
//
// As in BenchmarkSpawnTopLevel, every iteration leaves a child alive under
// the parent; run with a fixed iteration count to compare runs.
func BenchmarkSpawnChild(b *testing.B) {
	for _, depth := range spawnChildDepths {
		b.Run(fmt.Sprintf("depth=%d", depth), func(b *testing.B) {
			benchmarkSpawnChild(b, depth)
		})
	}
}

// BenchmarkSpawnTopLevelParallel measures the aggregate rate of
// ActorSystem.Spawn from GOMAXPROCS goroutines spawning distinct names at
// once. Against BenchmarkSpawnTopLevel it shows how far spawns overlap:
// anything the spawn path serialises across actors, such as the actor tree's
// lock, shows up here as an aggregate rate below GOMAXPROCS times the
// sequential one.
func BenchmarkSpawnTopLevelParallel(b *testing.B) {
	ctx, system := startSpawnBenchSystem(b)

	var next atomic.Int64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := system.Spawn(ctx, "spawn-"+strconv.FormatInt(next.Add(1), 10), new(noopActor))
			require.NoError(b, err)
		}
	})
	b.StopTimer()
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "spawns/sec")
}

// benchmarkSpawnChild builds a chain of depth actors, then times b.N child
// spawns under the last one.
func benchmarkSpawnChild(b *testing.B, depth int) {
	b.Helper()

	ctx, system := startSpawnBenchSystem(b)
	parent, err := system.Spawn(ctx, "ancestor-0", new(noopActor))
	require.NoError(b, err)

	for level := 1; level < depth; level++ {
		parent, err = parent.SpawnChild(ctx, "ancestor-"+strconv.Itoa(level), new(noopActor))
		require.NoError(b, err)
	}

	names := spawnBenchNames("child-", b.N)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := parent.SpawnChild(ctx, names[i], new(noopActor))
		require.NoError(b, err)
	}

	b.StopTimer()
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "spawns/sec")
}

// startSpawnBenchSystem starts a fresh actor system for one benchmark run and
// stops it when the run ends, so every run spawns into an empty system.
func startSpawnBenchSystem(b *testing.B) (context.Context, actor.ActorSystem) {
	b.Helper()

	ctx := context.Background()
	system, err := actor.NewActorSystem("spawn-bench", actor.WithLogger(log.DiscardLogger), actor.WithActorInitMaxRetries(1))
	require.NoError(b, err)
	require.NoError(b, system.Start(ctx))
	b.Cleanup(func() { _ = system.Stop(ctx) })

	return ctx, system
}

// spawnBenchNames returns count distinct actor names with the given prefix,
// built before the timer starts so name formatting is not charged to the
// spawn.
func spawnBenchNames(prefix string, count int) []string {
	names := make([]string, count)
	for i := range names {
		names[i] = prefix + strconv.Itoa(i)
	}

	return names
}
