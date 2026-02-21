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

	"github.com/tochemey/goakt/v4/breaker"
	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// nolint
func TestReleaseGrainContextResetsFields(t *testing.T) {
	ctx := getGrainContext()

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
	acquired := getGrainContext()
	releaseGrainContext(acquired)
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
			return &testpb.Reply{Content: "ok"}, nil
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
			return &testpb.Reply{Content: "late"}, nil
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
			return &testpb.Reply{Content: "ok"}, nil
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
			return &testpb.Reply{Content: "ok"}, nil
		})
		require.ErrorIs(t, err, gerrors.ErrInvalidGrainIdentity)
	})

	t.Run("invalid identity", func(t *testing.T) {
		invalid := &GrainIdentity{kind: "bad", name: ""}
		err := gctx.PipeToGrain(invalid, func() (any, error) {
			return &testpb.Reply{Content: "ok"}, nil
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
			return &testpb.Reply{Content: "ok"}, nil
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
			return &testpb.Reply{Content: "ok"}, nil
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
			return &testpb.Reply{Content: "ok"}, nil
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
				return &testpb.Reply{Content: "ok"}, nil
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
