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

package queue

import (
	"sync/atomic"
	"unsafe"
)

// Queue defines a lock-free Queue.
//
// It is a Michael-Scott queue. Dequeued nodes are left to the garbage
// collector and never reused: a producer that loaded the tail may still hold a
// pointer to a node after a consumer unlinked it, and linking onto a reused
// node would lose the value or make the list cyclic.
type Queue struct {
	head unsafe.Pointer // pointer to the head of the queue
	tail unsafe.Pointer // pointer to the tail of the queue
	len  int64          // length of the queue
}

// item is a single node in the queue.
type item struct {
	next unsafe.Pointer // pointer to the next item in the queue
	v    any            // the value stored in the queue item
}

// NewQueue creates and returns a new lock-free queue.
func NewQueue() *Queue {
	// Initial node is an empty item to act as a sentinel (dummy node).
	dummy := &item{}
	return &Queue{
		head: unsafe.Pointer(dummy), // both head and tail point to the dummy node
		tail: unsafe.Pointer(dummy),
		len:  0,
	}
}

// Enqueue adds a value to the tail of the queue.
func (q *Queue) Enqueue(v any) {
	newNode := &item{v: v}
	newNodePtr := unsafe.Pointer(newNode)

	for {
		tail := (*item)(atomic.LoadPointer(&q.tail))
		next := atomic.LoadPointer(&tail.next)

		// Another thread might have already enqueued a node
		if next != nil {
			// Try to help advance the tail
			atomic.CompareAndSwapPointer(&q.tail, unsafe.Pointer(tail), next)
			continue
		}

		// Try to link the new node
		if atomic.CompareAndSwapPointer(&tail.next, nil, newNodePtr) {
			// Successfully linked, now try to advance tail
			atomic.CompareAndSwapPointer(&q.tail, unsafe.Pointer(tail), newNodePtr)

			// Increment length atomically
			atomic.AddInt64(&q.len, 1)

			return
		}
	}
}

// Dequeue removes and returns the value at the head of the queue.
// It returns nil if the queue is empty.
func (q *Queue) Dequeue() any {
	for {
		head := (*item)(atomic.LoadPointer(&q.head))
		next := atomic.LoadPointer(&head.next)

		// Queue is empty
		if next == nil {
			return nil
		}

		// Read the value before advancing the head: once the head moves the
		// node belongs to the new head and another consumer may be past it.
		value := (*item)(next).v

		// Try to advance the head
		if atomic.CompareAndSwapPointer(&q.head, unsafe.Pointer(head), next) {
			// Decrement length atomically
			atomic.AddInt64(&q.len, -1)

			return value
		}
	}
}

// Length returns the number of items in the queue.
func (q *Queue) Length() uint64 {
	return uint64(atomic.LoadInt64(&q.len))
}

// IsEmpty returns true when the queue is empty
func (q *Queue) IsEmpty() bool {
	return atomic.LoadInt64(&q.len) == 0
}
