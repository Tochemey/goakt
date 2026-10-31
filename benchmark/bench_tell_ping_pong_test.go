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
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// pingPongInFlight is the sweep of messages each BenchmarkTellPingPong client
// keeps in flight to its echo actor.
var pingPongInFlight = []int{100, 1000}

// BenchmarkTellPingPong measures actor-to-actor fire-and-forget throughput,
// in the shape actor frameworks commonly use to report messaging throughput:
// each client actor keeps a fixed number of messages in flight to its own echo
// actor and sends its next batch once all the replies of the current one are
// back. Every request and every reply counts as one message.
//
// Unlike BenchmarkTellPairwise, whose producers are goroutines that can run
// arbitrarily far ahead of their receivers, the in-flight bound keeps every
// mailbox short, so the number reflects the cost of delivering and dispatching
// messages rather than the cost of buffering a growing backlog. The pairs
// sweep runs GOMAXPROCS pairs and eight times as many.
func BenchmarkTellPingPong(b *testing.B) {
	procs := runtime.GOMAXPROCS(0)
	for _, pairs := range []int{procs, 8 * procs} {
		for _, inFlight := range pingPongInFlight {
			b.Run(fmt.Sprintf("pairs=%d/inflight=%d", pairs, inFlight), func(b *testing.B) {
				benchmarkTellPingPong(b, pairs, inFlight)
			})
		}
	}
}

// benchmarkTellPingPong runs BenchmarkTellPingPong for pairs client/echo pairs
// with inFlight messages in flight per pair. b.N is the number of messages,
// requests and replies together.
func benchmarkTellPingPong(b *testing.B, pairs, inFlight int) {
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

	// each request comes back as a reply, so a pair exchanges two messages
	// per request
	requests := max(b.N/(2*pairs), 1)
	clients := make([]*actor.PID, pairs)
	dones := make([]chan struct{}, pairs)

	for i := range pairs {
		echo, err := actorSystem.Spawn(ctx, fmt.Sprintf("echo-%d", i), new(pingPongEcho))
		if err != nil {
			b.Fatalf("failed to spawn echo: %v", err)
		}

		dones[i] = make(chan struct{})
		clients[i], err = actorSystem.Spawn(ctx, fmt.Sprintf("client-%d", i), &pingPongClient{
			echo:      echo,
			batch:     inFlight,
			remaining: requests,
			message:   new(testpb.TestSend),
			done:      dones[i],
		})
		if err != nil {
			b.Fatalf("failed to spawn client: %v", err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()

	for _, client := range clients {
		if err := actor.Tell(ctx, client, new(pingPongStart)); err != nil {
			b.Fatal(err)
		}
	}

	for _, done := range dones {
		<-done
	}

	b.StopTimer()
	messagesPerSec := float64(2*requests*pairs) / b.Elapsed().Seconds()
	b.ReportMetric(messagesPerSec, "messages/sec")
}
