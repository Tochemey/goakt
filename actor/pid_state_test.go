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
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInitialState(t *testing.T) {
	s := &pidState{}

	require.False(t, s.IsRunning(), "Expected IsRunning() to be false initially")
	require.False(t, s.IsSuspended(), "Expected IsSuspended() to be false initially")
	require.False(t, s.IsStopping(), "Expected IsStopping() to be false initially")
	require.False(t, s.IsRunnable(), "Expected IsRunnable() to be false initially")
}

func TestSetRunning(t *testing.T) {
	s := &pidState{}

	s.SetRunning()

	require.True(t, s.IsRunning(), "Expected IsRunning() to be true after SetRunning()")
	require.True(t, s.IsRunnable(), "Expected IsRunnable() to be true when only running flag is set")
}

func TestClearRunning(t *testing.T) {
	s := &pidState{}

	s.SetRunning()
	s.ClearRunning()

	require.False(t, s.IsRunning(), "Expected IsRunning() to be false after ClearRunning()")
	require.False(t, s.IsRunnable(), "Expected IsRunnable() to be false after clearing running flag")
}

func TestSetSuspended(t *testing.T) {
	s := &pidState{}

	s.SetSuspended()

	require.True(t, s.IsSuspended(), "Expected IsSuspended() to be true after SetSuspended()")
}

func TestClearSuspended(t *testing.T) {
	s := &pidState{}

	s.SetSuspended()
	s.ClearSuspended()

	require.False(t, s.IsSuspended(), "Expected IsSuspended() to be false after ClearSuspended()")
}

func TestSetStopping(t *testing.T) {
	s := &pidState{}

	s.SetStopping()

	require.True(t, s.IsStopping(), "Expected IsStopping() to be true after SetStopping()")
}

func TestClearStopping(t *testing.T) {
	s := &pidState{}

	s.SetStopping()
	s.ClearStopping()

	require.False(t, s.IsStopping(), "Expected IsStopping() to be false after ClearStopping()")
}

func TestMultipleFlagsCanBeSet(t *testing.T) {
	s := &pidState{}

	s.SetRunning()
	s.SetSuspended()
	s.SetStopping()

	require.True(t, s.IsRunning(), "Expected IsRunning() to be true")
	require.True(t, s.IsSuspended(), "Expected IsSuspended() to be true")
	require.True(t, s.IsStopping(), "Expected IsStopping() to be true")
}

func TestIsRunnable(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(*pidState)
		expected bool
	}{
		{
			name:     "not runnable when no flags set",
			setup:    func(s *pidState) {}, // nolint
			expected: false,
		},
		{
			name: "runnable when only running flag set",
			setup: func(s *pidState) {
				s.SetRunning()
			},
			expected: true,
		},
		{
			name: "not runnable when running and suspended",
			setup: func(s *pidState) {
				s.SetRunning()
				s.SetSuspended()
			},
			expected: false,
		},
		{
			name: "not runnable when running and stopping",
			setup: func(s *pidState) {
				s.SetRunning()
				s.SetStopping()
			},
			expected: false,
		},
		{
			name: "not runnable when all flags set",
			setup: func(s *pidState) {
				s.SetRunning()
				s.SetSuspended()
				s.SetStopping()
			},
			expected: false,
		},
		{
			name: "not runnable when only suspended",
			setup: func(s *pidState) {
				s.SetSuspended()
			},
			expected: false,
		},
		{
			name: "not runnable when only stopping",
			setup: func(s *pidState) {
				s.SetStopping()
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &pidState{}
			tt.setup(s)

			require.Equal(t, tt.expected, s.IsRunnable(), "IsRunnable() returned unexpected value")
		})
	}
}

func TestReset(t *testing.T) {
	s := &pidState{}

	// Set all flags
	s.SetRunning()
	s.SetSuspended()
	s.SetStopping()

	// Verify they're set
	require.True(t, s.IsRunning(), "Expected running flag to be set before reset")
	require.True(t, s.IsSuspended(), "Expected suspended flag to be set before reset")
	require.True(t, s.IsStopping(), "Expected stopping flag to be set before reset")

	// Reset
	s.Reset()

	// Verify they're all cleared
	require.False(t, s.IsRunning(), "Expected IsRunning() to be false after Reset()")
	require.False(t, s.IsSuspended(), "Expected IsSuspended() to be false after Reset()")
	require.False(t, s.IsStopping(), "Expected IsStopping() to be false after Reset()")
	require.False(t, s.IsRunnable(), "Expected IsRunnable() to be false after Reset()")
}

func TestClearingIndividualFlags(t *testing.T) {
	s := &pidState{}

	// Set all flags
	s.SetRunning()
	s.SetSuspended()
	s.SetStopping()

	// Clear running, others should remain
	s.ClearRunning()
	require.False(t, s.IsRunning(), "Expected IsRunning() to be false")
	require.True(t, s.IsSuspended(), "Expected IsSuspended() to remain true")
	require.True(t, s.IsStopping(), "Expected IsStopping() to remain true")

	// Clear suspended, stopping should remain
	s.ClearSuspended()
	require.False(t, s.IsSuspended(), "Expected IsSuspended() to be false")
	require.True(t, s.IsStopping(), "Expected IsStopping() to remain true")

	// Clear stopping, all should be false
	s.ClearStopping()
	require.False(t, s.IsStopping(), "Expected IsStopping() to be false")
}

func TestAtomicityOfSetFlag(t *testing.T) {
	s := &pidState{}
	const numGoroutines = 100

	var wg sync.WaitGroup

	// Multiple goroutines try to set the same flag simultaneously
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.SetRunning()
		}()
	}

	wg.Wait()

	// Flag should be set exactly once, not corrupted
	require.True(t, s.IsRunning(), "Expected flag to be set after concurrent SetRunning() calls")
}

func TestAtomicityOfClearFlag(t *testing.T) {
	s := &pidState{}
	s.SetRunning()
	s.SetSuspended()
	s.SetStopping()

	const numGoroutines = 100
	var wg sync.WaitGroup

	// Multiple goroutines try to clear the same flag simultaneously
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.ClearRunning()
		}()
	}

	wg.Wait()

	// Running flag should be cleared, others should remain
	require.False(t, s.IsRunning(), "Expected running flag to be cleared")
	require.True(t, s.IsSuspended(), "Expected suspended flag to remain set")
	require.True(t, s.IsStopping(), "Expected stopping flag to remain set")
}

func TestStateTransitions(t *testing.T) {
	s := &pidState{}

	// Test a realistic state transition sequence

	// Process starts
	s.SetRunning()
	require.True(t, s.IsRunnable(), "Process should be runnable when running and no other flags set")

	// Process gets suspended
	s.SetSuspended()
	require.False(t, s.IsRunnable(), "Process should not be runnable when suspended")

	// Process resumes
	s.ClearSuspended()
	require.True(t, s.IsRunnable(), "Process should be runnable after resuming")

	// Process begins stopping
	s.SetStopping()
	require.False(t, s.IsRunnable(), "Process should not be runnable when stopping")

	// Process fully stops
	s.ClearRunning()
	require.False(t, s.IsRunnable(), "Process should not be runnable when not running")

	// Process is cleaned up
	s.Reset()
	require.False(t, s.IsRunning(), "IsRunning should be false after reset")
	require.False(t, s.IsSuspended(), "IsSuspended should be false after reset")
	require.False(t, s.IsStopping(), "IsStopping should be false after reset")
}
