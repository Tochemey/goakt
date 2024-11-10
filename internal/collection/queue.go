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
	len  uint64         // length of the queue
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
		pool: sync.Pool{
			New: func() interface{} {
				return &item{}
			},
		},
	}
}

// Enqueue adds a value to the tail of the queue.
func (q *Queue) Enqueue(v interface{}) {
	xitem := q.pool.Get().(*item)
	xitem.next = nil
	xitem.v = v

	for {
		last := loadItem(&q.tail)        // Load current tail
		lastNext := loadItem(&last.next) // Load the next pointer of tail
		if last == loadItem(&q.tail) {   // Check tail consistency
			if lastNext == nil { // Is tail really pointing to the last node?
				// Try to link the new item at the end
				if casItem(&last.next, nil, xitem) {
					// Enqueue successful, now try to move the tail pointer
					casItem(&q.tail, last, xitem)
					atomic.AddUint64(&q.len, 1)
					return
				}
			} else {
				// Tail was pointing to an intermediate node, help move it forward
				casItem(&q.tail, last, lastNext)
			}
		}
	}
}

// Dequeue removes and returns the value at the head of the queue.
// It returns nil if the queue is empty.
func (q *Queue) Dequeue() interface{} {
	for {
		head := loadItem(&q.head)      // Load the current head
		tail := loadItem(&q.tail)      // Load the current tail
		next := loadItem(&head.next)   // Load the next node after head
		if head == loadItem(&q.head) { // Check head consistency
			if head == tail { // Is the queue empty?
				if next == nil { // Confirm that queue is empty
					return nil
				}
				// Tail is lagging behind, move it forward
				casItem(&q.tail, tail, next)
			} else {
				// Get the value before CAS to avoid freeing the node too early
				v := next.v
				// Try to swing the head to the next node
				if casItem(&q.head, head, next) {
					atomic.AddUint64(&q.len, ^uint64(0)) // decrement length
					q.pool.Put(next)
					return v // return the dequeued value
				}
			}
		}
	}
}

// Length returns the number of items in the queue.
func (q *Queue) Length() uint64 {
	return atomic.LoadUint64(&q.len)
}

// IsEmpty returns true when the queue is empty
func (q *Queue) IsEmpty() bool {
	return atomic.LoadUint64(&q.len) == 0
}

// loadItem atomically loads an item pointer from the given unsafe pointer.
func loadItem(p *unsafe.Pointer) *item {
	return (*item)(atomic.LoadPointer(p))
}

// casItem performs an atomic compare-and-swap on an unsafe pointer.
func casItem(p *unsafe.Pointer, old, new *item) bool {
	return atomic.CompareAndSwapPointer(p, unsafe.Pointer(old), unsafe.Pointer(new))
}
