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
	"unsafe"
)

// localQueueCap bounds each worker's local ring buffer. Capacity is
// fixed: a full local queue spills into the global queue rather than
// growing, which keeps per-worker memory deterministic.
const localQueueCap = 256

// globalQueueInitialCap is the starting capacity of the global ring
// buffer. The buffer doubles on overflow and never shrinks, mirroring
// the amortised growth strategy used by Go's channel implementation.
const globalQueueInitialCap = 64

// schedulable is the unit scheduled by the dispatcher. The interface is
// intentionally minimal: scheduling state, throughput budgeting, and
// mailbox handling are all delegated to the implementation's runTurn.
// The worker passes itself so the implementation can re-push onto the
// owning worker's local queue when the throughput budget is exhausted.
type schedulable interface {
	// runTurn processes up to dispatcherThroughput messages from the
	// schedulable's backing source, then yields cooperatively.
	runTurn(w *worker)
}

// localQueue is a bounded ring buffer owned by a single worker. The
// owner pushes and pops from one end (FIFO); sibling workers may steal
// from the head. All accesses go through mu; the owner contention path
// is uncontended at rest because no one else reads or writes when the
// owner is mid-turn.
type localQueue struct {
	// mu guards every field below. Held briefly by the owner on push/pop
	// and by thieves during stealHalf.
	mu sync.Mutex
	// buf is the fixed-capacity ring storage.
	buf [localQueueCap]schedulable
	// head is the index of the next item to read.
	head int
	// tail is the index of the next slot to write.
	tail int
	// size is the live item count, used to detect full/empty without
	// disambiguating head==tail wrap-around.
	size int
	// sizeAtomic mirrors size and is updated under mu. Stealers load it
	// without taking mu to skip empty victims on the hot path; the
	// worst-case false positive is a wasted lock, not a lost item.
	sizeAtomic atomic.Int32
}

// globalQueue is an unbounded amortised-FIFO ring buffer guarded by an
// external mutex (readyQueue.parkMu). It grows by doubling on overflow
// and never shrinks. Replaces a naive slice-reslicing implementation
// whose backing array would grow unboundedly under steady churn.
type globalQueue struct {
	// buf is the ring storage, sized to a power of two for cheap modulo.
	buf []schedulable
	// head is the index of the next item to pop.
	head int
	// tail is the index of the next slot to push.
	tail int
	// size is the live item count.
	size int
}

// readyQueue is the dispatcher's aggregate ready queue: one local ring
// per worker plus a shared global ring guarded by parkMu. The park
// condition variable shares parkMu so push operations can wake exactly
// one sleeping worker without dropping signals.
type readyQueue struct {
	// locals is the per-worker local ring, indexed by worker id.
	locals []*localQueue
	// globalCount mirrors global.size and is updated under parkMu, but
	// reads are lock-free so producers and would-be parkers can sample
	// queue depth without taking the mutex on the hot path.
	globalCount atomic.Int32
	// parkMu guards global, parked, closed and is the cond's locker.
	parkMu sync.Mutex
	// cond is signalled on push and broadcast on close.
	cond *sync.Cond
	// global is the shared overflow queue.
	global globalQueue
	// parked counts workers currently waiting on cond. Producers skip
	// the signal call when this is zero to avoid an unnecessary syscall.
	parked int
	// closed becomes true after close has been called; new parkers exit
	// instead of waiting.
	closed bool
}

// newReadyQueue constructs a readyQueue with workerCount per-worker
// local rings. workerCount must be at least 1.
func newReadyQueue(workerCount int) *readyQueue {
	rq := &readyQueue{
		locals: make([]*localQueue, workerCount),
		global: globalQueue{buf: make([]schedulable, globalQueueInitialCap)},
	}
	for i := range rq.locals {
		rq.locals[i] = &localQueue{}
	}
	rq.cond = sync.NewCond(&rq.parkMu)
	return rq
}

// push appends s to the global queue and wakes one parked worker if
// any. Used by external producers (the actor enqueue path) that have
// no worker affinity.
func (rq *readyQueue) push(s schedulable) {
	rq.parkMu.Lock()
	rq.global.push(s)
	rq.globalCount.Store(int32(rq.global.size))
	if rq.parked > 0 {
		rq.cond.Signal()
	}
	rq.parkMu.Unlock()
}

