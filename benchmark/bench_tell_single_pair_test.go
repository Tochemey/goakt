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

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// BenchmarkTellSinglePair measures fire-and-forget throughput for a
// single producer goroutine sending to a single receiver actor.
//
// It complements BenchmarkTell, which races GOMAXPROCS producers on one
// receiver: with exactly one producer the enqueue side is
// contention-free, so this number isolates the per-message dispatch
// cost. Elapsed time covers full processing: the timer stops only once
// the receiver has handled all b.N messages, not when the last Tell
// returns.
func BenchmarkTellSinglePair(b *testing.B) {
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

	done := make(chan struct{})
	receiver, err := actorSystem.Spawn(ctx, "receiver", &countingActor{
		done:   done,
		target: int64(b.N),
	})
	if err != nil {
		b.Fatalf("failed to spawn receiver: %v", err)
	}

	msg := new(testpb.TestSend)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := sender.Tell(ctx, receiver, msg); err != nil {
			b.Fatal(err)
		}
	}

	<-done
	b.StopTimer()
	messagesPerSec := float64(b.N) / b.Elapsed().Seconds()
	b.ReportMetric(messagesPerSec, "messages/sec")
}
