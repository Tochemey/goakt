/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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
	"testing"
	"time"

	"github.com/tochemey/goakt/v2/actors"
	"github.com/tochemey/goakt/v2/bench/benchmarkpb"
	"github.com/tochemey/goakt/v2/internal/types"
	"github.com/tochemey/goakt/v2/log"
)

const receivingTimeout = 100 * time.Millisecond

func BenchmarkActor(b *testing.B) {
	b.Run("Tell(api)", func(b *testing.B) {
		ctx := context.TODO()

		// create the actor system
		actorSystem, _ := actors.NewActorSystem("bench",
			actors.WithLogger(log.DiscardLogger),
			actors.WithActorInitMaxRetries(1),
			actors.WithSupervisorDirective(actors.NewStopDirective()),
			actors.WithReplyTimeout(receivingTimeout))

		// start the actor system
		_ = actorSystem.Start(ctx)

		// wait for system to start properly
		pause(1 * time.Second)

		// define the benchmark actor
		actor := &Benchmarker{}

		// create the actor ref
		pid, _ := actorSystem.Spawn(ctx, "test", actor)

		// wait for actors to start properly
		pause(1 * time.Second)

		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_ = actors.Tell(ctx, pid, new(benchmarkpb.BenchTell))
			}
		})

		_ = pid.Shutdown(ctx)
		_ = actorSystem.Stop(ctx)
	})
	b.Run("Tell", func(b *testing.B) {
		ctx := context.TODO()

		// create the actor system
		actorSystem, _ := actors.NewActorSystem("bench",
			actors.WithLogger(log.DiscardLogger),
			actors.WithActorInitMaxRetries(1),
			actors.WithSupervisorDirective(actors.NewStopDirective()),
			actors.WithReplyTimeout(receivingTimeout))

		// start the actor system
		_ = actorSystem.Start(ctx)

		// wait for system to start properly
		pause(1 * time.Second)

		// create the actors
		sender, _ := actorSystem.Spawn(ctx, "sender", new(Benchmarker))
		receiver, _ := actorSystem.Spawn(ctx, "receiver", new(Benchmarker))

		// wait for actors to start properly
		pause(1 * time.Second)

		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_ = sender.Tell(ctx, receiver, new(benchmarkpb.BenchTell))
			}
		})

		_ = actorSystem.Stop(ctx)
	})
	b.Run("SendAsync", func(b *testing.B) {
		ctx := context.TODO()

		// create the actor system
		actorSystem, _ := actors.NewActorSystem("bench",
			actors.WithLogger(log.DiscardLogger),
			actors.WithActorInitMaxRetries(1),
			actors.WithSupervisorDirective(actors.NewStopDirective()),
			actors.WithReplyTimeout(receivingTimeout))

		// start the actor system
		_ = actorSystem.Start(ctx)

		// wait for system to start properly
		pause(1 * time.Second)

		// create the actors
		sender, _ := actorSystem.Spawn(ctx, "sender", new(Benchmarker))
		receiver, _ := actorSystem.Spawn(ctx, "receiver", new(Benchmarker))

		// wait for actors to start properly
		pause(1 * time.Second)
		b.ResetTimer()
		b.ReportAllocs()

		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_ = sender.SendAsync(ctx, receiver.Name(), new(benchmarkpb.BenchTell))
			}
		})

		_ = actorSystem.Stop(ctx)
	})
	b.Run("Ask(api)", func(b *testing.B) {
		ctx := context.TODO()
		// create the actor system
		actorSystem, _ := actors.NewActorSystem("bench",
			actors.WithLogger(log.DiscardLogger),
			actors.WithActorInitMaxRetries(1),
			actors.WithExpireActorAfter(5*time.Second),
			actors.WithReplyTimeout(receivingTimeout))

		// start the actor system
		_ = actorSystem.Start(ctx)

		// wait for system to start properly
		pause(1 * time.Second)

		// define the benchmark actor
		actor := &Benchmarker{}

		// create the actor ref
		pid, _ := actorSystem.Spawn(ctx, "test", actor)

		// wait for actors to start properly
		pause(1 * time.Second)
		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_, _ = actors.Ask(ctx, pid, new(benchmarkpb.BenchRequest), receivingTimeout)
			}
		})

		_ = pid.Shutdown(ctx)
		_ = actorSystem.Stop(ctx)
	})
	b.Run("Ask", func(b *testing.B) {
		ctx := context.TODO()

		// create the actor system
		actorSystem, _ := actors.NewActorSystem("bench",
			actors.WithLogger(log.DiscardLogger),
			actors.WithActorInitMaxRetries(1),
			actors.WithSupervisorDirective(actors.NewStopDirective()),
			actors.WithReplyTimeout(receivingTimeout))

		// start the actor system
		_ = actorSystem.Start(ctx)

		// wait for system to start properly
		pause(1 * time.Second)

		// create the actors
		sender, _ := actorSystem.Spawn(ctx, "sender", new(Benchmarker))
		receiver, _ := actorSystem.Spawn(ctx, "receiver", new(Benchmarker))

		// wait for actors to start properly
		pause(1 * time.Second)

		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_, _ = sender.Ask(ctx, receiver, new(benchmarkpb.BenchRequest))
			}
		})
		_ = actorSystem.Stop(ctx)
	})
}

func pause(duration time.Duration) {
	bootstrapChan := make(chan types.Unit, 1)
	timer := time.AfterFunc(duration, func() {
		bootstrapChan <- types.Unit{}
	})
	<-bootstrapChan
	timer.Stop()
}
