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
	"testing"
	"time"

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
		slotPIDs:   []*actor.PID{slot0PID, slot1PID},
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
		slotPIDs:   []*actor.PID{slot0PID, slot1PID},
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
		slotPIDs:   []*actor.PID{slot0PID, slot1PID},
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

	// Build a shared with n=1 but mark as started so registerSlot does not spawn
	// a sub-pipeline (srcStages is empty, which would fail materialization).
	shared := newSharedBroadcast[int](1, []*stageDesc{})
	shared.mu.Lock()
	shared.started = true // prevent upstream spawn
	shared.mu.Unlock()

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
	require.NoError(t, actor.Tell(ctx, slotPID, &hubReady{hubPID: hubPID}))
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
		slotPIDs:   []*actor.PID{slot0PID},
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

	shared := newSharedBroadcast[int](1, []*stageDesc{})
	shared.mu.Lock()
	shared.started = true
	shared.mu.Unlock()

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
