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
	"slices"
	"sync"
	"sync/atomic"

	"github.com/tochemey/goakt/v4/actor"
)

// fanOutBranch is one materialization of a fan-out branch: the slot actor at
// the head of that branch's pipeline and the stream it belongs to.
type fanOutBranch struct {
	// pid is the slot actor at the head of the branch's pipeline; the hub
	// sends it the branch's elements.
	pid *actor.PID
	// subID is the id of the branch's stream; the hub stamps it on every
	// message it sends to that slot.
	subID string
}

// fanOutGenerations pairs the materializations of a fan-out's branches into
// generations. The branches returned by one Broadcast, Balance or Partition
// call are separate graphs that are run separately, and the same graphs may
// be run again. A generation is one materialization of every branch: the
// k-th run of each branch belongs to generation k, whatever the order in
// which the runs arrive, and every generation gets its own hub and upstream.
//
// A branch materialized before its siblings waits; a branch that stops while
// it waits is withdrawn, so a later generation never pairs with a dead slot.
type fanOutGenerations struct {
	mu sync.Mutex
	// waiting holds, per slot, the materializations not yet paired into a
	// generation, oldest first.
	waiting [][]fanOutBranch
}

// sharedBroadcast is the coordination point shared by all N broadcastSlotActors
// that result from a single Broadcast call. It collects the slot actors as each
// branch is materialized and, every time one materialization of every branch
// is available, spawns a fresh upstream sub-pipeline with a new
// broadcastHubActor as its terminal sink for that generation.
type sharedBroadcast[T any] struct {
	n           int
	srcStages   []*stage
	generations *fanOutGenerations
}

// newSharedBroadcast creates the coordination struct for n branches of srcStages.
func newSharedBroadcast[T any](n int, srcStages []*stage) *sharedBroadcast[T] {
	return &sharedBroadcast[T]{
		n:           n,
		srcStages:   srcStages,
		generations: newFanOutGenerations(n),
	}
}

