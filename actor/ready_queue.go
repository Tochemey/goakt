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
	"time"
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

// runnextStealAge is how long the actor in a worker's run-next slot is left
// to that worker before another worker may steal it: the time the owner's
// turn has been running, measured from the turn's start. A turn that readies
// an actor normally ends within a microsecond or two and runs it next, on the
// same core, cache-hot; a turn still running after this long is in a long or
// blocked handler, and the readied actor is better off elsewhere. The Go
// scheduler applies the same rule to its runnext slot, with a 3 µs wait.
const runnextStealAge = 10 * time.Microsecond

// runnextChainLimit is how many actors in a row a worker runs from its
// run-next slot while its ring holds actors waiting. Each hand-off refills
// the slot, so two actors exchanging messages could otherwise keep the slot
// full and the ring waiting for as long as they talk; at this count the
// slot's actor goes to the back of the ring and the ring is served. The Go
// scheduler bounds the same chain with the time slice a runnext goroutine
// inherits; this uses the same count as the global queue check, so a ring
// actor waits at most that many hand-offs, a few microseconds.
const runnextChainLimit = 61

// globalQueueCheckInterval is how often, in takes, a worker looks at the
// global queue before its own local ring. A worker otherwise reads its local
// ring first, and an actor that spends its throughput budget goes back onto
// that ring, so a worker serving an actor whose mailbox never drains would
// never reach the global queue: with every worker in that state, an actor
// scheduled meanwhile would wait until one of those mailboxes drained. Go's
// scheduler checks its global run queue every 61 ticks for the same reason;
// the odd number avoids falling into step with other periodic work.
const globalQueueCheckInterval = 61

// monotonicEpoch is the origin of monotonicNanos.
var monotonicEpoch = time.Now()

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
	// takes counts the owner's calls to take, to schedule its periodic look
	// at the global queue (see globalQueueCheckInterval). Only the owning
	// worker reads or writes it, so it needs neither mu nor an atomic.
	takes uint32
	// runnextTakes counts the owner's consecutive takes from the run-next
	// slot, to serve the ring at runnextChainLimit. Owner-only, like takes.
	runnextTakes uint32
	// runnext is the actor a turn on the owning worker has just made ready,
	// which the owner runs next, before its ring, as the Go scheduler runs the
	// goroutine readied by the running one from its runnext slot. It is
	// guarded by mu. The owner fills and empties it; a thief may take it only
	// once the owner's turn has run for runnextStealAge (see stealRunnext).
	runnext schedulable
	// runnextSet mirrors runnext != nil and is updated under mu, so the owner
	// and thieves skip the lock while the slot is empty, the common case.
	runnextSet atomic.Bool
	// turnStart is the monotonic time at which the owner's current turn
	// began (see worker.beginTurn). Thieves and the dispatcher's watchdog
	// read it to age the run-next slot.
	turnStart atomic.Int64
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

// idleFlag is a per-worker parked marker padded to its own cache line
// so park/claim CAS traffic on one worker does not invalidate its
// siblings' lines. flag is 1 while the worker is parked and claimable,
// 0 otherwise; transitions are CAS-guarded so at most one claimant wins
// a parked worker.
type idleFlag struct {
	flag atomic.Int32
	_    [60]byte
}

