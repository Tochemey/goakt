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
	"time"
)

// dispatcherThroughput is the maximum number of messages a worker
// processes from one actor per turn before rotating. The bound caps the
// blocking window an actor can impose on its peers and amortises the
// per-turn park/unpark cost over a batch.
const dispatcherThroughput = 32

// watchdogInterval is how often the dispatcher's watchdog looks at the
// workers' run-next slots while any is occupied. An actor left in a slot by a
// turn that blocks in user code, on a channel or a lock the framework does
// not see, waits at most this long plus runnextStealAge before a searcher is
// woken to take it. The Go scheduler's sysmon gives its runnext slots the
// same guarantee, starting at 20 µs.
const watchdogInterval = 100 * time.Microsecond

// dispatcher coordinates a fixed pool of worker goroutines that drain
// scheduled actors from a shared ready queue. Worker count is bounded
// by GOMAXPROCS independent of the actor population, replacing the
// per-actor drainer-goroutine model with cooperative scheduling.
type dispatcher struct {
	// name is the name the dispatcher was declared under with
	// WithDispatcherPool, empty for the default dispatcher. It travels with
	// the wire record of every actor and grain assigned to the dispatcher.
	name string
	// readyQueue holds actors with pending work. Workers consume from it.
	readyQueue *readyQueue
	// workers is the fixed pool of goroutines created by start.
	workers []*worker
	// throughput is the per-turn message budget passed to runTurn.
	throughput int
	// started becomes true after the first successful start call.
	started atomic.Bool
	// stopping becomes true after the first signalStop call, gating
	// the ready-queue close so repeated signalStop invocations are
	// no-ops.
	stopping atomic.Bool
	// watchdog fires runWatchdog watchdogInterval after it is armed. It is
	// armed when a run-next slot is filled while it is not, and re-arms itself
	// while any slot is occupied, so an idle dispatcher has no timer running.
	// readyQueue.runnextWatched is true from its arming until runWatchdog
	// finds every slot empty.
	watchdog *time.Timer
	// supervisor is the shared, system-wide consumer that handles actor
	// failures, replacing the per-actor supervision goroutine.
	supervisor *supervision
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
		supervisor: newSupervision(),
	}

	for i := range d.workers {
		d.workers[i] = &worker{id: i, dispatcher: d}
	}

	d.watchdog = time.AfterFunc(watchdogInterval, d.runWatchdog)
	d.watchdog.Stop()
	return d
}

// start spawns the worker goroutines. Idempotent: subsequent calls are
// no-ops. Must be called before the first schedule.
func (d *dispatcher) start() {
	if !d.started.CompareAndSwap(false, true) {
		return
	}

	for _, w := range d.workers {
		go w.run()
	}
	d.supervisor.start()
}

// signalStop closes the ready queue and wakes all parked workers, then
// returns without waiting for the workers to exit. Safe to call from
// within a worker turn; each worker finishes its current turn and exits
// on its next take. Idempotent.
func (d *dispatcher) signalStop() {
	if d == nil {
		return
	}

	if !d.stopping.CompareAndSwap(false, true) {
		return
	}

	d.readyQueue.close()
	d.supervisor.stop()
	d.watchdog.Stop()
}

// schedule enqueues s onto the global ready queue. Safe to call from any
// goroutine. External producers (the actor enqueue path) call this; the
// worker's own rescheduling path goes through worker.reschedule.
func (d *dispatcher) schedule(s schedulable) {
	d.readyQueue.push(s)
}

// submitSupervision hands a failing actor and its signal to the shared
// supervision consumer, and reports whether the consumer took it.
func (d *dispatcher) submitSupervision(pid *PID, signal *supervisionSignal) bool {
	return d.supervisor.Submit(pid, signal)
}

// renewed returns d when it can still run, and a fresh, unstarted dispatcher
// of the same size and budget when d was stopped: a closed ready queue does
// not reopen, so a Start that follows a Stop needs a new pool.
// armWatchdog starts the watchdog unless it is already running. A worker
// calls it when it fills its run-next slot; while the watchdog runs, that is
// one atomic load.
func (d *dispatcher) armWatchdog() {
	armed := &d.readyQueue.runnextWatched
	if armed.Load() || !armed.CompareAndSwap(false, true) {
		return
	}

	d.watchdog.Reset(watchdogInterval)
}

// runWatchdog is the watchdog's tick. It wakes a searcher when some run-next
// slot holds an actor a thief may take, re-arms itself while any slot is
// occupied, and otherwise disarms. The disarming is checked once more after
// the flag is cleared, so a slot filled between the check and the clearing,
// whose worker found the watchdog armed and did not arm it, is not left
// unwatched.
func (d *dispatcher) runWatchdog() {
	rq := d.readyQueue
	if d.stopping.Load() {
		rq.runnextWatched.Store(false)
		return
	}

	if rq.hasStaleRunnext(monotonicNanos()) {
		rq.wakeSearcher()
	}

	if rq.hasRunnext() {
		d.watchdog.Reset(watchdogInterval)
		return
	}

	rq.runnextWatched.Store(false)
	if rq.hasRunnext() && rq.runnextWatched.CompareAndSwap(false, true) {
		d.watchdog.Reset(watchdogInterval)
	}
}

func (d *dispatcher) renewed() *dispatcher {
	if d.stopping.Load() {
		fresh := newDispatcher(len(d.workers), d.throughput)
		fresh.name = d.name
		return fresh
	}

	return d
}
