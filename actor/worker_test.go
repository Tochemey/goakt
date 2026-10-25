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

// TestWorkerScheduleLocalQueuesOnItsOwnRing verifies that scheduleLocal puts
// the actor on the calling worker's ring and not on the shared queue.
func TestWorkerScheduleLocalQueuesOnItsOwnRing(t *testing.T) {
	d := newDispatcher(2, dispatcherThroughput)
	item := NewMockSchedulable(1)

	d.workers[1].scheduleLocal(item)
	require.Equal(t, 1, d.readyQueue.locals[1].length())
	require.Zero(t, d.readyQueue.locals[0].length())
	require.Zero(t, d.readyQueue.globalLen())
}
