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
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/breaker"
	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// nolint
func TestReleaseGrainContextResetsFields(t *testing.T) {
	ctx := getGrainContext(0)

	identity := &GrainIdentity{kind: "TestKind", name: "id"}
	ctx.self = identity
	ctx.message = &testpb.TestMessage{}
	ctx.err = make(chan error, 1)
	ctx.response = make(chan any, 1)
	ctx.pid = &grainPID{}

	releaseGrainContext(ctx)

	require.Nil(t, ctx.self)
	require.Nil(t, ctx.message)
	require.Nil(t, ctx.err)
	require.Nil(t, ctx.response)
	require.Nil(t, ctx.pid)

	// Return the context to the pool to keep the pool populated for other tests.
	// releaseGrainContext already returned it, but we re-acquire and release to ensure clean state.
	acquired := getGrainContext(0)
	releaseGrainContext(acquired)
}

func TestGrainContextAskReplyRouting(t *testing.T) {
	identity := &GrainIdentity{kind: "TestKind", name: "id"}
	newAskContext := func() *GrainContext {
		return new(GrainContext).build(context.Background(), nil, nil, identity, new(testpb.TestReply), grainAsk)
	}

	t.Run("Response delivers the payload", func(t *testing.T) {
		gctx := newAskContext()
		gctx.Response("payload")
		require.Equal(t, "payload", <-gctx.response)
	})

	t.Run("NoErr delivers a nil reply", func(t *testing.T) {
		gctx := newAskContext()
		gctx.NoErr()
		require.Nil(t, <-gctx.response)
	})

	t.Run("Err delivers the wrapped failure", func(t *testing.T) {
		gctx := newAskContext()
		failure := errors.New("handler failure")
		gctx.Err(failure)

		reply, ok := (<-gctx.response).(grainReplyError)
		require.True(t, ok)
		require.Equal(t, failure, reply.err)
	})

	t.Run("Unhandled delivers ErrUnhandledMessage", func(t *testing.T) {
		gctx := newAskContext()
		gctx.Unhandled()

		reply, ok := (<-gctx.response).(grainReplyError)
		require.True(t, ok)
		require.ErrorIs(t, reply.err, gerrors.ErrUnhanledMessage)
	})

	t.Run("error-typed payload stays a payload", func(t *testing.T) {
		gctx := newAskContext()
		payload := errors.New("payload that implements error")
		gctx.Response(payload)

		reply := <-gctx.response
		_, wrapped := reply.(grainReplyError)
		require.False(t, wrapped)
		require.Equal(t, payload, reply)
	})

	t.Run("only the first reply wins", func(t *testing.T) {
		gctx := newAskContext()
		gctx.Response("first")
		gctx.Err(errors.New("late failure"))

		require.Equal(t, "first", <-gctx.response)
		require.Empty(t, gctx.response)
	})

	t.Run("late reply after timeout is suppressed", func(t *testing.T) {
		gctx := newAskContext()

		// A tripped guard (first reply already taken) drops further sends.
		gctx.responseClosed.Store(true)

		gctx.Err(errors.New("late failure"))
		gctx.Response("late payload")
		gctx.NoErr()
		require.Empty(t, gctx.response)
	})
}

func TestGrainContext(t *testing.T) {
	t.Run("With Grain to Actor messaging", func(t *testing.T) {
		ctx := t.Context()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start a system cluster
		node1, sd1 := testNATs(t, srv.Addr().String())
		require.NotNil(t, node1)
		require.NotNil(t, sd1)

		// create and start a system cluster
		node2, sd2 := testNATs(t, srv.Addr().String())
		require.NotNil(t, node2)
		require.NotNil(t, sd2)

		// create and start a system cluster
		node3, sd3 := testNATs(t, srv.Addr().String())
		require.NotNil(t, node3)
		require.NotNil(t, sd3)

		grain := NewMockGrain()
		identity, err := node1.GrainIdentity(ctx, "testGrain", func(_ context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)
		require.NotNil(t, identity)

		pause.For(time.Second)

		// check if the grain is activated
		gp, ok := node1.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isActive())

		// send a message to the grain
		message := new(testpb.TestReply)
		response, err := node1.AskGrain(ctx, identity, message, time.Second)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.IsType(t, &testpb.Reply{}, response)

		// create an actor
		pid, err := node2.Spawn(ctx, "Actor20", NewMockGrainActor())
		require.NoError(t, err)
		require.NotNil(t, pid)

		pause.For(time.Second)

		// this simulates a message sent from a Grain to an actor
		response, err = node1.AskGrain(ctx, identity, new(testpb.TestPing), time.Second)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.IsType(t, &testpb.TestPong{}, response)

		err = node2.TellGrain(ctx, identity, new(testpb.TestBye))
		require.NoError(t, err)

		pause.For(time.Second)

		exist, err := node3.ActorExists(ctx, "Actor20")
		require.NoError(t, err)
		require.False(t, exist)

		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, sd3.Close())
		srv.Shutdown()
	})
	t.Run("With Grain to Grain messaging", func(t *testing.T) {
		ctx := t.Context()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start a system cluster
		node1, sd1 := testNATs(t, srv.Addr().String())
		require.NotNil(t, node1)
		require.NotNil(t, sd1)

		// create and start a system cluster
		node2, sd2 := testNATs(t, srv.Addr().String())
		require.NotNil(t, node2)
		require.NotNil(t, sd2)

		// create and start a system cluster
		node3, sd3 := testNATs(t, srv.Addr().String())
		require.NotNil(t, node3)
		require.NotNil(t, sd3)

		identity, err := node1.GrainIdentity(ctx, "Grain1", func(_ context.Context) (Grain, error) {
			return NewMockGrain(), nil
		})
		require.NoError(t, err)
		require.NotNil(t, identity)

		// check if the grain is activated
		gp, ok := node1.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isActive())

		// wait for cluster synchronization
		pause.For(time.Second)

		// send a message to the grain
		message := new(testpb.TestMessage)
		response, err := node2.AskGrain(ctx, identity, message, time.Second)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.IsType(t, &testpb.Reply{}, response)

		pause.For(600 * time.Millisecond)

		// send a message to the grain
		err = node3.TellGrain(ctx, identity, new(testpb.TestReady))
		require.NoError(t, err)

		pause.For(600 * time.Millisecond)

		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, sd3.Close())
		srv.Shutdown()
	})
	t.Run("With Dependencies", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		dependencyID := "MyDependency"
		dependency := NewMockDependency(dependencyID, "user", "email")

		grain := NewMockGrain()
		identity, err := testSystem.GrainIdentity(ctx, "testGrain", func(_ context.Context) (Grain, error) {
			return grain, nil
		}, WithGrainDependencies(dependency))
		require.NoError(t, err)
		require.NotNil(t, identity)

		pause.For(time.Second)

		// check if the grain is activated
		gp, ok := testSystem.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isActive())

		// mirror the grain context
		gctx := &GrainContext{
			ctx:         ctx,
			actorSystem: testSystem,
			self:        identity,
			pid:         gp,
		}

		// retrieve dependencies from the grain context
		dependencies := gctx.Dependencies()
		require.Len(t, dependencies, 1)
		actual := gctx.Dependency(dependencyID)
		require.NotNil(t, actual)
		require.Equal(t, dependencyID, actual.ID())

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("With Extensions", func(t *testing.T) {
		ctx := t.Context()
		ext := new(MockExtension)
		testSystem, err := NewActorSystem("testSys",
			WithLogger(log.DiscardLogger),
			WithExtensions(ext))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		grain := NewMockGrain()
		identity, err := testSystem.GrainIdentity(ctx, "testGrain", func(_ context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)
		require.NotNil(t, identity)

		pause.For(time.Second)

		// check if the grain is activated
		gp, ok := testSystem.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isActive())

		// mirror the grain context
		gctx := &GrainContext{
			ctx:         ctx,
			actorSystem: testSystem,
			self:        identity,
			pid:         gp,
		}

		// retrieve extensions from the grain context
		extensions := gctx.Extensions()
		require.Len(t, extensions, 1)
		actual := gctx.Extension(ext.ID())
		require.NotNil(t, actual)

		require.NoError(t, testSystem.Stop(ctx))
	})
}

