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

// Package stream internal tests for flow stage actor edge cases.
package stream

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/actor"
)

// TestBatchFlowActor_TimerFlush_NonEmptyWindow tests that a batchFlush message
// while the window is non-empty triggers an actual flush.
// This covers lines 209-213 (the *batchFlush case calling flush).
func TestBatchFlowActor_TimerFlush_NonEmptyWindow(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	// Send elements one-by-one with a pause so the timer fires before the source completes.
	ch := make(chan int)
	go func() {
		ch <- 1
		time.Sleep(150 * time.Millisecond) // let the 50ms timer fire with 1 element in window
		ch <- 2
		close(ch)
	}()

	// Batch size=10 so no size-based flush; timer=50ms fires before element 2 arrives.
	col, sink := Collect[[]int]()
	handle, err := Via(
		FromChannel(ch),
		Batch[int](10, 50*time.Millisecond),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("batch timer-flush stream did not complete")
	}

	batches := col.Items()
	require.NotEmpty(t, batches)
	// Element 1 flushed by timer, element 2 flushed by streamComplete.
	var all []int
	for _, b := range batches {
		all = append(all, b...)
	}
	assert.ElementsMatch(t, []int{1, 2}, all)
}

// TestBatchFlowActor_StreamError_FromUpstream tests that a *streamError received
// by the batchFlowActor is forwarded downstream.
// This covers lines 223-225 (the *streamError case in batchFlowActor).
func TestBatchFlowActor_StreamError_FromUpstream(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	// Error on element 1 — TryMap sends streamError to the Batch flow.
	handle, err := Via(
		Via(
			Of(1, 2, 3),
			TryMap(func(n int) (int, error) {
				if n == 1 {
					return 0, errors.New("upstream error")
				}
				return n, nil
			}),
		),
		Batch[int](10, time.Second),
	).To(Ignore[[]int]()).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream with batch+upstream-error did not terminate")
	}
}

// TestBatchFlowActor_SizeFlush_ThenTimerFires verifies that when a size-based flush
// empties the window, a subsequent timer fire is handled gracefully (empty window case,
// lines 209-211 covered without calling flush).
func TestBatchFlowActor_SizeFlush_ThenTimerFires(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	// 2 elements at batch size=2: size flush at element 2 empties the window.
	// A short timer (20ms) will fire afterward when the window is already empty.
	col, sink := Collect[[]int]()
	handle, err := Via(
		Of(1, 2),
		Batch[int](2, 20*time.Millisecond),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not complete")
	}

	// One batch of [1,2] expected (size flush triggered before timer).
	items := col.Items()
	assert.NotEmpty(t, items)
	var all []int
	for _, b := range items {
		all = append(all, b...)
	}
	assert.ElementsMatch(t, []int{1, 2}, all)
}

// dummyStageActor is a no-op actor used as a stand-in upstream or downstream
// in unit tests for individual stage actors.
type dummyStageActor struct{}

func (a *dummyStageActor) PreStart(_ *actor.Context) error    { return nil }
func (a *dummyStageActor) PostStop(_ *actor.Context) error    { return nil }
func (a *dummyStageActor) Receive(rctx *actor.ReceiveContext) { rctx.Unhandled() }

// TestFlowActor_StreamCancel_Unit verifies that a flowActor's *streamCancel handler
// (lines that propagate cancel to upstream) is executed. A direct actor-level test
// is used because the integration pipeline signals handle.Done the moment the sink
// stops, before the flowActor necessarily processes the cancel from its mailbox.
func TestFlowActor_StreamCancel_Unit(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	upPID, err := sys.Spawn(ctx, "flow-cancel-up", &dummyStageActor{})
	require.NoError(t, err)
	downPID, err := sys.Spawn(ctx, "flow-cancel-down", &dummyStageActor{})
	require.NoError(t, err)

	fa := newFlowActor(func(v any) ([]any, error) { return []any{v}, nil }, defaultStageConfig())
	flowPID, err := sys.Spawn(ctx, "flow-cancel-actor", fa)
	require.NoError(t, err)

	// Wire the flowActor.
	require.NoError(t, actor.Tell(ctx, flowPID, &stageWire{
		subID: "unit", upstream: upPID, downstream: downPID,
	}))
	time.Sleep(10 * time.Millisecond)

	// Send a streamCancel (simulating a downstream cancel) and wait for the actor to stop.
	require.NoError(t, actor.Tell(ctx, flowPID, &streamCancel{subID: "unit"}))

	require.Eventually(t, func() bool {
		_, err := sys.ActorOf(ctx, flowPID.Name())
		return err != nil // actor stopped when lookup fails
	}, 2*time.Second, 5*time.Millisecond)
}

