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
	"sync"
	"sync/atomic"
)

// dispatcherThroughput is the maximum number of messages a worker
// processes from one actor per turn before rotating. The bound caps the
// blocking window an actor can impose on its peers and amortises the
// per-turn park/unpark cost over a batch.
const dispatcherThroughput = 32

// dispatcher coordinates a fixed pool of worker goroutines that drain
// scheduled actors from a shared ready queue. Worker count is bounded
// by GOMAXPROCS independent of the actor population, replacing the
// per-actor drainer-goroutine model with cooperative scheduling.
type dispatcher struct {
	// readyQueue holds actors with pending work. Workers consume from it.
	readyQueue *readyQueue
	// workers is the fixed pool of goroutines created by start.
	workers []*worker
	// throughput is the per-turn message budget passed to runTurn.
	throughput int
	// started becomes true after the first successful start call.
	started atomic.Bool
	// stopping becomes true after the first stop or signalStop call.
	stopping atomic.Bool
	// wg tracks live worker goroutines for stop's blocking wait.
	wg sync.WaitGroup
}

// dispatcherWorkerCount is the default worker pool size. We size the
// pool to GOMAXPROCS so there is one worker per OS thread; actors are
// cooperatively multiplexed onto that pool via the throughput budget.
// Floored at 2 so work-stealing always has at least one sibling.
func dispatcherWorkerCount() int {
	return max(runtime.GOMAXPROCS(0), 2)
}

// newDispatcher constructs an unstarted dispatcher with workerCount
// worker goroutines and the supplied per-turn throughput budget. Both
// arguments must be positive.
func newDispatcher(workerCount, throughput int) *dispatcher {
	d := &dispatcher{
		readyQueue: newReadyQueue(workerCount),
		workers:    make([]*worker, workerCount),
		throughput: throughput,
	}
	for i := range d.workers {
		d.workers[i] = &worker{id: i, dispatcher: d}
	}
	return d
}

// start spawns the worker goroutines. Idempotent: subsequent calls are
// no-ops. Must be called before the first schedule.
func (d *dispatcher) start() {
	if !d.started.CompareAndSwap(false, true) {
		return
	}
	for _, w := range d.workers {
		d.wg.Add(1)
		go w.run()
	}
}

// stop signals all workers to drain their current turn and exit, then
// blocks until every worker has returned. Idempotent.
//
// Must NOT be called from within a worker turn; the caller's goroutine
// would wait on its own WaitGroup and deadlock. Callers that may run on
// a worker (for example actorSystem.shutdown triggered from an actor
// receive) must use signalStop instead.
func (d *dispatcher) stop() {
	if !d.stopping.CompareAndSwap(false, true) {
		d.wg.Wait()
		return
	}
	d.readyQueue.close()
	d.wg.Wait()
}

// signalStop closes the ready queue and wakes all parked workers, then
// returns without waiting for the workers to exit. Safe to call from
// within a worker turn; each worker finishes its current turn and exits
// on its next take. Idempotent.
func (d *dispatcher) signalStop() {
	if !d.stopping.CompareAndSwap(false, true) {
		return
	}
	d.readyQueue.close()
}

// schedule enqueues s onto the global ready queue. Safe to call from any
// goroutine. External producers (the actor enqueue path) call this; the
// worker's own rescheduling path goes through worker.reschedule.
func (d *dispatcher) schedule(s schedulable) {
	d.readyQueue.push(s)
}
