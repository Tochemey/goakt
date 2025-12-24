/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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

package testkit

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/tochemey/goakt/v3/actor"
	"github.com/tochemey/goakt/v3/internal/pause"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

func TestGrainProbe(t *testing.T) {
	t.Run("Assert no response received", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create a test kit
		testkit := New(ctx, t, WithLogging(log.ErrorLevel))

		// get the grain identity
		id := testkit.GrainIdentity(ctx, "grain", func(ctx context.Context) (actor.Grain, error) {
			return &grain{}, nil
		})

		// create the test probe
		probe := testkit.NewGrainProbe(ctx)
		// send a message to the grain to be tested
		probe.Send(id, new(testpb.TestPing))

		// here we expect no response from the grain
		probe.ExpectNoResponse()
		testkit.Shutdown(ctx)
	})

	t.Run("Assert response received", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create a test kit
		testkit := New(ctx, t, WithLogging(log.ErrorLevel))

		// get the grain identity
		id := testkit.GrainIdentity(ctx, "grain", func(ctx context.Context) (actor.Grain, error) {
			return &grain{}, nil
		})

		// create the message to expect
		msg := new(testpb.Reply)

		// create the test probe
		probe := testkit.NewGrainProbe(ctx)
		// send a message to the grain to be tested
		probe.SendSync(id, new(testpb.TestReply), time.Second)

		// here we expect response from the grain
		probe.ExpectResponse(msg)
		// once the response is received, we expect no further response
		probe.ExpectNoResponse()

		testkit.Shutdown(ctx)
	})

	t.Run("Assert response within received", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create a test kit
		testkit := New(ctx, t, WithLogging(log.ErrorLevel))

		// get the grain identity
		id := testkit.GrainIdentity(ctx, "grain", func(ctx context.Context) (actor.Grain, error) {
			return &grain{}, nil
		})

		// create the message to expect
		msg := new(testpb.Reply)

		// create the test probe
		probe := testkit.NewGrainProbe(ctx)
		// send a message to the grain to be tested
		duration := time.Second
		probe.SendSync(id, &testpb.TestWait{Duration: uint64(duration)}, duration+time.Second)

		// here we expect response from the grain
		probe.ExpectResponseWithin(2*time.Second, msg)
		// once the response is received, we expect no further response
		probe.ExpectNoResponse()

		testkit.Shutdown(ctx)
	})

	t.Run("Assert any response", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create a test kit
		testkit := New(ctx, t, WithLogging(log.ErrorLevel))

		// get the grain identity
		id := testkit.GrainIdentity(ctx, "grain", func(ctx context.Context) (actor.Grain, error) {
			return &grain{}, nil
		})

		// create the test probe
		probe := testkit.NewGrainProbe(ctx)
		// send a message to the grain to be tested
		probe.SendSync(id, new(testpb.TestReply), time.Second)

		// here we expect response from the grain
		probe.ExpectAnyResponse()
		// once the response is received, we expect no further response
		probe.ExpectNoResponse()

		testkit.Shutdown(ctx)
	})

	t.Run("Assert any response within", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create a test kit
		testkit := New(ctx, t, WithLogging(log.ErrorLevel))

		// get the grain identity
		id := testkit.GrainIdentity(ctx, "grain", func(ctx context.Context) (actor.Grain, error) {
			return &grain{}, nil
		})

		// create the test probe
		probe := testkit.NewGrainProbe(ctx)
		// send a message to the grain to be tested
		probe.SendSync(id, new(testpb.TestReply), time.Second)

		// here we expect response from the grain
		probe.ExpectAnyResponseWithin(2 * time.Second)
		// once the response is received, we expect no further response
		probe.ExpectNoResponse()

		testkit.Shutdown(ctx)
	})

	t.Run("Assert response received of type within", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create a test kit
		testkit := New(ctx, t, WithLogging(log.ErrorLevel))

		// get the grain identity
		id := testkit.GrainIdentity(ctx, "grain", func(ctx context.Context) (actor.Grain, error) {
			return &grain{}, nil
		})

		// create the message to expect
		msg := new(testpb.Reply)

		// create the test probe
		probe := testkit.NewGrainProbe(ctx)
		// send a message to the grain to be tested
		probe.SendSync(id, new(testpb.TestReply), time.Second)

		// here we expect response from the grain
		probe.ExpectResponseOfTypeWithin(2*time.Second, msg)
		// once the response is received, we expect no further response
		probe.ExpectNoResponse()

		testkit.Shutdown(ctx)
	})

	t.Run("Assert response received of type", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create a test kit
		testkit := New(ctx, t, WithLogging(log.ErrorLevel))

		// get the grain identity
		id := testkit.GrainIdentity(ctx, "grain", func(ctx context.Context) (actor.Grain, error) {
			return &grain{}, nil
		})

		// create the message to expect
		msg := new(testpb.Reply)

		// create the test probe
		probe := testkit.NewGrainProbe(ctx)
		// send a message to the grain to be tested
		probe.SendSync(id, new(testpb.TestReply), time.Second)

		// here we expect response from the grain
		probe.ExpectResponseOfType(msg)
		// once the response is received, we expect no further response
		probe.ExpectNoResponse()

		testkit.Shutdown(ctx)
	})

	t.Run("Assert terminated", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create a test kit
		testkit := New(ctx, t, WithLogging(log.ErrorLevel))
		// create the test probe
		probe := testkit.NewGrainProbe(ctx)
		// get the grain identity
		id := testkit.GrainIdentity(ctx, "grain", func(ctx context.Context) (actor.Grain, error) {
			return &grain{}, nil
		}, actor.WithGrainDeactivateAfter(200*time.Millisecond))

		pause.For(time.Second)

		probe.ExpectTerminated(id, time.Second)

		testkit.Shutdown(ctx)
	})
}

type grain struct{}

var _ actor.Grain = &grain{}

// OnActivate implements [actor.Grain].
func (g *grain) OnActivate(context.Context, *actor.GrainProps) error {
	return nil
}

// OnDeactivate implements [actor.Grain].
func (g *grain) OnDeactivate(context.Context, *actor.GrainProps) error {
	return nil
}

// OnReceive implements [actor.Grain].
func (g *grain) OnReceive(ctx *actor.GrainContext) {
	switch x := ctx.Message().(type) {
	case *testpb.TestPing:
		ctx.NoErr()
	case *testpb.TestReply:
		ctx.Response(new(testpb.Reply))
	case *testpb.TestWait:
		// delay for a while before sending the reply
		wg := sync.WaitGroup{}
		wg.Go(func() {
			pause.For(time.Duration(x.Duration))
		})
		// block until timer is up
		wg.Wait()
		ctx.Response(new(testpb.Reply))
	}
}
