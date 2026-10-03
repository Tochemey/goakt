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

// Package stream internal tests for the broadcast stage actor edge cases.
package stream

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/actor"
)

var errBad = errors.New("bad element")

// TestBroadcast_SlotCancel_HubReady_NoPendingDemand verifies the hubReady
// path when the slot has not yet accumulated any pending demand (pendingDemand == 0).
// With n=1, the hub is spawned immediately after the single slot registers and
// sends hubReady before the branch sink's initial streamRequest can arrive.
func TestBroadcast_SlotCancel_HubReady_NoPendingDemand(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	srcs := Broadcast(Of(1, 2, 3), 1)

	col, sink := Collect[int]()
	h, err := srcs[0].To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("single-slot broadcast did not complete")
	}

	require.NotEmpty(t, col.Items())
}

// TestBroadcast_MinDemandUnequal exercises the minDemand comparison branch
// where a second slot has lower demand than the first, exercising a.demand[i] < min.
func TestBroadcast_MinDemandUnequal(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	ch := make(chan int)
	srcs := Broadcast(FromChannel(ch), 2)

	col0, sink0 := Collect[int]()
	col1, sink1 := Collect[int]()

	h0, err := srcs[0].To(sink0).Run(ctx, sys)
	require.NoError(t, err)
	h1, err := srcs[1].To(sink1).Run(ctx, sys)
	require.NoError(t, err)

	go func() {
		for i := range 10 {
			ch <- i
		}
		close(ch)
	}()

	select {
	case <-h0.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("branch 0 did not complete")
	}
	select {
	case <-h1.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("branch 1 did not complete")
	}

	require.Len(t, col0.Items(), 10)
	require.Len(t, col1.Items(), 10)
}

// TestBroadcastHubActor_SlotCancel_Unit directly drives the broadcastHubActor
// through its slotCancel paths using actor-level message injection. This avoids
// the integration race where branch handles complete before the hub processes
// slotCancel messages from its actor goroutine.
//
// Covered paths:
//   - slotCancel with remaining active slots (cancelled < n) → maybePull
//   - streamElement with a nil slot → continue (skip delivery)
//   - slotCancel with all slots cancelled (cancelled == n) → cancel upstream + shutdown
func TestBroadcastHubActor_SlotCancel_Unit(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	slot0PID, err := sys.Spawn(ctx, "hub-unit-slot0", &dummyStageActor{})
	require.NoError(t, err)
	slot1PID, err := sys.Spawn(ctx, "hub-unit-slot1", &dummyStageActor{})
	require.NoError(t, err)
	upPID, err := sys.Spawn(ctx, "hub-unit-up", &dummyStageActor{})
	require.NoError(t, err)

	// Construct hub directly with pre-populated slot info (mimicking registerSlot).
	hub := &broadcastHubActor[int]{
		n:          2,
		slots:      []*actor.PID{slot0PID, slot1PID},
		slotSubIDs: []string{"sub-a", "sub-b"},
		demand:     make([]int64, 2),
	}
	hubPID, err := sys.Spawn(ctx, "hub-unit-hub", hub)
	require.NoError(t, err)

	// Wire the hub: sets upstream, sends hubReady to all slot PIDs.
	require.NoError(t, actor.Tell(ctx, hubPID, &stageWire{
		subID:      "unit",
		upstream:   upPID,
		downstream: nil,
	}))
	time.Sleep(10 * time.Millisecond)

	// Deliver demand from both slots so hub starts pulling.
	require.NoError(t, actor.Tell(ctx, hubPID, &slotDemand{slot: 0, n: 10}))
	require.NoError(t, actor.Tell(ctx, hubPID, &slotDemand{slot: 1, n: 10}))
	time.Sleep(10 * time.Millisecond)
	// Hub has sent streamRequest to upPID (dummy, ignored); pending > 0.

	// Cancel slot 0: covers slotCancel with remaining slots → maybePull.
	require.NoError(t, actor.Tell(ctx, hubPID, &slotCancel{slot: 0}))
	time.Sleep(5 * time.Millisecond)

	// Inject a streamElement: slot0 is nil → continue (covered); slot1 is non-nil → Tell.
	require.NoError(t, actor.Tell(ctx, hubPID, &streamElement{subID: "unit", value: 42, seqNo: 1}))
	time.Sleep(5 * time.Millisecond)

	// Cancel slot 1: all cancelled → cancel upstream + shutdown.
	require.NoError(t, actor.Tell(ctx, hubPID, &slotCancel{slot: 1}))

	require.Eventually(t, func() bool {
		_, err := sys.ActorOf(ctx, hubPID.Name())
		return err != nil // actor stopped when lookup fails
	}, 2*time.Second, 5*time.Millisecond)
}

