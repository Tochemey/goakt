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

import (
	"sync/atomic"
)

// Bitflags representing individual process states.
const (
	stateRunning   = 1 << 0 // Indicates the process is actively running.
	stateSuspended = 1 << 1 // Indicates the process is suspended.
	stateStopping  = 1 << 2 // Indicates the process is stopping.
)

// pidState represents the internal state of a process using a compact
// atomic bitfield. It tracks whether the process is running, suspended,
// or stopping—all in a single 32-bit word.
type pidState struct {
	flags uint32 // accessed atomically
}

// SetRunning marks the process as running.
func (s *pidState) SetRunning() {
	s.setFlag(stateRunning)
}

// ClearRunning unmarks the running state.
func (s *pidState) ClearRunning() {
	s.clearFlag(stateRunning)
}

// IsRunning checks whether the process is currently running.
func (s *pidState) IsRunning() bool {
	return s.isFlagSet(stateRunning)
}

// SetSuspended marks the process as suspended.
func (s *pidState) SetSuspended() {
	s.setFlag(stateSuspended)
}

// ClearSuspended unmarks the suspended state.
func (s *pidState) ClearSuspended() {
	s.clearFlag(stateSuspended)
}

// IsSuspended checks whether the process is currently suspended.
func (s *pidState) IsSuspended() bool {
	return s.isFlagSet(stateSuspended)
}

// SetStopping marks the process as stopping.
func (s *pidState) SetStopping() {
	s.setFlag(stateStopping)
}

// ClearStopping unmarks the stopping state.
func (s *pidState) ClearStopping() {
	s.clearFlag(stateStopping)
}

// IsStopping checks whether the process is currently stopping.
func (s *pidState) IsStopping() bool {
	return s.isFlagSet(stateStopping)
}

// IsRunnable returns true if the process is in a runnable state.
// A runnable process is one that is running, and is neither suspended nor stopping.
func (s *pidState) IsRunnable() bool {
	flags := atomic.LoadUint32(&s.flags)
	return flags&stateRunning != 0 &&
		flags&stateStopping == 0 &&
		flags&stateSuspended == 0
}

// Reset clears all flags, resetting the state to zero.
func (s *pidState) Reset() {
	atomic.StoreUint32(&s.flags, 0)
}

// setFlag atomically sets the given flag bit.
func (s *pidState) setFlag(flag uint32) {
	for {
		old := atomic.LoadUint32(&s.flags)
		n := old | flag
		if atomic.CompareAndSwapUint32(&s.flags, old, n) {
			return
		}
	}
}

// clearFlag atomically clears the given flag bit.
func (s *pidState) clearFlag(flag uint32) {
	for {
		old := atomic.LoadUint32(&s.flags)
		n := old &^ flag // AND NOT clears the bit
		if atomic.CompareAndSwapUint32(&s.flags, old, n) {
			return
		}
	}
}

// isFlagSet returns true if the specified flag bit is set.
func (s *pidState) isFlagSet(flag uint32) bool {
	return atomic.LoadUint32(&s.flags)&flag != 0
}
