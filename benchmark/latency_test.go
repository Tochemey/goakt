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
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// BenchmarkAskTailLatencyUnderLoad measures request/reply latency
// percentiles on a saturated system. One dedicated probe pair performs
// sequential Ask round trips while GOMAXPROCS background pairs flood
// the dispatcher with fire-and-forget Tells at full speed.
//
// The probe actor is deliberately not one of the loaded actors, so the
// distribution reflects scheduler interference, not backlog queueing in
// a shared mailbox: how long a request waits for dispatcher attention
// while every worker is busy draining throughput turns. The throughput
// budget bounds how long a worker holds a core per turn, so the tail
// percentiles are the observable cost of that fairness trade-off.
//
// ns/op is the mean probe round trip. The p50/p90/p99/p99.9/max metrics
// are nanoseconds per round trip at that percentile. Compare p50 against
// the unloaded BenchmarkAsk to read the interference cost directly.
func BenchmarkAskTailLatencyUnderLoad(b *testing.B) {
	loadPairs := runtime.GOMAXPROCS(0)
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

	loadSenders := make([]*actor.PID, loadPairs)
	loadReceivers := make([]*actor.PID, loadPairs)

	for i := range loadPairs {
		sender, err := actorSystem.Spawn(ctx, fmt.Sprintf("load-sender-%d", i), new(Actor))
		if err != nil {
			b.Fatalf("failed to spawn load sender: %v", err)
		}

		receiver, err := actorSystem.Spawn(ctx, fmt.Sprintf("load-receiver-%d", i), new(Actor))
		if err != nil {
			b.Fatalf("failed to spawn load receiver: %v", err)
		}

		loadSenders[i], loadReceivers[i] = sender, receiver
	}

	probeSender, err := actorSystem.Spawn(ctx, "probe-sender", new(Actor))
	if err != nil {
		b.Fatalf("failed to spawn probe sender: %v", err)
	}

	probeReceiver, err := actorSystem.Spawn(ctx, "probe-receiver", new(Actor))
	if err != nil {
		b.Fatalf("failed to spawn probe receiver: %v", err)
	}

	stop := make(chan struct{})
	var loadWG sync.WaitGroup
	loadWG.Add(loadPairs)

	for i := range loadPairs {
		go func(i int) {
			defer loadWG.Done()

			msg := new(testpb.TestSend)
			for {
				select {
				case <-stop:
					return
				default:
				}

				if err := loadSenders[i].Tell(ctx, loadReceivers[i], msg); err != nil {
					return
				}
			}
		}(i)
	}

	latencies := make([]time.Duration, b.N)
	msg := new(testpb.TestReply)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		started := time.Now()
		if _, err := probeSender.Ask(ctx, probeReceiver, msg, time.Second); err != nil {
			b.Fatal(err)
		}
		latencies[i] = time.Since(started)
	}

	b.StopTimer()
	close(stop)
	loadWG.Wait()

	slices.Sort(latencies)
	percentile := func(p float64) float64 {
		idx := int(p * float64(len(latencies)-1))
		return float64(latencies[idx])
	}

	b.ReportMetric(percentile(0.50), "p50-ns")
	b.ReportMetric(percentile(0.90), "p90-ns")
	b.ReportMetric(percentile(0.99), "p99-ns")
	b.ReportMetric(percentile(0.999), "p99.9-ns")
	b.ReportMetric(float64(latencies[len(latencies)-1]), "max-ns")
}