// TestBroadcastHubActor_StreamComplete_WithNilSlot verifies that streamComplete
// skips nil (cancelled) slots when fanning out completion to remaining branches.
func TestBroadcastHubActor_StreamComplete_WithNilSlot(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	slot0PID, err := sys.Spawn(ctx, "hub-cmp-slot0", &dummyStageActor{})
	require.NoError(t, err)
	slot1PID, err := sys.Spawn(ctx, "hub-cmp-slot1", &dummyStageActor{})
	require.NoError(t, err)
	upPID, err := sys.Spawn(ctx, "hub-cmp-up", &dummyStageActor{})
	require.NoError(t, err)

	hub := &broadcastHubActor[int]{
		n:          2,
		slots:      []*actor.PID{slot0PID, slot1PID},
		slotSubIDs: []string{"sub-a", "sub-b"},
		demand:     make([]int64, 2),
	}
	hubPID, err := sys.Spawn(ctx, "hub-cmp-hub", hub)
	require.NoError(t, err)

	require.NoError(t, actor.Tell(ctx, hubPID, &stageWire{subID: "unit", upstream: upPID}))
	time.Sleep(10 * time.Millisecond)

	// Cancel slot 0 so it is nil when streamComplete arrives.
	require.NoError(t, actor.Tell(ctx, hubPID, &slotCancel{slot: 0}))
	time.Sleep(5 * time.Millisecond)

	// Send streamComplete: slot0 nil (skip), slot1 non-nil (Tell) → hub shuts down.
	require.NoError(t, actor.Tell(ctx, hubPID, &streamComplete{subID: "unit"}))

	require.Eventually(t, func() bool {
		_, err := sys.ActorOf(ctx, hubPID.Name())
		return err != nil
	}, 2*time.Second, 5*time.Millisecond)
}

// TestBroadcastHubActor_StreamError_WithNilSlot verifies that streamError
// skips nil (cancelled) slots when fanning out the error to remaining branches.
func TestBroadcastHubActor_StreamError_WithNilSlot(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	slot0PID, err := sys.Spawn(ctx, "hub-err-slot0", &dummyStageActor{})
	require.NoError(t, err)
	slot1PID, err := sys.Spawn(ctx, "hub-err-slot1", &dummyStageActor{})
	require.NoError(t, err)
	upPID, err := sys.Spawn(ctx, "hub-err-up", &dummyStageActor{})
	require.NoError(t, err)

	hub := &broadcastHubActor[int]{
		n:          2,
		slots:      []*actor.PID{slot0PID, slot1PID},
		slotSubIDs: []string{"sub-a", "sub-b"},
		demand:     make([]int64, 2),
	}
	hubPID, err := sys.Spawn(ctx, "hub-err-hub", hub)
	require.NoError(t, err)

	require.NoError(t, actor.Tell(ctx, hubPID, &stageWire{subID: "unit", upstream: upPID}))
	time.Sleep(10 * time.Millisecond)

	// Cancel slot 0 so it is nil when streamError arrives.
	require.NoError(t, actor.Tell(ctx, hubPID, &slotCancel{slot: 0}))
	time.Sleep(5 * time.Millisecond)

	// Send streamError: slot0 nil (skip), slot1 non-nil (Tell) → hub shuts down.
	require.NoError(t, actor.Tell(ctx, hubPID, &streamError{subID: "unit", err: errBad}))

	require.Eventually(t, func() bool {
		_, err := sys.ActorOf(ctx, hubPID.Name())
		return err != nil
	}, 2*time.Second, 5*time.Millisecond)
}

