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

// BenchmarkSpawnStop measures one actor lifecycle: an ActorSystem.Spawn
// followed by PID.Shutdown, timed until the actor's PostStop has run. Against
// BenchmarkSpawnTopLevel it isolates the stop path: the watchers, watchees
// and children released, the name freed and the stopped event published.
// The same name is spawned every iteration, since a stop frees it, so the
// population stays at one actor and the number does not depend on the
// iteration count.
func BenchmarkSpawnStop(b *testing.B) {
	ctx, system := startSpawnBenchSystem(b)
	stopped := make(chan struct{}, 1)
	stopper := &stopSignalActor{stopped: stopped}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		pid, err := system.Spawn(ctx, "lifecycle", stopper)
		require.NoError(b, err)
		require.NoError(b, pid.Shutdown(ctx))
		<-stopped
	}

	b.StopTimer()
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "lifecycles/sec")
}
