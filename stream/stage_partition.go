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

// sharedPartition coordinates the N partitionSlotActors that result from a
// single Partition call. It mirrors sharedBalance: each slot registers itself
// at wire-time, and every time one materialization of every slot is
// available, a fresh upstream sub-pipeline is spawned with a new
// partitionHubActor as its terminal sink for that generation.
type sharedPartition[T any] struct {
	n           int
	srcStages   []*stage
	partitionFn func(any) int // routing function handed to every generation's hub
	generations *fanOutGenerations
}

// newSharedPartition creates the coordination struct for n branches of srcStages.
func newSharedPartition[T any](n int, srcStages []*stage, fn func(any) int) *sharedPartition[T] {
	return &sharedPartition[T]{
		n:           n,
		srcStages:   srcStages,
		partitionFn: fn,
		generations: newFanOutGenerations(n),
	}
}

// registerSlot records one materialization of a slot actor. When it completes
// a generation (one materialization of every branch), it builds that
// generation's hub over the paired slot actors and spawns the upstream
// sub-pipeline in a goroutine.
func (s *sharedPartition[T]) registerSlot(ctx context.Context, slot int, pid *actor.PID, subID string, sys actor.ActorSystem) {
	generation := s.generations.register(slot, pid, subID)
	if generation == nil {
		return
	}

	hub := &partitionHubActor[T]{
		n:           s.n,
		slots:       make([]*actor.PID, s.n),
		slotSubIDs:  make([]string, s.n),
		demand:      make([]int64, s.n),
		partitionFn: s.partitionFn,
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

// partitionSlotActor is the source actor for one branch of a Partition fan-out.
// It is structurally identical to balanceSlotActor: it relays demand to the hub
// and forwards element/complete/error/cancel messages through to its branch.
type partitionSlotActor[T any] struct {
	shared        *sharedPartition[T]
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

func (a *partitionSlotActor[T]) PreStart(_ *actor.Context) error { return nil }

// Receive handles stageWire, streamRequest, hubReady, streamElement,
// streamComplete, streamError, and streamCancel.
func (a *partitionSlotActor[T]) Receive(rctx *actor.ReceiveContext) {
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
func (a *partitionSlotActor[T]) PostStop(_ *actor.Context) error {
	a.shared.generations.withdraw(a.slot, a.self.Load())
	return nil
}

// partitionHubActor is the terminal sink of the upstream sub-pipeline spawned by
// a Partition call. Each element is routed to exactly one slot determined by
// partitionFn(elem). Backpressure is conservative: the hub pulls a batch only
// when every active slot has outstanding demand, guaranteeing that the slot
// chosen by partitionFn for any incoming element has capacity.
//
// If partitionFn returns a slot that is out of range or already cancelled, the
// element is dropped silently.
type partitionHubActor[T any] struct {
	n           int
	slots       []*actor.PID
	slotSubIDs  []string
	demand      []int64
	upstream    *actor.PID
	subID       string
	pending     int64
	seqNo       uint64
	cancelled   int
	partitionFn func(any) int
	config      StageConfig
}

func (a *partitionHubActor[T]) PreStart(_ *actor.Context) error { return nil }

// Receive handles stageWire, slotDemand, streamElement, streamComplete,
// streamError, slotCancel, and actor.Terminated for the slot actors.
func (a *partitionHubActor[T]) Receive(rctx *actor.ReceiveContext) {
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
		slot := a.partitionFn(msg.value)
		if slot >= 0 && slot < a.n && a.slots[slot] != nil {
			a.seqNo++
			rctx.Tell(a.slots[slot], &streamElement{
				subID: a.slotSubIDs[slot],
				value: msg.value,
				seqNo: a.seqNo,
			})
			a.demand[slot]--
		}
		// Out-of-range or cancelled slots are silently dropped.
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

func (a *partitionHubActor[T]) PostStop(_ *actor.Context) error { return nil }

// releaseSlot removes slot from the active slots, because its branch cancelled
// or its actor stopped. When no slot is left it cancels the upstream and shuts
// the hub down; otherwise the remaining slots may now unblock the pull. It
// does nothing for a slot that was already released.
func (a *partitionHubActor[T]) releaseSlot(rctx *actor.ReceiveContext, slot int) {
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

// maybePull pulls a batch of size min(demand[i]) when no batch is in flight.
// Conservative: requires every active slot to have demand. This guarantees
// that any element routed via partitionFn finds outstanding demand on its
// target slot.
func (a *partitionHubActor[T]) maybePull(rctx *actor.ReceiveContext) {
	if a.upstream == nil || a.pending > 0 {
		return
	}
	m := a.minDemand()
	if m <= 0 {
		return
	}
	rctx.Tell(a.upstream, &streamRequest{subID: a.subID, n: m})
	a.pending = m
}

// minDemand returns the minimum outstanding demand across all active slots.
// Returns 0 when no active slots remain or all active slots have zero demand.
func (a *partitionHubActor[T]) minDemand() int64 {
	result := int64(-1)
	for i, pid := range a.slots {
		if pid == nil {
			continue
		}
		if result < 0 || a.demand[i] < result {
			result = a.demand[i]
		}
	}
	if result <= 0 {
		return 0
	}
	return result
}
