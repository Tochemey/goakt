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
	"sync/atomic"

	"github.com/tochemey/goakt/v4/actor"
)

// sharedBalance is the coordination point shared by all N balanceSlotActors
// that result from a single Balance call. It collects slot actors as each
// branch is materialized and, every time one materialization of every branch
// is available, spawns a fresh upstream sub-pipeline with a new
// balanceHubActor as its terminal sink for that generation.
type sharedBalance[T any] struct {
	n           int
	srcStages   []*stage
	generations *fanOutGenerations
}

// newSharedBalance creates the coordination struct for n branches of srcStages.
func newSharedBalance[T any](n int, srcStages []*stage) *sharedBalance[T] {
	return &sharedBalance[T]{
		n:           n,
		srcStages:   srcStages,
		generations: newFanOutGenerations(n),
	}
}

// registerSlot records one materialization of a slot actor. When it completes
// a generation (one materialization of every branch), it builds that
// generation's hub over the paired slot actors and spawns the upstream
// sub-pipeline in a goroutine.
func (s *sharedBalance[T]) registerSlot(ctx context.Context, slot int, pid *actor.PID, subID string, sys actor.ActorSystem) {
	generation := s.generations.register(slot, pid, subID)
	if generation == nil {
		return
	}

	hub := &balanceHubActor[T]{
		n:          s.n,
		slots:      make([]*actor.PID, s.n),
		slotSubIDs: make([]string, s.n),
		demand:     make([]int64, s.n),
	}

	for i, branch := range generation {
		hub.slots[i] = branch.pid
		hub.slotSubIDs[i] = branch.subID
	}

	hubSinkDesc := &stage{
		id:   newStageID(),
		kind: sinkKind,
		actorFn: func(cfg StageConfig) actor.Actor {
			hub.config = cfg
			return hub
		},
		config:        defaultStageConfig(),
		manyProducers: true,
	}
	all := make([]*stage, len(s.srcStages)+1)
	copy(all, s.srcStages)
	all[len(s.srcStages)] = hubSinkDesc
	go spawnFanOutUpstream(ctx, sys, all, generation)
}

// balanceSlotActor is the source actor for one branch of a Balance fan-out.
// It relays streamRequest messages as slotDemand to the hub, and forwards
// streamElement, streamComplete, and streamError messages from the hub
// directly downstream. Demand arriving before hubReady is buffered.
type balanceSlotActor[T any] struct {
	shared        *sharedBalance[T]
	slot          int
	downstream    *actor.PID
	subID         string
	hub           *actor.PID
	pendingDemand int64
	config        StageConfig
	// self is the slot's own PID, stored when it is wired so that PostStop,
	// which may run on another goroutine, can withdraw it from the fan-out.
	self atomic.Pointer[actor.PID]
}

func (a *balanceSlotActor[T]) PreStart(_ *actor.Context) error { return nil }

// Receive handles stageWire, streamRequest, hubReady, streamElement,
// streamComplete, streamError, and streamCancel.
func (a *balanceSlotActor[T]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.downstream = msg.downstream
		a.subID = msg.subID
		a.self.Store(rctx.Self())
		a.shared.registerSlot(rctx.Context(), a.slot, rctx.Self(), msg.subID, rctx.ActorSystem())

	case *streamRequest:
		if a.hub != nil {
			rctx.Tell(a.hub, &slotDemand{slot: a.slot, n: msg.n})
		} else {
			a.pendingDemand += msg.n
		}

	case *hubReady:
		a.hub = msg.hub
		if a.pendingDemand > 0 {
			rctx.Tell(a.hub, &slotDemand{slot: a.slot, n: a.pendingDemand})
			a.pendingDemand = 0
		}

	case *streamElement:
		rctx.Tell(a.downstream, msg)

	case *streamComplete:
		rctx.Tell(a.downstream, msg)
		rctx.Shutdown()

	case *streamError:
		rctx.Tell(a.downstream, msg)
		rctx.Shutdown()

	case *streamCancel:
		if a.hub != nil {
			rctx.Tell(a.hub, &slotCancel{slot: a.slot})
		}
		// Notify downstream so the sink's completionWrapper can fire.
		if a.downstream != nil {
			rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		}
		rctx.Shutdown()

	default:
		rctx.Unhandled()
	}
}

// PostStop withdraws the slot from the fan-out if it stops while still
// waiting for its sibling branches to be materialized.
func (a *balanceSlotActor[T]) PostStop(_ *actor.Context) error {
	a.shared.generations.withdraw(a.slot, a.self.Load())
	return nil
}

