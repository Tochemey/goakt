/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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

package queue

import "sync/atomic"

type linkedNode[T any] struct {
	value T
	next  atomic.Pointer[linkedNode[T]]
}

// Linked is a concurrent non-blocking queue
type Linked[T any] struct {
	head, tail atomic.Pointer[linkedNode[T]]
}

// NewLinked creates an instance of Linked
func NewLinked[T any]() *Linked[T] {
	empty := new(linkedNode[T])
	lnk := new(Linked[T])
	lnk.head.Store(empty)
	lnk.tail.Store(empty)
	return lnk
}

// Push place the given value in the queue head (FIFO).
func (q *Linked[T]) Push(value T) {
	node := &linkedNode[T]{value, atomic.Pointer[linkedNode[T]]{}}
	var currentTail *linkedNode[T]
	for added := false; !added; {
		currentTail = q.tail.Load()
		currentNext := currentTail.next.Load()
		if currentNext != nil {
			q.tail.CompareAndSwap(currentTail, currentNext)
			continue
		}
		added = q.tail.Load().next.CompareAndSwap(currentNext, node)
	}
	q.tail.CompareAndSwap(currentTail, node)
}

// Pop removes the QueueItem from the front of the queue
// If false is returned, it means there were no items on the queue
func (q *Linked[T]) Pop() (T, bool) {
	var currentHead *linkedNode[T]
	for removed := false; !removed; {
		head, tail := q.head.Load(), q.tail.Load()
		currentHead = head.next.Load()
		if tail == head {
			if currentHead != nil {
				q.tail.CompareAndSwap(tail, currentHead)
				continue
			}
			return *new(T), false
		}
		removed = q.head.CompareAndSwap(head, currentHead)
	}
	return currentHead.value, true
}

// Peek return the next element in the queue without removing it
func (q *Linked[T]) Peek() T {
	return q.head.Load().next.Load().value
}

// IsEmpty returns true when the queue is empty
func (q *Linked[T]) IsEmpty() bool {
	return q.head.Load().next.Load() == nil
}