func TestGrainContextPipeToGrain(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		ctx := t.Context()
		sys := startTestActorSystem(t, "pipe-grain-success")

		target := newPipeTargetGrain()
		identity, err := sys.GrainIdentity(ctx, "pipe-target-success", func(_ context.Context) (Grain, error) {
			return target, nil
		})
		require.NoError(t, err)

		gctx := &GrainContext{ctx: ctx, actorSystem: sys}
		err = gctx.PipeToGrain(identity, func() (any, error) {
			return testpb.Reply_builder{Content: "ok"}.Build(), nil
		})
		require.NoError(t, err)

		select {
		case msg := <-target.received:
			reply, ok := msg.(*testpb.Reply)
			require.True(t, ok)
			require.Equal(t, "ok", reply.GetContent())
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for piped grain message")
		}
	})

	t.Run("error", func(t *testing.T) {
		ctx := t.Context()
		sys := startTestActorSystem(t, "pipe-grain-error")

		target := newPipeTargetGrain()
		identity, err := sys.GrainIdentity(ctx, "pipe-target-error", func(_ context.Context) (Grain, error) {
			return target, nil
		})
		require.NoError(t, err)

		gctx := &GrainContext{ctx: ctx, actorSystem: sys}
		err = gctx.PipeToGrain(identity, func() (any, error) {
			return nil, errors.New("boom")
		})
		require.NoError(t, err)

		select {
		case failure := <-target.failures:
			require.Contains(t, failure.Error(), "boom")
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for piped grain failure")
		}
	})

	t.Run("timeout", func(t *testing.T) {
		ctx := t.Context()
		sys := startTestActorSystem(t, "pipe-grain-timeout")

		target := newPipeTargetGrain()
		identity, err := sys.GrainIdentity(ctx, "pipe-target-timeout", func(_ context.Context) (Grain, error) {
			return target, nil
		})
		require.NoError(t, err)

		gctx := &GrainContext{ctx: ctx, actorSystem: sys}
		err = gctx.PipeToGrain(identity, func() (any, error) {
			time.Sleep(150 * time.Millisecond)
			return testpb.Reply_builder{Content: "late"}.Build(), nil
		}, WithTimeout(50*time.Millisecond))
		require.NoError(t, err)

		select {
		case failure := <-target.failures:
			require.Contains(t, failure.Error(), context.DeadlineExceeded.Error())
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for piped grain timeout")
		}
	})

	t.Run("circuit breaker", func(t *testing.T) {
		ctx := t.Context()
		sys := startTestActorSystem(t, "pipe-grain-breaker")

		target := newPipeTargetGrain()
		identity, err := sys.GrainIdentity(ctx, "pipe-target-breaker", func(_ context.Context) (Grain, error) {
			return target, nil
		})
		require.NoError(t, err)

		cb := breaker.NewCircuitBreaker(breaker.WithMinRequests(1))
		gctx := &GrainContext{ctx: ctx, actorSystem: sys}
		err = gctx.PipeToGrain(identity, func() (any, error) {
			return testpb.Reply_builder{Content: "ok"}.Build(), nil
		}, WithCircuitBreaker(cb))
		require.NoError(t, err)

		select {
		case msg := <-target.received:
			_, ok := msg.(*testpb.Reply)
			require.True(t, ok)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for piped grain message")
		}
	})
}

func TestGrainContextPipeToGrainInvalidInput(t *testing.T) {
	gctx := &GrainContext{ctx: context.Background()}

	t.Run("nil task", func(t *testing.T) {
		err := gctx.PipeToGrain(nil, nil)
		require.ErrorIs(t, err, gerrors.ErrUndefinedTask)
	})

	t.Run("nil identity", func(t *testing.T) {
		err := gctx.PipeToGrain(nil, func() (any, error) {
			return testpb.Reply_builder{Content: "ok"}.Build(), nil
		})
		require.ErrorIs(t, err, gerrors.ErrInvalidGrainIdentity)
	})

	t.Run("invalid identity", func(t *testing.T) {
		invalid := &GrainIdentity{kind: "bad", name: ""}
		err := gctx.PipeToGrain(invalid, func() (any, error) {
			return testpb.Reply_builder{Content: "ok"}.Build(), nil
		})
		require.ErrorIs(t, err, gerrors.ErrInvalidGrainIdentity)
	})
}

func TestGrainContextPipeToActor(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		ctx := t.Context()
		sys := startTestActorSystem(t, "pipe-actor-success")

		target := newPipeTargetActor()
		_, err := sys.Spawn(ctx, "pipe-target-actor", target)
		require.NoError(t, err)

		gctx := &GrainContext{ctx: ctx, actorSystem: sys}
		err = gctx.PipeToActor("pipe-target-actor", func() (any, error) {
			return testpb.Reply_builder{Content: "ok"}.Build(), nil
		})
		require.NoError(t, err)

		select {
		case msg := <-target.received:
			reply, ok := msg.(*testpb.Reply)
			require.True(t, ok)
			require.Equal(t, "ok", reply.GetContent())
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for piped actor message")
		}
	})

	t.Run("nil task", func(t *testing.T) {
		ctx := t.Context()
		sys := startTestActorSystem(t, "pipe-actor-nil-task")

		gctx := &GrainContext{ctx: ctx, actorSystem: sys}
		err := gctx.PipeToActor("pipe-target-actor", nil)
		require.ErrorIs(t, err, gerrors.ErrUndefinedTask)
	})
}

func TestGrainContextPipeToSelf(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		ctx := t.Context()
		sys := startTestActorSystem(t, "pipe-self-success")

		target := newPipeTargetGrain()
		identity, err := sys.GrainIdentity(ctx, "pipe-self-target", func(_ context.Context) (Grain, error) {
			return target, nil
		})
		require.NoError(t, err)

		gctx := &GrainContext{ctx: ctx, actorSystem: sys, self: identity}
		err = gctx.PipeToSelf(func() (any, error) {
			return testpb.Reply_builder{Content: "ok"}.Build(), nil
		})
		require.NoError(t, err)

		select {
		case msg := <-target.received:
			reply, ok := msg.(*testpb.Reply)
			require.True(t, ok)
			require.Equal(t, "ok", reply.GetContent())
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for piped self message")
		}
	})

	t.Run("nil self", func(t *testing.T) {
		gctx := &GrainContext{ctx: context.Background()}
		err := gctx.PipeToSelf(func() (any, error) {
			return testpb.Reply_builder{Content: "ok"}.Build(), nil
		})
		require.ErrorIs(t, err, gerrors.ErrInvalidGrainIdentity)
	})
}

