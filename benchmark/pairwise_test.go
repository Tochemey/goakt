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

// pingPongInFlight is the sweep of messages each BenchmarkTellPingPong client
// keeps in flight to its echo actor.
var pingPongInFlight = []int{100, 1000}

// pingPongStart tells a pingPongClient to send its first batch.
type pingPongStart struct{}

// pingPongEcho answers every TestSend with the same message, to its sender.
type pingPongEcho struct{}

// pingPongClient sends batches of TestSend to echo and sends the next batch
// once every reply of the current one is back, until it has sent remaining
// messages; then it closes done. Only its own turns touch its fields.
type pingPongClient struct {
	// echo is the actor the client exchanges messages with.
	echo *actor.PID
	// batch is the number of messages the client keeps in flight.
	batch int
	// remaining is the number of messages still to send.
	remaining int
	// outstanding is the number of replies the current batch still awaits.
	outstanding int
	// message is the payload sent in every request, reused to keep
	// allocation out of the measurement.
	message *testpb.TestSend
	// done is closed once the last reply is back.
	done chan struct{}
}

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

// PreStart does nothing.
func (x *pingPongEcho) PreStart(*actor.Context) error { return nil }

// Receive answers a TestSend with the same message.
func (x *pingPongEcho) Receive(ctx *actor.ReceiveContext) {
	if message, ok := ctx.Message().(*testpb.TestSend); ok {
		ctx.Tell(ctx.Sender(), message)
	}
}

// PostStop does nothing.
func (x *pingPongEcho) PostStop(*actor.Context) error { return nil }

// PreStart does nothing.
func (x *pingPongClient) PreStart(*actor.Context) error { return nil }

// Receive starts the exchange on pingPongStart and counts the replies.
func (x *pingPongClient) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *pingPongStart:
		x.send(ctx)
	case *testpb.TestSend:
		x.outstanding--
		if x.outstanding > 0 {
			return
		}

		if x.remaining == 0 {
			close(x.done)
			return
		}

		x.send(ctx)
	}
}

// PostStop does nothing.
func (x *pingPongClient) PostStop(*actor.Context) error { return nil }

// BenchmarkAskPairwise measures aggregate request/reply throughput
// across GOMAXPROCS independent asker/receiver pairs: each producer
// goroutine issues sequential Ask round trips against its own private
// receiver actor, and the reported messages/sec aggregates all pairs.
//
// BenchmarkAsk is the single-pair counterpart: one asker, one receiver,
// sequential round trips. As with Tell, the pairwise shape is the
// scaling surface: aggregate throughput should approach pair-count
// times the BenchmarkAsk rate, and anything the ask path shares across
// actors (pooled reply channels, timers) shows up here as a flat curve.
// No done channels are needed because Ask is synchronous: when every
// asker has returned, every message has been processed.
// BenchmarkGrainTellPairwise measures aggregate TellGrain throughput
// across GOMAXPROCS independent producer/grain pairs: each producer
// goroutine tells its own private grain, and the reported messages/sec
// aggregates all pairs.
//
// This is the scaling surface that BenchmarkGrainTell cannot see. With
// every producer racing on one grain, throughput is bounded by that
// grain's serial drain rate; with independent pairs, anything the grain
// path shares across grains (context pool shards, ack channel pool
// shards, the passivation manager) shows up here as a flat curve. Each
// TellGrain blocks for the processed acknowledgment, so per-pair
// throughput is round-trip bound and no completion tracking is needed:
// a producer returning proves its messages were processed. Cite this
// number for multi-grain workloads.
func BenchmarkGrainTellPairwise(b *testing.B) {
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
	// take one extra message.
	identities := make([]*actor.GrainIdentity, pairs)
	targets := make([]int64, pairs)
	base := int64(b.N) / int64(pairs)
	extra := int64(b.N) % int64(pairs)

	for i := range pairs {
		target := base
		if int64(i) < extra {
			target++
		}

		identity, err := actor.GrainOf[*benchGrain](ctx, actorSystem, fmt.Sprintf("receiver-%d", i))
		if err != nil {
			b.Fatalf("failed to create grain identity: %v", err)
		}

		identities[i], targets[i] = identity, target
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
				if err := actorSystem.TellGrain(ctx, identities[i], msg); err != nil {
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

// BenchmarkGrainAskPairwise measures aggregate AskGrain throughput
// across GOMAXPROCS independent asker/grain pairs: each producer
// goroutine issues sequential AskGrain round trips against its own
// private grain, and the reported messages/sec aggregates all pairs.
//
// BenchmarkGrainAsk is the single-pair counterpart. As with actors, the
// pairwise shape is the scaling surface: aggregate throughput should
// approach pair-count times the BenchmarkGrainAsk rate, and anything
// the ask path shares across grains shows up here as a flat curve. No
// done channels are needed because AskGrain is synchronous: when every
// asker has returned, every message has been processed.
func BenchmarkGrainAskPairwise(b *testing.B) {
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
	identities := make([]*actor.GrainIdentity, pairs)
	targets := make([]int64, pairs)
	base := int64(b.N) / int64(pairs)
	extra := int64(b.N) % int64(pairs)

	for i := range pairs {
		target := base
		if int64(i) < extra {
			target++
		}

		identity, err := actor.GrainOf[*benchGrain](ctx, actorSystem, fmt.Sprintf("receiver-%d", i))
		if err != nil {
			b.Fatalf("failed to create grain identity: %v", err)
		}

		identities[i], targets[i] = identity, target
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
				if _, err := actorSystem.AskGrain(ctx, identities[i], msg, time.Second); err != nil {
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

// send sends the client's next batch, at most the messages it has left.
func (x *pingPongClient) send(ctx *actor.ReceiveContext) {
	n := min(x.batch, x.remaining)
	x.remaining -= n
	x.outstanding = n
	for range n {
		ctx.Tell(x.echo, x.message)
	}
}