// pushLocal appends s to worker workerID's local ring, spilling to the
// global queue when the local ring is full. The owner is the only
// consumer of its own local ring, so a successful local push needs no
// cond signal.
func (rq *readyQueue) pushLocal(workerID int, s schedulable) {
	if rq.locals[workerID].pushBack(s) {
		return
	}
	rq.push(s)
}

// take returns the next schedulable for workerID, blocking until one is
// available or the queue is closed. The second return value is false
// when the queue has been closed and no items remain for this worker.
//
// Take order: own local ring -> global ring -> steal from siblings ->
// park. This order maximises cache locality for the owner while still
// guaranteeing fairness via the global queue and progress via stealing.
func (rq *readyQueue) take(workerID int) (schedulable, bool) {
	for {
		if s := rq.locals[workerID].popFront(); s != nil {
			return s, true
		}

		if s := rq.popGlobal(); s != nil {
			return s, true
		}

		if s := rq.trySteal(workerID); s != nil {
			return s, true
		}

		s, ok := rq.parkAndTake()
		if !ok {
			return nil, false
		}

		if s != nil {
			return s, true
		}
	}
}

// popGlobal returns the head of the global queue, or nil if the queue
// is empty. A lock-free globalCount check avoids parkMu when the queue
// is already observed empty — the dominant case on the worker wake-up
// loop once an actor's mailbox has drained.
func (rq *readyQueue) popGlobal() schedulable {
	if rq.globalCount.Load() == 0 {
		return nil
	}

	rq.parkMu.Lock()
	s := rq.global.pop()
	if s != nil {
		rq.globalCount.Store(int32(rq.global.size))
	}

	rq.parkMu.Unlock()
	return s
}

// trySteal walks sibling worker queues in a rotated order and steals
// half of the first non-empty queue. Returns nil if every sibling is
// empty. Single-worker pools have no siblings to steal from and exit
// immediately.
//
// Each victim is probed via a lock-free atomic size load first so empty
// siblings cost a cheap read instead of a mutex acquire. The sibling's
// mu is only taken when the atomic load observes pending work, eliminating
// the N-way mutex fan-out that otherwise dominated the take loop when
// most queues are idle.
func (rq *readyQueue) trySteal(workerID int) schedulable {
	n := len(rq.locals)
	if n == 1 {
		return nil
	}
	own := rq.locals[workerID]
	for i := 1; i < n; i++ {
		victim := rq.locals[(workerID+i)%n]
		if victim.sizeAtomic.Load() == 0 {
			continue
		}

		if s := victim.stealHalf(own); s != nil {
			return s
		}
	}
	return nil
}

// parkAndTake waits on the condition variable until woken by push or
// close, and returns the head of the global queue in the same critical
// section that observed it. This fuses park + popGlobal so a signalled
// worker does not unlock parkMu only to re-acquire it one call later.
//
// Returns (nil, false) when the queue is closed and the worker should
// exit. Returns (s, true) with a non-nil item on a successful wake-up,
// or (nil, true) when the queue was non-empty on entry (caller retries
// the take loop).
func (rq *readyQueue) parkAndTake() (schedulable, bool) {
	rq.parkMu.Lock()
	for {
		if rq.closed {
			rq.parkMu.Unlock()
			return nil, false
		}

		if rq.global.size > 0 {
			s := rq.global.pop()
			rq.globalCount.Store(int32(rq.global.size))
			rq.parkMu.Unlock()
			return s, true
		}

		rq.parked++
		rq.cond.Wait()
		rq.parked--
	}
}

// close marks the queue closed and broadcasts to wake every parked
// worker so they can observe the closed state and exit.
func (rq *readyQueue) close() {
	rq.parkMu.Lock()
	rq.closed = true
	rq.cond.Broadcast()
	rq.parkMu.Unlock()
}

// globalLen returns the current global queue depth. Test-only accessor.
func (rq *readyQueue) globalLen() int {
	rq.parkMu.Lock()
	defer rq.parkMu.Unlock()
	return rq.global.size
}

// parkedCount returns the number of workers currently parked. Test-only.
func (rq *readyQueue) parkedCount() int {
	rq.parkMu.Lock()
	defer rq.parkMu.Unlock()
	return rq.parked
}

