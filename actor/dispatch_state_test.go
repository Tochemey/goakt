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

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDispatchStateHappyPath(t *testing.T) {
	var s dispatchState
	require.Equal(t, dispatchIdle, s.Load())

	require.True(t, s.TrySchedule())
	require.Equal(t, dispatchScheduled, s.Load())

	// Second TrySchedule while Scheduled must fail.
	require.False(t, s.TrySchedule())

	require.True(t, s.TakeForProcessing())
	require.Equal(t, dispatchProcessing, s.Load())

	// TakeForProcessing when not Scheduled must fail.
	require.False(t, s.TakeForProcessing())

	s.reset()
	require.Equal(t, dispatchIdle, s.Load())
}

func TestDispatchStateYieldThenReschedule(t *testing.T) {
	var s dispatchState
	require.True(t, s.TrySchedule())
	require.True(t, s.TakeForProcessing())

	s.YieldToScheduled()
	require.Equal(t, dispatchScheduled, s.Load())

	require.True(t, s.TakeForProcessing())
	s.reset()
	require.Equal(t, dispatchIdle, s.Load())
}

func TestDispatchStateReclaimAfterReset(t *testing.T) {
	var s dispatchState
	require.True(t, s.TrySchedule())
	require.True(t, s.TakeForProcessing())

	// Worker observed empty mailbox: drops to Idle, then races a concurrent
	// Enqueue that re-Schedules. Worker reclaims via TakeForProcessing.
	s.reset()
	require.True(t, s.TrySchedule())
	require.True(t, s.TakeForProcessing())
	require.Equal(t, dispatchProcessing, s.Load())
}

func TestDispatchStateReclaimLosesRace(t *testing.T) {
	var s dispatchState
	require.True(t, s.TrySchedule())
	require.True(t, s.TakeForProcessing())

	// After the worker's reset, another enqueuer wins the Idle -> Scheduled
	// transition; the worker's own TrySchedule must therefore fail.
	s.reset()
	require.True(t, s.TrySchedule())
	require.False(t, s.TrySchedule())
}

func TestDispatchStateConcurrentTrySchedule(t *testing.T) {
	var s dispatchState
	const N = 256
	var winners atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range N {
		wg.Go(func() {
			<-start
			if s.TrySchedule() {
				winners.Add(1)
			}
		})
	}
	close(start)
	wg.Wait()
	assert.EqualValues(t, 1, winners.Load())
	assert.Equal(t, dispatchScheduled, s.Load())
}
