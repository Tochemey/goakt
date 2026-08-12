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

package actor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/commands"
	dynaport "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

func actorReplyTarget() *commands.AsyncReplyTo {
	return &commands.AsyncReplyTo{Kind: commands.ReplyToActor, Actor: address.New("actor", "sys", "127.0.0.1", 9000)}
}

func TestRouteAsyncReplyValidation(t *testing.T) {
	sys, ctx := newReentrancySystem(t)
	system := sys.(*actorSystem)
	sender := spawnReentrancyActor(t, sys, ctx, "validation-sender", func(*ReceiveContext) {})

	t.Run("empty correlation", func(t *testing.T) {
		err := system.routeAsyncReply(ctx, sender, actorReplyTarget(), "", new(testpb.TestSend), nil)
		require.ErrorIs(t, err, gerrors.ErrInvalidMessage)
	})

	// A reply carrying neither payload nor failure is the wire form of a
	// grain's NoErr: it completes the awaiting caller with a nil result.
	t.Run("empty reply routes as success", func(t *testing.T) {
		slot := system.pendingAsks.Register("noerr")
		require.NoError(t, system.routeAsyncReply(ctx, nil, nil, "noerr", nil, nil))

		response := <-slot
		require.Empty(t, response.Error)
		require.Nil(t, response.Message)
	})

	t.Run("actor target without an address", func(t *testing.T) {
		replyTo := &commands.AsyncReplyTo{Kind: commands.ReplyToActor}
		err := system.routeAsyncReply(ctx, sender, replyTo, "corr", new(testpb.TestSend), nil)
		require.ErrorIs(t, err, gerrors.ErrInvalidMessage)
	})

	// Grain repliers and deferred handles have no PID; the router substitutes
	// NoSender so their responses still reach an actor requester.
	t.Run("actor target without a sender delivers via NoSender", func(t *testing.T) {
		replyTo := &commands.AsyncReplyTo{Kind: commands.ReplyToActor, Actor: pathToAddress(sender.Path())}
		require.NoError(t, system.routeAsyncReply(ctx, nil, replyTo, "corr", new(testpb.TestSend), nil))
	})

	// The serializer validates grain targets at the node boundary and local
	// targets are produced from live identities, so a malformed string here
	// means a bug; the router surfaces it instead of guessing.
	t.Run("grain target with a malformed identity", func(t *testing.T) {
		replyTo := &commands.AsyncReplyTo{Kind: commands.ReplyToGrain, Grain: "no-separator"}
		err := system.routeAsyncReply(ctx, sender, replyTo, "corr", new(testpb.TestSend), nil)
		require.ErrorIs(t, err, gerrors.ErrInvalidGrainIdentity)
	})
}

// TestRouteAsyncReplyToGrain pins the grain arm of the router: a response
// addressed to a grain travels through deliverAsyncEnvelope into the grain's
// response queue and completes the in-flight request on the grain's turn.
func TestRouteAsyncReplyToGrain(t *testing.T) {
	sys, pid, _, identity := startReentrantGrainFixture(t, reentrancy.AllowAll)
	ctx := context.Background()

	var result any
	done := make(chan struct{})

	registerGrainRequestState(pid, "corr-grain", reentrancy.AllowAll, func(res any, _ error) {
		result = res
		close(done)
	})

	replyTo := &commands.AsyncReplyTo{Kind: commands.ReplyToGrain, Grain: identity.String()}
	require.NoError(t, sys.routeAsyncReply(ctx, nil, replyTo, "corr-grain", testpb.Reply_builder{Content: "grain-reply"}.Build(), nil))

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("grain request was not completed")
	}

	reply, ok := result.(*testpb.Reply)
	require.True(t, ok)
	require.Equal(t, "grain-reply", reply.GetContent())
}

func TestRouteAsyncReplyToPendingAsk(t *testing.T) {
	t.Run("completes the waiting caller", func(t *testing.T) {
		sys, ctx := newReentrancySystem(t)
		system := sys.(*actorSystem)

		slot := system.pendingAsks.Register("corr")
		require.NoError(t, system.routeAsyncReply(ctx, nil, nil, "corr", testpb.Reply_builder{Content: "ok"}.Build(), nil))

		response := <-slot
		require.Equal(t, "corr", response.CorrelationID)

		reply, ok := response.Message.(*testpb.Reply)
		require.True(t, ok)
		require.Equal(t, "ok", reply.GetContent())
	})

	t.Run("carries the failure reason", func(t *testing.T) {
		sys, ctx := newReentrancySystem(t)
		system := sys.(*actorSystem)

		slot := system.pendingAsks.Register("corr")
		require.NoError(t, system.routeAsyncReply(ctx, nil, nil, "corr", nil, errors.New("boom")))

		response := <-slot
		require.Equal(t, "boom", response.Error)
		require.Nil(t, response.Message)
	})

	// A reply that loses the race against the caller's timeout has nowhere to go.
	// That is an expected outcome, not a delivery failure.
	t.Run("a departed caller is not an error", func(t *testing.T) {
		sys, ctx := newReentrancySystem(t)
		system := sys.(*actorSystem)

		require.NoError(t, system.routeAsyncReply(ctx, nil, nil, "missing", testpb.Reply_builder{Content: "ok"}.Build(), nil))
		require.Zero(t, system.pendingAsks.Len())
	})
}