// TestBatchFlowActor_StreamCancel_Unit verifies that a batchFlowActor's
// *streamCancel handler propagates the cancel to its upstream. A direct
// actor-level test is used for reliability.
func TestBatchFlowActor_StreamCancel_Unit(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	upPID, err := sys.Spawn(ctx, "batch-cancel-up", &dummyStageActor{})
	require.NoError(t, err)
	downPID, err := sys.Spawn(ctx, "batch-cancel-down", &dummyStageActor{})
	require.NoError(t, err)

	ba := newBatchFlowActor[int](10, time.Second, defaultStageConfig())
	batchPID, err := sys.Spawn(ctx, "batch-cancel-actor", ba)
	require.NoError(t, err)

	require.NoError(t, actor.Tell(ctx, batchPID, &stageWire{
		subID: "unit", upstream: upPID, downstream: downPID,
	}))
	time.Sleep(10 * time.Millisecond)

	require.NoError(t, actor.Tell(ctx, batchPID, &streamCancel{subID: "unit"}))

	require.Eventually(t, func() bool {
		_, err := sys.ActorOf(ctx, batchPID.Name())
		return err != nil
	}, 2*time.Second, 5*time.Millisecond)
}

// TestBatchFlowActor_StreamCancel tests that a batchFlowActor propagates
// *streamCancel upstream when the downstream sink cancels. An infinite source
// ensures no *streamComplete arrives before the cancel.
func TestBatchFlowActor_StreamCancel(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	// A sink that errors on every []int batch it receives (FailFast).
	sentinel := errors.New("batch rejected")
	config := defaultStageConfig()
	config.ErrorStrategy = FailFast
	desc := &stageDesc{
		id:   newStageID(),
		kind: sinkKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newSinkActor(func(v any) error {
				_ = v.([]int)
				return sentinel
			}, nil, cfg)
		},
		config: config,
	}

	// Infinite source so the batchFlowActor never sees *streamComplete before the cancel.
	handle, err := Via(
		Unfold(1, func(s int) (int, int, bool) { return s + 1, s, true }),
		Batch[int](3, time.Second),
	).To(Sink[[]int]{desc: desc}).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("batch stream with cancel did not terminate")
	}
}

// TestBatchFlowActor_Flush_NoDownstreamDemand verifies that flush() returns
// immediately when downstreamDemand is zero, exercising the early-return branch.
func TestBatchFlowActor_Flush_NoDownstreamDemand(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	upPID, err := sys.Spawn(ctx, "batch-nd-up", &dummyStageActor{})
	require.NoError(t, err)
	downPID, err := sys.Spawn(ctx, "batch-nd-down", &dummyStageActor{})
	require.NoError(t, err)

	// batch size = 2, long timeout so only size-flush triggers.
	ba := newBatchFlowActor[int](2, 10*time.Second, defaultStageConfig())
	batchPID, err := sys.Spawn(ctx, "batch-nd-actor", ba)
	require.NoError(t, err)

	// Wire without any streamRequest → downstreamDemand stays at 0.
	require.NoError(t, actor.Tell(ctx, batchPID, &stageWire{
		subID: "unit", upstream: upPID, downstream: downPID,
	}))
	time.Sleep(10 * time.Millisecond)

	// Inject 2 elements to fill the batch window without any downstream demand.
	// flush() is called but returns early because downstreamDemand == 0.
	require.NoError(t, actor.Tell(ctx, batchPID, &streamElement{subID: "unit", value: 1, seqNo: 1}))
	require.NoError(t, actor.Tell(ctx, batchPID, &streamElement{subID: "unit", value: 2, seqNo: 2}))
	time.Sleep(20 * time.Millisecond)

	// Actor is still alive — flush returned early rather than panicking.
	_, err = sys.ActorOf(ctx, batchPID.Name())
	require.NoError(t, err)

	_ = batchPID.Shutdown(ctx)
}

