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

// BenchmarkTellPairwise measures aggregate fire-and-forget throughput
// across GOMAXPROCS independent sender/receiver pairs: each producer
// goroutine tells its own private receiver actor, and the reported
// messages/sec aggregates all pairs.
//
// This is the scaling surface that BenchmarkTell cannot see. With every
// producer racing on one receiver, throughput is bounded by the single
// consumer's drain rate, so a process-wide bottleneck on the send path
// hides behind that bound. With independent pairs, aggregate throughput
// should approach pair-count times the single-pair rate; anything
// shared across actors (a global pool, a global lock) shows up here as
// a flat curve. Cite this number for multi-actor workloads.
func BenchmarkTellPairwise(b *testing.B) {
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
	// take one extra message. A pair with a zero target has its done
	// channel pre-closed because countingActor only closes it on the
	// target'th delivery.
	senders := make([]*actor.PID, pairs)
	receivers := make([]*actor.PID, pairs)
	dones := make([]chan struct{}, pairs)
	targets := make([]int64, pairs)
	base := int64(b.N) / int64(pairs)
	extra := int64(b.N) % int64(pairs)

	for i := range pairs {
		target := base
		if int64(i) < extra {
			target++
		}

		done := make(chan struct{})
		if target == 0 {
			close(done)
		}

		sender, err := actorSystem.Spawn(ctx, fmt.Sprintf("sender-%d", i), new(Actor))
		if err != nil {
			b.Fatalf("failed to spawn sender: %v", err)
		}

		receiver, err := actorSystem.Spawn(ctx, fmt.Sprintf("receiver-%d", i), &countingActor{
			done:   done,
			target: target,
		})
		if err != nil {
			b.Fatalf("failed to spawn receiver: %v", err)
		}

		senders[i], receivers[i], dones[i], targets[i] = sender, receiver, done, target
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
				if err := senders[i].Tell(ctx, receivers[i], msg); err != nil {
					b.Error(err)
					return
				}
			}
		}(i)
	}

	// Wait for the producers first: a producer that failed and bailed out
	// leaves its receiver short of its target, so waiting on the done
	// channels directly would hang instead of reporting the failure.
	wg.Wait()
	if b.Failed() {
		return
	}

	for _, done := range dones {
		<-done
	}
	b.StopTimer()
	messagesPerSec := float64(b.N) / b.Elapsed().Seconds()
	b.ReportMetric(messagesPerSec, "messages/sec")
}