func TestRouteAsyncReplyToActor(t *testing.T) {
	t.Run("delivers a payload and a failure locally", func(t *testing.T) {
		sys, ctx := newReentrancySystem(t)
		system := sys.(*actorSystem)
		receiver := spawnReentrancyActor(t, sys, ctx, "reply-receiver", func(*ReceiveContext) {}, WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))
		sender := spawnReentrancyActor(t, sys, ctx, "reply-sender", func(*ReceiveContext) {})
		replyTo := &commands.AsyncReplyTo{Kind: commands.ReplyToActor, Actor: pathToAddress(receiver.Path())}

		okState := newRequestState("corr-ok", reentrancy.AllowAll, receiver)
		require.NoError(t, receiver.registerRequestState(okState))
		t.Cleanup(func() { receiver.deregisterRequestState(okState) })

		okCh := make(chan any, 1)
		okState.setCallback(func(msg any, err error) {
			if err == nil {
				okCh <- msg
			}
		})

		require.NoError(t, system.routeAsyncReply(ctx, sender, replyTo, "corr-ok", testpb.Reply_builder{Content: "ok"}.Build(), nil))
		select {
		case msg := <-okCh:
			reply, ok := msg.(*testpb.Reply)
			require.True(t, ok)
			require.Equal(t, "ok", reply.GetContent())
		case <-time.After(reentrancyReplyTimeout):
			t.Fatal("expected async response")
		}

		errState := newRequestState("corr-err", reentrancy.AllowAll, receiver)
		require.NoError(t, receiver.registerRequestState(errState))
		t.Cleanup(func() { receiver.deregisterRequestState(errState) })

		errCh := make(chan error, 1)
		errState.setCallback(func(_ any, err error) {
			if err != nil {
				errCh <- err
			}
		})

		require.NoError(t, system.routeAsyncReply(ctx, sender, replyTo, "corr-err", nil, errors.New("boom")))
		select {
		case err := <-errCh:
			require.EqualError(t, err, "boom")
		case <-time.After(reentrancyReplyTimeout):
			t.Fatal("expected error response")
		}

		require.Zero(t, receiver.reentrancy.Load().requestStates.Len())
	})

	t.Run("reports remoting disabled for an off-node target", func(t *testing.T) {
		sys, ctx := newReentrancySystem(t)
		system := sys.(*actorSystem)
		sender := spawnReentrancyActor(t, sys, ctx, "remote-sender", func(*ReceiveContext) {})
		replyTo := &commands.AsyncReplyTo{Kind: commands.ReplyToActor, Actor: address.New("remote", "remote-system", "127.0.0.1", 9002)}

		err := system.routeAsyncReply(ctx, sender, replyTo, "corr", testpb.Reply_builder{Content: "ok"}.Build(), nil)
		require.ErrorIs(t, err, gerrors.ErrRemotingDisabled)
	})

	t.Run("reports an unknown local target", func(t *testing.T) {
		sys, ctx := newReentrancySystem(t)
		system := sys.(*actorSystem)
		sender := spawnReentrancyActor(t, sys, ctx, "unknown-target-sender", func(*ReceiveContext) {})
		replyTo := &commands.AsyncReplyTo{
			Kind:  commands.ReplyToActor,
			Actor: address.New("ghost", sys.Name(), sys.Host(), sys.Port()),
		}

		err := system.routeAsyncReply(ctx, sender, replyTo, "corr", testpb.Reply_builder{Content: "ok"}.Build(), nil)
		require.Error(t, err)
	})
}

// TestRouteAsyncReplyRemotingWithoutCluster pins the reply route for a system
// that has remoting enabled but no cluster. A requester can reach such a peer
// with Request against a remote PID, so the reply has to travel back by the
// address recorded on the request: the cluster registry that resolves actor
// names is not available here.
func TestRouteAsyncReplyRemotingWithoutCluster(t *testing.T) {
	ctx := context.Background()
	ports := dynaport.Get(1)

	sys, err := NewActorSystem("remoting-no-cluster",
		WithLogger(log.DiscardLogger),
		WithRemote(remote.NewConfig("127.0.0.1", ports[0])),
	)
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	t.Cleanup(func() { _ = sys.Stop(ctx) })

	system := sys.(*actorSystem)
	sender := spawnReentrancyActor(t, sys, ctx, "no-cluster-sender", func(*ReceiveContext) {})

	// An address on another node: reachable by remoting, unresolvable by name.
	replyTo := &commands.AsyncReplyTo{
		Kind:  commands.ReplyToActor,
		Actor: address.New("peer-actor", "peer-system", "127.0.0.1", ports[0]+1),
	}

	// The router hands the response to remoting, which accepts it for delivery.
	// Resolving the requester by name instead would refuse outright here: without
	// a cluster there is no registry that can locate an actor on another node.
	require.NoError(t, system.routeAsyncReply(ctx, sender, replyTo, "corr", testpb.Reply_builder{Content: "ok"}.Build(), nil))
}

// TestResponseWithoutActorSystem covers the guard in ReceiveContext.Response
// that previously lived inside the deleted PID.sendAsyncResponse.
func TestResponseWithoutActorSystem(t *testing.T) {
	rctx := &ReceiveContext{
		ctx:            context.Background(),
		self:           &PID{},
		requestID:      "corr",
		requestReplyTo: actorReplyTarget(),
	}

	rctx.Response(new(testpb.TestSend))
	require.ErrorIs(t, rctx.getError(), gerrors.ErrActorSystemNotStarted)
}