// readyQueue is the dispatcher's aggregate ready queue: one local ring
// per worker plus a shared global ring guarded by parkMu.
//
// Worker wake-up uses per-worker direct handoff instead of a shared
// condition variable. A parked worker publishes itself in its idleFlag;
// a producer claims exactly one parked worker with a CAS on that flag
// and passes the schedulable straight through the worker's handoff
// channel, bypassing the global queue and its mutex entirely. The
// previous design funneled every idle-to-busy transition through
// parkMu plus a cond signal, which serialized all producers and all
// waking workers on one mutex: under a pairwise request/reply load,
// aggregate throughput fell below the single-pair rate.
//
// Lost-wakeup safety relies on a store/load protocol on two atomics: a
// producer publishes work (globalCount) before scanning idle flags,
// and a parking worker publishes its flag before re-checking
// globalCount. Sequential consistency of the atomics guarantees at
// least one side observes the other, so work cannot be enqueued while
// a worker parks unnoticed.
type readyQueue struct {
	// locals is the per-worker local ring, indexed by worker id.
	locals []*localQueue
	// globalCount mirrors global.size and is updated under parkMu, but
	// reads are lock-free so producers and would-be parkers can sample
	// queue depth without taking the mutex on the hot path.
	globalCount atomic.Int32
	// idleCount counts workers whose idleFlag is set. Producers skip the
	// claim scan entirely when it is zero, which is the dominant case
	// under saturation.
	idleCount atomic.Int32
	// idleFlags holds one claimable parked marker per worker.
	idleFlags []idleFlag
	// handoff carries a claimed worker's next schedulable. Each channel
	// has capacity 1 and the claim CAS guarantees at most one in-flight
	// send per claim, so a producer's send never blocks. A nil send
	// tells the woken worker to re-run its take loop instead of running
	// an item directly.
	handoff []chan schedulable
	// parkMu guards global.
	parkMu sync.Mutex
	// global is the shared overflow queue.
	global globalQueue
	// closed reports that close has been called; parking workers exit
	// instead of waiting.
	closed atomic.Bool
	// runnextWatched is set while the dispatcher's watchdog is armed, which
	// is from the moment a run-next slot is filled until the watchdog finds
	// every slot empty (see dispatcher.armWatchdog). Thieves probe the slots
	// only while it is set, so a worker that finds nothing to do and parks
	// pays one load for them, not one per sibling, when no turn has readied
	// an actor.
	runnextWatched atomic.Bool
	// searching is set while a worker woken by wakeSearcher has not yet
	// found work or parked again. It allows one such wake at a time, the
	// rule the Go scheduler applies to its spinning threads: work pushed to
	// local rings then wakes one thief rather than every parked worker.
	searching atomic.Bool
}

// newReadyQueue constructs a readyQueue with workerCount per-worker
// local rings. workerCount must be at least 1.
func newReadyQueue(workerCount int) *readyQueue {
	rq := &readyQueue{
		locals:    make([]*localQueue, workerCount),
		global:    globalQueue{buf: make([]schedulable, globalQueueInitialCap)},
		idleFlags: make([]idleFlag, workerCount),
		handoff:   make([]chan schedulable, workerCount),
	}

	for i := range rq.locals {
		rq.locals[i] = &localQueue{}
		rq.handoff[i] = make(chan schedulable, 1)
	}

	return rq
}

// push routes s to a parked worker when one is available, handing the
// item straight through the worker's handoff channel without touching
// the global queue. Otherwise s is appended to the global queue, and a
// worker that began parking during the append is woken with a nil
// handoff so it re-scans the queues. Used by external producers (the
// actor enqueue path) that have no worker affinity.
func (rq *readyQueue) push(s schedulable) {
	if rq.claimIdleWorker(s) {
		return
	}

	rq.parkMu.Lock()
	rq.global.push(s)
	rq.globalCount.Store(int32(rq.global.size))
	rq.parkMu.Unlock()

	// A worker may have set its idle flag after the claim scan above and
	// before the globalCount store became visible to it. The store/load
	// protocol makes that worker visible to this second scan; waking it
	// with nil closes the lost-wakeup window.
	if rq.idleCount.Load() > 0 {
		rq.claimIdleWorker(nil)
	}
}

// claimIdleWorker claims one parked worker via its idleFlag CAS and
// sends s (which may be nil, meaning "re-scan the queues") through its
// handoff channel. Returns false when no parked worker was claimable.
//
// The scan deliberately starts at worker 0 every time: it concentrates
// wake-ups on the lowest-indexed parked workers, which keeps their
// caches warm and lets high-indexed workers sleep undisturbed. The cost
// is that worker 0's flag line is the hottest CAS target; at pool sizes
// near GOMAXPROCS that contention is negligible, and rotating the start
// index would trade cache affinity for it. Revisit if profiles on
// high-core-count hosts show this scan contending.
func (rq *readyQueue) claimIdleWorker(s schedulable) bool {
	if rq.idleCount.Load() == 0 {
		return false
	}

	for i := range rq.idleFlags {
		flag := &rq.idleFlags[i].flag
		if flag.Load() == 1 && flag.CompareAndSwap(1, 0) {
			rq.idleCount.Add(-1)
			rq.handoff[i] <- s
			return true
		}
	}

	return false
}

// pushLocal appends s to worker workerID's local ring, spilling to the
// global queue when the local ring is full. It wakes nobody: a parked
// sibling learns of the item only through wakeSearcher, which the
// worker calls after it (see worker.scheduleLocal).
func (rq *readyQueue) pushLocal(workerID int, s schedulable) {
	if rq.locals[workerID].pushBack(s) {
		return
	}
	rq.push(s)
}

