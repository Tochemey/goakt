/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package actor

// pidFlag models the bitmask used to track the PID's internal state. Instead of
// sprinkling multiple atomic.Bool fields across the struct (which wastes cache
// lines and padding), we flip individual bits inside a single atomic.Uint32.
// Each flag represents a mutually independent property—e.g. "running" and
// "suspended" should never be true at the same time—but the combined value lets
// us toggle them efficiently.
type pidFlag uint32

// PID flag definitions. Each flag occupies a dedicated bit inside PID.stateFlags.
//
//   - runningFlag:     PID has completed initialization and may process messages.
//   - stoppingFlag:    PID is in the middle of Shutdown/Stop/Passivation.
//   - suspendedFlag:   PID has been suspended by the supervisor.
//   - passivatingFlag: PID is currently executing the passivation path.
//   - passivationPausedFlag: Passivation is temporarily paused (e.g. during Watch/Reinstate).
//   - passivationSkipNextFlag: One-shot guard to skip the next passivation decision.
//   - isSingletonFlag: PID represents a cluster singleton.
//   - isRelocatableFlag: PID may be relocated to another node (cluster mode).
//   - isSystemFlag:    PID is a system actor (guardian, topic actor, etc.).
const (
	runningFlag pidFlag = 1 << iota
	stoppingFlag
	suspendedFlag
	passivatingFlag
	passivationPausedFlag
	passivationSkipNextFlag
	isSingletonFlag
	isRelocatableFlag
	isSystemFlag
)

func (pid *PID) isFlagEnabled(flag pidFlag) bool {
	return pid.stateFlags.Load()&uint32(flag) != 0
}

// toggleFlag sets or clears the given flag.
// It uses a CAS loop to avoid races when multiple goroutines try to update
// different PID state bits at the same time. If the flag already matches the
// requested state we exit early to avoid an unnecessary write.
func (pid *PID) toggleFlag(flag pidFlag, enabled bool) {
	for {
		state := pid.stateFlags.Load()
		var desired uint32
		if enabled {
			desired = state | uint32(flag)
		} else {
			desired = state &^ uint32(flag)
		}
		if desired == state {
			return
		}
		if pid.stateFlags.CompareAndSwap(state, desired) {
			return
		}
	}
}

// compareAndSwapFlag changes the flag only when the current state matches `old`.
// This is useful for one-shot guards—e.g. passivationSkipNext—which should only
// flip when the caller knows the previous value. Returns true if the swap happened.
func (pid *PID) compareAndSwapFlag(flag pidFlag, prev, next bool) bool {
	for {
		state := pid.stateFlags.Load()
		has := state&uint32(flag) != 0
		if has != prev {
			return false
		}
		var desired uint32
		if next {
			desired = state | uint32(flag)
		} else {
			desired = state &^ uint32(flag)
		}
		if pid.stateFlags.CompareAndSwap(state, desired) {
			return true
		}
	}
}
