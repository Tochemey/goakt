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
	"testing"
)

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
