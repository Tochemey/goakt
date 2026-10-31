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
	"testing"

	"github.com/stretchr/testify/require"
)

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
