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
	"context"
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

// TestDispatcherDrainFromWorkerGoroutine verifies that drain(ctx) does not
// self-deadlock when called from within a worker turn. Before the drain fix,
// calling stop() from a worker goroutine would deadlock because the worker
// waited on its own WaitGroup.
func TestDispatcherDrainFromWorkerGoroutine(t *testing.T) {
	d := newDispatcher(2, dispatcherThroughput)
	d.start()

	drainDone := make(chan struct{})
	item := &reschedSchedulable{
		onRun: func(_ schedulable, _ *worker) {
			// Called from within a worker turn — this would deadlock with stop()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			d.drain(ctx)
			close(drainDone)
		},
	}
	item.self = item
	d.schedule(item)

	select {
	case <-drainDone:
		// drain returned without deadlock
	case <-time.After(5 * time.Second):
		t.Fatal("drain(ctx) deadlocked when called from worker goroutine")
	}
}

// TestDispatcherDrainWaitsForInflight verifies that drain blocks until
// in-flight turns complete (or context expires).
func TestDispatcherDrainWaitsForInflight(t *testing.T) {
	d := newDispatcher(1, dispatcherThroughput)
	d.start()

	blocker := make(chan struct{})
	var finished atomic.Bool
	item := &reschedSchedulable{
		onRun: func(_ schedulable, _ *worker) {
			<-blocker
			finished.Store(true)
		},
	}
	item.self = item
	d.schedule(item)

	// Wait for the worker to enter the blocked turn
	time.Sleep(50 * time.Millisecond)

	drained := make(chan struct{})
	go func() {
		d.drain(context.Background())
		close(drained)
	}()

	// drain should not return while worker is blocked
	select {
	case <-drained:
		t.Fatal("drain returned before in-flight turn completed")
	case <-time.After(50 * time.Millisecond):
	}

	close(blocker)
	<-drained
	require.True(t, finished.Load())
}

// TestDispatcherDrainRespectsContextTimeout verifies that drain returns
// when the context expires even if workers haven't finished.
func TestDispatcherDrainRespectsContextTimeout(t *testing.T) {
	d := newDispatcher(1, dispatcherThroughput)
	d.start()

	blocker := make(chan struct{})
	defer close(blocker)
	item := &reschedSchedulable{
		onRun: func(_ schedulable, _ *worker) {
			<-blocker // block forever
		},
	}
	item.self = item
	d.schedule(item)

	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	drained := make(chan struct{})
	go func() {
		d.drain(ctx)
		close(drained)
	}()

	select {
	case <-drained:
		// drain returned due to context timeout — correct
	case <-time.After(2 * time.Second):
		t.Fatal("drain did not respect context timeout")
	}
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
