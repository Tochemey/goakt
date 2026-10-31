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

import "time"

// worker is a single goroutine in the dispatcher pool. It pulls
// schedulable items from the ready queue and delegates processing to the
// item's runTurn method. The worker is intentionally agnostic of the
// actor state machine, mailbox, and throughput policy; those are the
// schedulable's concern.
type worker struct {
	// id is the worker's index in the dispatcher pool, used for steal
	// rotation and as the local-queue selector.
	id int
	// dispatcher owns this worker; back-reference gives access to the
	// shared ready queue and throughput configuration.
	dispatcher *dispatcher
}

// run is the worker's main loop. It exits cleanly when the dispatcher's
// ready queue is closed and no further work is available.
func (w *worker) run() {
	for {
		s, ok := w.dispatcher.readyQueue.take(w.id)
		if !ok {
			return
		}
		s.runTurn(w)
	}
}

// reschedule re-enqueues s onto this worker's local queue, spilling to
// the global queue when the local ring is full. Called by schedulable
// implementations after a yield on throughput exhaustion. The actor goes to
// the back of the ring, not to the run-next slot, as a goroutine that used up
// its time slice goes to the tail of its P's run queue.
//
// An idle sibling is woken to steal it if none is already looking for work:
// this worker may spend its next turn on an actor that blocks, and a parked
// sibling never looks at another worker's queue on its own.
func (w *worker) reschedule(s schedulable) {
	w.queueLocal(s)
}

// scheduleLocal queues s, an actor that a turn on this worker has made ready.
// The first such actor of a turn goes to this worker's run-next slot, which
// the worker runs as soon as the turn ends, on the same core, without waking
// anyone: with one message in flight between two actors, that is every
// hand-off, and a wake per hand-off was most of its cost. A later one goes on
// the ring and wakes an idle sibling to steal it, since there is now work for
// two workers. The dispatcher's watchdog and the hand-off before a blocking
// call (see handoffRunnext) see to it that a turn which does not end soon
// does not keep the slot's actor waiting. It is meant for code running on
// this worker's goroutine.
func (w *worker) scheduleLocal(s schedulable) {
	if w.dispatcher.readyQueue.locals[w.id].pushRunnext(s) {
		w.dispatcher.armWatchdog()
		return
	}

	w.queueLocal(s)
}

// handoffRunnext moves the actor in this worker's run-next slot, if any, to
// the ring and wakes a searcher to take it. A turn calls it before it blocks
// waiting for another actor, since the actor it readied may be the one it is
// waiting for, and a parked sibling never looks at the slot on its own.
func (w *worker) handoffRunnext() {
	if s := w.dispatcher.readyQueue.locals[w.id].popRunnext(); s != nil {
		w.queueLocal(s)
	}
}

// beginTurn records that a turn starts on this worker at now; the age of its
// run-next slot is measured from it (see runnextStealAge).
func (w *worker) beginTurn(now time.Time) {
	w.dispatcher.readyQueue.locals[w.id].turnStart.Store(int64(now.Sub(monotonicEpoch)))
}

// queueLocal puts s on this worker's ring, spilling to the global queue when
// the ring is full, and wakes an idle sibling to steal it if none is already
// looking for work (see readyQueue.wakeSearcher).
func (w *worker) queueLocal(s schedulable) {
	rq := w.dispatcher.readyQueue
	rq.pushLocal(w.id, s)
	rq.wakeSearcher()
}
