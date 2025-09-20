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

// CacheLinePadding prevents false sharing between CPU cache lines
type CacheLinePadding [64]byte

// node returns the queue node
type node struct {
	value atomic.Pointer[ReceiveContext]
	next  unsafe.Pointer
}

// Single global pool for mpscNode to avoid per-op allocations.
var nodePool = sync.Pool{New: func() any { return new(node) }}

// UnboundedMailbox is a lock-free multi-producer, single-consumer (MPSC)
// FIFO queue used as the actor mailbox.
//
// It is safe for many producer goroutines to call Enqueue concurrently, while
// exactly one consumer goroutine calls Dequeue. Ordering is preserved (FIFO)
// with respect to overall arrival order. Operations are non-blocking and rely
// on atomic pointer updates; underlying nodes are pooled via sync.Pool to
// reduce allocations.
//
// The mailbox is unbounded: if producers outpace the consumer, memory usage can
// grow without limit. Consider higher-level backpressure if needed.
//
// The zero value of UnboundedMailbox is not ready for use; always construct via
// NewUnboundedMailbox.
//
// Reference: https://concurrencyfreaks.blogspot.com/2014/04/multi-producer-single-consumer-queue.html
type UnboundedMailbox struct {
	// head pointer for dequeue operations (consumer side)
	// Padded to prevent false sharing
	head unsafe.Pointer // *node
	_    CacheLinePadding

	// tail pointer for enqueue operations (producer side)
	// Padded to prevent false sharing
	tail unsafe.Pointer // *node
	_    CacheLinePadding
}

// enforces compilation error
var _ Mailbox = (*UnboundedMailbox)(nil)

// NewUnboundedMailbox returns a new, initialized UnboundedMailbox.
//
// The returned mailbox supports concurrent producers and a single consumer.
// Always use this constructor; the zero value is not usable.
func NewUnboundedMailbox() *UnboundedMailbox {
	item := new(node)
	return &UnboundedMailbox{
		head: unsafe.Pointer(item),
		tail: unsafe.Pointer(item),
	}
}

// Enqueue appends the given ReceiveContext to the tail of the mailbox.
//
// It is safe to call Enqueue concurrently from multiple goroutines. The call is
// non-blocking and preserves FIFO ordering. The method currently always returns
// nil; the error is present to satisfy the Mailbox interface.
func (m *UnboundedMailbox) Enqueue(value *ReceiveContext) error {
	tnode := nodePool.Get().(*node)
	tnode.value.Store(value)
	atomic.StorePointer(&tnode.next, nil)

	// Atomically swap the tail pointer and link the previous tail to this node
	prev := (*node)(atomic.SwapPointer(&m.tail, unsafe.Pointer(tnode)))
	atomic.StorePointer(&prev.next, unsafe.Pointer(tnode))
	return nil
}

// Dequeue removes and returns the next message at the head of the mailbox.
//
// It returns nil if the mailbox is empty. Dequeue must be called by exactly one
// consumer goroutine; concurrent calls to Dequeue are not supported. The call is
// non-blocking and suitable for use inside an actor/event loop.
func (m *UnboundedMailbox) Dequeue() *ReceiveContext {
	head := (*node)(atomic.LoadPointer(&m.head))
	next := (*node)(atomic.LoadPointer(&head.next))

	if next == nil {
		return nil
	}

	atomic.StorePointer(&m.head, unsafe.Pointer(next))
	value := next.value.Load()
	next.value.Store(nil) // avoid memory leaks

	nodePool.Put(head)
	return value
}

// Len returns an approximate number of messages currently in the mailbox.
//
// This performs an O(n) traversal and may race with concurrent producers,
// making the result a point-in-time estimate. Avoid calling Len in hot paths;
// prefer external accounting if frequent checks are required.
func (m *UnboundedMailbox) Len() int64 {
	var count int64
	head := (*node)(atomic.LoadPointer(&m.head))
	current := (*node)(atomic.LoadPointer(&head.next))

	for current != nil {
		count++
		current = (*node)(atomic.LoadPointer(&current.next))
	}

	return count
}

// IsEmpty reports whether the mailbox currently holds no messages.
//
// The result is a snapshot that may become stale immediately in the presence of
// concurrent producers. It is O(1) and safe to call from the consumer.
func (m *UnboundedMailbox) IsEmpty() bool {
	head := (*node)(atomic.LoadPointer(&m.head))
	next := (*node)(atomic.LoadPointer(&head.next))
	return next == nil
}

// Dispose implements the Mailbox interface.
//
// For UnboundedMailbox this is a no-op provided for interface compliance. It
// does not free resources beyond what the garbage collector and internal pools
// already manage, and it does not affect in-flight producers/consumer.
func (m *UnboundedMailbox) Dispose() {}
