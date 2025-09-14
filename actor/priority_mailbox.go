/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package actor

import (
	hp "container/heap"
	"sync"
	"sync/atomic"

	"google.golang.org/protobuf/proto"
)

// PriorityFunc defines the ordering rule used by PriorityMailBox to
// prioritize messages.
//
// It must return true if msg1 has higher priority than msg2 (i.e.,
// msg1 should be dequeued before msg2). Implementations are free to
// interpret "higher priority" as needed (e.g., larger numeric value is
// higher, smaller numeric value is higher, custom severity ordering, etc.).
//
// Example (higher numeric value first):
//
//	priority := func(m1, m2 proto.Message) bool {
//	    return m1.(*testpb.TestMessage).GetPriority() > m2.(*testpb.TestMessage).GetPriority()
//	}
type PriorityFunc func(msg1, msg2 proto.Message) bool

// heap implements the standard heap.Interface
type heap struct {
	items        []*ReceiveContext
	priorityFunc PriorityFunc
}

// enforce compilation error
var _ hp.Interface = (*heap)(nil)

func (h *heap) Len() int {
	return len(h.items)
}

func (h *heap) Push(x any) {
	h.items = append(h.items, x.(*ReceiveContext))
}

func (h *heap) Less(i, j int) bool {
	return h.priorityFunc(
		h.items[i].Message(),
		h.items[j].Message(),
	)
}

// Pop is called after the first element is swapped with the last
// so return the last element and resize the slice
func (h *heap) Pop() any {
	last := len(h.items) - 1
	element := h.items[last]
	h.items = h.items[:last]
	return element
}

func (h *heap) Swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
}

// PriorityMailBox is a lock‑protected, unbounded priority mailbox for actors.
//
// It stores ReceiveContext entries in a binary heap (backed by container/heap)
// and dequeues items based on a caller‑supplied PriorityFunc. Complexity is
// O(log n) for Enqueue and Dequeue. When two messages compare equal according
// to the PriorityFunc, the relative order is unspecified (i.e., not stable).
//
// Concurrency model
//   - Multi‑producer: Many goroutines may Enqueue concurrently.
//   - Single‑consumer: Only one goroutine should call Dequeue. Multiple concurrent
//     consumers are not supported and may lead to races (e.g., Pop on an empty heap).
//
// Semantics and caveats
//   - The priority function fully determines ordering; choose it carefully to avoid
//     panics from invalid type assertions inside the function.
//   - Len() is tracked with an atomic counter and is intended for observability, not
//     strict synchronization. IsEmpty() uses Len() and is therefore best‑effort.
//   - FIFO within the same priority is not guaranteed. If stability is required,
//     encode a tiebreaker (e.g., timestamp/sequence) into the PriorityFunc.
type PriorityMailBox struct {
	heap   *heap
	lock   *sync.RWMutex
	length int64
}

// enforce compilation error
var _ Mailbox = (*PriorityMailBox)(nil)

// NewPriorityMailBox creates a PriorityMailBox that orders messages using the
// provided PriorityFunc. The mailbox is unbounded and backed by a binary heap.
//
// The returned mailbox is safe for concurrent producers (Enqueue), but Dequeue
// must be called by a single consumer goroutine.
func NewPriorityMailBox(priorityFunc PriorityFunc) *PriorityMailBox {
	h := &heap{
		items:        make([]*ReceiveContext, 0),
		priorityFunc: priorityFunc,
	}

	hp.Init(h)

	return &PriorityMailBox{
		heap: h,
		lock: &sync.RWMutex{},
	}
}

// Enqueue inserts a message into the priority mailbox.
//
// Semantics
// - Ordering is determined by the PriorityFunc supplied at construction time.
// - Complexity is O(log n) due to the underlying binary heap maintenance.
// - This method is non-blocking from a caller perspective and always returns nil.
//
// Concurrency
//   - Safe for multiple concurrent producers; a write lock guards heap mutations.
//   - The PriorityFunc may be invoked during heap adjustments; ensure it can safely
//     compare the message types you plan to enqueue. Type mismatches inside the
//     PriorityFunc can cause panics.
//
// Returns
// - Always returns nil. The return type matches the Mailbox interface.
func (q *PriorityMailBox) Enqueue(msg *ReceiveContext) error {
	q.lock.Lock()
	hp.Push(q.heap, msg)
	q.lock.Unlock()
	atomic.AddInt64(&q.length, 1)
	return nil
}

// Dequeue removes and returns the highest‑priority message according to the
// PriorityFunc supplied at construction time.
//
// Semantics
// - Returns nil if the mailbox is empty (based on a best‑effort atomic length check).
// - Complexity is O(log n) due to binary heap rebalancing.
//
// Concurrency
//   - Intended for a single consumer goroutine. The method uses a mutex to protect
//     the heap, but running multiple concurrent consumers is not supported at the
//     abstraction level and may lead to unexpected fairness/ordering.
func (q *PriorityMailBox) Dequeue() (msg *ReceiveContext) {
	// to avoid overflow
	if q.IsEmpty() {
		return nil
	}
	q.lock.Lock()
	msg = hp.Pop(q.heap).(*ReceiveContext)
	q.lock.Unlock()
	atomic.AddInt64(&q.length, -1)
	return msg
}

// IsEmpty returns true when the mailbox is empty
func (q *PriorityMailBox) IsEmpty() bool {
	return q.Len() == 0
}

// Len returns mailbox length
func (q *PriorityMailBox) Len() int64 {
	return atomic.LoadInt64(&q.length)
}

// Dispose will dispose of this queue and free any blocked threads
// in the Enqueue and/or Dequeue methods.
func (q *PriorityMailBox) Dispose() {}