func TestHandleGrainCompletionSendError(t *testing.T) {
	t.Run("failure message send error", func(t *testing.T) {
		sendErr := errors.New("send failure")
		system := &stubGrainPipeSystem{err: sendErr}
		completion := &grainTaskCompletion{
			Target: &GrainIdentity{kind: "grain", name: "id"},
			Task: func() (any, error) {
				return nil, errors.New("boom")
			},
		}

		err := handleGrainCompletion(context.Background(), system, nil, completion)
		require.ErrorIs(t, err, sendErr)
		require.IsType(t, &StatusFailure{}, system.lastMessage)
	})

	t.Run("success message send error", func(t *testing.T) {
		sendErr := errors.New("send failure")
		system := &stubGrainPipeSystem{err: sendErr}
		completion := &grainTaskCompletion{
			Target: &GrainIdentity{kind: "grain", name: "id"},
			Task: func() (any, error) {
				return testpb.Reply_builder{Content: "ok"}.Build(), nil
			},
		}

		err := handleGrainCompletion(context.Background(), system, nil, completion)
		require.ErrorIs(t, err, sendErr)
		require.IsType(t, &testpb.Reply{}, system.lastMessage)
	})
}

type pipeTargetGrain struct {
	received chan any
	failures chan *StatusFailure
}

func newPipeTargetGrain() *pipeTargetGrain {
	return &pipeTargetGrain{
		received: make(chan any, 1),
		failures: make(chan *StatusFailure, 1),
	}
}

func (p *pipeTargetGrain) OnActivate(context.Context, *GrainProps) error {
	return nil
}

func (p *pipeTargetGrain) OnDeactivate(context.Context, *GrainProps) error {
	return nil
}

func (p *pipeTargetGrain) OnReceive(ctx *GrainContext) {
	switch msg := ctx.Message().(type) {
	case *StatusFailure:
		select {
		case p.failures <- msg:
		default:
		}
	default:
		select {
		case p.received <- msg:
		default:
		}
	}
	ctx.NoErr()
}

type pipeTargetActor struct {
	received chan any
}

func newPipeTargetActor() *pipeTargetActor {
	return &pipeTargetActor{
		received: make(chan any, 1),
	}
}

func (p *pipeTargetActor) PreStart(*Context) error {
	return nil
}

func (p *pipeTargetActor) PostStop(*Context) error {
	return nil
}

func (p *pipeTargetActor) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *testpb.Reply:
		select {
		case p.received <- msg:
		default:
		}
	}
}

type stubGrainPipeSystem struct {
	err         error
	lastMessage any
}

func (s *stubGrainPipeSystem) TellGrain(ctx context.Context, identity *GrainIdentity, message any) error {
	s.lastMessage = message
	return s.err
}

func (s *stubGrainPipeSystem) Logger() log.Logger {
	return log.DiscardLogger
}

func startTestActorSystem(t *testing.T, name string) ActorSystem {
	t.Helper()

	sys, err := NewActorSystem(name, WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(t.Context()))

	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = sys.Stop(stopCtx)
	})

	return sys
}

// scriptedGrain runs a per-test OnReceive function.
type scriptedGrain struct {
	receive func(*GrainContext)
}

var _ Grain = (*scriptedGrain)(nil)

func (g *scriptedGrain) OnActivate(context.Context, *GrainProps) error { return nil }

func (g *scriptedGrain) OnDeactivate(context.Context, *GrainProps) error { return nil }

func (g *scriptedGrain) OnReceive(gctx *GrainContext) { g.receive(gctx) }

// newRequestTestSystem starts a system for the grain request and reply tests.
// The logger discards but stays enabled so the debug and error paths execute.
func newRequestTestSystem(t *testing.T) *actorSystem {
	t.Helper()
	ctx := context.Background()

	system, err := NewActorSystem("testSys", WithLogger(log.NewSlog(log.DebugLevel, io.Discard)))
	require.NoError(t, err)
	require.NoError(t, system.Start(ctx))

	t.Cleanup(func() {
		_ = system.Stop(context.Background())
	})

	return system.(*actorSystem)
}

// activateReentrantGrain activates grain under name and equips its pid with
// reentrancy state the way the config plumbing will.
func activateReentrantGrain(t *testing.T, system *actorSystem, grain Grain, name string) *GrainIdentity {
	t.Helper()

	identity, err := system.GrainIdentity(context.Background(), name, func(context.Context) (Grain, error) {
		return grain, nil
	})
	require.NoError(t, err)

	pid, ok := system.grains.Get(identity.String())
	require.True(t, ok)

	pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 0))
	pid.responses = newGrainMailbox(0)
	return identity
}

func TestGrainEnvelopeReplyModes(t *testing.T) {
	system := newRequestTestSystem(t)
	ctx := context.Background()

	grain := &scriptedGrain{receive: func(gctx *GrainContext) {
		switch gctx.Message().(type) {
		case *testpb.TestPing:
			gctx.Response(testpb.Reply_builder{Content: "pong"}.Build())
		case *testpb.TestBye:
			gctx.Err(errors.New("handler failed"))
		case *testpb.TestSend:
			gctx.NoErr()
		default:
			gctx.Unhandled()
		}
	}}
	identity := activateReentrantGrain(t, system, grain, "replyModesGrain")

	t.Run("Response carries the payload", func(t *testing.T) {
		response, err := system.AskGrain(ctx, identity, new(testpb.TestPing), time.Second)
		require.NoError(t, err)
		reply, ok := response.(*testpb.Reply)
		require.True(t, ok)
		require.Equal(t, "pong", reply.GetContent())
	})

	t.Run("Err carries the failure", func(t *testing.T) {
		response, err := system.AskGrain(ctx, identity, new(testpb.TestBye), time.Second)
		require.Nil(t, response)
		require.EqualError(t, err, "handler failed")
	})

	t.Run("NoErr completes with nil", func(t *testing.T) {
		response, err := system.AskGrain(ctx, identity, new(testpb.TestSend), time.Second)
		require.NoError(t, err)
		require.Nil(t, response)
	})

	t.Run("Unhandled keeps its identity", func(t *testing.T) {
		_, err := system.AskGrain(ctx, identity, new(testpb.TestReply), time.Second)
		require.ErrorIs(t, err, gerrors.ErrUnhanledMessage)
	})
}

func TestGrainEnvelopeReplyIsOneShot(t *testing.T) {
	system := newRequestTestSystem(t)

	grain := &scriptedGrain{receive: func(gctx *GrainContext) {
		gctx.Response(testpb.Reply_builder{Content: "first"}.Build())
		gctx.Response(testpb.Reply_builder{Content: "second"}.Build())
		gctx.Err(errors.New("late failure"))
	}}
	identity := activateReentrantGrain(t, system, grain, "oneShotGrain")

	response, err := system.AskGrain(context.Background(), identity, new(testpb.TestPing), time.Second)
	require.NoError(t, err)

	reply, ok := response.(*testpb.Reply)
	require.True(t, ok)
	require.Equal(t, "first", reply.GetContent())
	require.Zero(t, system.pendingAsks.Len())
}

func TestGrainCorrelationID(t *testing.T) {
	system := newRequestTestSystem(t)
	ctx := context.Background()

	correlations := make(chan string, 2)
	grain := &scriptedGrain{receive: func(gctx *GrainContext) {
		correlations <- gctx.CorrelationID()

		if gctx.CorrelationID() != "" {
			gctx.Response(&testpb.Reply{})
			return
		}
		gctx.NoErr()
	}}
	identity := activateReentrantGrain(t, system, grain, "correlationGrain")

	_, err := system.AskGrain(ctx, identity, new(testpb.TestPing), time.Second)
	require.NoError(t, err)
	require.NotEmpty(t, <-correlations)

	require.NoError(t, system.TellGrain(ctx, identity, new(testpb.TestSend)))
	require.Empty(t, <-correlations)
}

