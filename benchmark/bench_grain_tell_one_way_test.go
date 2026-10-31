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

// BenchmarkGrainTellOneWay is the one-way counterpart of BenchmarkGrainTell.
// Each TellGrain returns once the message is enqueued instead of blocking
// for the processed acknowledgment, so producers only pay the mailbox
// enqueue and the schedule CAS. The timer still runs until the grain has
// processed every message, so messages/sec remains an end-to-end figure
// comparable with BenchmarkGrainTell.
func BenchmarkGrainTellOneWay(b *testing.B) {
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

	done := make(chan struct{})
	target := int64(b.N)
	//nolint:staticcheck // the deprecated factory path is kept here: the benchmark injects per-run state that zero-value construction cannot carry
	identity, err := actorSystem.GrainIdentity(ctx, "receiver", func(context.Context) (actor.Grain, error) {
		return &countingGrain{done: done, target: target}, nil
	})
	if err != nil {
		b.Fatalf("failed to create grain identity: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		msg := new(testpb.TestSend)
		for pb.Next() {
			if err := actorSystem.TellGrain(ctx, identity, msg, actor.WithOneWay()); err != nil {
				b.Fatal(err)
			}
		}
	})
	<-done
	b.StopTimer()
	messagesPerSec := float64(b.N) / b.Elapsed().Seconds()
	b.ReportMetric(messagesPerSec, "messages/sec")
}
