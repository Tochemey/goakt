package testkit

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/prototext"

	"github.com/tochemey/goakt/v2/actors"
	"github.com/tochemey/goakt/v2/internal/lib"
	"github.com/tochemey/goakt/v2/test/data/testpb"
)

func TestTestProbe(t *testing.T) {
	t.Run("Assert message received", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create a test kit
		testkit := New(ctx, t)

		// create the actor
		pinger := testkit.Spawn(ctx, "pinger", &pinger{})
		// create the test probe
		probe := testkit.NewProbe(ctx)

		// create the message to expect
		msg := new(testpb.Pong)
		// send a message to the actor to be tested
		probe.Send(pinger, new(testpb.Ping))

		actual := probe.ExpectMessage(msg)
		require.Equal(t, prototext.Format(msg), prototext.Format(actual))
		probe.ExpectNoMessage()

		t.Cleanup(func() {
			probe.Stop()
			testkit.Shutdown(ctx)
		})
	})
	t.Run("Assert any message received", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create a test kit
		testkit := New(ctx, t)

		// create the actor
		pinger := testkit.Spawn(ctx, "pinger", &pinger{})
		// create the test probe
		probe := testkit.NewProbe(ctx)
		// create the message to send
		msg := new(testpb.Pong)

		// send a message to the actor to be tested
		probe.Send(pinger, new(testpb.Ping))

		actual := probe.ExpectAnyMessage()
		require.Equal(t, prototext.Format(msg), prototext.Format(actual))
		probe.ExpectNoMessage()

		t.Cleanup(func() {
			probe.Stop()
			testkit.Shutdown(ctx)
		})
	})
	t.Run("Assert sender", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create a test kit
		testkit := New(ctx, t)

		// create the actor
		pinger := testkit.Spawn(ctx, "pinger", &pinger{})
		// create the test probe
		probe := testkit.NewProbe(ctx)
		// create the message to send
		msg := new(testpb.Pong)
		// send a message to the actor to be tested
		probe.Send(pinger, new(testpb.Ping))

		actual := probe.ExpectMessage(msg)
		require.Equal(t, prototext.Format(msg), prototext.Format(actual))
		require.Equal(t, pinger.Address().String(), probe.Sender().Address().String())
		probe.ExpectNoMessage()

		t.Cleanup(func() {
			probe.Stop()
			testkit.Shutdown(ctx)
		})
	})
	t.Run("Assert message type", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create a test kit
		testkit := New(ctx, t)

		// create the actor
		pinger := testkit.Spawn(ctx, "pinger", &pinger{})
		// create the test probe
		probe := testkit.NewProbe(ctx)
		// create the message to send
		msg := new(testpb.Pong)
		// send a message to the actor to be tested
		probe.Send(pinger, new(testpb.Ping))

		probe.ExpectMessageOfType(msg.ProtoReflect().Type())
		probe.ExpectNoMessage()

		t.Cleanup(func() {
			probe.Stop()
			testkit.Shutdown(ctx)
		})
	})
	t.Run("Assert message received within a time period", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create a test kit
		testkit := New(ctx, t)

		// create the actor
		pinger := testkit.Spawn(ctx, "pinger", &pinger{})
		// create the test probe
		probe := testkit.NewProbe(ctx)

		// create the message to expect
		msg := new(testpb.Pong)
		// send a message to the actor to be tested
		duration := time.Second
		probe.Send(pinger, &testpb.Wait{Duration: uint64(duration)})

		actual := probe.ExpectMessageWithin(2*time.Second, msg)
		require.Equal(t, prototext.Format(msg), prototext.Format(actual))
		probe.ExpectNoMessage()

		t.Cleanup(func() {
			probe.Stop()
			testkit.Shutdown(ctx)
		})
	})
	t.Run("Assert message type within a time period", func(t *testing.T) {
		// create a test context
		ctx := context.TODO()
		// create a test kit
		testkit := New(ctx, t)

		// create the actor
		pinger := testkit.Spawn(ctx, "pinger", &pinger{})
		// create the test probe
		probe := testkit.NewProbe(ctx)

		// create the message to expect
		msg := new(testpb.Pong)
		// send a message to the actor to be tested
		duration := time.Second
		probe.Send(pinger, &testpb.Wait{Duration: uint64(duration)})

		probe.ExpectMessageOfTypeWithin(2*time.Second, msg.ProtoReflect().Type())
		probe.ExpectNoMessage()

		t.Cleanup(func() {
			probe.Stop()
			testkit.Shutdown(ctx)
		})
	})
}

type pinger struct {
}

func (t pinger) PreStart(_ context.Context) error {
	return nil
}

func (t pinger) Receive(ctx *actors.ReceiveContext) {
	switch x := ctx.Message().(type) {
	case *testpb.Ping:
		_ = ctx.Self().Tell(ctx.Context(), ctx.Sender(), new(testpb.Pong))
	case *testpb.Wait:
		// delay for a while before sending the reply
		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			lib.Pause(time.Duration(x.Duration))
			wg.Done()
		}()
		// block until timer is up
		wg.Wait()
		// reply the sender
		_ = ctx.Self().Tell(ctx.Context(), ctx.Sender(), new(testpb.Pong))
	}
}

func (t pinger) PostStop(_ context.Context) error {
	return nil
}

var _ actors.Actor = &pinger{}
