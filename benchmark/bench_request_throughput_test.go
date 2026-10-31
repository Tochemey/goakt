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
	"testing"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// BenchmarkRequestThroughput is a variant of BenchmarkRequest that sweeps the
// dispatcher's per-turn message budget. Request/response workloads interleave
// ask replies with incoming messages, so the budget affects how quickly a
// requester's continuation runs relative to new requests.
func BenchmarkRequestThroughput(b *testing.B) {
	for _, budget := range throughputBudgets {
		b.Run(fmt.Sprintf("budget=%d", budget), func(b *testing.B) {
			ctx := context.Background()

			actorSystem, err := actor.NewActorSystem("bench",
				actor.WithLogger(log.DiscardLogger),
				actor.WithActorInitMaxRetries(1),
				actor.WithThroughputBudget(budget))
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
			receiver, err := actorSystem.Spawn(ctx, "receiver", new(Actor))
			if err != nil {
				b.Fatalf("failed to spawn receiver: %v", err)
			}

			doneCh := make(chan struct{}, runtime.GOMAXPROCS(0))
			errCh := make(chan error, 1)
			requesterActor := &requestBenchActor{
				target: receiver,
				doneCh: doneCh,
				errCh:  errCh,
			}
			requester, err := actorSystem.Spawn(ctx, "requester", requesterActor, actor.WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))
			if err != nil {
				b.Fatalf("failed to spawn requester: %v", err)
			}

			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				msg := new(testpb.TestSend)
				for pb.Next() {
					if err := sender.Tell(ctx, requester, msg); err != nil {
						b.Fatal(err)
					}
					<-doneCh
					select {
					case err := <-errCh:
						b.Fatal(err)
					default:
					}
				}
			})
			b.StopTimer()
			messagesPerSec := float64(b.N) / b.Elapsed().Seconds()
			b.ReportMetric(messagesPerSec, "messages/sec")
		})
	}
}
