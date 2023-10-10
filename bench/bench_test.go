/*
 * MIT License
 *
 * Copyright (c) 2022-2023 Tochemey
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

	"github.com/tochemey/goakt/actors"
	"github.com/tochemey/goakt/log"
	testspb "github.com/tochemey/goakt/test/data/pb/v1"
)

const (
	receivingTimeout = 100 * time.Millisecond
)

// Benchmarker is an actor that helps run benchmark tests
type Benchmarker struct {
}

func (p *Benchmarker) PreStart(context.Context) error {
	return nil
}

func (p *Benchmarker) Receive(ctx actors.ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.TestSend:
	case *testspb.TestReply:
		ctx.Response(&testspb.Reply{Content: "received message"})
	}
}

func (p *Benchmarker) PostStop(context.Context) error {
	return nil
}

func BenchmarkActor(b *testing.B) {
	b.Run("tell(send only)", func(b *testing.B) {
		ctx := context.TODO()

		// create the actor system
		actorSystem, _ := actors.NewActorSystem("testSys",
			actors.WithLogger(log.DiscardLogger),
			actors.WithActorInitMaxRetries(1),
			actors.WithMailboxSize(uint64(b.N)),
			actors.WithReplyTimeout(receivingTimeout))

		// start the actor system
		_ = actorSystem.Start(ctx)

		// define the benchmark actor
		actor := &Benchmarker{}

		// create the actor ref
		pid, _ := actorSystem.Spawn(ctx, "test", actor)

		b.ResetTimer() // Reset the benchmark timer
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				// send a message to the actor
				_ = actors.Tell(ctx, pid, &testspb.TestSend{})
			}
		})

		_ = pid.Shutdown(ctx)
		_ = actorSystem.Stop(ctx)
	})
	b.Run("ask(send/reply)", func(b *testing.B) {
		ctx := context.TODO()
		// create the actor system
		actorSystem, _ := actors.NewActorSystem("testSys",
			actors.WithLogger(log.DiscardLogger),
			actors.WithActorInitMaxRetries(1),
			actors.WithMailboxSize(uint64(b.N)),
			actors.WithExpireActorAfter(5*time.Second),
			actors.WithReplyTimeout(receivingTimeout))

		// start the actor system
		_ = actorSystem.Start(ctx)

		// define the benchmark actor
		actor := &Benchmarker{}

		// create the actor ref
		pid, _ := actorSystem.Spawn(ctx, "test", actor)

		b.ResetTimer() // Reset the benchmark timer
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				// send a message to the actor
				_, _ = actors.Ask(ctx, pid, new(testspb.TestReply), receivingTimeout)
			}
		})
		_ = pid.Shutdown(ctx)
		_ = actorSystem.Stop(ctx)
	})
}
