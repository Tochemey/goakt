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
	"sync"

	"github.com/tochemey/goakt/v4/actor"
)

// sharedBalance is the coordination point shared by all N balanceSlotActors
// that result from a single Balance call. It collects slot actor PIDs as each
// branch is materialized, then spawns the upstream sub-pipeline (with the
// balanceHubActor as its terminal sink) once every slot has registered.
type sharedBalance[T any] struct {
	mu         sync.Mutex
	n          int
	srcStages  []*stageDesc
	slots      []*actor.PID
	slotSubIDs []string
	registered int
	started    bool
	hub        *balanceHubActor[T]
}

// newSharedBalance creates the coordination struct and pre-allocates the hub.
func newSharedBalance[T any](n int, srcStages []*stageDesc) *sharedBalance[T] {
	hub := &balanceHubActor[T]{
		n:          n,
		slots:      make([]*actor.PID, n),
		slotSubIDs: make([]string, n),
		demand:     make([]int64, n),
	}
	return &sharedBalance[T]{
		n:          n,
		srcStages:  srcStages,
		slots:      make([]*actor.PID, n),
		slotSubIDs: make([]string, n),
		hub:        hub,
	}
}

// registerSlot records a slot actor's PID and subID. When the last slot
// registers, it snapshots slot info into the hub struct and spawns the
// upstream sub-pipeline in a goroutine.
func (s *sharedBalance[T]) registerSlot(ctx context.Context, slot int, pid *actor.PID, subID string, sys actor.ActorSystem) {
	s.mu.Lock()
	s.slots[slot] = pid
	s.slotSubIDs[slot] = subID
	s.registered++
	allReady := s.registered == s.n && !s.started
	if allReady {
		s.started = true
		copy(s.hub.slots, s.slots)
		copy(s.hub.slotSubIDs, s.slotSubIDs)
	}
	s.mu.Unlock()

	if allReady {
		hubSinkDesc := &stageDesc{
			id:   newStageID(),
			kind: sinkKind,
			makeActor: func(cfg StageConfig) actor.Actor {
				s.hub.config = cfg
				return s.hub
			},
			config: defaultStageConfig(),
		}
		all := make([]*stageDesc, len(s.srcStages)+1)
		copy(all, s.srcStages)
		all[len(s.srcStages)] = hubSinkDesc
		go spawnSubPipeline(ctx, sys, all)
	}
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
}

func (a *balanceSlotActor[T]) PreStart(_ *actor.Context) error { return nil }

// Receive handles stageWire, streamRequest, hubReady, streamElement,
// streamComplete, streamError, and streamCancel.
func (a *balanceSlotActor[T]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.downstream = msg.downstream
		a.subID = msg.subID
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

func (a *balanceSlotActor[T]) PostStop(_ *actor.Context) error { return nil }

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
// streamError, and slotCancel.
func (a *balanceHubActor[T]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.upstream = msg.upstream
		a.subID = msg.subID
		hub := rctx.Self()
		for _, slotPID := range a.slots {
			rctx.Tell(slotPID, &hubReady{hub: hub})
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
		a.slots[msg.slot] = nil
		a.cancelled++
		if a.cancelled >= a.n {
			if a.upstream != nil {
				rctx.Tell(a.upstream, &streamCancel{subID: a.subID})
			}
			rctx.Shutdown()
			return
		}
		a.maybePull(rctx)

	default:
		rctx.Unhandled()
	}
}

func (a *balanceHubActor[T]) PostStop(_ *actor.Context) error { return nil }

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