func TestGrainEnvelopePanicRepliesError(t *testing.T) {
	system := newRequestTestSystem(t)

	grain := &scriptedGrain{receive: func(*GrainContext) {
		panic("handler exploded")
	}}
	identity := activateReentrantGrain(t, system, grain, "panickyGrain")

	response, err := system.AskGrain(context.Background(), identity, new(testpb.TestPing), time.Second)
	require.Nil(t, response)
	require.Error(t, err)
	require.Contains(t, err.Error(), "handler exploded")
}

func TestGrainDeferResponse(t *testing.T) {
	t.Run("completes after the turn", func(t *testing.T) {
		system := newRequestTestSystem(t)
		ctx := context.Background()

		var (
			mu      sync.Mutex
			pending []*GrainReply
		)
		requests := make(chan struct{}, 1)

		grain := &scriptedGrain{receive: func(gctx *GrainContext) {
			switch gctx.Message().(type) {
			case *testpb.TestPing:
				mu.Lock()
				pending = append(pending, gctx.DeferResponse())
				mu.Unlock()
				requests <- struct{}{}
			case *testpb.TestSend:
				mu.Lock()
				replies := pending
				pending = nil
				mu.Unlock()

				for _, reply := range replies {
					reply.Response(testpb.Reply_builder{Content: "deferred"}.Build())
				}
				gctx.NoErr()
			}
		}}
		identity := activateReentrantGrain(t, system, grain, "deferGrain")

		type askResult struct {
			response any
			err      error
		}
		results := make(chan askResult, 1)

		go func() {
			response, err := system.AskGrain(ctx, identity, new(testpb.TestPing), 2*time.Second)
			results <- askResult{response: response, err: err}
		}()

		<-requests
		require.NoError(t, system.TellGrain(ctx, identity, new(testpb.TestSend)))

		select {
		case result := <-results:
			require.NoError(t, result.err)
			reply, ok := result.response.(*testpb.Reply)
			require.True(t, ok)
			require.Equal(t, "deferred", reply.GetContent())
		case <-time.After(2 * time.Second):
			t.Fatal("deferred reply never completed the ask")
		}
	})

	t.Run("in-turn replies after defer are no-ops", func(t *testing.T) {
		system := newRequestTestSystem(t)

		grain := &scriptedGrain{receive: func(gctx *GrainContext) {
			reply := gctx.DeferResponse()

			// Ownership moved to the handle: none of these must reach the caller.
			gctx.Response(testpb.Reply_builder{Content: "wrong"}.Build())
			gctx.Err(errors.New("wrong"))
			gctx.NoErr()

			reply.Response(testpb.Reply_builder{Content: "right"}.Build())
		}}
		identity := activateReentrantGrain(t, system, grain, "deferOwnerGrain")

		response, err := system.AskGrain(context.Background(), identity, new(testpb.TestPing), time.Second)
		require.NoError(t, err)

		reply, ok := response.(*testpb.Reply)
		require.True(t, ok)
		require.Equal(t, "right", reply.GetContent())
	})

	t.Run("double completion is a no-op", func(t *testing.T) {
		system := newRequestTestSystem(t)

		grain := &scriptedGrain{receive: func(gctx *GrainContext) {
			reply := gctx.DeferResponse()
			reply.Response(testpb.Reply_builder{Content: "first"}.Build())
			reply.Response(testpb.Reply_builder{Content: "second"}.Build())
			reply.Err(errors.New("late failure"))
		}}
		identity := activateReentrantGrain(t, system, grain, "deferOnceGrain")

		response, err := system.AskGrain(context.Background(), identity, new(testpb.TestPing), time.Second)
		require.NoError(t, err)

		reply, ok := response.(*testpb.Reply)
		require.True(t, ok)
		require.Equal(t, "first", reply.GetContent())
	})

	t.Run("nil for ordinary messages", func(t *testing.T) {
		system := newRequestTestSystem(t)

		handles := make(chan *GrainReply, 1)
		grain := &scriptedGrain{receive: func(gctx *GrainContext) {
			reply := gctx.DeferResponse()
			handles <- reply

			// A nil handle is safe to complete; the calls do nothing.
			reply.Response(&testpb.Reply{})
			reply.Err(errors.New("ignored"))
			reply.NoErr()
			gctx.NoErr()
		}}
		identity := activateReentrantGrain(t, system, grain, "deferTellGrain")

		require.NoError(t, system.TellGrain(context.Background(), identity, new(testpb.TestSend)))
		require.Nil(t, <-handles)
	})
}

