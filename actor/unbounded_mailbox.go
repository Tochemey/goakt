/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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
	"sync"
	"sync/atomic"
	"unsafe"
)

// node returns the queue node
type node struct {
	value *ReceiveContext
	next  *node
}

var nodePool = sync.Pool{ // reuse queue nodes to avoid per-message allocations
	New: func() any { return new(node) },
}

func getNode(v *ReceiveContext) *node {
	n := nodePool.Get().(*node)
	n.value = v
	n.next = nil
	return n
}

func releaseNode(n *node) {
	n.value = nil
	n.next = nil
	nodePool.Put(n)
}

// UnboundedMailbox is a Multi-Producer-Single-Consumer Queue (FIFO)
// reference: https://concurrencyfreaks.blogspot.com/2014/04/multi-producer-single-consumer-queue.html
type UnboundedMailbox struct {
	head, tail *node
	length     int64
}

// enforces compilation error
var _ Mailbox = (*UnboundedMailbox)(nil)

// NewUnboundedMailbox create an instance of UnboundedMailbox
func NewUnboundedMailbox() *UnboundedMailbox {
	item := getNode(nil)
	return &UnboundedMailbox{
		head:   item,
		tail:   item,
		length: 0,
	}
}

// Enqueue places the given value in the mailbox
func (m *UnboundedMailbox) Enqueue(value *ReceiveContext) error {
	tnode := getNode(value)
	previousHead := (*node)(atomic.SwapPointer((*unsafe.Pointer)(unsafe.Pointer(&m.head)), unsafe.Pointer(tnode)))
	atomic.StorePointer((*unsafe.Pointer)(unsafe.Pointer(&previousHead.next)), unsafe.Pointer(tnode))
	atomic.AddInt64(&m.length, 1)
	return nil
}

// Dequeue takes the mail from the mailbox
// Returns nil if the mailbox is empty. Can be used in a single consumer (goroutine) only.
func (m *UnboundedMailbox) Dequeue() *ReceiveContext {
	next := (*node)(atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(&m.tail.next))))
	if next == nil {
		return nil
	}

	oldTail := m.tail
	m.tail = next
	value := next.value
	next.value = nil
	atomic.AddInt64(&m.length, -1)
	// recycle the consumed node
	releaseNode(oldTail)
	return value
}

// Len returns mailbox length
func (m *UnboundedMailbox) Len() int64 {
	return atomic.LoadInt64(&m.length)
}

// IsEmpty returns true when the mailbox is empty
func (m *UnboundedMailbox) IsEmpty() bool {
	// Single-consumer check: queue is empty if tail.next is nil
	next := (*node)(atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(&m.tail.next))))
	return next == nil
}

// Dispose will dispose of this queue and free any blocked threads
// in the Enqueue and/or Dequeue methods.
func (m *UnboundedMailbox) Dispose() {}
