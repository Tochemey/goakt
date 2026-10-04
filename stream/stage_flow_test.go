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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/internal/pause"
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
		pause.For(150 * time.Millisecond) // let the 50ms timer fire with 1 element in window
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

// probeStageActor stands in for the upstream or downstream neighbor of a
// stage under test and records every stream protocol message it receives, so
// a test can assert exactly what the stage sent and in which order.
type probeStageActor struct {
	received chan any
}

// newProbeStageActor creates a probeStageActor with room for 1024 messages.
func newProbeStageActor() *probeStageActor {
	return &probeStageActor{received: make(chan any, 1024)}
}

// PreStart does nothing: the probe has no state to prepare.
func (a *probeStageActor) PreStart(_ *actor.Context) error { return nil }

// PostStop does nothing: the probe holds no resource.
func (a *probeStageActor) PostStop(_ *actor.Context) error { return nil }

// Receive records every stream protocol message on the received channel and
// reports anything else as unhandled.
func (a *probeStageActor) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *streamElement, *streamComplete, *streamError, *streamRequest, *streamCancel:
		a.received <- msg
	default:
		rctx.Unhandled()
	}
}

// expectProbeMessage returns the next message recorded by probe, failing the
// test when it is not of type M or does not arrive in time.
func expectProbeMessage[M any](t *testing.T, probe *probeStageActor) M {
	t.Helper()
	select {
	case msg := <-probe.received:
		typed, ok := msg.(M)
		require.Truef(t, ok, "expected %T, got %T", *new(M), msg)
		return typed
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %T", *new(M))
		return *new(M)
	}
}

