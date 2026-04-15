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

package actor

import "sync/atomic"

// Dispatch state values for the per-actor scheduling state machine.
//
//	Idle       -> no pending work, not on the ready queue.
//	Scheduled  -> on the ready queue waiting for a worker.
//	Processing -> a worker has exclusive ownership and is running a turn.
//
// The transitions are driven by atomic CAS to enforce that at most one
// worker runs an actor at any time, preserving the actor model's
// single-threaded execution invariant per actor.
const (
	// dispatchIdle: actor has no pending messages and is unscheduled.
	dispatchIdle uint32 = iota
	// dispatchScheduled: actor has pending messages and sits on the ready queue.
	dispatchScheduled
	// dispatchProcessing: a worker currently owns and is executing the actor.
	dispatchProcessing
)

// dispatchState is the lock-free three-state machine that drives an
// actor's membership on the dispatcher's ready queue.
type dispatchState struct {
	// v holds the current state as one of dispatchIdle, dispatchScheduled,
	// or dispatchProcessing.
	v atomic.Uint32
}

// Load returns the current state.
func (s *dispatchState) Load() uint32 {
	return s.v.Load()
}

// TrySchedule attempts the Idle -> Scheduled transition. Returns true
// if the caller made the transition and must push the actor onto the
// ready queue.
//
// The implementation reads the current state first and returns early
// when it is not Idle. Under N-way parallel Tell, N-1 callers lose the
// race and the plain Load is a non-contending read of a hot cache line
// whereas an unconditional CAS would cost an RFO (request for ownership)
// on every call, bouncing the line across cores for no effect.
func (s *dispatchState) TrySchedule() bool {
	if s.v.Load() != dispatchIdle {
		return false
	}
	return s.v.CompareAndSwap(dispatchIdle, dispatchScheduled)
}

// TakeForProcessing attempts the Scheduled -> Processing transition.
// Called by the worker that pulled the actor off the ready queue.
func (s *dispatchState) TakeForProcessing() bool {
	return s.v.CompareAndSwap(dispatchScheduled, dispatchProcessing)
}

// YieldToScheduled performs the Processing -> Scheduled transition.
// Called when a worker rotates off an actor that still has pending
// messages after exhausting its throughput budget.
func (s *dispatchState) YieldToScheduled() {
	s.v.Store(dispatchScheduled)
}

// reset forces the state back to Idle. Called by the worker turn loop
// when it observes a drained mailbox (paired with TrySchedule for the
// race-safe reclaim) and by the actor restart path after the caller
// has confirmed no worker holds the actor.
func (s *dispatchState) reset() {
	s.v.Store(dispatchIdle)
}