// wakeSearcher wakes one parked worker to look for work, typically to
// steal an item just pushed to a local ring, unless a worker woken that
// way is still looking. A parked worker reads only its handoff channel,
// so without a wake an item left on the ring of a worker that is busy, or
// blocked in a handler, waits for that worker even while others sleep.
func (rq *readyQueue) wakeSearcher() {
	if rq.idleCount.Load() == 0 || !rq.searching.CompareAndSwap(false, true) {
		return
	}

	if !rq.claimIdleWorker(nil) {
		rq.searching.Store(false)
	}
}

// take returns the next schedulable for workerID, blocking until one is
// available or the queue is closed. The second return value is false
// when the queue has been closed and no items remain for this worker.
//
// Take order: own run-next slot -> own local ring -> global ring -> steal
// from siblings -> park. This order maximises cache locality for the owner, progress comes
// from stealing, and every globalQueueCheckInterval takes the global ring
// is read first, so a worker kept busy by its local ring still serves the
// actors waiting there.
//
// A worker that comes back from parking without an item may be the one
// wakeSearcher woke. It ends that search when it finds work or parks
// again, so the next wake can go out (see endSearch).
func (rq *readyQueue) take(workerID int) (schedulable, bool) {
	local := rq.locals[workerID]
	woken := false
	for {
		local.takes++
		if local.takes%globalQueueCheckInterval == 0 {
			if s := rq.popGlobal(); s != nil {
				return rq.endSearch(s, woken, false)
			}
		}

		if s := local.takeRunnext(rq); s != nil {
			return rq.endSearch(s, woken, false)
		}

		if s := local.popFront(); s != nil {
			return rq.endSearch(s, woken, false)
		}

		if s := rq.popGlobal(); s != nil {
			return rq.endSearch(s, woken, false)
		}

		if s := rq.trySteal(workerID); s != nil {
			return rq.endSearch(s, woken, true)
		}

		if woken {
			rq.searching.Store(false)
			woken = false
		}

		s, ok := rq.parkAndTake(workerID)
		if !ok {
			return nil, false
		}

		if s != nil {
			return s, true
		}

		woken = true
	}
}

// endSearch returns s from take. When the taking worker was woken to
// search, it clears searching, and if it stole s it wakes the next
// searcher, since the ring it stole from may hold more: the Go scheduler
// passes the wake on the same way when a spinning thread finds work.
func (rq *readyQueue) endSearch(s schedulable, woken, stolen bool) (schedulable, bool) {
	if woken {
		rq.searching.Store(false)
		if stolen {
			rq.wakeSearcher()
		}
	}

	return s, true
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
//
// A sibling's run-next slot is a target too, after every ring, while any slot
// may be occupied (see runnextWatched): only once the sibling's turn has run
// for runnextStealAge (see stealRunnext), so the actor a short turn readied
// stays with the worker that will run it next.
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

	if !rq.runnextWatched.Load() {
		return nil
	}

	now := monotonicNanos()
	for i := 1; i < n; i++ {
		victim := rq.locals[(workerID+i)%n]
		if !victim.runnextSet.Load() {
			continue
		}

		if s := victim.stealRunnext(now); s != nil {
			return s
		}
	}
	return nil
}

// parkAndTake parks workerID until a producer hands it work, the
// global queue or a local ring turns out to be non-empty after the park
// was published, or the queue is closed.
//
// Returns (nil, false) when the queue is closed and the worker should
// exit. Returns (s, true) with a non-nil item on a direct handoff, or
// (nil, true) when the caller should retry the take loop.
func (rq *readyQueue) parkAndTake(workerID int) (schedulable, bool) {
	if rq.closed.Load() {
		return nil, false
	}

	flag := &rq.idleFlags[workerID].flag
	flag.Store(1)
	rq.idleCount.Add(1)

	// Publish-then-check: the flag store above is ordered before these
	// loads, so a producer that pushed to the global queue without
	// seeing this worker's flag is observed here, and vice versa. The
	// same holds for a local ring: a worker that pushed to its own ring
	// and found no parked worker to wake (see wakeSearcher) is observed
	// through the ring's size, so its item is stolen instead of waiting
	// for its owner, which may be blocked in a handler.
	if rq.globalCount.Load() > 0 || rq.hasLocalWork() || rq.closed.Load() {
		if flag.CompareAndSwap(1, 0) {
			rq.idleCount.Add(-1)
			return nil, !rq.closed.Load()
		}

		// A producer won the flag CAS: its handoff is in flight and must
		// be consumed to keep the channel empty for the next claim.
		s := <-rq.handoff[workerID]
		return s, true
	}

	// A bare channel receive keeps the park path cheap; close wakes
	// parked workers by claiming their flags and sending nil, so no
	// second wake channel (and no two-way select) is needed. A nil
	// handoff sends the caller back around the take loop, where the
	// closed state is observed.
	s := <-rq.handoff[workerID]
	return s, true
}

