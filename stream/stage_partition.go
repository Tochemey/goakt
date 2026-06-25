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

// sharedPartition coordinates the N partitionSlotActors that result from a
// single Partition call. It mirrors sharedBalance: each slot registers itself
// at wire-time, and once every slot has registered, the upstream sub-pipeline
// is spawned with partitionHubActor as its terminal sink.
type sharedPartition[T any] struct {
	mu         sync.Mutex
	n          int
	srcStages  []*stage
	slots      []*actor.PID
	slotSubIDs []string
	registered int
	started    bool
	hub        *partitionHubActor[T]
}

// newSharedPartition creates the coordination struct and pre-allocates the hub.
func newSharedPartition[T any](n int, srcStages []*stage, fn func(any) int) *sharedPartition[T] {
	hub := &partitionHubActor[T]{
		n:           n,
		slots:       make([]*actor.PID, n),
		slotSubIDs:  make([]string, n),
		demand:      make([]int64, n),
		partitionFn: fn,
	}
	return &sharedPartition[T]{
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
func (s *sharedPartition[T]) registerSlot(ctx context.Context, slot int, pid *actor.PID, subID string, sys actor.ActorSystem) {
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
		hubSinkDesc := &stage{
			id:   newStageID(),
			kind: sinkKind,
			actorFn: func(cfg StageConfig) actor.Actor {
				s.hub.config = cfg
				return s.hub
			},
			config: defaultStageConfig(),
		}
		all := make([]*stage, len(s.srcStages)+1)
		copy(all, s.srcStages)
		all[len(s.srcStages)] = hubSinkDesc
		go spawnSubPipeline(ctx, sys, all)
	}
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
}

func (a *partitionSlotActor[T]) PreStart(_ *actor.Context) error { return nil }

// Receive handles stageWire, streamRequest, hubReady, streamElement,
// streamComplete, streamError, and streamCancel.
func (a *partitionSlotActor[T]) Receive(rctx *actor.ReceiveContext) {
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
		if a.downstream != nil {
			rctx.Tell(a.downstream, &streamComplete{subID: a.subID})
		}
		rctx.Shutdown()

	default:
		rctx.Unhandled()
	}
}

func (a *partitionSlotActor[T]) PostStop(_ *actor.Context) error { return nil }

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
// streamError, and slotCancel.
func (a *partitionHubActor[T]) Receive(rctx *actor.ReceiveContext) {
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

func (a *partitionHubActor[T]) PostStop(_ *actor.Context) error { return nil }

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
