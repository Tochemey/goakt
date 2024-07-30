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

import "sync"

// minQueueLen is the smallest capacity that queue may have.
// Must be power of 2 for bitwise modulus: x % n == x & (n - 1).
const minQueueLen = 16

// Queue thread-safe Queue using ring-buffer
// reference: https://blog.dubbelboer.com/2015/04/25/go-faster-queue.html
// https://github.com/eapache/queue
type Queue[T any] struct {
	mu      sync.RWMutex
	cond    *sync.Cond
	nodes   []*T
	head    int
	tail    int
	count   int
	closed  bool
	initCap int
}

// New creates an instance of Unbounded
func New[T any]() *Queue[T] {
	sq := &Queue[T]{
		initCap: minQueueLen,
		nodes:   make([]*T, minQueueLen),
	}
	sq.cond = sync.NewCond(&sq.mu)
	return sq
}

// Push adds an item to the back of the queue
// It can be safely called from multiple goroutines
// It will return false if the queue is closed.
// In that case the Item is dropped.
func (q *Queue[T]) Push(i T) bool {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return false
	}
	if q.count == len(q.nodes) {
		q.resize()
	}
	q.nodes[q.tail] = &i
	// bitwise modulus
	q.tail = (q.tail + 1) & (len(q.nodes) - 1)
	q.count++
	q.cond.Signal()
	q.mu.Unlock()
	return true
}

// Close the queue and discard all entries in the queue
// all goroutines in wait() will return
func (q *Queue[T]) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.count = 0
	q.nodes = nil
	q.cond.Broadcast()
}

// CloseRemaining will close the queue and return all entries in the queue.
// All goroutines in wait() will return.
func (q *Queue[T]) CloseRemaining() []T {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return []T{}
	}
	rem := make([]T, 0, q.count)
	for q.count > 0 {
		i := q.nodes[q.head]
		// bitwise modulus
		q.head = (q.head + 1) & (len(q.nodes) - 1)
		q.count--
		rem = append(rem, *i)
	}
	q.closed = true
	q.count = 0
	q.nodes = nil
	q.cond.Broadcast()
	return rem
}

// IsClosed returns true if the queue has been closed
// The call cannot guarantee that the queue hasn't been
// closed while the function returns, so only "true" has a definite meaning.
func (q *Queue[T]) IsClosed() bool {
	q.mu.RLock()
	c := q.closed
	q.mu.RUnlock()
	return c
}

// Wait for an item to be added.
// If there is items on the queue the first will
// be returned immediately.
// Will return nil, false if the queue is closed.
// Otherwise, the return value of "remove" is returned.
func (q *Queue[T]) Wait() (T, bool) {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		var nilElt T
		return nilElt, false
	}
	if q.count != 0 {
		q.mu.Unlock()
		return q.Pop()
	}
	q.cond.Wait()
	q.mu.Unlock()
	return q.Pop()
}

// Pop removes the item from the front of the queue
// If false is returned, it either means 1) there were no items on the queue
// or 2) the queue is closed.
func (q *Queue[T]) Pop() (T, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.count == 0 {
		var nilElt T
		return nilElt, false
	}
	i := q.nodes[q.head]
	q.nodes[q.head] = nil
	// bitwise modulus
	q.head = (q.head + 1) & (len(q.nodes) - 1)
	q.count--
	// Resize down if buffer 1/4 full.
	if len(q.nodes) > minQueueLen && (q.count<<2) == len(q.nodes) {
		q.resize()
	}

	return *i, true
}

// Cap return the capacity (without allocations)
func (q *Queue[T]) Cap() int {
	q.mu.RLock()
	c := cap(q.nodes)
	q.mu.RUnlock()
	return c
}

// Len return the current length of the queue.
func (q *Queue[T]) Len() int {
	q.mu.RLock()
	l := q.count
	q.mu.RUnlock()
	return l
}

// IsEmpty returns true when the queue is empty
func (q *Queue[T]) IsEmpty() bool {
	q.mu.Lock()
	cnt := q.count
	q.mu.Unlock()
	return cnt == 0
}

// resize the queue
func (q *Queue[T]) resize() {
	nodes := make([]*T, q.count<<1)
	if q.tail > q.head {
		copy(nodes, q.nodes[q.head:q.tail])
	} else {
		n := copy(nodes, q.nodes[q.head:])
		copy(nodes[n:], q.nodes[:q.tail])
	}

	q.tail = q.count
	q.head = 0
	q.nodes = nodes
}
