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
// implementations after a yield on throughput exhaustion.
//
// An idle sibling is woken to steal it if none is already looking for work:
// this worker may spend its next turn on an actor that blocks, and a parked
// sibling never looks at another worker's queue on its own.
func (w *worker) reschedule(s schedulable) {
	w.scheduleLocal(s)
}

// scheduleLocal queues s, an actor that a turn on this worker has made ready,
// on this worker's local queue, and wakes an idle sibling to steal it if none
// is already looking for work (see readyQueue.wakeSearcher). It is meant for
// code running on this worker's goroutine; the local queue's mutex keeps any
// other caller safe, only less local.
func (w *worker) scheduleLocal(s schedulable) {
	rq := w.dispatcher.readyQueue
	rq.pushLocal(w.id, s)
	rq.wakeSearcher()
}
