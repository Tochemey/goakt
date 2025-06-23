/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package bench

import (
	"context"
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	goakt "github.com/tochemey/goakt/v3/actor"
	"github.com/tochemey/goakt/v3/bench/benchpb"
	"github.com/tochemey/goakt/v3/internal/util"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/passivation"
)

const receivingTimeout = 100 * time.Millisecond

func BenchmarkActor(b *testing.B) {
	b.Run("Tell(api:default mailbox)", func(b *testing.B) {
		ctx := context.TODO()

		// create the actor system
		actorSystem, _ := goakt.NewActorSystem("bench",
			goakt.WithLogger(log.DiscardLogger),
			goakt.WithActorInitMaxRetries(1))

		// start the actor system
		_ = actorSystem.Start(ctx)

		// wait for system to start properly
		util.Pause(1 * time.Second)

		// define the benchmark actor
		actor := &Actor{}

		// create the actor ref
		pid, _ := actorSystem.Spawn(ctx, "test", actor)

		// wait for goakt to start properly
		util.Pause(1 * time.Second)

		var counter int64
		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				if err := goakt.Tell(ctx, pid, new(benchpb.BenchTell)); err != nil {
					b.Fatal(err)
				}
				atomic.AddInt64(&counter, 1)
			}
		})

		b.StopTimer()

		messagesPerSec := float64(atomic.LoadInt64(&counter)) / b.Elapsed().Seconds()
		b.ReportMetric(messagesPerSec, "messages/sec")

		_ = pid.Shutdown(ctx)
		_ = actorSystem.Stop(ctx)
	})
	b.Run("Tell(actor-2-actor)", func(b *testing.B) {
		ctx := context.TODO()

		// create the actor system
		actorSystem, _ := goakt.NewActorSystem("bench",
			goakt.WithLogger(log.DiscardLogger),
			goakt.WithActorInitMaxRetries(1))

		// start the actor system
		_ = actorSystem.Start(ctx)

		// wait for system to start properly
		util.Pause(1 * time.Second)

		// create the goakt
		sender, _ := actorSystem.Spawn(ctx, "sender", new(Actor))
		receiver, _ := actorSystem.Spawn(ctx, "receiver", new(Actor))

		// wait for goakt to start properly
		util.Pause(1 * time.Second)
		var counter int64
		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				err := sender.Tell(ctx, receiver, new(benchpb.BenchTell))
				if err != nil {
					b.Fatal(err)
				}
				atomic.AddInt64(&counter, 1)
			}
		})
		b.StopTimer()
		messagesPerSec := float64(atomic.LoadInt64(&counter)) / b.Elapsed().Seconds()
		b.ReportMetric(messagesPerSec, "messages/sec")
		_ = actorSystem.Stop(ctx)
	})
	b.Run("Tell(bounded mailbox)", func(b *testing.B) {
		ctx := context.TODO()

		// create the actor system
		actorSystem, _ := goakt.NewActorSystem("bench",
			goakt.WithLogger(log.DiscardLogger),
			goakt.WithActorInitMaxRetries(1))

		// start the actor system
		_ = actorSystem.Start(ctx)

		// wait for system to start properly
		util.Pause(1 * time.Second)

		// create the goakt
		sender, _ := actorSystem.Spawn(ctx, "sender", new(Actor), goakt.WithMailbox(goakt.NewBoundedMailbox(b.N)))
		receiver, _ := actorSystem.Spawn(ctx, "receiver", new(Actor), goakt.WithMailbox(goakt.NewBoundedMailbox(b.N)))

		// wait for goakt to start properly
		util.Pause(1 * time.Second)
		var counter int64
		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				err := sender.Tell(ctx, receiver, new(benchpb.BenchTell))
				if err != nil {
					b.Fatal(err)
				}
				atomic.AddInt64(&counter, 1)
			}
		})
		b.StopTimer()
		messagesPerSec := float64(atomic.LoadInt64(&counter)) / b.Elapsed().Seconds()
		b.ReportMetric(messagesPerSec, "messages/sec")
		_ = actorSystem.Stop(ctx)
	})
	b.Run("Tell(priority mailbox)", func(b *testing.B) {
		ctx := context.TODO()

		priorityFunc := func(msg1, msg2 proto.Message) bool {
			p1 := msg1.(*benchpb.BenchPriorityMailbox)
			p2 := msg2.(*benchpb.BenchPriorityMailbox)
			return p1.Priority > p2.Priority
		}

		// create the actor system
		actorSystem, _ := goakt.NewActorSystem("bench",
			goakt.WithLogger(log.DiscardLogger),
			goakt.WithActorInitMaxRetries(1))

		// start the actor system
		_ = actorSystem.Start(ctx)

		// wait for system to start properly
		util.Pause(1 * time.Second)

		// create the goakt
		sender, _ := actorSystem.Spawn(ctx, "sender", new(Actor), goakt.WithMailbox(goakt.NewUnboundedPriorityMailBox(priorityFunc)))
		receiver, _ := actorSystem.Spawn(ctx, "receiver", new(Actor), goakt.WithMailbox(goakt.NewUnboundedPriorityMailBox(priorityFunc)))

		// wait for goakt to start properly
		util.Pause(1 * time.Second)
		var counter int64
		b.ResetTimer()
		b.ReportAllocs()
		var priorityCounter atomic.Int64
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				priorityCounter.Add(1)
				priorityMessage := &benchpb.BenchPriorityMailbox{
					Priority: priorityCounter.Load(),
				}
				err := sender.Tell(ctx, receiver, priorityMessage)
				if err != nil {
					b.Fatal(err)
				}
				atomic.AddInt64(&counter, 1)
			}
		})
		b.StopTimer()
		messagesPerSec := float64(atomic.LoadInt64(&counter)) / b.Elapsed().Seconds()
		b.ReportMetric(messagesPerSec, "messages/sec")
		_ = actorSystem.Stop(ctx)
	})
	b.Run("SendAsync(default mailbox)", func(b *testing.B) {
		ctx := context.TODO()

		// create the actor system
		actorSystem, _ := goakt.NewActorSystem("bench",
			goakt.WithLogger(log.DiscardLogger),
			goakt.WithActorInitMaxRetries(1))

		// start the actor system
		_ = actorSystem.Start(ctx)

		// wait for system to start properly
		util.Pause(1 * time.Second)

		// create the goakt
		sender, _ := actorSystem.Spawn(ctx, "sender", new(Actor))
		receiver, _ := actorSystem.Spawn(ctx, "receiver", new(Actor))

		// wait for goakt to start properly
		util.Pause(1 * time.Second)
		var counter int64
		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				if err := sender.SendAsync(ctx, receiver.Name(), new(benchpb.BenchTell)); err != nil {
					b.Fatal(err)
				}
				atomic.AddInt64(&counter, 1)
			}
		})
		b.StopTimer()

		messagesPerSec := float64(atomic.LoadInt64(&counter)) / b.Elapsed().Seconds()
		b.ReportMetric(messagesPerSec, "messages/sec")

		_ = actorSystem.Stop(ctx)
	})
	b.Run("Ask(api:default mailbox)", func(b *testing.B) {
		ctx := context.TODO()
		// create the actor system
		actorSystem, _ := goakt.NewActorSystem("bench",
			goakt.WithLogger(log.DiscardLogger),
			goakt.WithActorInitMaxRetries(1))

		// start the actor system
		_ = actorSystem.Start(ctx)

		// wait for system to start properly
		util.Pause(1 * time.Second)

		// define the benchmark actor
		actor := &Actor{}

		// create the actor ref
		pid, _ := actorSystem.Spawn(ctx, "test", actor,
			goakt.WithPassivationStrategy(
				passivation.NewTimeBasedStrategy(5*time.Second),
			))

		// wait for goakt to start properly
		util.Pause(1 * time.Second)
		var counter int64
		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				if _, err := goakt.Ask(ctx, pid, new(benchpb.BenchRequest), receivingTimeout); err != nil {
					b.Fatal(err)
				}
				atomic.AddInt64(&counter, 1)
			}
		})
		b.StopTimer()

		messagesPerSec := float64(atomic.LoadInt64(&counter)) / b.Elapsed().Seconds()
		b.ReportMetric(messagesPerSec, "messages/sec")

		_ = pid.Shutdown(ctx)
		_ = actorSystem.Stop(ctx)
	})
	b.Run("Ask(actor-2-actor)", func(b *testing.B) {
		ctx := context.TODO()

		// create the actor system
		actorSystem, _ := goakt.NewActorSystem("bench",
			goakt.WithLogger(log.DiscardLogger),
			goakt.WithActorInitMaxRetries(1))

		// start the actor system
		_ = actorSystem.Start(ctx)

		// wait for system to start properly
		util.Pause(1 * time.Second)

		// create the goakt
		sender, _ := actorSystem.Spawn(ctx, "sender", new(Actor))
		receiver, _ := actorSystem.Spawn(ctx, "receiver", new(Actor))

		// wait for goakt to start properly
		util.Pause(1 * time.Second)

		var counter int64
		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				if _, err := sender.Ask(ctx, receiver, new(benchpb.BenchRequest), receivingTimeout); err != nil {
					b.Fatal(err)
				}
				atomic.AddInt64(&counter, 1)
			}
		})
		b.StopTimer()

		messagesPerSec := float64(atomic.LoadInt64(&counter)) / b.Elapsed().Seconds()
		b.ReportMetric(messagesPerSec, "messages/sec")

		_ = actorSystem.Stop(ctx)
	})
	b.Run("Ask(bounded mailbox)", func(b *testing.B) {
		ctx := context.TODO()

		// create the actor system
		actorSystem, _ := goakt.NewActorSystem("bench",
			goakt.WithLogger(log.DiscardLogger),
			goakt.WithActorInitMaxRetries(1))

		// start the actor system
		_ = actorSystem.Start(ctx)

		// wait for system to start properly
		util.Pause(1 * time.Second)

		// create the goakt
		sender, _ := actorSystem.Spawn(ctx, "sender", new(Actor), goakt.WithMailbox(goakt.NewBoundedMailbox(b.N)))
		receiver, _ := actorSystem.Spawn(ctx, "receiver", new(Actor), goakt.WithMailbox(goakt.NewBoundedMailbox(b.N)))

		// wait for goakt to start properly
		util.Pause(1 * time.Second)

		var counter int64
		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				if _, err := sender.Ask(ctx, receiver, new(benchpb.BenchRequest), receivingTimeout); err != nil {
					b.Fatal(err)
				}
				atomic.AddInt64(&counter, 1)
			}
		})
		b.StopTimer()

		messagesPerSec := float64(atomic.LoadInt64(&counter)) / b.Elapsed().Seconds()
		b.ReportMetric(messagesPerSec, "messages/sec")

		_ = actorSystem.Stop(ctx)
	})
}

