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
	"sync/atomic"
	"testing"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// benchEqualPriority ranks every message the same. It drives the priority
// mailboxes in the throughput benchmarks: equal priorities keep the heap busy
// and, for the stable variant, exercise the insertion-order tiebreaker.
func benchEqualPriority(any, any) bool { return false }

// windowActor counts the messages it has processed so a producer can keep the
// number of in-flight messages below the mailbox capacity.
type windowActor struct {
	outstanding *atomic.Int64
}

func (a *windowActor) PreStart(*actor.Context) error { return nil }

func (a *windowActor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
		a.outstanding.Add(-1)
	default:
		ctx.Unhandled()
	}
}

func (a *windowActor) PostStop(*actor.Context) error { return nil }

// benchmarkMailboxTell measures steady-state Tell throughput through actors
// backed by the mailbox produced by newMailbox.
//
// Each parallel producer targets its own receiver and bounds the number of
// in-flight messages to half the capacity, so a bounded drop-on-full mailbox
// never overflows and the timed region measures the enqueue and dispatch path
// rather than the dead-letter path a saturated mailbox would exercise. The
// bound also keeps the unbounded mailbox's backlog flat. A producer only waits
// when its receiver falls a whole window behind, which is the mailbox's own
// backpressure and is exactly what a bounded mailbox is for.
func benchmarkMailboxTell(b *testing.B, capacity int, newMailbox func() actor.Mailbox) {
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

	sender, err := actorSystem.Spawn(ctx, "sender", new(Actor))
	if err != nil {
		b.Fatalf("failed to spawn sender: %v", err)
	}

	// one receiver per parallel producer so no single mailbox is saturated
	receiverCount := runtime.GOMAXPROCS(0)
	receivers := make([]*actor.PID, receiverCount)
	outstanding := make([]*atomic.Int64, receiverCount)
	for i := range receivers {
		outstanding[i] = &atomic.Int64{}
		receivers[i], err = actorSystem.Spawn(ctx, fmt.Sprintf("receiver-%d", i),
			&windowActor{outstanding: outstanding[i]}, actor.WithMailbox(newMailbox()))
		if err != nil {
			b.Fatalf("failed to spawn receiver: %v", err)
		}
	}

	// keep in-flight below capacity so a bounded mailbox never overflows
	window := int64(capacity / 2)

	var nextReceiver atomic.Int64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		idx := (nextReceiver.Add(1) - 1) % int64(receiverCount)
		receiver := receivers[idx]
		counter := outstanding[idx]
		// Reuse the same message per goroutine to reduce allocs in the hot path.
		msg := new(testpb.TestSend)

		for pb.Next() {
			for counter.Load() >= window {
				runtime.Gosched()
			}

			counter.Add(1)
			if err := sender.Tell(ctx, receiver, msg); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.StopTimer()

	messagesPerSec := float64(b.N) / b.Elapsed().Seconds()
	b.ReportMetric(messagesPerSec, "messages/sec")
}

// BenchmarkMailboxTell measures Tell throughput through an actor backed by each
// of the mailbox implementations added alongside the default unbounded mailbox.
func BenchmarkMailboxTell(b *testing.B) {
	const capacity = 1 << 16

	b.Run("NonBlockingBounded", func(b *testing.B) {
		benchmarkMailboxTell(b, capacity, func() actor.Mailbox {
			return actor.NewNonBlockingBoundedMailbox(capacity)
		})
	})
	b.Run("UnboundedStablePriority", func(b *testing.B) {
		benchmarkMailboxTell(b, capacity, func() actor.Mailbox {
			return actor.NewUnboundedStablePriorityMailbox(benchEqualPriority)
		})
	})
	b.Run("BoundedPriority", func(b *testing.B) {
		benchmarkMailboxTell(b, capacity, func() actor.Mailbox {
			return actor.NewBoundedPriorityMailbox(capacity, benchEqualPriority)
		})
	})
	b.Run("BoundedStablePriority", func(b *testing.B) {
		benchmarkMailboxTell(b, capacity, func() actor.Mailbox {
			return actor.NewBoundedStablePriorityMailbox(capacity, benchEqualPriority)
		})
	})
}