// spawnStageUnderTest spawns stage wired between two probes and returns its
// PID together with the upstream and downstream probes.
func spawnStageUnderTest(t *testing.T, sys actor.ActorSystem, name string, stage actor.Actor) (*actor.PID, *probeStageActor, *probeStageActor) {
	t.Helper()
	ctx := context.Background()

	up, down := newProbeStageActor(), newProbeStageActor()
	upPID, err := sys.Spawn(ctx, name+"-up", up)
	require.NoError(t, err)
	downPID, err := sys.Spawn(ctx, name+"-down", down)
	require.NoError(t, err)

	stagePID, err := sys.Spawn(ctx, name, stage)
	require.NoError(t, err)
	require.NoError(t, actor.Tell(ctx, stagePID, &stageWire{subID: "unit", upstream: upPID, downstream: downPID}))
	return stagePID, up, down
}

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
	pause.For(10 * time.Millisecond)

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
	pause.For(10 * time.Millisecond)

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
	desc := &stage{
		id:   newStageID(),
		kind: sinkKind,
		actorFn: func(cfg StageConfig) actor.Actor {
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
	flowDesc := &stage{
		id:   newStageID(),
		kind: flowKind,
		actorFn: func(cfg StageConfig) actor.Actor {
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
	stages := make([]*stage, 0, len(src.stages)+2)
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
	pause.For(10 * time.Millisecond)

	// Push InitialDemand elements to saturate upstreamCredit so available becomes <= 0.
	for i := range cfg.InitialDemand {
		require.NoError(t, actor.Tell(ctx, flowPID, &streamElement{subID: "unit", value: int(i), seqNo: uint64(i + 1)}))
	}
	pause.For(20 * time.Millisecond)

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

// TestBatchFlowActor_NoDemand_KeepsBatchSize verifies that windows filled
// while downstream has no demand are held as batches of at most maxSize and
// are emitted as soon as demand arrives, without waiting for another element.
func TestBatchFlowActor_NoDemand_KeepsBatchSize(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	batchPID, _, down := spawnStageUnderTest(t, sys, "batch-size-nd", newBatchFlowActor[int](2, 10*time.Second, defaultStageConfig()))

	for i := 1; i <= 5; i++ {
		require.NoError(t, actor.Tell(ctx, batchPID, &streamElement{subID: "unit", value: i, seqNo: uint64(i)}))
	}

	require.NoError(t, actor.Tell(ctx, batchPID, &streamRequest{subID: "unit", n: 10}))
	assert.Equal(t, []int{1, 2}, expectProbeMessage[*streamElement](t, down).value)
	assert.Equal(t, []int{3, 4}, expectProbeMessage[*streamElement](t, down).value)

	// The partial window is flushed on completion, before the completion signal.
	require.NoError(t, actor.Tell(ctx, batchPID, &streamComplete{subID: "unit"}))
	assert.Equal(t, []int{5}, expectProbeMessage[*streamElement](t, down).value)
	expectProbeMessage[*streamComplete](t, down)
}

// TestBatchFlowActor_Complete_WithoutDemand_KeepsPartialWindow verifies that
// a partial window is not discarded when upstream completes while downstream
// has no demand: the stage delivers it once demand arrives, then completes.
func TestBatchFlowActor_Complete_WithoutDemand_KeepsPartialWindow(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	batchPID, _, down := spawnStageUnderTest(t, sys, "batch-complete-nd", newBatchFlowActor[int](2, 10*time.Second, defaultStageConfig()))

	require.NoError(t, actor.Tell(ctx, batchPID, &streamElement{subID: "unit", value: 1, seqNo: 1}))
	require.NoError(t, actor.Tell(ctx, batchPID, &streamComplete{subID: "unit"}))

	// The first message downstream sees is the batch, delivered against its
	// demand: a completion sent ahead of it would be recorded first.
	require.NoError(t, actor.Tell(ctx, batchPID, &streamRequest{subID: "unit", n: 1}))
	assert.Equal(t, []int{1}, expectProbeMessage[*streamElement](t, down).value)
	expectProbeMessage[*streamComplete](t, down)
}

// TestBatchFlowActor_TimerFlush_WithoutDemand verifies that a window whose
// maxWait elapsed while downstream had no demand is delivered when demand
// arrives.
func TestBatchFlowActor_TimerFlush_WithoutDemand(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	batchPID, _, down := spawnStageUnderTest(t, sys, "batch-timer-nd", newBatchFlowActor[int](10, 10*time.Second, defaultStageConfig()))

	// batchFlush is injected directly so the test does not depend on timing:
	// the real timer is 10s away.
	require.NoError(t, actor.Tell(ctx, batchPID, &streamElement{subID: "unit", value: 1, seqNo: 1}))
	require.NoError(t, actor.Tell(ctx, batchPID, &batchFlush{}))
	require.NoError(t, actor.Tell(ctx, batchPID, &streamRequest{subID: "unit", n: 1}))
	assert.Equal(t, []int{1}, expectProbeMessage[*streamElement](t, down).value)
}

// TestBatch_SizeAboveInitialDemand verifies that a batch size larger than the
// stage's demand window is still filled by size rather than waiting for the
// maxWait timer with a short window.
func TestBatch_SizeAboveInitialDemand(t *testing.T) {
	sys := newInternalTestSystem(t)

	input := make([]int, 500)
	for i := range input {
		input[i] = i
	}

	col, sink := Collect[[]int]()
	handle, err := Via(Of(input...), Batch[int](300, 30*time.Second)).To(sink).Run(context.Background(), sys)
	require.NoError(t, err)

	waitDone(t, handle, 5*time.Second)
	batches := col.Items()
	require.Len(t, batches, 2)
	assert.Len(t, batches[0], 300)
	assert.Len(t, batches[1], 200)
}

// TestFlowActor_StreamComplete_EmptyBuffer_CompletesOnce verifies that a
// flowActor with nothing buffered signals completion downstream exactly once.
func TestFlowActor_StreamComplete_EmptyBuffer_CompletesOnce(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	fa := newFlowActor(func(v any) ([]any, error) { return []any{v}, nil }, defaultStageConfig())
	flowPID, _, down := spawnStageUnderTest(t, sys, "flow-complete-once", fa)

	require.NoError(t, actor.Tell(ctx, flowPID, &streamComplete{subID: "unit"}))
	expectProbeMessage[*streamComplete](t, down)

	// Once the flow has stopped it sends nothing more, so a barrier sent to
	// the probe now is queued behind everything the flow sent: it must be the
	// next message the probe records.
	require.Eventually(t, func() bool {
		_, err := sys.ActorOf(ctx, flowPID.Name())
		return err != nil
	}, 5*time.Second, 5*time.Millisecond)

	downPID, err := sys.ActorOf(ctx, "flow-complete-once-down")
	require.NoError(t, err)
	require.NoError(t, actor.Tell(ctx, downPID, &streamRequest{subID: "barrier"}))
	assert.Equal(t, "barrier", expectProbeMessage[*streamRequest](t, down).subID)
}

// TestFusedFlowActor_RespectsDownstreamDemand verifies that a fused stage
// requests from upstream only what downstream has asked for, and asks for a
// replacement when it filters an element out.
func TestFusedFlowActor_RespectsDownstreamDemand(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	keepEven := func(v any) (any, bool, error) { return v, v.(int)%2 == 0, nil }
	fusedPID, up, down := spawnStageUnderTest(t, sys, "fused-demand", newFusedFlowActor(keepEven, defaultStageConfig()))

	// The first request upstream sees is the downstream demand passed through:
	// a request sent at wire time, before any demand, would be recorded first.
	require.NoError(t, actor.Tell(ctx, fusedPID, &streamRequest{subID: "unit", n: 2}))
	assert.EqualValues(t, 2, expectProbeMessage[*streamRequest](t, up).n)

	// A filtered-out element does not consume downstream demand: one more is requested.
	require.NoError(t, actor.Tell(ctx, fusedPID, &streamElement{subID: "unit", value: 1, seqNo: 1}))
	assert.EqualValues(t, 1, expectProbeMessage[*streamRequest](t, up).n)

	require.NoError(t, actor.Tell(ctx, fusedPID, &streamElement{subID: "unit", value: 2, seqNo: 2}))
	require.NoError(t, actor.Tell(ctx, fusedPID, &streamElement{subID: "unit", value: 4, seqNo: 3}))
	assert.Equal(t, 2, expectProbeMessage[*streamElement](t, down).value)
	assert.Equal(t, 4, expectProbeMessage[*streamElement](t, down).value)

	// Demand is exhausted: the next request upstream sees is the next downstream
	// demand, exactly. A request sent in between would be recorded first.
	require.NoError(t, actor.Tell(ctx, fusedPID, &streamRequest{subID: "unit", n: 5}))
	assert.EqualValues(t, 5, expectProbeMessage[*streamRequest](t, up).n)
}

// TestFusedFlowActor_FnError_CancelsUpstream verifies that a fused stage whose
// function fails cancels its upstream and reports the error downstream.
func TestFusedFlowActor_FnError_CancelsUpstream(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	sentinel := errors.New("fused failure")
	failing := func(any) (any, bool, error) { return nil, false, sentinel }
	fusedPID, up, down := spawnStageUnderTest(t, sys, "fused-error", newFusedFlowActor(failing, defaultStageConfig()))

	require.NoError(t, actor.Tell(ctx, fusedPID, &streamRequest{subID: "unit", n: 1}))
	expectProbeMessage[*streamRequest](t, up)

	require.NoError(t, actor.Tell(ctx, fusedPID, &streamElement{subID: "unit", value: 1, seqNo: 1}))
	expectProbeMessage[*streamCancel](t, up)
	require.ErrorIs(t, expectProbeMessage[*streamError](t, down).err, sentinel)
}

// TestFusedFlow_FnError_StopsSource verifies end to end that a failure inside
// a fused chain stops the source stage instead of leaving it running.
func TestFusedFlow_FnError_StopsSource(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	sentinel := errors.New("fused failure")
	src := Unfold(0, func(s int) (int, int, bool) { return s + 1, s, true })
	fused := Via(Via(src, Map(func(n int) int { return n })), TryMap(func(n int) (int, error) {
		if n == 300 {
			return 0, sentinel
		}
		return n, nil
	}))
	handle, err := fused.To(Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)

	waitDone(t, handle, 5*time.Second)
	require.ErrorIs(t, handle.Err(), sentinel)

	sourceName := fmt.Sprintf("stream-%s-0", handle.ID())
	require.Eventually(t, func() bool {
		_, err := sys.ActorOf(ctx, sourceName)
		return err != nil
	}, 5*time.Second, 10*time.Millisecond, "source stage outlived the failed stream")
}