func BenchmarkGrain(b *testing.B) {
	b.Run("TellGrain", func(b *testing.B) {
		ctx := b.Context()
		actorSystem, _ := goakt.NewActorSystem("testSys", goakt.WithLogger(log.DiscardLogger))

		// start the actor system
		_ = actorSystem.Start(ctx)

		// wait for system to start properly
		util.Pause(1 * time.Second)

		// create a grain instance
		grain := NewGrain()
		identity, _ := actorSystem.GetGrain(ctx, "benchGrain", func(_ context.Context) (goakt.Grain, error) {
			return grain, nil
		})

		util.Pause(1 * time.Second)
		var counter int64
		b.ResetTimer()
		b.ReportAllocs()

		for n := 0; n < b.N; n++ {
			err := actorSystem.TellGrain(ctx, identity, new(benchpb.BenchTell))
			if err != nil {
				b.Fatal(err)
			}
			atomic.AddInt64(&counter, 1)
		}

		b.StopTimer()
		messagesPerSec := float64(atomic.LoadInt64(&counter)) / b.Elapsed().Seconds()
		b.ReportMetric(messagesPerSec, "messages/sec")
		_ = actorSystem.Stop(ctx)
	})
}

func TestBenchTell(t *testing.T) {
	ctx := context.TODO()

	toSend := 10_000_000

	benchmark := NewBenchmark(toSend)
	require.NoError(t, benchmark.Start(ctx))

	fmt.Printf("Starting benchmark...\n")
	startTime := time.Now()
	if err := benchmark.BenchTell(ctx); err != nil {
		t.Fatal(err)
	}

	duration := time.Since(startTime)
	metric := benchmark.ActorRef().Metric(ctx)
	processedCount := metric.ProcessedCount()

	fmt.Printf("Go Version: %s\n", runtime.Version())
	fmt.Printf("Runtime CPUs: %d\n", runtime.NumCPU())
	fmt.Printf("Total messages sent: (%d) - duration: (%v)\n", toSend, duration)
	fmt.Printf("Total messages processed: (%d) - duration: (%v)\n", processedCount, duration)
	fmt.Printf("Messages per second: (%d)\n", int64(processedCount)/int64(duration.Seconds()))
	require.NoError(t, benchmark.Stop(ctx))
}

func TestBenchAsk(t *testing.T) {
	ctx := context.TODO()

	toSend := 10_000_000

	benchmark := NewBenchmark(toSend)
	require.NoError(t, benchmark.Start(ctx))

	fmt.Printf("Starting benchmark....\n")
	startTime := time.Now()
	if err := benchmark.BenchAsk(ctx); err != nil {
		t.Fatal(err)
	}

	duration := time.Since(startTime)
	metric := benchmark.ActorRef().Metric(ctx)
	processedCount := metric.ProcessedCount()

	fmt.Printf("Go Version: %s\n", runtime.Version())
	fmt.Printf("Runtime CPUs: %d\n", runtime.NumCPU())
	fmt.Printf("Total messages sent: (%d) - duration: (%v)\n", toSend, duration)
	fmt.Printf("Total messages processed: (%d) - duration: (%v)\n", processedCount, duration)
	fmt.Printf("Messages per second: (%d)\n", int64(processedCount)/int64(duration.Seconds()))
	require.NoError(t, benchmark.Stop(ctx))
}
