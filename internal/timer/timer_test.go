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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/internal/util"
)

func TestStartAndExpiration(t *testing.T) {
	timer := New(100 * time.Millisecond)

	require.True(t, timer.Start(), "Start() should return true on first start")
	assert.Equal(t, StateRunning, timer.State(), "Timer should be in running state")

	select {
	case <-timer.C():
		// Success
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Timer did not expire as expected")
	}
}

func TestDoubleStart(t *testing.T) {
	timer := New(1 * time.Second)
	require.True(t, timer.Start(), "First start should succeed")

	assert.False(t, timer.Start(), "Second Start() should return false")
}

func TestPauseAndResume(t *testing.T) {
	timer := New(300 * time.Millisecond)
	require.True(t, timer.Start())

	util.Pause(100 * time.Millisecond)
	timer.Pause()

	assert.Equal(t, StatePaused, timer.State(), "Timer should be paused")

	timer.Resume()
	assert.Equal(t, StateRunning, timer.State(), "Timer should resume running")

	select {
	case <-timer.C():
	case <-time.After(300 * time.Millisecond):
		t.Fatal("Timer did not expire after resume")
	}
}

func TestStop(t *testing.T) {
	timer := New(500 * time.Millisecond)
	require.True(t, timer.Start())

	assert.True(t, timer.Stop(), "Stop() should return true")
	assert.Equal(t, StateStopped, timer.State(), "Timer should be in stopped state")

	select {
	case <-timer.C():
		t.Fatal("Timer should not fire after Stop()")
	case <-time.After(200 * time.Millisecond):
		// OK
	}
}

func TestReset(t *testing.T) {
	timer := New(1 * time.Second)
	require.True(t, timer.Start())

	timer.Reset(200 * time.Millisecond)
	assert.Equal(t, StateRunning, timer.State(), "Timer should be running after reset")

	select {
	case <-timer.C():
	case <-time.After(300 * time.Millisecond):
		t.Fatal("Timer did not expire after Reset")
	}
}

func TestPauseBeforeStart(t *testing.T) {
	timer := New(500 * time.Millisecond)
	timer.Pause() // no-op

	assert.Equal(t, StateStopped, timer.State(), "Pause before Start should not change state")
}

func TestResumeWithoutPause(t *testing.T) {
	timer := New(500 * time.Millisecond)
	timer.Resume() // no-op

	assert.Equal(t, StateStopped, timer.State(), "Resume without pause should be no-op")
}

func TestStopBeforeStart(t *testing.T) {
	timer := New(500 * time.Millisecond)

	assert.False(t, timer.Stop(), "Stop() should return false when timer hasn't started")
	assert.Equal(t, StateStopped, timer.State(), "Timer should remain in stopped state")
}

func TestResumeAfterOriginalExpiration(t *testing.T) {
	timer := New(100 * time.Millisecond)
	require.True(t, timer.Start())

	util.Pause(50 * time.Millisecond)
	timer.Pause()

	// Wait longer than original duration
	util.Pause(150 * time.Millisecond)

	timer.Resume()
	select {
	case <-timer.C():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Expected timer to expire after resume")
	}
}

func TestPauseThenStop(t *testing.T) {
	timer := New(300 * time.Millisecond)
	require.True(t, timer.Start())

	util.Pause(100 * time.Millisecond)
	timer.Pause()

	assert.True(t, timer.Stop(), "Stop should work after Pause")
	assert.Equal(t, StateStopped, timer.State())

	select {
	case <-timer.C():
		t.Fatal("Timer should not fire after being stopped")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestResetWhilePaused(t *testing.T) {
	timer := New(1 * time.Second)
	require.True(t, timer.Start())

	util.Pause(100 * time.Millisecond)
	timer.Pause()

	timer.Reset(200 * time.Millisecond)
	assert.Equal(t, StateRunning, timer.State(), "Timer should be running after reset")

	select {
	case <-timer.C():
	case <-time.After(300 * time.Millisecond):
		t.Fatal("Timer did not fire after Reset while paused")
	}
}

func TestStartStopStart(t *testing.T) {
	timer := New(100 * time.Millisecond)

	// Start the timer first time
	require.True(t, timer.Start(), "First Start() should succeed")
	assert.Equal(t, StateRunning, timer.State(), "Timer should be running after first start")

	// Stop the timer
	assert.True(t, timer.Stop(), "Stop() should succeed after first start")
	assert.Equal(t, StateStopped, timer.State(), "Timer should be stopped after Stop()")

	// Start the timer second time
	require.True(t, timer.Start(), "Second Start() should succeed after Stop()")
	assert.Equal(t, StateRunning, timer.State(), "Timer should be running after second start")

	// Confirm timer fires on second start
	select {
	case <-timer.C():
		// success
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Timer did not fire on second start")
	}
}

func TestConcurrentAccess(t *testing.T) {
	timer := New(200 * time.Millisecond)

	var wg sync.WaitGroup
	wg.Add(4)

	// Goroutine: Start timer multiple times
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			timer.Start()
			util.Pause(10 * time.Millisecond)
		}
	}()

	// Goroutine: Pause timer multiple times
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			util.Pause(5 * time.Millisecond)
			timer.Pause()
		}
	}()

	// Goroutine: Stop timer multiple times
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			util.Pause(7 * time.Millisecond)
			timer.Stop()
		}
	}()

	// Goroutine: Listen for expiration events concurrently
	go func() {
		defer wg.Done()
		for {
			select {
			case <-timer.C():
				// Timer expired, break out of listener
				return
			case <-time.After(500 * time.Millisecond):
				// Timeout to avoid hanging if no expiration
				return
			}
		}
	}()

	wg.Wait()

	// Final assertions — timer should be either stopped or running, no panic or deadlock
	state := timer.State()
	assert.True(t, state == StateStopped || state == StateRunning || state == StatePaused, "Timer state must be valid after concurrent operations")
}
