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

package timer

import (
	"sync"
	"time"
)

// State represents the current state of a Timer
type State int

const (
	// StateStopped indicates the timer is stopped.
	StateStopped State = iota
	// StateRunning indicates the timer is currently running.
	StateRunning
	// StatePaused indicates the timer is paused and can be resumed.
	StatePaused
)

// Timer provides a thread-safe, pausable, resettable timer.
// It wraps time.Timer and adds explicit support for Start, Stop, Pause, Resume, and Reset semantics.
type Timer struct {
	mu       sync.Mutex
	timer    *time.Timer
	duration time.Duration
	expireAt time.Time
	remain   time.Duration
	state    State
}

// New creates a new SafeTimer with the given duration.
// The timer is created in a stopped state and must be explicitly started using Start().
func New(duration time.Duration) *Timer {
	return &Timer{
		duration: duration,
		timer:    time.NewTimer(duration),
		state:    StateStopped,
	}
}

// Start starts the timer if it is currently stopped.
// Returns true if the timer was successfully started, false otherwise
func (t *Timer) Start() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.state != StateStopped {
		return false
	}
	t.resetLocked(t.duration)
	t.state = StateRunning
	return true
}

// Stop stops the timer if it is running or paused.
// Returns true if the timer was successfully stopped, false if it was already stopped.
func (t *Timer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.state == StateStopped {
		return false
	}
	t.timer.Stop() // may return false, but we don't care here
	t.drainChannel()
	t.state = StateStopped
	t.remain = 0
	return true // we successfully transitioned state
}

// Reset stops the current timer (if running or paused) and starts it again with a new duration.
// It also clears the paused or running state, setting it to running with the new duration.
func (t *Timer) Reset(duration time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.duration = duration
	t.resetLocked(duration)
	t.state = StateRunning
}

// Pause stops the timer temporarily, preserving the remaining duration.
// The timer can later be resumed using Resume().
func (t *Timer) Pause() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.state != StateRunning {
		return
	}
	if !t.timer.Stop() {
		t.drainChannel()
	}
	t.remain = time.Until(t.expireAt)
	t.state = StatePaused
}

// Resume resumes a previously paused timer from where it left off.
// Does nothing if the timer is not currently paused.
func (t *Timer) Resume() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.state != StatePaused {
		return
	}
	t.resetLocked(t.remain)
	t.state = StateRunning
}

// C returns a read-only channel that receives a value when the timer expires.
// It behaves the same as time.Timer.C.
func (t *Timer) C() <-chan time.Time {
	return t.timer.C
}

// State returns the current state of the timer (Running, Paused, or Stopped).
func (t *Timer) State() State {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

// resetLocked resets the timer to the specified duration.
// It assumes the caller already holds the mutex lock.
func (t *Timer) resetLocked(d time.Duration) {
	if !t.timer.Stop() {
		t.drainChannel()
	}
	t.expireAt = time.Now().Add(d)
	t.timer.Reset(d)
}

// drainChannel drains the timer's channel if it has already fired, to prevent blocking.
func (t *Timer) drainChannel() {
	select {
	case <-t.timer.C:
	default:
	}
}