func TestGrainRequestGrain(t *testing.T) {
	t.Run("completes with the target's response", func(t *testing.T) {
		system := newRequestTestSystem(t)
		ctx := context.Background()

		target := &scriptedGrain{receive: func(gctx *GrainContext) {
			gctx.Response(testpb.TestCount_builder{Value: 42}.Build())
		}}
		targetID := activateReentrantGrain(t, system, target, "target-grain")

		results := make(chan *testpb.TestCount, 1)
		failures := make(chan error, 1)

		caller := &scriptedGrain{receive: func(gctx *GrainContext) {
			call := gctx.RequestGrain(targetID, new(testpb.TestPing))
			call.Then(func(result any, err error) {
				if err != nil {
					failures <- err
					return
				}
				results <- result.(*testpb.TestCount)
			})
			gctx.NoErr()
		}}
		callerID := activateReentrantGrain(t, system, caller, "caller-grain")

		require.NoError(t, system.TellGrain(ctx, callerID, new(testpb.TestSend)))

		select {
		case count := <-results:
			require.EqualValues(t, 42, count.GetValue())
		case err := <-failures:
			t.Fatalf("request failed: %v", err)
		case <-time.After(2 * time.Second):
			t.Fatal("continuation never ran")
		}
	})

	t.Run("unhandled keeps its identity across the request", func(t *testing.T) {
		system := newRequestTestSystem(t)
		ctx := context.Background()

		target := &scriptedGrain{receive: func(gctx *GrainContext) {
			gctx.Unhandled()
		}}
		targetID := activateReentrantGrain(t, system, target, "unhandled-grain")

		failures := make(chan error, 1)
		caller := &scriptedGrain{receive: func(gctx *GrainContext) {
			gctx.RequestGrain(targetID, new(testpb.TestPing)).Then(func(_ any, err error) {
				failures <- err
			})
			gctx.NoErr()
		}}
		callerID := activateReentrantGrain(t, system, caller, "caller-grain")

		require.NoError(t, system.TellGrain(ctx, callerID, new(testpb.TestSend)))

		select {
		case err := <-failures:
			require.ErrorIs(t, err, gerrors.ErrUnhanledMessage)
		case <-time.After(2 * time.Second):
			t.Fatal("continuation never ran")
		}
	})

	t.Run("timeout fails the request", func(t *testing.T) {
		system := newRequestTestSystem(t)
		ctx := context.Background()

		silent := &scriptedGrain{receive: func(*GrainContext) {}}
		silentID := activateReentrantGrain(t, system, silent, "silent-grain")

		failures := make(chan error, 1)
		caller := &scriptedGrain{receive: func(gctx *GrainContext) {
			gctx.RequestGrain(silentID, new(testpb.TestPing), WithRequestTimeout(200*time.Millisecond)).Then(func(_ any, err error) {
				failures <- err
			})
			gctx.NoErr()
		}}
		callerID := activateReentrantGrain(t, system, caller, "caller-grain")

		require.NoError(t, system.TellGrain(ctx, callerID, new(testpb.TestSend)))

		select {
		case err := <-failures:
			require.ErrorIs(t, err, gerrors.ErrRequestTimeout)
		case <-time.After(2 * time.Second):
			t.Fatal("timeout never fired")
		}
	})

	t.Run("guard failures complete the handle immediately", func(t *testing.T) {
		system := newRequestTestSystem(t)
		ctx := context.Background()

		target := &scriptedGrain{receive: func(*GrainContext) {}}
		targetID := activateReentrantGrain(t, system, target, "guard-target")

		captured := make(chan error, 5)
		then := func(_ any, err error) { captured <- err }

		caller := &scriptedGrain{receive: func(gctx *GrainContext) {
			gctx.RequestGrain(targetID, nil).Then(then)
			gctx.RequestGrain(nil, new(testpb.TestPing)).Then(then)
			gctx.RequestGrain(&GrainIdentity{}, new(testpb.TestPing)).Then(then)
			gctx.RequestGrain(targetID, new(testpb.TestPing), WithReentrancyMode(reentrancy.Off)).Then(then)
			gctx.RequestGrain(targetID, new(testpb.TestPing), WithReentrancyMode(reentrancy.Mode(99))).Then(then)
			gctx.NoErr()
		}}
		callerID := activateReentrantGrain(t, system, caller, "guard-caller")

		require.NoError(t, system.TellGrain(ctx, callerID, new(testpb.TestSend)))

		expected := []error{
			gerrors.ErrInvalidMessage,
			gerrors.ErrInvalidGrainIdentity,
			gerrors.ErrInvalidGrainIdentity,
			gerrors.ErrReentrancyDisabled,
			gerrors.ErrInvalidReentrancyMode,
		}

		for _, want := range expected {
			select {
			case got := <-captured:
				require.ErrorIs(t, got, want)
			case <-time.After(time.Second):
				t.Fatalf("missing completed-handle error %v", want)
			}
		}
	})

	t.Run("reentrancy disabled on the caller", func(t *testing.T) {
		system := newRequestTestSystem(t)
		ctx := context.Background()

		failures := make(chan error, 1)
		plain := &scriptedGrain{receive: func(gctx *GrainContext) {
			gctx.RequestGrain(&GrainIdentity{kind: "Kind", name: "name"}, new(testpb.TestPing)).Then(func(_ any, err error) {
				failures <- err
			})
			gctx.NoErr()
		}}

		identity, err := system.GrainIdentity(ctx, "plain-grain", func(context.Context) (Grain, error) {
			return plain, nil
		})
		require.NoError(t, err)

		require.NoError(t, system.TellGrain(ctx, identity, new(testpb.TestSend)))

		select {
		case err := <-failures:
			require.ErrorIs(t, err, gerrors.ErrReentrancyDisabled)
		case <-time.After(time.Second):
			t.Fatal("continuation never ran")
		}
	})

	t.Run("in-flight limit", func(t *testing.T) {
		system := newRequestTestSystem(t)
		ctx := context.Background()

		silent := &scriptedGrain{receive: func(*GrainContext) {}}
		silentID := activateReentrantGrain(t, system, silent, "silent-grain")

		failures := make(chan error, 1)
		caller := &scriptedGrain{receive: func(gctx *GrainContext) {
			first := gctx.RequestGrain(silentID, new(testpb.TestPing), WithRequestTimeout(0))
			gctx.RequestGrain(silentID, new(testpb.TestPing)).Then(func(_ any, err error) {
				failures <- err
			})
			_ = first.Cancel()
			gctx.NoErr()
		}}

		callerID := activateReentrantGrain(t, system, caller, "limited-caller")
		pid, ok := system.grains.Get(callerID.String())
		require.True(t, ok)
		pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 1))

		require.NoError(t, system.TellGrain(ctx, callerID, new(testpb.TestSend)))

		select {
		case err := <-failures:
			require.ErrorIs(t, err, gerrors.ErrReentrancyInFlightLimit)
		case <-time.After(time.Second):
			t.Fatal("continuation never ran")
		}
	})

	t.Run("delivery failure deregisters the request", func(t *testing.T) {
		system := newRequestTestSystem(t)
		ctx := context.Background()

		// An identity whose kind was never registered cannot activate.
		unknown := newGrainIdentity(&MockGrainActivationFailure{}, "never-registered")

		failures := make(chan error, 1)
		caller := &scriptedGrain{receive: func(gctx *GrainContext) {
			gctx.RequestGrain(unknown, new(testpb.TestPing)).Then(func(_ any, err error) {
				failures <- err
			})
			gctx.NoErr()
		}}
		callerID := activateReentrantGrain(t, system, caller, "orphan-caller")

		require.NoError(t, system.TellGrain(ctx, callerID, new(testpb.TestSend)))

		select {
		case err := <-failures:
			require.Error(t, err)
		case <-time.After(time.Second):
			t.Fatal("continuation never ran")
		}

		pid, ok := system.grains.Get(callerID.String())
		require.True(t, ok)
		require.Zero(t, pid.reentrancy.Load().inFlightCount.Load())
	})

	t.Run("default timeout is armed and disable works", func(t *testing.T) {
		system := newRequestTestSystem(t)
		ctx := context.Background()

		silent := &scriptedGrain{receive: func(*GrainContext) {}}
		silentID := activateReentrantGrain(t, system, silent, "silent-grain")

		issued := make(chan struct{}, 1)
		caller := &scriptedGrain{receive: func(gctx *GrainContext) {
			switch gctx.Message().(type) {
			case *testpb.TestPing:
				gctx.RequestGrain(silentID, new(testpb.TestSend))
			case *testpb.TestBye:
				gctx.RequestGrain(silentID, new(testpb.TestSend), WithRequestTimeout(0))
			}

			issued <- struct{}{}
			gctx.NoErr()
		}}
		callerID := activateReentrantGrain(t, system, caller, "timeout-caller")
		pid, ok := system.grains.Get(callerID.String())
		require.True(t, ok)

		requireArmed := func(want bool) {
			states := pid.reentrancy.Load().requestStates.Values()
			require.Len(t, states, 1)

			state := states[0]
			state.mu.Lock()
			armed := state.stopTimeout != nil
			state.mu.Unlock()
			require.Equal(t, want, armed)

			// Complete inline to leave the fixture clean for the next case.
			state.stopTimeoutIfSet()
			pid.reentrancy.Load().requestStates.Delete(state.id)
			pid.reentrancy.Load().inFlightCount.Dec()
		}

		require.NoError(t, system.TellGrain(ctx, callerID, new(testpb.TestPing)))
		<-issued
		requireArmed(true)

		require.NoError(t, system.TellGrain(ctx, callerID, new(testpb.TestBye)))
		<-issued
		requireArmed(false)
	})
}

