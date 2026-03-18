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

// sharedBroadcast is the coordination point shared by all N broadcastSlotActors
// that result from a single Broadcast call. It collects the slot actor PIDs and
// subIDs as each branch is materialized, then spawns the upstream sub-pipeline
// (with the broadcastHubActor as its terminal sink) once every slot has
// registered. The hub struct is pre-created here and populated with slot info
// before the goroutine start, ensuring race-free visibility in the hub's actor
// goroutine (Go memory model: writes before goroutine start happen-before the
// goroutine body).
type sharedBroadcast[T any] struct {
	mu         sync.Mutex
	n          int
	srcStages  []*stageDesc
	slotPIDs   []*actor.PID
	slotSubIDs []string
	registered int
	started    bool
	hub        *broadcastHubActor[T]
}

// newSharedBroadcast creates the coordination struct and pre-allocates the hub.
func newSharedBroadcast[T any](n int, srcStages []*stageDesc) *sharedBroadcast[T] {
	hub := &broadcastHubActor[T]{
		n:          n,
		slotPIDs:   make([]*actor.PID, n),
		slotSubIDs: make([]string, n),
		demand:     make([]int64, n),
	}
	return &sharedBroadcast[T]{
		n:          n,
		srcStages:  srcStages,
		slotPIDs:   make([]*actor.PID, n),
		slotSubIDs: make([]string, n),
		hub:        hub,
	}
}

// registerSlot records a slot actor's PID and subID. When the last slot
// registers, it snapshots the collected slot info into the hub struct and
// spawns the upstream sub-pipeline in a goroutine.
func (s *sharedBroadcast[T]) registerSlot(ctx context.Context, slot int, pid *actor.PID, subID string, sys actor.ActorSystem) {
	s.mu.Lock()
	s.slotPIDs[slot] = pid
	s.slotSubIDs[slot] = subID
	s.registered++
	allReady := s.registered == s.n && !s.started
	if allReady {
		s.started = true
		// Copy slot info into the hub before the goroutine start so the hub's
		// actor goroutine sees the writes without additional synchronization.
		copy(s.hub.slotPIDs, s.slotPIDs)
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

// broadcastSlotActor is the source actor for one branch of a Broadcast fan-out.
// It is transparent to its branch's downstream: it relays streamRequest messages
// as slotDemand to the hub, and forwards streamElement, streamComplete, and
// streamError messages from the hub directly downstream. Demand arriving before
// the hub announces itself (via hubReady) is buffered and flushed on receipt of
// hubReady.
type broadcastSlotActor[T any] struct {
	shared        *sharedBroadcast[T]
	slot          int
	downstream    *actor.PID
	subID         string
	hubPID        *actor.PID
	pendingDemand int64
	config        StageConfig
}

func (a *broadcastSlotActor[T]) PreStart(_ *actor.Context) error { return nil }

// Receive handles stageWire, streamRequest, hubReady, streamElement,
// streamComplete, streamError, and streamCancel.
func (a *broadcastSlotActor[T]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.downstream = msg.downstream
		a.subID = msg.subID
		a.shared.registerSlot(rctx.Context(), a.slot, rctx.Self(), msg.subID, rctx.ActorSystem())

	case *streamRequest:
		if a.hubPID != nil {
			rctx.Tell(a.hubPID, &slotDemand{slot: a.slot, n: msg.n})
		} else {
			a.pendingDemand += msg.n
		}

	case *hubReady:
		a.hubPID = msg.hubPID
		if a.pendingDemand > 0 {
			rctx.Tell(a.hubPID, &slotDemand{slot: a.slot, n: a.pendingDemand})
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
		if a.hubPID != nil {
			rctx.Tell(a.hubPID, &slotCancel{slot: a.slot})
		}
		rctx.Shutdown()

	default:
		rctx.Unhandled()
	}
}

func (a *broadcastSlotActor[T]) PostStop(_ *actor.Context) error { return nil }

// broadcastHubActor is the terminal sink of the upstream sub-pipeline spawned by
// a Broadcast. It receives each element once from its upstream and delivers it
// to every active slot actor, enforcing backpressure by pulling from upstream
// only when the slot with the least outstanding demand still has capacity (i.e.
// minDemand > 0). Demand contributions arrive as slotDemand messages sent by
// slot actors when their own downstream requests more. Slot actors that cancel
// early are silently removed; when all slots have cancelled, the hub propagates
// cancellation upstream and shuts down.
type broadcastHubActor[T any] struct {
	n          int
	slotPIDs   []*actor.PID // own copy; slot[i] set nil on cancel
	slotSubIDs []string
	demand     []int64 // per-slot outstanding demand
	upstream   *actor.PID
	subID      string
	pending    int64 // elements requested from upstream but not yet received
	seqNo      uint64
	cancelled  int // cumulative count of slots that sent slotCancel
	config     StageConfig
}

func (a *broadcastHubActor[T]) PreStart(_ *actor.Context) error { return nil }

// Receive handles stageWire, slotDemand, streamElement, streamComplete,
// streamError, and slotCancel.
func (a *broadcastHubActor[T]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.upstream = msg.upstream
		a.subID = msg.subID
		// All slot PIDs are pre-populated (written before goroutine start).
		// Notify every slot that the hub is ready to receive demand.
		hubPID := rctx.Self()
		for _, slotPID := range a.slotPIDs {
			rctx.Tell(slotPID, &hubReady{hubPID: hubPID})
		}

	case *slotDemand:
		a.demand[msg.slot] += msg.n
		a.maybePull(rctx)

	case *streamElement:
		a.pending--
		a.seqNo++
		for i, slotPID := range a.slotPIDs {
			if slotPID == nil {
				continue // slot has cancelled
			}
			rctx.Tell(slotPID, &streamElement{
				subID: a.slotSubIDs[i],
				value: msg.value,
				seqNo: a.seqNo,
			})
			a.demand[i]--
		}
		a.maybePull(rctx)

	case *streamComplete:
		for i, slotPID := range a.slotPIDs {
			if slotPID != nil {
				rctx.Tell(slotPID, &streamComplete{subID: a.slotSubIDs[i]})
			}
		}
		rctx.Shutdown()

	case *streamError:
		for i, slotPID := range a.slotPIDs {
			if slotPID != nil {
				rctx.Tell(slotPID, &streamError{subID: a.slotSubIDs[i], err: msg.err})
			}
		}
		rctx.Shutdown()

	case *slotCancel:
		a.slotPIDs[msg.slot] = nil
		a.cancelled++
		if a.cancelled >= a.n {
			// Every branch has cancelled; propagate cancellation upstream.
			if a.upstream != nil {
				rctx.Tell(a.upstream, &streamCancel{subID: a.subID})
			}
			rctx.Shutdown()
			return
		}
		// Remaining slots may now unblock the pull if this slot was the bottleneck.
		a.maybePull(rctx)

	default:
		rctx.Unhandled()
	}
}

func (a *broadcastHubActor[T]) PostStop(_ *actor.Context) error { return nil }

// maybePull requests a batch from upstream when all active slots have
// outstanding demand and no elements are currently in flight.
func (a *broadcastHubActor[T]) maybePull(rctx *actor.ReceiveContext) {
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
func (a *broadcastHubActor[T]) minDemand() int64 {
	result := int64(-1)
	for i, pid := range a.slotPIDs {
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