// registerSlot records one materialization of a slot actor. When it completes
// a generation, it builds that generation's hub over the paired slot actors
// and spawns the upstream sub-pipeline in a goroutine. The hub struct is
// populated before the goroutine starts, ensuring race-free visibility in the
// hub's actor goroutine (Go memory model: writes before goroutine start
// happen-before the goroutine body).
func (s *sharedBroadcast[T]) registerSlot(ctx context.Context, slot int, pid *actor.PID, subID string, sys actor.ActorSystem) {
	generation := s.generations.register(slot, pid, subID)
	if generation == nil {
		return
	}

	hub := &broadcastHubActor[T]{
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
	hub           *actor.PID
	pendingDemand int64
	config        StageConfig
	// self is the slot's own PID, stored when it is wired so that PostStop,
	// which may run on another goroutine, can withdraw it from the fan-out.
	self atomic.Pointer[actor.PID]
}

func (a *broadcastSlotActor[T]) PreStart(_ *actor.Context) error { return nil }

// Receive handles stageWire, streamRequest, hubReady, streamElement,
// streamComplete, streamError, and streamCancel.
func (a *broadcastSlotActor[T]) Receive(rctx *actor.ReceiveContext) {
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
func (a *broadcastSlotActor[T]) PostStop(_ *actor.Context) error {
	a.shared.generations.withdraw(a.slot, a.self.Load())
	return nil
}

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
	slots      []*actor.PID // own copy; slot[i] set nil on cancel
	slotSubIDs []string
	demand     []int64 // per-slot outstanding demand
	upstream   *actor.PID
	subID      string
	pending    int64 // elements requested from upstream but not yet received
	seqNo      uint64
	cancelled  int // cumulative count of slots that sent slotCancel
	config     StageConfig
}

// PreStart does nothing: the hub starts on its stageWire.
func (a *broadcastHubActor[T]) PreStart(_ *actor.Context) error { return nil }

// Receive handles stageWire, slotDemand, streamElement, streamComplete,
// streamError, slotCancel, and actor.Terminated for the slot actors.
func (a *broadcastHubActor[T]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.upstream = msg.upstream
		a.subID = msg.subID
		// All slot PIDs are pre-populated (written before goroutine start).
		// Notify every slot that the hub is ready to receive demand.
		hub := rctx.Self()
		for _, slot := range a.slots {
			rctx.Watch(slot)
			rctx.Tell(slot, &hubReady{hub: hub})
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
		a.seqNo++
		for i, slot := range a.slots {
			if slot == nil {
				continue // slot has cancelled
			}
			rctx.Tell(slot, &streamElement{
				subID: a.slotSubIDs[i],
				value: msg.value,
				seqNo: a.seqNo,
			})
			a.demand[i]--
		}
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

// PostStop does nothing: the hub holds no resource of its own.
func (a *broadcastHubActor[T]) PostStop(_ *actor.Context) error { return nil }

// releaseSlot removes slot from the active slots, because its branch cancelled
// or its actor stopped. When no slot is left it cancels the upstream and shuts
// the hub down; otherwise the remaining slots may now unblock the pull. It
// does nothing for a slot that was already released.
func (a *broadcastHubActor[T]) releaseSlot(rctx *actor.ReceiveContext, slot int) {
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

// newFanOutGenerations creates the pairing state for a fan-out of n branches.
func newFanOutGenerations(n int) *fanOutGenerations {
	return &fanOutGenerations{waiting: make([][]fanOutBranch, n)}
}

// register records one materialization of the branch at slot. When every slot
// has a waiting materialization it removes the oldest of each and returns
// them, indexed by slot, as a complete generation; otherwise it returns nil.
// Waiting slot actors that have stopped are dropped first, so a generation is
// formed of running slots only.
func (x *fanOutGenerations) register(slot int, pid *actor.PID, subID string) []fanOutBranch {
	x.mu.Lock()
	defer x.mu.Unlock()

	x.waiting[slot] = append(x.waiting[slot], fanOutBranch{pid: pid, subID: subID})
	for i := range x.waiting {
		x.waiting[i] = slices.DeleteFunc(x.waiting[i], func(branch fanOutBranch) bool {
			return !branch.pid.IsRunning()
		})

		if len(x.waiting[i]) == 0 {
			return nil
		}
	}

	generation := make([]fanOutBranch, len(x.waiting))
	for i, queue := range x.waiting {
		generation[i] = queue[0]
		x.waiting[i] = queue[1:]
	}

	return generation
}

// withdraw removes the slot actor pid from the materializations waiting at
// slot. It does nothing when pid is nil or has already been paired into a
// generation. Slot actors call it from PostStop.
func (x *fanOutGenerations) withdraw(slot int, pid *actor.PID) {
	if pid == nil {
		return
	}

	x.mu.Lock()
	defer x.mu.Unlock()

	x.waiting[slot] = slices.DeleteFunc(x.waiting[slot], func(branch fanOutBranch) bool {
		return branch.pid == pid
	})
}

// spawnFanOutUpstream materializes the upstream of one fan-out generation:
// stages ends with that generation's hub. When the upstream cannot be
// materialized, no hub exists to serve the branches, so every branch of the
// generation is failed with the materialization error instead of being left
// waiting.
func spawnFanOutUpstream(ctx context.Context, sys actor.ActorSystem, stages []*stage, generation []fanOutBranch) {
	if _, err := materialize(ctx, sys, stages); err != nil {
		for _, branch := range generation {
			_ = actor.Tell(context.Background(), branch.pid, &streamError{subID: branch.subID, err: err})
		}
	}
}

// terminatedSlot returns the index of the active slot whose actor msg reports
// as terminated, or -1 when msg is about no active slot. slots holds nil for
// the slots already released.
func terminatedSlot(slots []*actor.PID, msg *actor.Terminated) int {
	for i, slot := range slots {
		if slot != nil && msg.ActorPath().Equals(slot.Path()) {
			return i
		}
	}

	return -1
}

// stoppedSlots returns the indexes of the active slots whose actors are not
// running. A hub calls it once it has watched its slots: watching an actor
// that has already stopped delivers no Terminated, so a branch that stopped
// between the pairing of its generation and the wiring of the hub is found
// here instead.
func stoppedSlots(slots []*actor.PID) []int {
	var stopped []int
	for i, slot := range slots {
		if slot != nil && !slot.IsRunning() {
			stopped = append(stopped, i)
		}
	}

	return stopped
}