func TestGrainDeferResponseFromContinuation(t *testing.T) {
	// The marquee flow: a grain defers its ask reply, requests another grain
	// and completes the deferred reply from the continuation, all without
	// blocking a single turn.
	system := newRequestTestSystem(t)
	ctx := context.Background()

	target := &scriptedGrain{receive: func(gctx *GrainContext) {
		gctx.Response(testpb.TestCount_builder{Value: 42}.Build())
	}}
	targetID := activateReentrantGrain(t, system, target, "answer-grain")

	front := &scriptedGrain{}
	front.receive = func(gctx *GrainContext) {
		reply := gctx.DeferResponse()

		gctx.RequestGrain(targetID, new(testpb.TestPing)).Then(func(result any, err error) {
			if err != nil {
				reply.Err(err)
				return
			}
			reply.Response(result)
		})
	}
	frontID := activateReentrantGrain(t, system, front, "front-grain")

	response, err := system.AskGrain(ctx, frontID, new(testpb.TestPing), 2*time.Second)
	require.NoError(t, err)

	count, ok := response.(*testpb.TestCount)
	require.True(t, ok)
	require.EqualValues(t, 42, count.GetValue())
}

func TestGrainRequestActor(t *testing.T) {
	t.Run("completes with the actor's response", func(t *testing.T) {
		system := newRequestTestSystem(t)
		ctx := context.Background()

		_, err := system.Spawn(ctx, "responder", &reentrancyTestActor{receive: func(rctx *ReceiveContext) {
			switch rctx.Message().(type) {
			case *testpb.TestPing:
				rctx.Response(testpb.TestCount_builder{Value: 7}.Build())
			default:
				rctx.Unhandled()
			}
		}})
		require.NoError(t, err)

		results := make(chan *testpb.TestCount, 1)
		failures := make(chan error, 1)

		caller := &scriptedGrain{receive: func(gctx *GrainContext) {
			gctx.RequestActor("responder", new(testpb.TestPing)).Then(func(result any, err error) {
				if err != nil {
					failures <- err
					return
				}
				results <- result.(*testpb.TestCount)
			})
			gctx.NoErr()
		}}
		callerID := activateReentrantGrain(t, system, caller, "actor-caller")

		require.NoError(t, system.TellGrain(ctx, callerID, new(testpb.TestSend)))

		select {
		case count := <-results:
			require.EqualValues(t, 7, count.GetValue())
		case err := <-failures:
			t.Fatalf("request failed: %v", err)
		case <-time.After(2 * time.Second):
			t.Fatal("continuation never ran")
		}
	})

	t.Run("unknown actor completes the handle immediately", func(t *testing.T) {
		system := newRequestTestSystem(t)
		ctx := context.Background()

		failures := make(chan error, 1)
		caller := &scriptedGrain{receive: func(gctx *GrainContext) {
			gctx.RequestActor("missing-actor", new(testpb.TestPing)).Then(func(_ any, err error) {
				failures <- err
			})
			gctx.NoErr()
		}}
		callerID := activateReentrantGrain(t, system, caller, "actor-caller")

		require.NoError(t, system.TellGrain(ctx, callerID, new(testpb.TestSend)))

		select {
		case err := <-failures:
			require.Error(t, err)
		case <-time.After(time.Second):
			t.Fatal("continuation never ran")
		}
	})
}

func TestGrainChannelLessReplyMethodsAreNoOps(t *testing.T) {
	// A response envelope context has no channels and no request ID: every
	// reply method must be a safe no-op instead of blocking the worker.
	gctx := getGrainContext(0).build(context.Background(), nil, nil, &GrainIdentity{kind: "Kind", name: "name"}, new(testpb.TestSend), grainEnvelope)
	t.Cleanup(func() {
		releaseGrainContext(gctx)
	})

	require.NotPanics(t, func() {
		gctx.Err(errors.New("dropped"))
		gctx.NoErr()
		gctx.Response(new(testpb.TestReply))
		gctx.Unhandled()
	})
}

func TestGrainSendAsyncReplyDeliveryFailureIsSwallowed(t *testing.T) {
	system := newRequestTestSystem(t)

	grain := &scriptedGrain{receive: func(gctx *GrainContext) {
		gctx.Response(testpb.Reply_builder{Content: "unroutable"}.Build())
	}}
	identity := activateReentrantGrain(t, system, grain, "unroutableReplier")

	pid, ok := system.grains.Get(identity.String())
	require.True(t, ok)

	// The reply target names a grain kind that was never registered, so the
	// reply cannot be delivered; the failure is logged at debug and the turn
	// completes normally.
	require.NoError(t, pid.enqueueEnvelope(context.Background(), &commands.AsyncRequest{
		CorrelationID: "corr",
		ReplyTo:       &commands.AsyncReplyTo{Kind: commands.ReplyToGrain, Grain: "neverRegistered/nope"},
		Message:       new(testpb.TestPing),
	}))

	require.Eventually(t, func() bool {
		return pid.processedCount.Load() > 0
	}, 2*time.Second, 10*time.Millisecond)
}

func TestGrainRequestGuardsWithoutSystem(t *testing.T) {
	captureError := func(call RequestCall) error {
		errCh := make(chan error, 1)
		call.Then(func(_ any, err error) {
			errCh <- err
		})
		return <-errCh
	}

	t.Run("inactive grain", func(t *testing.T) {
		gctx := &GrainContext{pid: &grainPID{}}
		require.ErrorIs(t, captureError(gctx.RequestGrain(&GrainIdentity{}, new(testpb.TestPing))), gerrors.ErrDead)
		require.ErrorIs(t, captureError(gctx.RequestActor("actor", new(testpb.TestPing))), gerrors.ErrDead)
	})

	t.Run("nil message", func(t *testing.T) {
		pid := &grainPID{}
		pid.activated.Store(true)
		gctx := &GrainContext{pid: pid}
		require.ErrorIs(t, captureError(gctx.RequestActor("actor", nil)), gerrors.ErrInvalidMessage)
	})

	t.Run("reentrancy disabled", func(t *testing.T) {
		pid := &grainPID{}
		pid.activated.Store(true)
		gctx := &GrainContext{pid: pid}
		require.ErrorIs(t, captureError(gctx.RequestActor("actor", new(testpb.TestPing))), gerrors.ErrReentrancyDisabled)
	})
}

func TestGrainRequestActorAdmissionFailure(t *testing.T) {
	system := newRequestTestSystem(t)
	ctx := context.Background()

	_, err := system.Spawn(ctx, "idle-responder", &reentrancyTestActor{receive: func(*ReceiveContext) {}})
	require.NoError(t, err)

	failures := make(chan error, 1)
	caller := &scriptedGrain{receive: func(gctx *GrainContext) {
		gctx.RequestActor("idle-responder", new(testpb.TestPing), WithReentrancyMode(reentrancy.Off)).Then(func(_ any, err error) {
			failures <- err
		})
		gctx.NoErr()
	}}
	callerID := activateReentrantGrain(t, system, caller, "off-mode-caller")

	require.NoError(t, system.TellGrain(ctx, callerID, new(testpb.TestSend)))

	select {
	case err := <-failures:
		require.ErrorIs(t, err, gerrors.ErrReentrancyDisabled)
	case <-time.After(time.Second):
		t.Fatal("continuation never ran")
	}
}

