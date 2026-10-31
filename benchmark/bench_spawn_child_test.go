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
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// spawnChildDepths are the ancestor counts BenchmarkSpawnChild sweeps. A
// child spawn validates and registers one address per ancestor, so the slope
// across these depths is the per-ancestor cost of a spawn.
var spawnChildDepths = []int{1, 10, 20}

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
