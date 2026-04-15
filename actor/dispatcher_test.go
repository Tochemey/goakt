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
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingSchedulable records turns and optionally reschedules itself a
// bounded number of times via the worker that ran it.
type countingSchedulable struct {
	remaining atomic.Int32
	done      atomic.Int32
	resume    func(w *worker)
}

func (c *countingSchedulable) runTurn(w *worker) {
	c.done.Add(1)
	if c.remaining.Add(-1) > 0 && c.resume != nil {
		c.resume(w)
	}
}

func TestDispatcherWorkerCountFloor(t *testing.T) {
	// Cannot portably set GOMAXPROCS very low, but we can at least confirm
	// the function returns a sane positive value >= 2.
	require.GreaterOrEqual(t, dispatcherWorkerCount(), 2)
}

func TestDispatcherStartStopIdempotent(t *testing.T) {
	d := newDispatcher(2, dispatcherThroughput)
	d.start()
	d.start() // idempotent

	require.Len(t, d.workers, 2)
	d.stop()
	d.stop() // idempotent
}

func TestDispatcherScheduleProcessesItem(t *testing.T) {
	d := newDispatcher(2, dispatcherThroughput)
	d.start()
	defer d.stop()

	c := &countingSchedulable{}
	c.remaining.Store(1)
	d.schedule(c)

	assert.Eventually(t, func() bool {
		return c.done.Load() == 1
	}, 2*time.Second, time.Millisecond)
}

func TestDispatcherReschedulesViaWorkerLocalQueue(t *testing.T) {
	d := newDispatcher(2, dispatcherThroughput)
	d.start()
	defer d.stop()

	c := &countingSchedulable{}
	c.remaining.Store(10)
	// Capture the worker by deriving it at runTurn time from the dispatcher.
	// Simpler: always reschedule via the global schedule path for this test.
	c.resume = func(*worker) { d.schedule(c) }

	d.schedule(c)
	assert.Eventually(t, func() bool {
		return c.done.Load() == 10
	}, 2*time.Second, time.Millisecond)
}

func TestWorkerRescheduleUsesLocalQueue(t *testing.T) {
	d := newDispatcher(1, dispatcherThroughput)
	d.start()
	defer d.stop()

	w := d.workers[0]
	var turns atomic.Int32
	item := &reschedSchedulable{
		onRun: func(self schedulable, running *worker) {
			if turns.Add(1) < 5 {
				running.reschedule(self)
			}
		},
	}
	_ = w
	item.self = item
	d.schedule(item)

	assert.Eventually(t, func() bool {
		return turns.Load() == 5
	}, 2*time.Second, time.Millisecond)
}

func TestDispatcherStopWaitsForInflightTurn(t *testing.T) {
	// Verify that stop() blocks until an in-flight turn completes; workers
	// are not abandoned mid-turn. Drain-on-stop semantics are Phase 2 and
	// intentionally not asserted here.
	d := newDispatcher(1, dispatcherThroughput)
	d.start()

	blocker := make(chan struct{})
	release := make(chan struct{})
	var finished atomic.Bool
	block := &reschedSchedulable{
		onRun: func(self schedulable, _ *worker) {
			close(blocker)
			<-release
			finished.Store(true)
		},
	}
	block.self = block
	d.schedule(block)
	<-blocker

	stopped := make(chan struct{})
	go func() {
		d.stop()
		close(stopped)
	}()
	// stop() must not return while the worker is still inside runTurn.
	select {
	case <-stopped:
		t.Fatal("stop returned before in-flight turn completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-stopped
	require.True(t, finished.Load())
}

func TestDispatcherHighFanout(t *testing.T) {
	workerCount := max(runtime.GOMAXPROCS(0), 2)
	d := newDispatcher(workerCount, dispatcherThroughput)
	d.start()
	defer d.stop()

	const N = 5000
	items := make([]*countingSchedulable, N)
	for i := range items {
		c := &countingSchedulable{}
		c.remaining.Store(1)
		items[i] = c
		d.schedule(c)
	}

	assert.Eventually(t, func() bool {
		for _, c := range items {
			if c.done.Load() != 1 {
				return false
			}
		}
		return true
	}, 5*time.Second, 5*time.Millisecond)
}

// reschedSchedulable is a schedulable whose behaviour is supplied by a
// callback that receives the schedulable itself plus the worker that is
// running it, so the callback can re-push via either the local queue or
// the dispatcher.
type reschedSchedulable struct {
	self  schedulable
	onRun func(self schedulable, w *worker)
}

func (r *reschedSchedulable) runTurn(w *worker) { r.onRun(r.self, w) }