// TestGrainEnableReentrancyAtRuntime covers the runtime toggle on grains: a
// grain activated without reentrancy enables it during message processing,
// requests, sees asks switch to the envelope path, disables, and reverts.
func TestGrainEnableReentrancyAtRuntime(t *testing.T) {
	system := newRequestTestSystem(t)
	ctx := context.Background()

	target := &scriptedGrain{receive: func(gctx *GrainContext) {
		gctx.Response(testpb.TestCount_builder{Value: 42}.Build())
	}}
	targetID := activateReentrantGrain(t, system, target, "toggle-target")

	results := make(chan *testpb.TestCount, 2)
	failures := make(chan error, 4)
	correlations := make(chan string, 4)

	// TestSend enables, TestBye disables, TestPing requests, TestReply probes
	// which ask path the message arrived on.
	toggling := &scriptedGrain{receive: func(gctx *GrainContext) {
		switch gctx.Message().(type) {
		case *testpb.TestSend:
			if err := gctx.EnableReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))); err != nil {
				failures <- err
			}
			gctx.NoErr()
		case *testpb.TestBye:
			gctx.DisableReentrancy()
			gctx.NoErr()
		case *testpb.TestPing:
			gctx.RequestGrain(targetID, new(testpb.TestPing)).Then(func(result any, err error) {
				if err != nil {
					failures <- err
					return
				}
				results <- result.(*testpb.TestCount)
			})
			gctx.NoErr()
		case *testpb.TestReply:
			correlations <- gctx.CorrelationID()
			gctx.Response(testpb.Reply_builder{Content: "probe"}.Build())
		}
	}}

	identity, err := system.GrainIdentity(ctx, "toggling-grain", func(context.Context) (Grain, error) {
		return toggling, nil
	})
	require.NoError(t, err)

	// Without reentrancy: requests are rejected and asks take the channel path.
	require.NoError(t, system.TellGrain(ctx, identity, new(testpb.TestPing)))
	select {
	case err := <-failures:
		require.ErrorIs(t, err, gerrors.ErrReentrancyDisabled)
	case <-time.After(time.Second):
		t.Fatal("expected rejection before enable")
	}

	_, err = system.AskGrain(ctx, identity, new(testpb.TestReply), time.Second)
	require.NoError(t, err)
	require.Empty(t, <-correlations)

	// Enabled at runtime: requests complete and asks switch to the envelope path.
	require.NoError(t, system.TellGrain(ctx, identity, new(testpb.TestSend)))
	require.NoError(t, system.TellGrain(ctx, identity, new(testpb.TestPing)))
	select {
	case count := <-results:
		require.EqualValues(t, 42, count.GetValue())
	case err := <-failures:
		t.Fatalf("request failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("request never completed")
	}

	_, err = system.AskGrain(ctx, identity, new(testpb.TestReply), time.Second)
	require.NoError(t, err)
	require.NotEmpty(t, <-correlations)

	// Disabled again: requests rejected, asks revert to the channel path.
	require.NoError(t, system.TellGrain(ctx, identity, new(testpb.TestBye)))
	require.NoError(t, system.TellGrain(ctx, identity, new(testpb.TestPing)))
	select {
	case err := <-failures:
		require.ErrorIs(t, err, gerrors.ErrReentrancyDisabled)
	case <-time.After(time.Second):
		t.Fatal("expected rejection after disable")
	}

	_, err = system.AskGrain(ctx, identity, new(testpb.TestReply), time.Second)
	require.NoError(t, err)
	require.Empty(t, <-correlations)
}

// TestGrainEnableReentrancyUnderConcurrentAsks exercises the gate flip under
// the race detector: the grain enables reentrancy while concurrent askers
// straddle both ask paths.
func TestGrainEnableReentrancyUnderConcurrentAsks(t *testing.T) {
	system := newRequestTestSystem(t)
	ctx := context.Background()

	grain := &scriptedGrain{receive: func(gctx *GrainContext) {
		if _, ok := gctx.Message().(*testpb.TestSend); ok {
			_ = gctx.EnableReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll)))
			gctx.NoErr()
			return
		}

		// Response is dual-mode: it serves the channel path and the envelope
		// path alike, so both ask generations complete.
		gctx.Response(testpb.Reply_builder{Content: "ok"}.Build())
	}}

	identity, err := system.GrainIdentity(ctx, "racing-grain", func(context.Context) (Grain, error) {
		return grain, nil
	})
	require.NoError(t, err)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for range 20 {
				_, err := system.AskGrain(ctx, identity, new(testpb.TestPing), time.Second)
				if err != nil {
					t.Errorf("ask failed: %v", err)
					return
				}
			}
		}()
	}

	// Flip the gate mid-hammer.
	require.NoError(t, system.TellGrain(ctx, identity, new(testpb.TestSend)))
	wg.Wait()
}

func TestGrainEnableReentrancyValidation(t *testing.T) {
	pid := &grainPID{}

	require.ErrorIs(t, pid.enableReentrancy(nil), gerrors.ErrInvalidReentrancyMode)
	require.ErrorIs(t, pid.enableReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.Mode(99)))), gerrors.ErrInvalidReentrancyMode)

	require.NoError(t, pid.enableReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.StashNonReentrant), reentrancy.WithMaxInFlight(4))))
	reentrant := pid.reentrancy.Load()
	require.NotNil(t, reentrant)
	require.Equal(t, reentrancy.StashNonReentrant, reentrant.getMode())
	require.EqualValues(t, 4, reentrant.maxInFlight.Load())

	pid.disableReentrancy()
	require.Same(t, reentrant, pid.reentrancy.Load())
	require.Equal(t, reentrancy.Off, reentrant.getMode())

	fresh := &grainPID{}
	require.NotPanics(t, fresh.disableReentrancy)
	require.Nil(t, fresh.reentrancy.Load())
}

