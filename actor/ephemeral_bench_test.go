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

package actor

import (
	"context"
	"strconv"
	"testing"

	"github.com/tochemey/goakt/v4/log"
)

// BenchmarkSpawnStopDefault spawns and stops one actor per iteration using the
// framework defaults (relocatable, idle-based passivation bookkeeping). It is the
// baseline against which BenchmarkSpawnStopEphemeral is compared for high-churn,
// connection-shaped workloads (see WithEphemeral).
func BenchmarkSpawnStopDefault(b *testing.B) {
	benchmarkSpawnStop(b)
}

// BenchmarkSpawnStopEphemeral spawns and stops one actor per iteration using
// WithEphemeral, the option recommended for connection-lifetime actors.
func BenchmarkSpawnStopEphemeral(b *testing.B) {
	benchmarkSpawnStop(b, WithEphemeral())
}

// benchmarkSpawnStop drives repeated spawn/stop cycles against a standalone
// (non-clustered) actor system so the measured cost isolates per-actor bookkeeping
// (passivation registration and relocation state) rather than cluster RPCs.
func benchmarkSpawnStop(b *testing.B, opts ...SpawnOption) {
	b.Helper()
	ctx := context.TODO()

	system, err := NewActorSystem("bench-spawn-stop", WithLogger(log.DiscardLogger))
	if err != nil {
		b.Fatal(err)
	}
	if err := system.Start(ctx); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = system.Stop(ctx)
	})

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		name := "conn-" + strconv.Itoa(i)
		pid, err := system.Spawn(ctx, name, NewMockActor(), opts...)
		if err != nil {
			b.Fatal(err)
		}
		if err := pid.Shutdown(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
