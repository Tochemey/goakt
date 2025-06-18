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

import "sync/atomic"

// Bitflags representing individual process states.
const (
	passivationStatePaused  uint32 = 1 << 0 // Indicates the process is actively paused.
	passivationStateResumed uint32 = 1 << 1 // Indicates the process is resumed.
	passivationStateRunning uint32 = 1 << 2 // Indicates the process is running.
)

// passivationState tracks the lifecycle state of a process using bitflags.
type passivationState struct {
	flags uint32 // bitwise state flags; accessed atomically
}

// Pause sets the process to paused.
func (s *passivationState) Pause() {
	s.setFlag(passivationStatePaused)
	s.clearFlag(passivationStateResumed)
}

// Resume resumes the process.
func (s *passivationState) Resume() {
	s.setFlag(passivationStateResumed)
	s.clearFlag(passivationStatePaused)
}

// Started marks the passivation as started.
func (s *passivationState) Started() {
	s.setFlag(passivationStateRunning)
}

// Stopped unsets the running state.
func (s *passivationState) Stopped() {
	s.clearFlag(passivationStateRunning)
}

// IsPaused returns true if the process is paused.
func (s *passivationState) IsPaused() bool {
	return s.isSet(passivationStatePaused)
}

// IsResumed returns true if the process is resumed.
func (s *passivationState) IsResumed() bool {
	return s.isSet(passivationStateResumed)
}

// IsRunning returns true if the process is running.
func (s *passivationState) IsRunning() bool {
	return s.isSet(passivationStateRunning)
}

// Reset clears all state flags.
func (s *passivationState) Reset() {
	atomic.StoreUint32(&s.flags, 0)
}

// setFlag atomically sets the specified state flag.
func (s *passivationState) setFlag(flag uint32) {
	for {
		old := atomic.LoadUint32(&s.flags)
		n := old | flag
		if atomic.CompareAndSwapUint32(&s.flags, old, n) {
			return
		}
	}
}

// clearFlag atomically clears the specified state flag.
func (s *passivationState) clearFlag(flag uint32) {
	for {
		old := atomic.LoadUint32(&s.flags)
		n := old &^ flag // AND NOT clears the bit
		if atomic.CompareAndSwapUint32(&s.flags, old, n) {
			return
		}
	}
}

// isSet checks if a specific flag is set.
func (s *passivationState) isSet(flag uint32) bool {
	return atomic.LoadUint32(&s.flags)&flag != 0
}
