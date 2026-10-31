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
	"time"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

func BenchmarkAskPairwise(b *testing.B) {
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

	// b.N round trips are split near-evenly: the first b.N % pairs
	// pairs take one extra.
	senders := make([]*actor.PID, pairs)
	receivers := make([]*actor.PID, pairs)
	targets := make([]int64, pairs)
	base := int64(b.N) / int64(pairs)
	extra := int64(b.N) % int64(pairs)

	for i := range pairs {
		target := base
		if int64(i) < extra {
			target++
		}

		sender, err := actorSystem.Spawn(ctx, fmt.Sprintf("sender-%d", i), new(Actor))
		if err != nil {
			b.Fatalf("failed to spawn sender: %v", err)
		}

		receiver, err := actorSystem.Spawn(ctx, fmt.Sprintf("receiver-%d", i), new(Actor))
		if err != nil {
			b.Fatalf("failed to spawn receiver: %v", err)
		}

		senders[i], receivers[i], targets[i] = sender, receiver, target
	}

	var wg sync.WaitGroup
	wg.Add(pairs)

	b.ReportAllocs()
	b.ResetTimer()

	for i := range pairs {
		go func(i int) {
			defer wg.Done()

			msg := new(testpb.TestReply)
			for range targets[i] {
				if _, err := senders[i].Ask(ctx, receivers[i], msg, time.Second); err != nil {
					b.Error(err)
					return
				}
			}
		}(i)
	}

	wg.Wait()
	b.StopTimer()
	messagesPerSec := float64(b.N) / b.Elapsed().Seconds()
	b.ReportMetric(messagesPerSec, "messages/sec")
}