// TestThrottleActor_StreamComplete_EmptyBuffer tests that throttleActor completes
// immediately when upstream sends *streamComplete with an empty buffer.
func TestThrottleActor_StreamComplete_EmptyBuffer(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	col, sink := Collect[int]()
	handle, err := Via(
		Of[int](), // zero elements → source immediately sends streamComplete
		Throttle[int](1, time.Millisecond),
	).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("throttle with empty source did not complete")
	}
	assert.Empty(t, col.Items())
}

// TestThrottleActor_StreamError tests that a throttleActor forwards a *streamError
// received from an upstream flow stage.
func TestThrottleActor_StreamError(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	handle, err := Via(
		Via(
			Of(1, 2, 3),
			TryMap(func(n int) (int, error) {
				if n == 1 {
					return 0, errors.New("upstream error")
				}
				return n, nil
			}),
		),
		Throttle[int](1, time.Millisecond),
	).To(Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("throttle with upstream error did not terminate")
	}
}

// TestFlowActor_ErrorSupervise tests the Supervise error strategy in flowActor,
// which currently behaves like FailFast (stream terminates on error).
func TestFlowActor_ErrorSupervise(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	config := defaultStageConfig()
	config.ErrorStrategy = Supervise
	flowDesc := &stageDesc{
		id:   newStageID(),
		kind: flowKind,
		makeActor: func(cfg StageConfig) actor.Actor {
			return newFlowActor(func(v any) ([]any, error) {
				n := v.(int)
				if n == 2 {
					return nil, errors.New("supervise-error")
				}
				return []any{n}, nil
			}, cfg)
		},
		config: config,
	}
	src := Of(1, 2, 3)
	_, sink := Collect[int]()
	stages := make([]*stageDesc, 0, len(src.stages)+2)
	stages = append(stages, src.stages...)
	stages = append(stages, flowDesc, sink.desc)
	handle, err := RunnableGraph{stages: stages}.Run(ctx, sys)
	require.NoError(t, err)
	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Supervise-strategy flow did not terminate")
	}
}

// TestFlowActor_MaybeRequestUpstream_NoAvailable exercises the available<=0
// early-return in flowActor.maybeRequestUpstream by saturating upstreamCredit.
func TestFlowActor_MaybeRequestUpstream_NoAvailable(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	upPID, err := sys.Spawn(ctx, "mru-up", &dummyStageActor{})
	require.NoError(t, err)
	downPID, err := sys.Spawn(ctx, "mru-down", &dummyStageActor{})
	require.NoError(t, err)

	cfg := defaultStageConfig()
	fa := newFlowActor(func(v any) ([]any, error) { return []any{v}, nil }, cfg)
	flowPID, err := sys.Spawn(ctx, "mru-flow", fa)
	require.NoError(t, err)

	require.NoError(t, actor.Tell(ctx, flowPID, &stageWire{
		subID: "unit", upstream: upPID, downstream: downPID,
	}))
	time.Sleep(10 * time.Millisecond)

	// Push InitialDemand elements to saturate upstreamCredit so available becomes <= 0.
	for i := range cfg.InitialDemand {
		require.NoError(t, actor.Tell(ctx, flowPID, &streamElement{subID: "unit", value: int(i), seqNo: uint64(i + 1)}))
	}
	time.Sleep(20 * time.Millisecond)

	// Actor is still alive; shutdown cleanly.
	require.NoError(t, flowPID.Shutdown(ctx))
}

// TestThrottleActor_StreamCancel tests that a throttleActor propagates *streamCancel
// upstream when the downstream sink cancels (FailFast error).
func TestThrottleActor_StreamCancel(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	handle, err := Via(
		Of(1, 2, 3),
		Throttle[int](1, time.Millisecond),
	).To(errSink(1, FailFast)).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("throttle with downstream cancel did not terminate")
	}
}
