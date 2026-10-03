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

// pidState models the bitmask used to track the PID's internal state. Instead of
// sprinkling multiple atomic.Bool fields across the struct (which wastes cache
// lines and padding), we flip individual bits inside a single atomic.Uint32.
// Each flag represents one property; several are set at once, e.g. a suspended
// actor keeps its "running" bit, and IsRunning reads both. The combined value
// lets us toggle them efficiently.
type pidState uint32

// PID flag definitions. Each flag occupies a dedicated bit inside PID.stateFlags.
//
//   - runningState:     PID has completed initialization and may process messages.
//   - stoppingState:    PID is in the middle of Shutdown/Stop/Passivation.
//   - suspendedState:   PID has been suspended by the supervisor.
//   - passivatingState: PID is currently executing the passivation path.
//   - passivationPausedState: Passivation is paused while the actor is suspended, until it is reinstated.
//   - passivationSkipNextState: One-shot guard to skip the next passivation decision.
//   - singletonState: PID represents a cluster singleton.
//   - relocationState: PID may be relocated to another node (cluster mode).
//   - systemState:    PID is a system actor (guardian, topic actor, etc.).
//   - remoteState:    PID is a lightweight handle for an actor on a remote node.
//   - remoteHoldsClosedState: teardown has drained the remote hold registry;
//     a credit share tracked after the drain must be repaid immediately
//     because nothing will ever walk the registry again.
//   - restartingState: PID is going through a restart. The restart tears the
//     actor down with Shutdown before re-initializing it, so this bit tells the
//     stop path that the teardown it is running belongs to a restart. Restart
//     churn is owned by actor.restart.count, and actor.stopped.count must not
//     count it a second time.
//   - supervisionPendingState: a failure has been handed to supervision and no
//     decision has been made yet. The actor handles no user message until the
//     supervision goroutine clears it, so the messages queued behind a failure
//     wait for the decision instead of running on a failed actor.
const (
	runningState pidState = 1 << iota
	stoppingState
	suspendedState
	passivatingState
	passivationPausedState
	passivationSkipNextState
	singletonState
	relocationState
	systemState
	remoteState
	remoteHoldsClosedState
	restartingState
	supervisionPendingState
)

func (pid *PID) isStateSet(state pidState) bool {
	return pid.state.Load()&uint32(state) != 0
}

// setState sets or clears the given flag.
// It uses a CAS loop to avoid races when multiple goroutines try to update
// different PID state bits at the same time. If the flag already matches the
// requested state we exit early to avoid an unnecessary write.
func (pid *PID) setState(state pidState, enabled bool) {
	for {
		pidState := pid.state.Load()
		var desired uint32
		if enabled {
			desired = pidState | uint32(state)
		} else {
			desired = pidState &^ uint32(state)
		}
		if desired == pidState {
			return
		}
		if pid.state.CompareAndSwap(pidState, desired) {
			return
		}
	}
}

// compareAndSwapState changes the state only when the current state matches `old`.
// This is useful for one-shot guards—e.g. passivationSkipNext—which should only
// flip when the caller knows the previous value. Returns true if the swap happened.
func (pid *PID) compareAndSwapState(state pidState, prev, next bool) bool {
	for {
		pidState := pid.state.Load()
		has := pidState&uint32(state) != 0
		if has != prev {
			return false
		}
		var desired uint32
		if next {
			desired = pidState | uint32(state)
		} else {
			desired = pidState &^ uint32(state)
		}
		if pid.state.CompareAndSwap(pidState, desired) {
			return true
		}
	}
}