// TestBroadcastSlotActor_StreamCancel_Unit verifies the broadcastSlotActor's
// streamCancel handler at the actor level. When a slot receives streamCancel from
// its downstream, it forwards slotCancel to the hub and shuts down.
func TestBroadcastSlotActor_StreamCancel_Unit(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	downPID, err := sys.Spawn(ctx, "sl-cancel-down", &dummyStageActor{})
	require.NoError(t, err)
	hubPID, err := sys.Spawn(ctx, "sl-cancel-hub", &dummyStageActor{})
	require.NoError(t, err)

	// Build a shared with n=2 and materialize only this slot, so the generation
	// stays incomplete and registerSlot does not spawn a sub-pipeline.
	shared := newSharedBroadcast[int](2, []*stage{})

	slotActor := &broadcastSlotActor[int]{shared: shared, slot: 0, config: defaultStageConfig()}
	slotPID, err := sys.Spawn(ctx, "sl-cancel-slot", slotActor)
	require.NoError(t, err)

	// Wire the slot.
	require.NoError(t, actor.Tell(ctx, slotPID, &stageWire{
		subID:      "unit",
		upstream:   nil,
		downstream: downPID,
	}))
	time.Sleep(10 * time.Millisecond)

	// Give the slot a hub PID so it can forward slotCancel.
	require.NoError(t, actor.Tell(ctx, slotPID, &hubReady{hub: hubPID}))
	time.Sleep(5 * time.Millisecond)

	// Send streamCancel — slot should forward slotCancel to hub and shut down.
	require.NoError(t, actor.Tell(ctx, slotPID, &streamCancel{subID: "unit"}))

	require.Eventually(t, func() bool {
		_, err := sys.ActorOf(ctx, slotPID.Name())
		return err != nil
	}, 2*time.Second, 5*time.Millisecond)
}

// TestBroadcastHubActor_Unhandled verifies that the hub's default case calls
// Unhandled for unrecognized message types without panicking.
func TestBroadcastHubActor_Unhandled(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	slot0PID, err := sys.Spawn(ctx, "hub-unk-slot0", &dummyStageActor{})
	require.NoError(t, err)
	upPID, err := sys.Spawn(ctx, "hub-unk-up", &dummyStageActor{})
	require.NoError(t, err)

	hub := &broadcastHubActor[int]{
		n:          1,
		slots:      []*actor.PID{slot0PID},
		slotSubIDs: []string{"sub-a"},
		demand:     make([]int64, 1),
	}
	hubPID, err := sys.Spawn(ctx, "hub-unk-hub", hub)
	require.NoError(t, err)

	require.NoError(t, actor.Tell(ctx, hubPID, &stageWire{subID: "unit", upstream: upPID}))
	time.Sleep(10 * time.Millisecond)

	// Send an unknown message — hits the default Unhandled branch.
	require.NoError(t, actor.Tell(ctx, hubPID, &struct{ x int }{x: 99}))
	time.Sleep(10 * time.Millisecond)

	// Hub is still alive (Unhandled does not shut it down).
	_, err = sys.ActorOf(ctx, hubPID.Name())
	require.NoError(t, err)

	// Clean up via streamComplete.
	require.NoError(t, actor.Tell(ctx, hubPID, &streamComplete{subID: "unit"}))
	require.Eventually(t, func() bool {
		_, err := sys.ActorOf(ctx, hubPID.Name())
		return err != nil
	}, 2*time.Second, 5*time.Millisecond)
}

// TestBroadcastSlotActor_Unhandled verifies that the slot actor's default case
// calls Unhandled for unrecognized message types without panicking.
func TestBroadcastSlotActor_Unhandled(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	downPID, err := sys.Spawn(ctx, "sl-unk-down", &dummyStageActor{})
	require.NoError(t, err)

	// n=2 with a single materialized slot: no generation, no sub-pipeline.
	shared := newSharedBroadcast[int](2, []*stage{})

	slotActor := &broadcastSlotActor[int]{shared: shared, slot: 0, config: defaultStageConfig()}
	slotPID, err := sys.Spawn(ctx, "sl-unk-slot", slotActor)
	require.NoError(t, err)

	require.NoError(t, actor.Tell(ctx, slotPID, &stageWire{
		subID:      "unit",
		upstream:   nil,
		downstream: downPID,
	}))
	time.Sleep(10 * time.Millisecond)

	// Send an unknown message — hits the default Unhandled branch.
	require.NoError(t, actor.Tell(ctx, slotPID, &struct{ x int }{x: 99}))
	time.Sleep(10 * time.Millisecond)

	// Slot is still alive (Unhandled does not shut it down).
	_, err = sys.ActorOf(ctx, slotPID.Name())
	require.NoError(t, err)

	_ = slotPID.Shutdown(ctx)
}

