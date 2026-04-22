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
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/test/data/testpb"
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

// countingActor counts TestSend deliveries and closes done exactly once
// when received reaches target. received is a plain int64 because an
// actor's Receive is invoked serially by a single dispatcher worker;
// no synchronisation is required.
type countingActor struct {
	received int64
	done     chan struct{}
	target   int64
}

func (a *countingActor) PreStart(*actor.Context) error { return nil }

func (a *countingActor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
		a.received++
		if a.received == a.target {
			close(a.done)
		}
	default:
		ctx.Unhandled()
	}
}

func (a *countingActor) PostStop(*actor.Context) error { return nil }

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
		call.Then(func(_ any, err error) {
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
	done := make(chan struct{})
	receiver, err := actorSystem.Spawn(ctx, "receiver", &countingActor{
		done:   done,
		target: int64(b.N),
	})
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
	<-done
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
	done := make(chan struct{})
	if _, err = actorSystem.Spawn(ctx, "receiver", &countingActor{
		done:   done,
		target: int64(b.N),
	}); err != nil {
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
	<-done
	b.StopTimer()
	messagesPerSec := float64(b.N) / b.Elapsed().Seconds()
	b.ReportMetric(messagesPerSec, "messages/sec")
}

// benchGrain is a minimal Grain used by the throughput benchmarks. It
// performs the same dispatch work as Actor (no business logic), so the
// numbers reflect the dispatcher + mailbox path rather than user code.
type benchGrain struct{}

func (*benchGrain) OnActivate(context.Context, *actor.GrainProps) error   { return nil }
func (*benchGrain) OnDeactivate(context.Context, *actor.GrainProps) error { return nil }

func (*benchGrain) OnReceive(ctx *actor.GrainContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
		ctx.NoErr()
	case *testpb.TestReply:
		ctx.Response(replyMessage)
	default:
		ctx.Unhandled()
	}
}

// countingGrain is the grain counterpart of countingActor. received is a
// plain int64 because a grain's OnReceive is invoked serially on a
// single dispatcher worker; no synchronisation is required.
type countingGrain struct {
	received int64
	done     chan struct{}
	target   int64
}

func (*countingGrain) OnActivate(context.Context, *actor.GrainProps) error   { return nil }
func (*countingGrain) OnDeactivate(context.Context, *actor.GrainProps) error { return nil }

func (g *countingGrain) OnReceive(ctx *actor.GrainContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
		g.received++
		if g.received == g.target {
			close(g.done)
		}
		ctx.NoErr()
	default:
		ctx.Unhandled()
	}
}

// BenchmarkGrainTell measures fire-and-forget throughput against a
// single grain. Producers race on the same mailbox + schedState CAS.
// Migrating grains onto the dispatcher pool removes the per-burst
// goroutine spawn from this hot path; the remaining cost is the
// mailbox enqueue and one schedule per idle->busy transition.
func BenchmarkGrainTell(b *testing.B) {
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
			if err := actorSystem.TellGrain(ctx, identity, msg); err != nil {
				b.Fatal(err)
			}
		}
	})
	<-done
	b.StopTimer()
	messagesPerSec := float64(b.N) / b.Elapsed().Seconds()
	b.ReportMetric(messagesPerSec, "messages/sec")
}

// BenchmarkGrainTellFanOut spreads load across many grains. This is the
// case where the dispatcher pool change matters most: under the old
// per-burst goroutine model, each active grain spawned its own
// goroutine, so N concurrent grains = N goroutines. With the shared
// pool, goroutine count is bounded by GOMAXPROCS regardless of N.
func BenchmarkGrainTellFanOut(b *testing.B) {
	const grainCount = 256
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

	identities, dones := setupFanOutGrains(b, ctx, actorSystem, grainCount)

	var counter atomic.Uint64

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		msg := new(testpb.TestSend)
		for pb.Next() {
			idx := counter.Add(1) % grainCount
			if err := actorSystem.TellGrain(ctx, identities[idx], msg); err != nil {
				b.Fatal(err)
			}
		}
	})
	for _, d := range dones {
		<-d
	}
	b.StopTimer()
	messagesPerSec := float64(b.N) / b.Elapsed().Seconds()
	b.ReportMetric(messagesPerSec, "messages/sec")
}

