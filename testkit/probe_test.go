package testkit

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/actors"
	testpb "github.com/tochemey/goakt/test/data/pb/v1"
	"google.golang.org/protobuf/encoding/prototext"
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
		require.Equal(t, pinger.ActorPath().String(), probe.Sender().ActorPath().String())
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
}

type pinger struct {
}

func (t pinger) PreStart(_ context.Context) error {
	return nil
}

func (t pinger) Receive(ctx actors.ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.Ping:
		_ = ctx.Self().Tell(ctx.Context(), ctx.Sender(), new(testpb.Pong))
	}
}

func (t pinger) PostStop(_ context.Context) error {
	return nil
}

var _ actors.Actor = &pinger{}