// pushBack enqueues s at the tail. Returns false if the queue is full.
func (q *localQueue) pushBack(s schedulable) bool {
	q.mu.Lock()
	if q.size == localQueueCap {
		q.mu.Unlock()
		return false
	}
	q.buf[q.tail] = s
	q.tail = (q.tail + 1) % localQueueCap
	q.size++
	q.sizeAtomic.Store(int32(q.size))
	q.mu.Unlock()
	return true
}

// popFront dequeues from the head. Returns nil when the queue is empty.
// The lock-free sizeAtomic probe lets callers skip the mutex entirely
// on an empty local ring, which is the common case on the worker's
// wake-up loop.
func (q *localQueue) popFront() schedulable {
	if q.sizeAtomic.Load() == 0 {
		return nil
	}
	q.mu.Lock()
	if q.size == 0 {
		q.mu.Unlock()
		return nil
	}
	s := q.buf[q.head]
	q.buf[q.head] = nil
	q.head = (q.head + 1) % localQueueCap
	q.size--
	q.sizeAtomic.Store(int32(q.size))
	q.mu.Unlock()
	return s
}

// stealHalf moves up to half of q's contents into dst, preserving FIFO
// order, and returns the first stolen item directly to the caller. The
// remainder is appended to dst. Returns nil when q is empty.
//
// Both queues are locked under a deterministic order derived from their
// pointer addresses to avoid deadlock when concurrent steals cross paths.
// The transfer happens under both locks with no intermediate allocation.
//
// q and dst must be distinct queues; trySteal enforces this and the
// pointer-ordered locking would deadlock if the same queue were passed
// twice.
func (q *localQueue) stealHalf(dst *localQueue) schedulable {
	if q == dst {
		return nil
	}
	first, second := lockOrder(q, dst)
	first.mu.Lock()
	second.mu.Lock()
	defer second.mu.Unlock()
	defer first.mu.Unlock()

	if q.size == 0 {
		return nil
	}
	stolen := (q.size + 1) / 2
	head := q.buf[q.head]
	q.buf[q.head] = nil
	q.head = (q.head + 1) % localQueueCap
	q.size--
	for i := 1; i < stolen; i++ {
		if dst.size == localQueueCap {
			break
		}
		dst.buf[dst.tail] = q.buf[q.head]
		q.buf[q.head] = nil
		dst.tail = (dst.tail + 1) % localQueueCap
		dst.size++
		q.head = (q.head + 1) % localQueueCap
		q.size--
	}
	q.sizeAtomic.Store(int32(q.size))
	dst.sizeAtomic.Store(int32(dst.size))
	return head
}

// length returns the current item count. Test-only accessor.
func (q *localQueue) length() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.size
}

// push appends s to the tail of the global ring, doubling capacity if
// the buffer is full.
func (g *globalQueue) push(s schedulable) {
	if g.size == len(g.buf) {
		g.grow()
	}
	g.buf[g.tail] = s
	g.tail = (g.tail + 1) % len(g.buf)
	g.size++
}

// pop removes and returns the head of the global ring, or nil when the
// ring is empty.
func (g *globalQueue) pop() schedulable {
	if g.size == 0 {
		return nil
	}
	s := g.buf[g.head]
	g.buf[g.head] = nil
	g.head = (g.head + 1) % len(g.buf)
	g.size--
	return s
}

// grow doubles the buffer capacity in-place, preserving FIFO order. The
// existing items are copied into a fresh slice with head reset to 0,
// making subsequent pushes and pops cheap modulo arithmetic.
func (g *globalQueue) grow() {
	newCap := len(g.buf) * 2
	if newCap == 0 {
		newCap = globalQueueInitialCap
	}
	next := make([]schedulable, newCap)
	for i := range g.size {
		next[i] = g.buf[(g.head+i)%len(g.buf)]
	}
	g.buf = next
	g.head = 0
	g.tail = g.size
}

// lockOrder returns a and b in a stable address-derived order so callers
// can lock both without risking deadlock.
func lockOrder(a, b *localQueue) (*localQueue, *localQueue) {
	if uintptr(unsafe.Pointer(a)) < uintptr(unsafe.Pointer(b)) {
		return a, b
	}
	return b, a
}
