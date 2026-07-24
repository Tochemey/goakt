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
	"sync/atomic"

	gerrors "github.com/tochemey/goakt/v4/errors"
)

// BoundedStablePriorityMailbox is a bounded priority mailbox that preserves
// FIFO ordering among messages of equal priority.
//
// It combines the capacity ceiling of BoundedPriorityMailbox with the stable
// insertion-order tiebreaking of UnboundedStablePriorityMailbox. Producers
// enqueue through a lock-free intake with atomic admission control, so Enqueue
// is O(1) and allocation free. When the mailbox is full, Enqueue is
// non-blocking and returns gerrors.ErrMailboxFull, which the dispatcher routes
// to the dead-letter stream. The single consumer drains the intake into the
// heap before each dequeue, assigning each drained message a monotonic sequence
// in arrival order; the heap is preallocated at capacity and never grows.
type BoundedStablePriorityMailbox struct {
	intake   priorityIntake
	heap     *stableHeap
	prev     *ReceiveContext
	seq      uint64
	capacity int64
	length   int64
}

// enforce compilation error
var _ Mailbox = (*BoundedStablePriorityMailbox)(nil)

// NewBoundedStablePriorityMailbox creates a bounded, stable priority mailbox
// with the given capacity, ordered by the priority function. Capacity must be
// a positive integer.
func NewBoundedStablePriorityMailbox(capacity int, priorityFunc PriorityFunc) *BoundedStablePriorityMailbox {
	return &BoundedStablePriorityMailbox{
		heap: &stableHeap{
			items:        make([]stableItem, 0, capacity),
			priorityFunc: priorityFunc,
		},
		capacity: int64(capacity),
	}
}

// Enqueue places the given message in the mailbox. It is lock-free, never
// blocks, and returns gerrors.ErrMailboxFull when the mailbox is at capacity.
func (q *BoundedStablePriorityMailbox) Enqueue(msg *ReceiveContext) error {
	if atomic.AddInt64(&q.length, 1) > q.capacity {
		atomic.AddInt64(&q.length, -1)
		return gerrors.ErrMailboxFull
	}

	q.intake.push(msg)
	return nil
}

// Dequeue returns the highest-priority message, breaking ties by arrival
// order, or nil when the mailbox is empty. It must be called by a single
// consumer.
func (q *BoundedStablePriorityMailbox) Dequeue() (msg *ReceiveContext) {
	if q.prev != nil {
		recycleContext(q.prev)
		q.prev = nil
	}

	if atomic.LoadInt64(&q.length) == 0 {
		return nil
	}

	for n := q.intake.drain(); n != nil; {
		next := chainNext(n)
		chainUnlink(n)
		q.heap.push(stableItem{ctx: n, seq: q.seq})
		q.seq++
		n = next
	}

	if len(q.heap.items) == 0 {
		return nil
	}

	item := q.heap.pop()
	atomic.AddInt64(&q.length, -1)
	q.prev = item.ctx
	return item.ctx
}

// IsEmpty returns true when the mailbox is empty
func (q *BoundedStablePriorityMailbox) IsEmpty() bool {
	return q.Len() == 0
}

// Len returns mailbox length
func (q *BoundedStablePriorityMailbox) Len() int64 {
	return atomic.LoadInt64(&q.length)
}

// Dispose will dispose of this queue and free any blocked threads
// in the Enqueue and/or Dequeue methods.
func (q *BoundedStablePriorityMailbox) Dispose() {}
