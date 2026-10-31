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
	"sync/atomic"
	"testing"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// BenchmarkGrainTellFanOut spreads load across many grains. This is the
// case where the dispatcher pool change matters most: under the old
// per-burst goroutine model, each active grain spawned its own
// goroutine, so N concurrent grains = N goroutines. With the shared
// pool, goroutine count is bounded by GOMAXPROCS regardless of N.
func BenchmarkGrainTellFanOut(b *testing.B) {
	const grainCount = 256
	ctx := context.Background()

	actorSystem, err := actor.NewActorSystem("bench",
		actor.WithLogger(log.DiscardLogger),
		actor.WithActorInitMaxRetries(1))
	if err != nil {
		b.Fatalf("failed to create actor system: %v", err)
	}
	if err := actorSystem.Start(ctx); err != nil {
		b.Fatalf("failed to start actor system: %v", err)
	}
	b.Cleanup(func() { _ = actorSystem.Stop(ctx) })

	identities, dones := setupFanOutGrains(b, ctx, actorSystem, grainCount)

	var counter atomic.Uint64

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		msg := new(testpb.TestSend)
		for pb.Next() {
			idx := counter.Add(1) % grainCount
			if err := actorSystem.TellGrain(ctx, identities[idx], msg); err != nil {
				b.Fatal(err)
			}
		}
	})
	for _, d := range dones {
		<-d
	}
	b.StopTimer()
	messagesPerSec := float64(b.N) / b.Elapsed().Seconds()
	b.ReportMetric(messagesPerSec, "messages/sec")
}
