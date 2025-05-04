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

package testkit

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/prototext"

	actors "github.com/tochemey/goakt/v3/actor"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/util"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

func TestTestProbe(t *testing.T) {
	t.Run("Assert message received", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create a test kit
		testkit := New(ctx, t, WithLogging(log.ErrorLevel))

		// create the actor
		testkit.Spawn(ctx, "pinger", &pinger{})
		// create the test probe
		probe := testkit.NewProbe(ctx)

		// create the message to expect
		msg := new(testpb.TestPong)
		// send a message to the actor to be tested
		probe.Send("pinger", new(testpb.TestPing))

		probe.ExpectMessage(msg)
		probe.ExpectNoMessage()

		probe.Stop()
		testkit.Shutdown(ctx)
	})
	t.Run("Assert any message received", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create a test kit
		testkit := New(ctx, t, WithLogging(log.ErrorLevel))

		// create the actor
		testkit.Spawn(ctx, "pinger", &pinger{})
		// create the test probe
		probe := testkit.NewProbe(ctx)
		// create the message to send
		msg := new(testpb.TestPong)

		// send a message to the actor to be tested
		probe.Send("pinger", new(testpb.TestPing))

		actual := probe.ExpectAnyMessage()
		require.Equal(t, prototext.Format(msg), prototext.Format(actual))
		probe.ExpectNoMessage()

		probe.Stop()
		testkit.Shutdown(ctx)
	})
	t.Run("Assert sender", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create a test kit
		testkit := New(ctx, t, WithLogging(log.ErrorLevel))

		// create the actor
		testkit.Spawn(ctx, "pinger", &pinger{})
		// create the test probe
		probe := testkit.NewProbe(ctx)
		// create the message to send
		msg := new(testpb.TestPong)
		// send a message to the actor to be tested
		probe.Send("pinger", new(testpb.TestPing))

		probe.ExpectMessage(msg)
		require.Equal(t, "pinger", probe.Sender().Name())
		probe.ExpectNoMessage()

		probe.Stop()
		testkit.Shutdown(ctx)
	})
	t.Run("Assert message type", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create a test kit
		testkit := New(ctx, t)

		// create the actor
		testkit.Spawn(ctx, "pinger", &pinger{})
		// create the test probe
		probe := testkit.NewProbe(ctx)
		// create the message to send
		msg := new(testpb.TestPong)
		// send a message to the actor to be tested
		probe.Send("pinger", new(testpb.TestPing))

		probe.ExpectMessageOfType(msg.ProtoReflect().Type())
		probe.ExpectNoMessage()

		probe.Stop()
		testkit.Shutdown(ctx)
	})
	t.Run("Assert message received within a time period", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create a test kit
		testkit := New(ctx, t)

		// create the actor
		testkit.Spawn(ctx, "pinger", &pinger{})
		// create the test probe
		probe := testkit.NewProbe(ctx)

		// create the message to expect
		msg := new(testpb.TestPong)
		// send a message to the actor to be tested
		duration := time.Second
		probe.Send("pinger", &testpb.TestWait{Duration: uint64(duration)})

		probe.ExpectMessageWithin(2*time.Second, msg)
		probe.ExpectNoMessage()

		probe.Stop()
		testkit.Shutdown(ctx)
	})
	t.Run("Assert message type within a time period", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create a test kit
		testkit := New(ctx, t)

		// create the actor
		testkit.Spawn(ctx, "pinger", &pinger{})
		// create the test probe
		probe := testkit.NewProbe(ctx)

		// create the message to expect
		msg := new(testpb.TestPong)
		// send a message to the actor to be tested
		duration := time.Second
		probe.Send("pinger", &testpb.TestWait{Duration: uint64(duration)})

		probe.ExpectMessageOfTypeWithin(2*time.Second, msg.ProtoReflect().Type())
		probe.ExpectNoMessage()

		probe.Stop()
		testkit.Shutdown(ctx)
	})
	t.Run("Assert SendSync", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create a test kit
		testkit := New(ctx, t, WithLogging(log.ErrorLevel))

		// create the actor
		testkit.Spawn(ctx, "pinger", &pinger{})
		// create the test probe
		probe := testkit.NewProbe(ctx)

		// send a message to the actor to be tested
		probe.SendSync("pinger", new(testpb.TestReply), time.Second)
		probe.ExpectMessage(new(testpb.Reply))
		probe.ExpectNoMessage()

		probe.Stop()
		testkit.Shutdown(ctx)
	})
	t.Run("Assert WatchNamed", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create a test kit
		testkit := New(ctx, t, WithLogging(log.ErrorLevel))

		// create the actor
		testkit.Spawn(ctx, "pinger", &pinger{})
		// create the test probe
		probe := testkit.NewProbe(ctx)

		// watch the actor
		probe.WatchNamed("pinger")

		// kill the actor
		probe.Send("pinger", new(goaktpb.PoisonPill))
		probe.ExpectTerminated("pinger")
		probe.ExpectNoMessage()

		probe.Stop()
		testkit.Shutdown(ctx)
	})
	t.Run("Assert SpawnChild", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create a test kit
		testkit := New(ctx, t, WithLogging(log.ErrorLevel))

		// create the actor
		testkit.Spawn(ctx, "pinger", &pinger{})

		// create the child actor
		testkit.SpawnChild(ctx, "child", "pinger", &pinger{})

		// create the test probe
		probe := testkit.NewProbe(ctx)

		// send a message to the actor to be tested
		probe.SendSync("child", new(testpb.TestReply), time.Second)
		probe.ExpectMessage(new(testpb.Reply))
		probe.ExpectNoMessage()

		probe.Stop()
		testkit.Shutdown(ctx)
	})
	t.Run("Assert Watch", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create a test kit
		testkit := New(ctx, t, WithLogging(log.ErrorLevel))

		// create the actor outside the test kit
		pid, err := testkit.ActorSystem().Spawn(ctx, "pinger", &pinger{})
		require.NoError(t, err)
		require.NotNil(t, pid)

		// create the test probe
		probe := testkit.NewProbe(ctx)

		// watch the actor
		probe.Watch(pid)

		// shutdown the actor
		err = pid.Shutdown(ctx)
		require.NoError(t, err)

		// wait for the actor to be terminated
		util.Pause(time.Second)

		probe.ExpectTerminated(pid.Name())
		probe.ExpectNoMessage()

		probe.Stop()
		testkit.Shutdown(ctx)
	})
}

type pinger struct {
}

func (t pinger) PreStart(_ *actors.Context) error {
	return nil
}

func (t pinger) Receive(ctx *actors.ReceiveContext) {
	switch x := ctx.Message().(type) {
	case *testpb.TestPing:
		ctx.Tell(ctx.Sender(), new(testpb.TestPong))
	case *testpb.TestReply:
		ctx.Response(new(testpb.Reply))
	case *testpb.TestWait:
		// delay for a while before sending the reply
		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			util.Pause(time.Duration(x.Duration))
			wg.Done()
		}()
		// block until timer is up
		wg.Wait()
		// reply the sender
		ctx.Tell(ctx.Sender(), new(testpb.TestPong))
	}
}

func (t pinger) PostStop(_ *actors.Context) error {
	return nil
}

var _ actors.Actor = &pinger{}
