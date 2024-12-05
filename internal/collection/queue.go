/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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

package collection

import (
	"sync"
	"sync/atomic"
	"unsafe"
)

// Queue defines a lock-free Queue.
type Queue struct {
	head unsafe.Pointer // pointer to the head of the queue
	tail unsafe.Pointer // pointer to the tail of the queue
	len  int64          // length of the queue
	pool sync.Pool
}

// item is a single node in the queue.
type item struct {
	next unsafe.Pointer // pointer to the next item in the queue
	v    interface{}    // the value stored in the queue item
}

// NewQueue creates and returns a new lock-free queue.
func NewQueue() *Queue {
	// Initial node is an empty item to act as a sentinel (dummy node).
	dummy := &item{}
	return &Queue{
		head: unsafe.Pointer(dummy), // both head and tail point to the dummy node
		tail: unsafe.Pointer(dummy),
		len:  0,
		pool: sync.Pool{
			New: func() interface{} {
				return &item{}
			},
		},
	}
}

// Enqueue adds a value to the tail of the queue.
func (q *Queue) Enqueue(v interface{}) {
	// Get a node from the pool
	newNode := q.getItem()
	newNode.v = v
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
func (q *Queue) Dequeue() interface{} {
	for {
		head := (*item)(atomic.LoadPointer(&q.head))
		next := atomic.LoadPointer(&head.next)

		// Queue is empty
		if next == nil {
			return nil
		}

		nextNode := (*item)(next)

		// Try to advance the head
		if atomic.CompareAndSwapPointer(&q.head, unsafe.Pointer(head), next) {
			// Get the value before potentially releasing the node
			value := nextNode.v

			// Release the old head node back to the pool
			q.releaseItem(head)

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

// getItem retrieves a node from the pool or creates a new one
func (q *Queue) getItem() *item {
	return q.pool.Get().(*item)
}

// releaseItem returns a node to the pool for reuse
func (q *Queue) releaseItem(i *item) {
	// Reset i to prevent memory leaks
	i.v = nil
	i.next = nil
	q.pool.Put(i)
}
