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

package bench

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/actor"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/reentrancy"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

type Actor struct{}

var replyMessage = &testpb.Reply{Content: "reply"}
var errRequestBenchFailed = errors.New("request benchmark failed")

func (a *Actor) PreStart(*actor.Context) error {
	return nil
}

func (a *Actor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
	case *testpb.TestReply:
		ctx.Response(replyMessage)
	default:
		ctx.Unhandled()
	}
}

func (a *Actor) PostStop(*actor.Context) error {
	return nil
}

type requestBenchActor struct {
	target *actor.PID
	doneCh chan struct{}
	errCh  chan error
}

func (a *requestBenchActor) PreStart(*actor.Context) error { return nil }

func (a *requestBenchActor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
		call := ctx.Request(a.target, new(testpb.TestReply))
		if call == nil {
			a.signalError(errRequestBenchFailed)
			a.signalDone()
			return
		}
		call.Then(func(_ proto.Message, err error) {
			if err != nil {
				a.signalError(err)
			}
			a.signalDone()
		})
	default:
		ctx.Unhandled()
	}
}

func (a *requestBenchActor) PostStop(*actor.Context) error { return nil }

func (a *requestBenchActor) signalDone() {
	a.doneCh <- struct{}{}
}

func (a *requestBenchActor) signalError(err error) {
	if err == nil {
		return
	}
	select {
	case a.errCh <- err:
	default:
	}
}

func BenchmarkTell(b *testing.B) {
	ctx := context.Background()

	// create the actor system
	actorSystem, err := actor.NewActorSystem("bench",
		actor.WithLogger(log.DiscardLogger),
		actor.WithActorInitMaxRetries(1))
	if err != nil {
		b.Fatalf("failed to create actor system: %v", err)
	}

	// start the actor system
	if err := actorSystem.Start(ctx); err != nil {
		b.Fatalf("failed to start actor system: %v", err)
	}
	b.Cleanup(func() { _ = actorSystem.Stop(ctx) })

	// create the actor refs
	sender, err := actorSystem.Spawn(ctx, "sender", new(Actor))
	if err != nil {
		b.Fatalf("failed to spawn sender: %v", err)
	}
	receiver, err := actorSystem.Spawn(ctx, "receiver", new(Actor))
	if err != nil {
		b.Fatalf("failed to spawn receiver: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		// Reuse the same message per goroutine to reduce allocs in the hot path.
		msg := new(testpb.TestSend)
		for pb.Next() {
			if err := sender.Tell(ctx, receiver, msg); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.StopTimer()
	messagesPerSec := float64(b.N) / b.Elapsed().Seconds()
	b.ReportMetric(messagesPerSec, "messages/sec")
}

func BenchmarkRequest(b *testing.B) {
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
}

func BenchmarkAsk(b *testing.B) {
	ctx := context.Background()

	// create the actor system
	actorSystem, err := actor.NewActorSystem("bench",
		actor.WithLogger(log.DiscardLogger),
		actor.WithActorInitMaxRetries(1))
	if err != nil {
		b.Fatalf("failed to create actor system: %v", err)
	}

	// start the actor system
	if err := actorSystem.Start(ctx); err != nil {
		b.Fatalf("failed to start actor system: %v", err)
	}
	b.Cleanup(func() { _ = actorSystem.Stop(ctx) })

	// create the actor refs
	sender, err := actorSystem.Spawn(ctx, "sender", new(Actor))
	if err != nil {
		b.Fatalf("failed to spawn sender: %v", err)
	}
	receiver, err := actorSystem.Spawn(ctx, "receiver", new(Actor))
	if err != nil {
		b.Fatalf("failed to spawn receiver: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	// Use sequential execution for Ask benchmark instead of parallel.
	// Ask is inherently slower than Tell due to synchronous response waiting,
	// and parallel execution causes mailbox contention where a single receiver
	// cannot keep up with multiple parallel senders, leading to timeouts.
	// Sequential execution provides a more realistic and stable benchmark.
	msg := new(testpb.TestReply)
	for i := 0; i < b.N; i++ {
		if _, err := sender.Ask(ctx, receiver, msg, time.Second); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	messagesPerSec := float64(b.N) / b.Elapsed().Seconds()
	b.ReportMetric(messagesPerSec, "messages/sec")
}

func BenchmarkSendAsync(b *testing.B) {
	ctx := context.Background()

	// create the actor system
	actorSystem, err := actor.NewActorSystem("bench",
		actor.WithLogger(log.DiscardLogger),
		actor.WithActorInitMaxRetries(1))
	if err != nil {
		b.Fatalf("failed to create actor system: %v", err)
	}

	// start the actor system
	if err := actorSystem.Start(ctx); err != nil {
		b.Fatalf("failed to start actor system: %v", err)
	}
	b.Cleanup(func() { _ = actorSystem.Stop(ctx) })

	// create the actor refs
	sender, err := actorSystem.Spawn(ctx, "sender", new(Actor))
	if err != nil {
		b.Fatalf("failed to spawn sender: %v", err)
	}
	_, err = actorSystem.Spawn(ctx, "receiver", new(Actor))
	if err != nil {
		b.Fatalf("failed to spawn receiver: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		// Reuse the same message per goroutine to reduce allocs in the hot path.
		msg := new(testpb.TestSend)
		for pb.Next() {
			if err := sender.SendAsync(ctx, "receiver", msg); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.StopTimer()
	messagesPerSec := float64(b.N) / b.Elapsed().Seconds()
	b.ReportMetric(messagesPerSec, "messages/sec")
}

func BenchmarkSendSync(b *testing.B) {
	ctx := context.Background()

	// create the actor system
	actorSystem, err := actor.NewActorSystem("bench",
		actor.WithLogger(log.DiscardLogger),
		actor.WithActorInitMaxRetries(1))
	if err != nil {
		b.Fatalf("failed to create actor system: %v", err)
	}

	// start the actor system
	if err := actorSystem.Start(ctx); err != nil {
		b.Fatalf("failed to start actor system: %v", err)
	}
	b.Cleanup(func() { _ = actorSystem.Stop(ctx) })

	// create the actor refs
	sender, err := actorSystem.Spawn(ctx, "sender", new(Actor))
	if err != nil {
		b.Fatalf("failed to spawn sender: %v", err)
	}
	_, err = actorSystem.Spawn(ctx, "receiver", new(Actor))
	if err != nil {
		b.Fatalf("failed to spawn receiver: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	// Use sequential execution for SendSync benchmark instead of parallel.
	// SendSync is inherently slower than SendAsync due to synchronous response waiting,
	// and parallel execution causes mailbox contention where a single receiver
	// cannot keep up with multiple parallel senders, leading to timeouts.
	// Sequential execution provides a more realistic and stable benchmark.
	msg := new(testpb.TestReply)
	for i := 0; i < b.N; i++ {
		if _, err := sender.SendSync(ctx, "receiver", msg, time.Second); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	messagesPerSec := float64(b.N) / b.Elapsed().Seconds()
	b.ReportMetric(messagesPerSec, "messages/sec")
}
