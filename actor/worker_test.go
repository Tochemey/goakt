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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestWorkerLocalWorkIsStolenWhileTheOwnerBlocks verifies that an actor a turn
// leaves on its worker's local queue, by scheduling it or by yielding it, runs
// on a sibling while that worker stays blocked in a handler. A parked sibling
// looks at other rings only when woken, so without the wake the actor would
// wait for the blocked handler.
func TestWorkerLocalWorkIsStolenWhileTheOwnerBlocks(t *testing.T) {
	for name, queue := range map[string]func(w *worker, s schedulable){
		"scheduleLocal": (*worker).scheduleLocal,
		"reschedule":    (*worker).reschedule,
	} {
		t.Run(name, func(t *testing.T) {
			d := newDispatcher(2, dispatcherThroughput)
			d.start()

			release := make(chan struct{})
			ran := make(chan struct{})
			waiting := &MockReschedulingSchedulable{onRun: func(schedulable, *worker) { close(ran) }}
			waiting.self = waiting

			blocker := &MockReschedulingSchedulable{onRun: func(_ schedulable, w *worker) {
				queue(w, waiting)
				<-release
			}}
			blocker.self = blocker

			t.Cleanup(func() {
				close(release)
				d.signalStop()
			})

			d.schedule(blocker)
			select {
			case <-ran:
			case <-time.After(5 * time.Second):
				t.Fatal("the queued actor waited for the blocked worker")
			}
		})
	}
}

// TestWorkerScheduleLocalFillsTheRunnextSlotThenTheRing verifies that the
// first actor a turn readies goes to the worker's run-next slot, arming the
// watchdog, that the next one goes on the worker's ring, and that neither
// touches another worker's ring or the shared queue.
func TestWorkerScheduleLocalFillsTheRunnextSlotThenTheRing(t *testing.T) {
	d := newDispatcher(2, dispatcherThroughput)
	t.Cleanup(d.signalStop)
	first, second := NewMockSchedulable(1), NewMockSchedulable(2)

	d.workers[1].scheduleLocal(first)
	require.True(t, d.readyQueue.locals[1].runnextSet.Load())
	require.Zero(t, d.readyQueue.locals[1].length())
	require.True(t, d.readyQueue.runnextWatched.Load(), "the watchdog is armed while a slot is occupied")

	d.workers[1].scheduleLocal(second)
	require.Equal(t, 1, d.readyQueue.locals[1].length())
	require.Same(t, first, d.readyQueue.locals[1].popRunnext().(*MockSchedulable))
	require.Zero(t, d.readyQueue.locals[0].length())
	require.Zero(t, d.readyQueue.globalLen())
}

// TestWorkerRescheduleGoesToTheRing verifies that an actor that spent its
// budget goes to the back of the worker's ring, not to the run-next slot.
func TestWorkerRescheduleGoesToTheRing(t *testing.T) {
	d := newDispatcher(2, dispatcherThroughput)
	t.Cleanup(d.signalStop)
	item := NewMockSchedulable(1)

	d.workers[0].reschedule(item)
	require.False(t, d.readyQueue.locals[0].runnextSet.Load())
	require.Equal(t, 1, d.readyQueue.locals[0].length())
}

// TestWorkerHandoffRunnextMovesTheActorToTheRing verifies that a turn about
// to block hands the actor in its run-next slot to the ring, where a searcher
// can steal it, and that an empty slot hands off nothing.
func TestWorkerHandoffRunnextMovesTheActorToTheRing(t *testing.T) {
	d := newDispatcher(2, dispatcherThroughput)
	t.Cleanup(d.signalStop)
	item := NewMockSchedulable(1)
	w := d.workers[0]

	w.handoffRunnext()
	require.Zero(t, d.readyQueue.locals[0].length())

	w.scheduleLocal(item)
	w.handoffRunnext()
	require.False(t, d.readyQueue.locals[0].runnextSet.Load())
	require.Equal(t, 1, d.readyQueue.locals[0].length())
}
