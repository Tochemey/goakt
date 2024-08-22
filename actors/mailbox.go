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
 *
 */

package actors

import (
	"sync"
	"sync/atomic"
	"unsafe"
)

// node returns the queue node
type node struct {
	value *ReceiveContext
	next  *node
}

// mailbox is a Multi-Producer-Single-Consumer Queue
// reference: https://concurrencyfreaks.blogspot.com/2014/04/multi-producer-single-consumer-queue.html
type mailbox struct {
	head   *node
	tail   *node
	length int64
	lock   sync.Mutex
}

// newMailbox create an instance of mailbox
func newMailbox() *mailbox {
	item := new(node)
	return &mailbox{
		head:   item,
		tail:   item,
		length: 0,
		lock:   sync.Mutex{},
	}
}

// Push place the given value in the queue head (FIFO).
func (q *mailbox) Push(value *ReceiveContext) {
	tnode := &node{
		value: value,
	}
	previousHead := (*node)(atomic.SwapPointer((*unsafe.Pointer)(unsafe.Pointer(&q.head)), unsafe.Pointer(tnode)))
	atomic.StorePointer((*unsafe.Pointer)(unsafe.Pointer(&previousHead.next)), unsafe.Pointer(tnode))
	atomic.AddInt64(&q.length, 1)
}

// Pop takes the QueueItem from the queue tail.
// Returns false if the queue is empty. Can be used in a single consumer (goroutine) only.
func (q *mailbox) Pop() (*ReceiveContext, bool) {
	next := (*node)(atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(&q.tail.next))))
	if next == nil {
		return nil, false
	}

	q.lock.Lock()
	q.tail = next
	q.lock.Unlock()
	value := next.value
	next.value = nil
	atomic.AddInt64(&q.length, -1)
	return value, true
}

// Len returns queue length
func (q *mailbox) Len() int64 {
	return atomic.LoadInt64(&q.length)
}

// IsEmpty returns true when the queue is empty
// must be called from a single, consumer goroutine
func (q *mailbox) IsEmpty() bool {
	q.lock.Lock()
	tail := q.tail
	q.lock.Unlock()
	next := (*node)(atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(&tail.next))))
	return next == nil
}
