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
	"runtime"
	"sync"
	"testing"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// BenchmarkGrainTellOneWayPairwise is the one-way counterpart of
// BenchmarkGrainTellPairwise: each producer goroutine sends one-way to
// its own private grain, so a producer returning only proves its
// messages were enqueued. Every grain therefore counts what it processed
// and the timer runs until all of them are done, which keeps messages/sec
// an end-to-end figure comparable with BenchmarkGrainTellPairwise. The
// gap between the two is the per-message acknowledgment round trip.
func BenchmarkGrainTellOneWayPairwise(b *testing.B) {
	pairs := runtime.GOMAXPROCS(0)
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

	// b.N messages are split near-evenly: the first b.N % pairs pairs
	// take one extra message. A pair with no message to send has nothing
	// to wait for, which matters for the small b.N of calibration runs.
	identities := make([]*actor.GrainIdentity, pairs)
	targets := make([]int64, pairs)
	dones := make([]chan struct{}, pairs)
	base := int64(b.N) / int64(pairs)
	extra := int64(b.N) % int64(pairs)

	for i := range pairs {
		target := base
		if int64(i) < extra {
			target++
		}

		done := make(chan struct{})
		//nolint:staticcheck // the deprecated factory path is kept here: the benchmark injects per-run state that zero-value construction cannot carry
		identity, err := actorSystem.GrainIdentity(ctx, fmt.Sprintf("receiver-%d", i), func(context.Context) (actor.Grain, error) {
			return &countingGrain{done: done, target: target}, nil
		})
		if err != nil {
			b.Fatalf("failed to create grain identity: %v", err)
		}

		identities[i], targets[i], dones[i] = identity, target, done
	}

	var wg sync.WaitGroup
	wg.Add(pairs)

	b.ReportAllocs()
	b.ResetTimer()

	for i := range pairs {
		go func(i int) {
			defer wg.Done()

			// Reuse the same message per goroutine to reduce allocs in the hot path.
			msg := new(testpb.TestSend)
			for range targets[i] {
				if err := actorSystem.TellGrain(ctx, identities[i], msg, actor.WithOneWay()); err != nil {
					b.Error(err)
					return
				}
			}
		}(i)
	}

	wg.Wait()

	for i := range pairs {
		if targets[i] > 0 {
			<-dones[i]
		}
	}

	b.StopTimer()
	messagesPerSec := float64(b.N) / b.Elapsed().Seconds()
	b.ReportMetric(messagesPerSec, "messages/sec")
}