// hasLocalWork reports whether any worker's local ring holds an item,
// from the rings' atomic sizes, without taking a lock.
func (rq *readyQueue) hasLocalWork() bool {
	for _, local := range rq.locals {
		if local.sizeAtomic.Load() > 0 {
			return true
		}
	}

	return false
}

// hasRunnext reports whether any worker's run-next slot is occupied.
func (rq *readyQueue) hasRunnext() bool {
	for _, local := range rq.locals {
		if local.runnextSet.Load() {
			return true
		}
	}

	return false
}

// hasStaleRunnext reports whether some worker's run-next slot holds an actor
// a thief may take at now (see stealRunnext): the dispatcher's watchdog then
// wakes a searcher to take it.
func (rq *readyQueue) hasStaleRunnext(now int64) bool {
	for _, local := range rq.locals {
		if local.runnextSet.Load() && now-local.turnStart.Load() > int64(runnextStealAge) {
			return true
		}
	}

	return false
}

// close marks the queue closed, then claims and wakes every parked
// worker with a nil handoff so it re-runs its take loop and observes
// the closed state. A worker that begins parking after the claim scan
// sees the closed flag in its own publish-then-check and exits without
// waiting. Idempotent.
func (rq *readyQueue) close() {
	if !rq.closed.CompareAndSwap(false, true) {
		return
	}

	for i := range rq.idleFlags {
		flag := &rq.idleFlags[i].flag
		if flag.Load() == 1 && flag.CompareAndSwap(1, 0) {
			rq.idleCount.Add(-1)
			rq.handoff[i] <- nil
		}
	}
}

// globalLen returns the current global queue depth. Test-only accessor.
func (rq *readyQueue) globalLen() int {
	rq.parkMu.Lock()
	defer rq.parkMu.Unlock()
	return rq.global.size
}

// parkedCount returns the number of workers currently parked. Test-only.
func (rq *readyQueue) parkedCount() int {
	return int(rq.idleCount.Load())
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

// pushRunnext fills the run-next slot with s and reports whether it did; the
// slot was occupied when it reports false. Only the owning worker calls it.
func (q *localQueue) pushRunnext(s schedulable) bool {
	q.mu.Lock()
	if q.runnext != nil {
		q.mu.Unlock()
		return false
	}
	q.runnext = s
	q.runnextSet.Store(true)
	q.mu.Unlock()
	return true
}

// takeRunnext is the owner's take from its run-next slot: the slot's actor,
// or nil when the slot is empty or the ring's turn has come. The ring's turn
// comes after runnextChainLimit consecutive takes from the slot while the
// ring holds actors; the slot's actor then goes to the back of the ring, or
// to rq's global queue when the ring is full, and the caller serves the ring.
func (q *localQueue) takeRunnext(rq *readyQueue) schedulable {
	if !q.runnextSet.Load() {
		q.runnextTakes = 0
		return nil
	}

	if q.runnextTakes >= runnextChainLimit && q.sizeAtomic.Load() > 0 {
		q.runnextTakes = 0
		if s := q.popRunnext(); s != nil && !q.pushBack(s) {
			rq.push(s)
		}

		return nil
	}

	s := q.popRunnext()
	if s != nil {
		q.runnextTakes++
	}

	return s
}

// popRunnext empties the run-next slot and returns what it held, or nil when
// it was empty. Only the owning worker calls it.
func (q *localQueue) popRunnext() schedulable {
	if !q.runnextSet.Load() {
		return nil
	}

	return q.clearRunnext()
}

// stealRunnext takes the run-next slot's actor for another worker, and
// returns nil while the slot is empty or the owner's turn has run for less
// than runnextStealAge at now (see runnextStealAge).
func (q *localQueue) stealRunnext(now int64) schedulable {
	if now-q.turnStart.Load() <= int64(runnextStealAge) {
		return nil
	}

	return q.clearRunnext()
}

// clearRunnext empties the run-next slot under the lock and returns what it
// held, nil when a concurrent take emptied it first.
func (q *localQueue) clearRunnext() schedulable {
	q.mu.Lock()
	s := q.runnext
	q.runnext = nil
	q.runnextSet.Store(false)
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

// monotonicNanos returns a monotonic reading in nanoseconds, from the
// process's own epoch, for aging run-next slots. time.Since reads the
// monotonic clock, so a wall-clock adjustment does not move the age.
func monotonicNanos() int64 {
	return int64(time.Since(monotonicEpoch))
}
