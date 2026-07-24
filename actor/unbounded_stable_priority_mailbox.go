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
)

// stableItem pairs a message with the insertion order in which it entered the
// mailbox. The sequence breaks ties between messages of equal priority so
// arrival order is preserved.
type stableItem struct {
	ctx *ReceiveContext
	seq uint64
}

// stableHeap is a binary heap ordered by priorityFunc first and by insertion
// sequence second. It implements the sift operations directly on a typed slice
// rather than through container/heap, so pushing and popping never box a
// stableItem value into an interface and the mailbox stays allocation free.
type stableHeap struct {
	items        []stableItem
	priorityFunc PriorityFunc
}

// less orders by priority, falling back to insertion sequence only when neither
// message strictly outranks the other. This keeps equal-priority messages in
// FIFO order.
func (h *stableHeap) less(i, j int) bool {
	a, b := h.items[i], h.items[j]

	if h.priorityFunc(a.ctx.Message(), b.ctx.Message()) {
		return true
	}

	if h.priorityFunc(b.ctx.Message(), a.ctx.Message()) {
		return false
	}

	return a.seq < b.seq
}

// push adds item to the heap and restores the heap invariant.
func (h *stableHeap) push(item stableItem) {
	h.items = append(h.items, item)
	h.up(len(h.items) - 1)
}

// pop removes and returns the highest-priority item. It must not be called on
// an empty heap. The vacated slot is cleared so a drained message becomes
// collectable.
func (h *stableHeap) pop() stableItem {
	n := len(h.items) - 1
	h.items[0], h.items[n] = h.items[n], h.items[0]
	h.down(0, n)

	item := h.items[n]
	h.items[n] = stableItem{}
	h.items = h.items[:n]
	return item
}

func (h *stableHeap) up(j int) {
	for {
		i := (j - 1) / 2 // parent
		if i == j || !h.less(j, i) {
			break
		}

		h.items[i], h.items[j] = h.items[j], h.items[i]
		j = i
	}
}

func (h *stableHeap) down(i0, n int) {
	i := i0
	for {
		left := 2*i + 1
		if left >= n || left < 0 {
			break
		}

		child := left
		if right := left + 1; right < n && h.less(right, left) {
			child = right
		}

		if !h.less(child, i) {
			break
		}

		h.items[i], h.items[child] = h.items[child], h.items[i]
		i = child
	}
}

// UnboundedStablePriorityMailbox is a priority mailbox that preserves FIFO
// ordering among messages of equal priority.
//
// Producers enqueue through a lock-free intake, so Enqueue is O(1), allocation
// free, and never contends with the consumer. The single consumer drains the
// intake into a binary heap before each dequeue, assigns each drained message a
// monotonic sequence in arrival order, and uses that sequence to break ties
// between messages the priority function ranks equally. The mailbox is
// unbounded: the heap grows with the backlog.
type UnboundedStablePriorityMailbox struct {
	intake priorityIntake
	heap   *stableHeap
	prev   *ReceiveContext
	seq    uint64
	length int64
}

// enforce compilation error
var _ Mailbox = (*UnboundedStablePriorityMailbox)(nil)

// NewUnboundedStablePriorityMailbox creates an instance of
// UnboundedStablePriorityMailbox ordered by the given priority function.
func NewUnboundedStablePriorityMailbox(priorityFunc PriorityFunc) *UnboundedStablePriorityMailbox {
	return &UnboundedStablePriorityMailbox{
		heap: &stableHeap{
			items:        make([]stableItem, 0),
			priorityFunc: priorityFunc,
		},
	}
}

// Enqueue places the given message in the mailbox. It is lock-free, allocation
// free, and always returns nil.
func (q *UnboundedStablePriorityMailbox) Enqueue(msg *ReceiveContext) error {
	atomic.AddInt64(&q.length, 1)
	q.intake.push(msg)
	return nil
}

// Dequeue returns the highest-priority message, breaking ties by arrival
// order, or nil when the mailbox is empty. It must be called by a single
// consumer.
func (q *UnboundedStablePriorityMailbox) Dequeue() (msg *ReceiveContext) {
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
func (q *UnboundedStablePriorityMailbox) IsEmpty() bool {
	return q.Len() == 0
}

// Len returns mailbox length
func (q *UnboundedStablePriorityMailbox) Len() int64 {
	return atomic.LoadInt64(&q.length)
}

// Dispose will dispose of this queue and free any blocked threads
// in the Enqueue and/or Dequeue methods.
func (q *UnboundedStablePriorityMailbox) Dispose() {}