// TestBroadcast_UpstreamError_HubForwards verifies the hub's streamError handler:
// when the upstream sub-pipeline errors (FailFast TryMap), the hub receives
// streamError and forwards it to every slot, which in turn terminates all branches.
func TestBroadcast_UpstreamError_HubForwards(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	errFlow := TryMap(func(n int) (int, error) {
		if n == 1 {
			return 0, errBad
		}
		return n, nil
	})

	srcs := Broadcast(Via(Of(1, 2, 3), errFlow), 2)

	h0, err := srcs[0].To(Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)
	h1, err := srcs[1].To(Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h0.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("branch 0 did not terminate on upstream error")
	}
	select {
	case <-h1.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("branch 1 did not terminate on upstream error")
	}
}

// countingSink returns a sink that adds one to counter for every element.
func countingSink[T any](counter *atomic.Int64) Sink[T] {
	return ForEach(func(T) { counter.Add(1) })
}

// fanOutUnderTest is a fan-out graph built once and run several times: one
// RunnableGraph per branch, and the element counters its sinks feed.
type fanOutUnderTest struct {
	// graphs holds the graphs to run for one generation of the fan-out.
	graphs []RunnableGraph
	// counters holds one element counter per branch sink.
	counters []*atomic.Int64
	// perRun is the total number of elements all branches receive in one run.
	perRun int64
}

// run materializes every graph once and returns the handles.
func (x *fanOutUnderTest) run(t *testing.T, sys actor.ActorSystem) []StreamHandle {
	t.Helper()
	handles := make([]StreamHandle, 0, len(x.graphs))
	for _, graph := range x.graphs {
		handle, err := graph.Run(context.Background(), sys)
		require.NoError(t, err)
		handles = append(handles, handle)
	}

	return handles
}

// total returns the number of elements received by all branch sinks so far.
func (x *fanOutUnderTest) total() int64 {
	var sum int64
	for _, counter := range x.counters {
		sum += counter.Load()
	}

	return sum
}

// fanOutCases builds, for each fan-out operator, a two-branch graph over a
// four-element source.
func fanOutCases(t *testing.T) map[string]func() *fanOutUnderTest {
	t.Helper()
	branchGraphs := func(branches []Source[int], perRun int64) *fanOutUnderTest {
		out := &fanOutUnderTest{perRun: perRun}
		for _, branch := range branches {
			counter := new(atomic.Int64)
			out.counters = append(out.counters, counter)
			out.graphs = append(out.graphs, branch.To(countingSink[int](counter)))
		}

		return out
	}

	return map[string]func() *fanOutUnderTest{
		"Broadcast": func() *fanOutUnderTest { return branchGraphs(Broadcast(Of(1, 2, 3, 4), 2), 8) },
		"Balance":   func() *fanOutUnderTest { return branchGraphs(Balance(Of(1, 2, 3, 4), 2), 4) },
		"Partition": func() *fanOutUnderTest {
			return branchGraphs(Partition(Of(1, 2, 3, 4), 2, func(n int) int { return n % 2 }), 4)
		},
		"Graph": func() *fanOutUnderTest {
			out := &fanOutUnderTest{perRun: 8, counters: []*atomic.Int64{new(atomic.Int64), new(atomic.Int64)}}
			identity := Map(func(v any) any { return v })
			graph, err := NewGraph().
				AddSource("src", Of[any](1, 2, 3, 4)).
				AddFlow("left", identity, "src").
				AddFlow("right", identity, "src").
				AddSink("left-sink", countingSink[any](out.counters[0]), "left").
				AddSink("right-sink", countingSink[any](out.counters[1]), "right").
				Build()
			require.NoError(t, err)
			out.graphs = []RunnableGraph{graph}
			return out
		},
	}
}

