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

package stream

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/actor"
)

func TestPullSourceActor_NaturalCompletion(t *testing.T) {
	// Verifies that a finite Of source completes naturally and delivers all elements.
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	input := make([]int, 300)
	for i := range input {
		input[i] = i
	}
	col, sink := Collect[int]()
	handle, err := Of(input...).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.NotNil(t, col.Items())
}

func TestPullSourceActor_Cancel_ViaStop(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	// Use a large Unfold that runs indefinitely so we can cancel it.
	// Ignore sink never blocks, so the cancel propagates cleanly.
	handle, err := Unfold(0, func(s int) (int, int, bool) {
		return s + 1, s, true // infinite
	}).To(Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)

	time.Sleep(20 * time.Millisecond)
	require.NoError(t, handle.Stop(ctx))

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not stop after cancel")
	}
}

// wrongTypePullActor responds to PullRequest with a string instead of PullResponse[int].
type wrongTypePullActor struct{}

func (a *wrongTypePullActor) PreStart(_ *actor.Context) error { return nil }
func (a *wrongTypePullActor) PostStop(_ *actor.Context) error { return nil }
func (a *wrongTypePullActor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *PullRequest:
		ctx.Response("wrong type") // not *PullResponse[int]
	default:
		ctx.Unhandled()
	}
}

func TestActorSourceActor_FetchErr_WrongType(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	pid, err := sys.Spawn(ctx, "wrong-type-pull", &wrongTypePullActor{})
	require.NoError(t, err)

	handle, err := FromActor[int](pid).To(Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("stream did not terminate after fetchErr")
	}
}

// silentPullActor never responds to PullRequest, causing actor.Ask to time out.
type silentPullActor struct{}

func (a *silentPullActor) PreStart(_ *actor.Context) error { return nil }
func (a *silentPullActor) PostStop(_ *actor.Context) error { return nil }
func (a *silentPullActor) Receive(ctx *actor.ReceiveContext) {
	ctx.Unhandled() // intentionally ignore all messages
}

func TestActorSourceActor_FetchErr_Timeout(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	pid, err := sys.Spawn(ctx, "silent-pull", &silentPullActor{})
	require.NoError(t, err)

	// Build a source with very short pull timeout so actor.Ask times out.
	config := defaultStageConfig()
	config.PullTimeout = 50 * time.Millisecond
	desc := &stageDesc{
		id:   newStageID(),
		kind: sourceKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newActorSourceActor[int](pid, cfg)
		},
		config: config,
	}
	src := Source[int]{stages: []*stageDesc{desc}}

	handle, err := src.To(Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not terminate after fetch timeout")
	}
}

// stalledPullActor responds to the first PullRequest with elements, then stalls
// so the source actor is waiting for a second pull when we kill the upstream.
type stalledPullActor struct {
	calls int
}

func (a *stalledPullActor) PreStart(_ *actor.Context) error { return nil }
func (a *stalledPullActor) PostStop(_ *actor.Context) error { return nil }
func (a *stalledPullActor) Receive(ctx *actor.ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *PullRequest:
		a.calls++
		if a.calls == 1 {
			// First pull: send some elements so the source actor sends them downstream
			// and then issues a second pull.
			elems := make([]int, msg.N)
			for i := range elems {
				elems[i] = i
			}
			ctx.Response(&PullResponse[int]{Elements: elems})
		}
		// Second pull: don't respond — the actor will be stopped externally.
	default:
		ctx.Unhandled()
	}
}

func TestActorSourceActor_Terminated(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	pid, err := sys.Spawn(ctx, "stalled-pull", &stalledPullActor{})
	require.NoError(t, err)

	handle, err := FromActor[int](pid).To(Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)

	// Let the first batch be consumed, then kill the upstream actor.
	// The actorSourceActor is watching it via rctx.Watch and should receive
	// actor.Terminated, which propagates a streamError downstream.
	require.Eventually(t, func() bool {
		return handle.Metrics().ElementsIn > 0
	}, 3*time.Second, 5*time.Millisecond)

	require.NoError(t, pid.Shutdown(ctx))

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not terminate after upstream actor was killed")
	}
}

// TestCombineSourceActor_TypeMismatch injects a wrong-typed value into the left
// buffer of a combineSourceActor so that the type assertion in tryEmit fails,
// exercises the defensive streamError + shutdown path.
func TestCombineSourceActor_TypeMismatch(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	downPID, err := sys.Spawn(ctx, "combine-tm-down", &dummyStageActor{})
	require.NoError(t, err)

	// combineSourceActor[int, string, string]: left expects int, right expects string.
	// Provide a real system so sub-pipeline spawning doesn't panic, even though
	// empty leftStages/rightStages causes validation to fail silently.
	cfg := defaultStageConfig()
	cfg.System = sys
	a := newCombineSourceActor(
		nil, nil,
		func(_ int, s string) string { return s },
		cfg,
	)
	actorPID, err := sys.Spawn(ctx, "combine-tm-actor", a)
	require.NoError(t, err)

	require.NoError(t, actor.Tell(ctx, actorPID, &stageWire{
		subID: "unit", upstream: nil, downstream: downPID,
	}))
	time.Sleep(20 * time.Millisecond)

	// Signal demand first so tryEmit will attempt to emit.
	require.NoError(t, actor.Tell(ctx, actorPID, &streamRequest{subID: "unit", n: 5}))
	time.Sleep(5 * time.Millisecond)

	// Inject wrong-typed value into left slot (expects int, receives string).
	require.NoError(t, actor.Tell(ctx, actorPID, &mergeSubValue{slot: 0, value: "not-an-int"}))
	// Inject a valid right value.
	require.NoError(t, actor.Tell(ctx, actorPID, &mergeSubValue{slot: 1, value: "valid"}))

	// The actor detects the type mismatch in tryEmit, sends streamError downstream,
	// and shuts down.
	require.Eventually(t, func() bool {
		_, err := sys.ActorOf(ctx, actorPID.Name())
		return err != nil
	}, 2*time.Second, 5*time.Millisecond)
}

// infinitePullActor always returns elements so we can cancel mid-stream.
type infinitePullActor struct{}

func (a *infinitePullActor) PreStart(_ *actor.Context) error { return nil }
func (a *infinitePullActor) PostStop(_ *actor.Context) error { return nil }
func (a *infinitePullActor) Receive(ctx *actor.ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *PullRequest:
		elems := make([]int, msg.N)
		for i := range elems {
			elems[i] = i
		}
		ctx.Response(&PullResponse[int]{Elements: elems})
	default:
		ctx.Unhandled()
	}
}

func TestActorSourceActor_Cancel(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	pid, err := sys.Spawn(ctx, "infinite-pull", &infinitePullActor{})
	require.NoError(t, err)

	handle, err := FromActor[int](pid).To(Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)

	time.Sleep(20 * time.Millisecond)
	require.NoError(t, handle.Stop(ctx))

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("actor source did not stop after cancel")
	}
}