// TestGrainRequestCycle drives the marquee reentrancy scenario across two
// grains: A requests B while B, still owing its reply, requests A back. Both
// run AllowAll, so every hop processes without pausing and the cycle completes
// without ErrRequestTimeout.
func TestGrainRequestCycle(t *testing.T) {
	system := newRequestTestSystem(t)
	ctx := context.Background()

	results := make(chan *testpb.TestCount, 1)
	failures := make(chan error, 2)

	var identityA *GrainIdentity

	grainB := &scriptedGrain{receive: func(gctx *GrainContext) {
		if _, ok := gctx.Message().(*testpb.TestPing); !ok {
			return
		}

		reply := gctx.DeferResponse()
		gctx.RequestGrain(identityA, new(testpb.TestGetCount)).Then(func(result any, err error) {
			if err != nil {
				failures <- err
				return
			}
			reply.Response(result.(*testpb.TestCount))
		})
	}}
	identityB := activateReentrantGrain(t, system, grainB, "cycle-b")

	grainA := &scriptedGrain{receive: func(gctx *GrainContext) {
		switch gctx.Message().(type) {
		case *testpb.TestSend:
			gctx.RequestGrain(identityB, new(testpb.TestPing)).Then(func(result any, err error) {
				if err != nil {
					failures <- err
					return
				}
				results <- result.(*testpb.TestCount)
			})
			gctx.NoErr()
		case *testpb.TestGetCount:
			gctx.Response(testpb.TestCount_builder{Value: 42}.Build())
		}
	}}
	identityA = activateReentrantGrain(t, system, grainA, "cycle-a")

	require.NoError(t, system.TellGrain(ctx, identityA, new(testpb.TestSend)))

	select {
	case count := <-results:
		require.EqualValues(t, 42, count.GetValue())
	case err := <-failures:
		t.Fatalf("cycle failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("reentrant cycle never completed")
	}
}

// TestGrainSelfRequest verifies a grain can request itself under AllowAll: the
// request queues behind the current turn and the reply completes the
// continuation on a later turn.
func TestGrainSelfRequest(t *testing.T) {
	system := newRequestTestSystem(t)
	ctx := context.Background()

	results := make(chan *testpb.TestCount, 1)
	failures := make(chan error, 1)

	grain := &scriptedGrain{receive: func(gctx *GrainContext) {
		switch gctx.Message().(type) {
		case *testpb.TestSend:
			gctx.RequestGrain(gctx.Self(), new(testpb.TestGetCount)).Then(func(result any, err error) {
				if err != nil {
					failures <- err
					return
				}
				results <- result.(*testpb.TestCount)
			})
			gctx.NoErr()
		case *testpb.TestGetCount:
			gctx.Response(testpb.TestCount_builder{Value: 7}.Build())
		}
	}}
	identity := activateReentrantGrain(t, system, grain, "self-request-grain")

	require.NoError(t, system.TellGrain(ctx, identity, new(testpb.TestSend)))

	select {
	case count := <-results:
		require.EqualValues(t, 7, count.GetValue())
	case err := <-failures:
		t.Fatalf("self-request failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("self-request never completed")
	}
}

// TestGrainMaxInFlightBurst issues more requests in one turn than maxInFlight
// admits and pins the counter bookkeeping end to end through the real config
// plumbing: exactly the configured number are admitted, the excess fail with
// ErrReentrancyInFlightLimit, and completions drain the counter back to zero.
func TestGrainMaxInFlightBurst(t *testing.T) {
	system := newRequestTestSystem(t)
	ctx := context.Background()

	replies := make(chan *GrainReply, 4)
	target := &scriptedGrain{receive: func(gctx *GrainContext) {
		replies <- gctx.DeferResponse()
	}}

	targetID, err := system.GrainIdentity(ctx, "burst-target", func(context.Context) (Grain, error) {
		return target, nil
	})
	require.NoError(t, err)

	outcomes := make(chan error, 4)
	caller := &scriptedGrain{receive: func(gctx *GrainContext) {
		for range 4 {
			gctx.RequestGrain(targetID, new(testpb.TestPing), WithRequestTimeout(0)).Then(func(_ any, err error) {
				outcomes <- err
			})
		}
		gctx.NoErr()
	}}

	callerID, err := system.GrainIdentity(ctx, "burst-caller", func(context.Context) (Grain, error) {
		return caller, nil
	}, WithGrainReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll), reentrancy.WithMaxInFlight(2))))
	require.NoError(t, err)

	require.NoError(t, system.TellGrain(ctx, callerID, new(testpb.TestSend)))

	// The two rejections complete their handles in-turn, before any admitted
	// request can finish: the target still holds every reply.
	for range 2 {
		select {
		case err := <-outcomes:
			require.ErrorIs(t, err, gerrors.ErrReentrancyInFlightLimit)
		case <-time.After(time.Second):
			t.Fatal("expected an in-flight limit rejection")
		}
	}

	pid, ok := system.grains.Get(callerID.String())
	require.True(t, ok)
	require.EqualValues(t, 2, pid.reentrancy.Load().inFlightCount.Load())

	for range 2 {
		select {
		case reply := <-replies:
			reply.NoErr()
		case <-time.After(time.Second):
			t.Fatal("target never received the admitted request")
		}
	}

	for range 2 {
		select {
		case err := <-outcomes:
			require.NoError(t, err)
		case <-time.After(time.Second):
			t.Fatal("admitted request never completed")
		}
	}

	require.Eventually(t, func() bool {
		return pid.reentrancy.Load().inFlightCount.Load() == 0
	}, time.Second, 10*time.Millisecond)
}

// TestGrainRequestLateReplyIdempotence completes a request through timeout or
// Cancel first and then lets the genuine reply arrive late. The late response
// must drop without effect: one continuation invocation, counters at zero.
func TestGrainRequestLateReplyIdempotence(t *testing.T) {
	t.Run("timeout then late reply", func(t *testing.T) {
		system := newRequestTestSystem(t)
		ctx := context.Background()

		replies := make(chan *GrainReply, 1)
		target := &scriptedGrain{receive: func(gctx *GrainContext) {
			replies <- gctx.DeferResponse()
		}}

		targetID, err := system.GrainIdentity(ctx, "late-target", func(context.Context) (Grain, error) {
			return target, nil
		})
		require.NoError(t, err)

		outcomes := make(chan error, 2)
		caller := &scriptedGrain{receive: func(gctx *GrainContext) {
			gctx.RequestGrain(targetID, new(testpb.TestPing), WithRequestTimeout(150*time.Millisecond)).Then(func(_ any, err error) {
				outcomes <- err
			})
			gctx.NoErr()
		}}
		callerID := activateReentrantGrain(t, system, caller, "late-caller")

		require.NoError(t, system.TellGrain(ctx, callerID, new(testpb.TestSend)))

		select {
		case err := <-outcomes:
			require.ErrorIs(t, err, gerrors.ErrRequestTimeout)
		case <-time.After(time.Second):
			t.Fatal("timeout never completed the request")
		}

		var reply *GrainReply

		select {
		case reply = <-replies:
		case <-time.After(time.Second):
			t.Fatal("target never received the request")
		}

		// The genuine reply lands after the timeout already completed the
		// state: it must drop as an unknown correlation.
		reply.Response(testpb.Reply_builder{Content: "late"}.Build())
		pause.For(200 * time.Millisecond)

		pid, ok := system.grains.Get(callerID.String())
		require.True(t, ok)
		require.Zero(t, pid.reentrancy.Load().inFlightCount.Load())
		require.Empty(t, outcomes)
	})

	t.Run("cancel then late reply", func(t *testing.T) {
		system := newRequestTestSystem(t)
		ctx := context.Background()

		replies := make(chan *GrainReply, 1)
		target := &scriptedGrain{receive: func(gctx *GrainContext) {
			replies <- gctx.DeferResponse()
		}}

		targetID, err := system.GrainIdentity(ctx, "late-cancel-target", func(context.Context) (Grain, error) {
			return target, nil
		})
		require.NoError(t, err)

		calls := make(chan RequestCall, 1)
		outcomes := make(chan error, 2)

		caller := &scriptedGrain{receive: func(gctx *GrainContext) {
			call := gctx.RequestGrain(targetID, new(testpb.TestPing), WithRequestTimeout(0))
			call.Then(func(_ any, err error) {
				outcomes <- err
			})
			calls <- call
			gctx.NoErr()
		}}
		callerID := activateReentrantGrain(t, system, caller, "late-cancel-caller")

		require.NoError(t, system.TellGrain(ctx, callerID, new(testpb.TestSend)))

		var reply *GrainReply

		select {
		case reply = <-replies:
		case <-time.After(time.Second):
			t.Fatal("target never received the request")
		}

		call := <-calls
		require.NoError(t, call.Cancel())

		select {
		case err := <-outcomes:
			require.ErrorIs(t, err, gerrors.ErrRequestCanceled)
		case <-time.After(time.Second):
			t.Fatal("cancel never completed the request")
		}

		// The genuine reply after the cancellation must drop without effect.
		reply.Response(testpb.Reply_builder{Content: "late"}.Build())
		pause.For(200 * time.Millisecond)

		pid, ok := system.grains.Get(callerID.String())
		require.True(t, ok)
		require.Zero(t, pid.reentrancy.Load().inFlightCount.Load())
		require.Empty(t, outcomes)
	})
}