// TestFanOut_RunsMoreThanOnce verifies that the graphs built from one fan-out
// can be run repeatedly: every run gets its own hub and upstream, delivers the
// full data and completes.
func TestFanOut_RunsMoreThanOnce(t *testing.T) {
	for name, build := range fanOutCases(t) {
		t.Run(name, func(t *testing.T) {
			sys := newInternalTestSystem(t)
			fanOut := build()

			for run := int64(1); run <= 3; run++ {
				for _, handle := range fanOut.run(t, sys) {
					waitDone(t, handle, 5*time.Second)
					require.NoError(t, handle.Err())
				}

				require.Equal(t, run*fanOut.perRun, fanOut.total())
			}

			requireNoStreamActors(t, sys)
		})
	}
}

// TestFanOut_SecondRunWhileFirstIsRunning verifies that a second run of the
// same branches is independent of a first run that has not finished.
func TestFanOut_SecondRunWhileFirstIsRunning(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	branches := Broadcast(Of(1, 2, 3, 4), 2)

	// First run: its sinks block on gate, so the run stays in flight.
	gate := make(chan struct{})
	var first atomic.Int64
	blocking := func() Sink[int] {
		return ForEach(func(int) {
			<-gate
			first.Add(1)
		})
	}
	firstHandles := make([]StreamHandle, 0, 2)
	for _, branch := range branches {
		handle, err := branch.To(blocking()).Run(ctx, sys)
		require.NoError(t, err)
		firstHandles = append(firstHandles, handle)
	}

	// Second run of the same branches completes with the full data meanwhile.
	var second atomic.Int64
	secondHandles := make([]StreamHandle, 0, 2)
	for _, branch := range branches {
		handle, err := branch.To(countingSink[int](&second)).Run(ctx, sys)
		require.NoError(t, err)
		secondHandles = append(secondHandles, handle)
	}

	for _, handle := range secondHandles {
		waitDone(t, handle, 5*time.Second)
		require.NoError(t, handle.Err())
	}

	require.EqualValues(t, 8, second.Load())
	require.Zero(t, first.Load())

	close(gate)
	for _, handle := range firstHandles {
		waitDone(t, handle, 5*time.Second)
		require.NoError(t, handle.Err())
	}

	require.EqualValues(t, 8, first.Load())
}

// TestFanOut_PartialGeneration verifies what happens when only some branches
// of a fan-out are run, in the first generation and in a later one alike: the
// branch waits for its siblings, completes once they are run, and can be
// stopped while it waits without disturbing the generations that follow.
func TestFanOut_PartialGeneration(t *testing.T) {
	testCases := []struct {
		name          string
		completedRuns int
	}{
		{name: "first generation", completedRuns: 0},
		{name: "later generation", completedRuns: 2},
	}

	for _, tc := range testCases {
		t.Run(tc.name+": completes when the missing branch is run", func(t *testing.T) {
			sys := newInternalTestSystem(t)
			ctx := context.Background()

			fanOut := fanOutCases(t)["Broadcast"]()
			for range tc.completedRuns {
				for _, handle := range fanOut.run(t, sys) {
					waitDone(t, handle, 5*time.Second)
				}
			}

			before := fanOut.total()
			lone, err := fanOut.graphs[0].Run(ctx, sys)
			require.NoError(t, err)

			// The lone branch is wired and waiting: its slot is registered and
			// the upstream has not started, so nothing was delivered.
			require.Eventually(t, func() bool {
				return slices.Contains(streamActorNames(t, sys), fmt.Sprintf("stream-%s-0", lone.ID()))
			}, 5*time.Second, 10*time.Millisecond)
			require.Equal(t, before, fanOut.total())

			sibling, err := fanOut.graphs[1].Run(ctx, sys)
			require.NoError(t, err)

			waitDone(t, lone, 5*time.Second)
			waitDone(t, sibling, 5*time.Second)
			require.Equal(t, before+fanOut.perRun, fanOut.total())
		})

		t.Run(tc.name+": can be stopped while waiting", func(t *testing.T) {
			sys := newInternalTestSystem(t)
			ctx := context.Background()

			fanOut := fanOutCases(t)["Broadcast"]()
			for range tc.completedRuns {
				for _, handle := range fanOut.run(t, sys) {
					waitDone(t, handle, 5*time.Second)
				}
			}

			before := fanOut.total()
			lone, err := fanOut.graphs[0].Run(ctx, sys)
			require.NoError(t, err)

			stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			t.Cleanup(cancel)
			require.NoError(t, lone.Stop(stopCtx))
			requireNoStreamActors(t, sys)
			require.Equal(t, before, fanOut.total())

			// The stopped branch left the generation: the next full run pairs
			// fresh branches and delivers the full data.
			for _, handle := range fanOut.run(t, sys) {
				waitDone(t, handle, 5*time.Second)
				require.NoError(t, handle.Err())
			}

			require.Equal(t, before+fanOut.perRun, fanOut.total())
		})

		t.Run(tc.name+": a branch whose partner withdrew waits for a new one", func(t *testing.T) {
			sys := newInternalTestSystem(t)
			ctx := context.Background()

			fanOut := fanOutCases(t)["Broadcast"]()
			for range tc.completedRuns {
				for _, handle := range fanOut.run(t, sys) {
					waitDone(t, handle, 5*time.Second)
				}
			}

			before := fanOut.total()
			withdrawn, err := fanOut.graphs[0].Run(ctx, sys)
			require.NoError(t, err)
			withdrawn.Abort()
			requireNoStreamActors(t, sys)

			// The sibling arrives after its partner has gone: it is not paired
			// with the dead slot, it waits.
			waiting, err := fanOut.graphs[1].Run(ctx, sys)
			require.NoError(t, err)
			require.Eventually(t, func() bool {
				return slices.Contains(streamActorNames(t, sys), fmt.Sprintf("stream-%s-0", waiting.ID()))
			}, 5*time.Second, 10*time.Millisecond)
			require.Equal(t, before, fanOut.total())

			// A new partner completes the generation.
			partner, err := fanOut.graphs[0].Run(ctx, sys)
			require.NoError(t, err)
			waitDone(t, waiting, 5*time.Second)
			waitDone(t, partner, 5*time.Second)
			require.Equal(t, before+fanOut.perRun, fanOut.total())
		})
	}
}

