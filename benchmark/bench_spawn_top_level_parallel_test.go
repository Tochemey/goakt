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
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

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