// balanceHubActor is the terminal sink of the upstream sub-pipeline spawned by
// a Balance call. Unlike broadcastHubActor (which fans out to ALL slots),
// balanceHubActor routes each element to exactly ONE slot — the next slot with
// outstanding demand in round-robin order. This distributes work across branches
// while preserving backpressure: upstream is pulled only when at least one
// downstream slot has signaled capacity.
type balanceHubActor[T any] struct {
	n          int
	slots      []*actor.PID
	slotSubIDs []string
	demand     []int64 // per-slot outstanding demand
	upstream   *actor.PID
	subID      string
	pending    int64 // elements requested from upstream but not yet received
	seqNo      uint64
	cancelled  int
	nextSlot   int // round-robin cursor for selecting next recipient
	config     StageConfig
}

func (a *balanceHubActor[T]) PreStart(_ *actor.Context) error { return nil }

// Receive handles stageWire, slotDemand, streamElement, streamComplete,
// streamError, slotCancel, and actor.Terminated for the slot actors.
func (a *balanceHubActor[T]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.upstream = msg.upstream
		a.subID = msg.subID
		hub := rctx.Self()
		for _, slotPID := range a.slots {
			rctx.Watch(slotPID)
			rctx.Tell(slotPID, &hubReady{hub: hub})
		}

		// A slot that stopped before it could be watched is released now.
		for _, slot := range stoppedSlots(a.slots) {
			a.releaseSlot(rctx, slot)
		}

	case *slotDemand:
		a.demand[msg.slot] += msg.n
		a.maybePull(rctx)

	case *streamElement:
		a.pending--
		// Route to the next slot with available demand (round-robin).
		chosen := -1
		for i := 0; i < a.n; i++ {
			idx := (a.nextSlot + i) % a.n
			if a.slots[idx] != nil && a.demand[idx] > 0 {
				chosen = idx
				break
			}
		}

		if chosen >= 0 {
			a.seqNo++
			rctx.Tell(a.slots[chosen], &streamElement{
				subID: a.slotSubIDs[chosen],
				value: msg.value,
				seqNo: a.seqNo,
			})
			a.demand[chosen]--
			a.nextSlot = (chosen + 1) % a.n
		}
		// Pull more if demand remains.
		a.maybePull(rctx)

	case *streamComplete:
		for i, slot := range a.slots {
			if slot != nil {
				rctx.Tell(slot, &streamComplete{subID: a.slotSubIDs[i]})
			}
		}
		rctx.Shutdown()

	case *streamError:
		for i, slot := range a.slots {
			if slot != nil {
				rctx.Tell(slot, &streamError{subID: a.slotSubIDs[i], err: msg.err})
			}
		}
		rctx.Shutdown()

	case *slotCancel:
		a.releaseSlot(rctx, msg.slot)

	case *actor.Terminated:
		// A slot actor that stops without cancelling (its branch was aborted)
		// is released like a cancelled one, so it cannot stall its siblings.
		if slot := terminatedSlot(a.slots, msg); slot >= 0 {
			a.releaseSlot(rctx, slot)
		}

	default:
		rctx.Unhandled()
	}
}

func (a *balanceHubActor[T]) PostStop(_ *actor.Context) error { return nil }

// releaseSlot removes slot from the active slots, because its branch cancelled
// or its actor stopped. When no slot is left it cancels the upstream and shuts
// the hub down; otherwise the remaining slots may now unblock the pull. It
// does nothing for a slot that was already released.
func (a *balanceHubActor[T]) releaseSlot(rctx *actor.ReceiveContext, slot int) {
	if a.slots[slot] == nil {
		return
	}

	a.slots[slot] = nil
	a.cancelled++
	if a.cancelled >= a.n {
		if a.upstream != nil {
			rctx.Tell(a.upstream, &streamCancel{subID: a.subID})
		}

		rctx.Shutdown()
		return
	}

	a.maybePull(rctx)
}

// maybePull requests elements from upstream when at least one active slot has
// outstanding demand and no elements are currently in flight.
func (a *balanceHubActor[T]) maybePull(rctx *actor.ReceiveContext) {
	if a.upstream == nil || a.pending > 0 {
		return
	}
	total := a.totalDemand()
	if total <= 0 {
		return
	}
	rctx.Tell(a.upstream, &streamRequest{subID: a.subID, n: total})
	a.pending = total
}

// totalDemand sums outstanding demand across all active slots.
func (a *balanceHubActor[T]) totalDemand() int64 {
	var total int64
	for i, pid := range a.slots {
		if pid != nil {
			total += a.demand[i]
		}
	}
	return total
}
