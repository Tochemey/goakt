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
	"testing"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
)

// payloadSizes is the sweep used by BenchmarkTellPayload. It spans from
// the empty-message dispatch baseline used by the other benchmarks up to
// 64 KiB so the curve where data-movement cost overtakes dispatch cost
// is visible.
var payloadSizes = []int{0, 64, 1024, 8192, 65536}

// BenchmarkTellPayload is a variant of BenchmarkTell that sweeps message
// payload size. Empty-payload benchmarks measure dispatcher and mailbox
// overhead in isolation; this benchmark separates that overhead from the
// per-message data cost so throughput claims can be cited at a payload
// size that matches the workload being argued about.
func BenchmarkTellPayload(b *testing.B) {
	for _, size := range payloadSizes {
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
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
			receiver, err := actorSystem.Spawn(ctx, "receiver", &payloadCounterActor{
				done:   done,
				target: int64(b.N),
			})
			if err != nil {
				b.Fatalf("failed to spawn receiver: %v", err)
			}

			msg := &payloadMsg{Data: make([]byte, size)}
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if err := sender.Tell(ctx, receiver, msg); err != nil {
						b.Fatal(err)
					}
				}
			})
			<-done
			b.StopTimer()
			messagesPerSec := float64(b.N) / b.Elapsed().Seconds()
			b.ReportMetric(messagesPerSec, "messages/sec")
		})
	}
}