// TestFanOut_ConcurrentRuns verifies that branches run from several goroutines
// at once are paired into complete generations.
func TestFanOut_ConcurrentRuns(t *testing.T) {
	for _, name := range []string{"Broadcast", "Balance", "Partition"} {
		t.Run(name, func(t *testing.T) {
			sys := newInternalTestSystem(t)
			ctx := context.Background()
			fanOut := fanOutCases(t)[name]()

			const runs = 8
			handles := make(chan StreamHandle, runs*len(fanOut.graphs))
			var wg sync.WaitGroup
			for _, graph := range fanOut.graphs {
				for range runs {
					wg.Go(func() {
						handle, err := graph.Run(ctx, sys)
						if assert.NoError(t, err) {
							handles <- handle
						}
					})
				}
			}

			wg.Wait()
			close(handles)

			for handle := range handles {
				waitDone(t, handle, 10*time.Second)
				require.NoError(t, handle.Err())
			}

			require.Equal(t, runs*fanOut.perRun, fanOut.total())
		})
	}
}

// TestFanOut_AbortedBranchDoesNotStallSiblings verifies that aborting one
// branch releases its slot at the hub, so the remaining branches keep
// receiving elements.
func TestFanOut_AbortedBranchDoesNotStallSiblings(t *testing.T) {
	infinite := func() Source[int] {
		return Unfold(0, func(s int) (int, int, bool) { return s + 1, s, true })
	}

	testCases := map[string]func() []Source[int]{
		"Broadcast": func() []Source[int] { return Broadcast(infinite(), 2) },
		"Balance":   func() []Source[int] { return Balance(infinite(), 2) },
		"Partition": func() []Source[int] {
			return Partition(infinite(), 2, func(n int) int { return n % 2 })
		},
	}

	for name, build := range testCases {
		t.Run(name, func(t *testing.T) {
			sys := newInternalTestSystem(t)
			ctx := context.Background()
			branches := build()

			aborted, err := branches[0].To(Ignore[int]()).Run(ctx, sys)
			require.NoError(t, err)

			var received atomic.Int64
			survivor, err := branches[1].To(countingSink[int](&received)).Run(ctx, sys)
			require.NoError(t, err)

			require.Eventually(t, func() bool { return received.Load() > 0 }, 5*time.Second, 5*time.Millisecond)
			aborted.Abort()

			// More than two full demand windows after the abort: the hub is no
			// longer waiting for the aborted branch.
			mark := received.Load()
			require.Eventually(t, func() bool {
				return received.Load() > mark+3*defaultInitialDemand
			}, 5*time.Second, 5*time.Millisecond, "surviving branch stalled after its sibling was aborted")

			survivor.Abort()
		})
	}
}