// setupFanOutGrains provisions grainCount grains for the round-robin
// fan-out benchmarks. Each grain gets a private int64 counter, a private
// done channel, and a target computed from the round-robin distribution
// counter.Add(1) % grainCount over b.N messages:
//   - grain 0 receives messages at positions grainCount, 2*grainCount, ...
//   - grain i (1..grainCount-1) receives messages at positions i, i+grainCount, ...
//
// The returned slices are aligned: dones[i] completes when identities[i]
// has received all its messages.
func setupFanOutGrains(b *testing.B, ctx context.Context, system actor.ActorSystem, grainCount uint64) ([]*actor.GrainIdentity, []chan struct{}) {
	n := uint64(b.N)
	identities := make([]*actor.GrainIdentity, grainCount)
	dones := make([]chan struct{}, grainCount)
	for i := range grainCount {
		var target int64
		if i == 0 {
			target = int64(n / grainCount)
		} else if i <= n {
			target = int64((n-i)/grainCount) + 1
		}
		done := make(chan struct{})
		if target == 0 {
			close(done)
		}
		id, err := system.GrainIdentity(ctx, fmt.Sprintf("g-%d", i), func(context.Context) (actor.Grain, error) {
			return &countingGrain{done: done, target: target}, nil
		})
		if err != nil {
			b.Fatalf("failed to create grain identity: %v", err)
		}
		identities[i] = id
		dones[i] = done
	}
	return identities, dones
}

// BenchmarkGrainAsk measures synchronous request/response latency.
// Sequential to avoid mailbox contention on a single grain — same
// rationale as BenchmarkAsk for actors.
func BenchmarkGrainAsk(b *testing.B) {
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

	identity, err := actorSystem.GrainIdentity(ctx, "receiver", func(context.Context) (actor.Grain, error) {
		return &benchGrain{}, nil
	})
	if err != nil {
		b.Fatalf("failed to create grain identity: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	msg := new(testpb.TestReply)
	for i := 0; i < b.N; i++ {
		if _, err := actorSystem.AskGrain(ctx, identity, msg, time.Second); err != nil {
			b.Fatal(err)
		}
	}
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

// throughputBudgets is the sweep used by the *Throughput variants of the
// benchmarks below. It spans from below-default (fair / latency-friendly) to
// well above default (throughput-oriented) so the curve is visible.
var throughputBudgets = []int{8, 32, 64, 128, 256}

// BenchmarkTellThroughput is a variant of BenchmarkTell that sweeps the
// dispatcher's per-turn message budget via WithThroughputBudget. Everything else
// (workload, parallelism, message shape) is held constant so the reported
// messages/sec deltas reflect only the scheduler knob.
func BenchmarkTellThroughput(b *testing.B) {
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
			done := make(chan struct{})
			receiver, err := actorSystem.Spawn(ctx, "receiver", &countingActor{
				done:   done,
				target: int64(b.N),
			})
			if err != nil {
				b.Fatalf("failed to spawn receiver: %v", err)
			}

			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				msg := new(testpb.TestSend)
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

// BenchmarkGrainTellThroughput is a variant of BenchmarkGrainTell that sweeps
// the dispatcher's per-turn message budget. All producers race on the same
// grain mailbox, so the budget controls how many messages the worker drains
// before yielding back to the scheduler.
func BenchmarkGrainTellThroughput(b *testing.B) {
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

			done := make(chan struct{})
			target := int64(b.N)
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
					if err := actorSystem.TellGrain(ctx, identity, msg); err != nil {
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

// BenchmarkGrainTellFanOutThroughput is a variant of BenchmarkGrainTellFanOut
// that sweeps the dispatcher's per-turn message budget. With 256 grains and
// work spread round-robin, the budget's fairness vs amortization trade-off is
// most visible: higher values reduce scheduling overhead but let individual
// grains hold workers longer before siblings get a turn.
func BenchmarkGrainTellFanOutThroughput(b *testing.B) {
	const grainCount = 256
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

			identities, dones := setupFanOutGrains(b, ctx, actorSystem, grainCount)

			var counter atomic.Uint64

			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				msg := new(testpb.TestSend)
				for pb.Next() {
					idx := counter.Add(1) % grainCount
					if err := actorSystem.TellGrain(ctx, identities[idx], msg); err != nil {
						b.Fatal(err)
					}
				}
			})
			for _, d := range dones {
				<-d
			}
			b.StopTimer()
			messagesPerSec := float64(b.N) / b.Elapsed().Seconds()
			b.ReportMetric(messagesPerSec, "messages/sec")
		})
	}
}

// BenchmarkSendSyncThroughput is a variant of BenchmarkSendSync that sweeps
// the dispatcher's per-turn message budget. SendSync is synchronous, so
// per-call latency dominates; the budget's effect is smaller than on Tell but
// still visible at higher values where cache locality on the receiver improves.
func BenchmarkSendSyncThroughput(b *testing.B) {
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
			_, err = actorSystem.Spawn(ctx, "receiver", new(Actor))
			if err != nil {
				b.Fatalf("failed to spawn receiver: %v", err)
			}

			b.ReportAllocs()
			b.ResetTimer()
			msg := new(testpb.TestReply)
			for i := 0; i < b.N; i++ {
				if _, err := sender.SendSync(ctx, "receiver", msg, time.Second); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			messagesPerSec := float64(b.N) / b.Elapsed().Seconds()
			b.ReportMetric(messagesPerSec, "messages/sec")
		})
	}
}