// TestFanOut_BranchAbortedRightAfterRun verifies that a branch aborted before
// the hub of its generation is wired neither stalls its sibling nor, when
// every branch is aborted, leaves the hub pipeline behind.
func TestFanOut_BranchAbortedRightAfterRun(t *testing.T) {
	infinite := func() Source[int] {
		return Unfold(0, func(s int) (int, int, bool) { return s + 1, s, true })
	}

	testCases := map[string]func() []Source[int]{
		"Broadcast": func() []Source[int] { return Broadcast(infinite(), 2) },
		"Balance":   func() []Source[int] { return Balance(infinite(), 2) },
		"Partition": func() []Source[int] {
			return Partition(infinite(), 2, func(n int) int { return n % 2 })
		},
	}

	for name, build := range testCases {
		t.Run(name+": one branch aborted", func(t *testing.T) {
			sys := newInternalTestSystem(t)
			ctx := context.Background()

			for range 20 {
				branches := build()
				aborted, err := branches[0].To(Ignore[int]()).Run(ctx, sys)
				require.NoError(t, err)

				var received atomic.Int64
				survivor, err := branches[1].To(countingSink[int](&received)).Run(ctx, sys)
				require.NoError(t, err)
				aborted.Abort()

				// The abort lands either after the two branches were paired, and
				// the hub releases the dead slot, or before the aborted branch
				// registered, and the survivor waits for a partner. A fresh run
				// of branch 0 supplies that partner; in the first case it waits
				// for the next generation and is aborted below.
				replacement, err := branches[0].To(Ignore[int]()).Run(ctx, sys)
				require.NoError(t, err)

				require.Eventually(t, func() bool {
					return received.Load() > 3*defaultInitialDemand
				}, 5*time.Second, time.Millisecond, "surviving branch stalled after its sibling was aborted")

				survivor.Abort()
				replacement.Abort()
			}

			requireNoStreamActors(t, sys)
		})

		t.Run(name+": every branch aborted", func(t *testing.T) {
			sys := newInternalTestSystem(t)
			ctx := context.Background()

			for range 20 {
				branches := build()
				handles := make([]StreamHandle, 0, len(branches))
				for _, branch := range branches {
					handle, err := branch.To(Ignore[int]()).Run(ctx, sys)
					require.NoError(t, err)
					handles = append(handles, handle)
				}

				for _, handle := range handles {
					handle.Abort()
				}
			}

			requireNoStreamActors(t, sys)
		})
	}
}

// TestFanOut_UpstreamMaterializationFailure_FailsEveryBranch verifies that
// when the upstream of a generation cannot be materialized, every branch of
// that generation fails with the materialization error instead of waiting.
func TestFanOut_UpstreamMaterializationFailure_FailsEveryBranch(t *testing.T) {
	// A source without a source stage is rejected by the materializer.
	invalid := Source[int]{}

	testCases := map[string]func() []Source[int]{
		"Broadcast": func() []Source[int] { return Broadcast(invalid, 2) },
		"Balance":   func() []Source[int] { return Balance(invalid, 2) },
		"Partition": func() []Source[int] { return Partition(invalid, 2, func(n int) int { return n % 2 }) },
	}

	for name, build := range testCases {
		t.Run(name, func(t *testing.T) {
			sys := newInternalTestSystem(t)

			var handles []StreamHandle
			for _, branch := range build() {
				handle, err := branch.To(Ignore[int]()).Run(context.Background(), sys)
				require.NoError(t, err)
				handles = append(handles, handle)
			}

			for _, handle := range handles {
				waitDone(t, handle, 5*time.Second)
				require.ErrorIs(t, handle.Err(), ErrInvalidGraph)
			}

			requireNoStreamActors(t, sys)
		})
	}
}
