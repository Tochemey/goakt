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
	"unsafe"
)

// CacheLinePadding prevents false sharing between CPU cache lines
type CacheLinePadding [64]byte

// UnboundedMailbox is a lock-free multi-producer, single-consumer (MPSC)
// FIFO queue used as the actor mailbox.
//
// Many producer goroutines may call Enqueue concurrently; exactly one
// consumer goroutine calls Dequeue. Ordering is FIFO with respect to
// arrival. Operations are non-blocking and rely on atomic pointer
// updates.
//
// ReceiveContext itself serves as the intrusive list node via its
// `next` field, so a ReceiveContext may be linked into only one mailbox
// at a time.
//
// The mailbox is unbounded: if producers outpace the consumer, memory
// grows without limit. Apply higher-level backpressure if needed.
//
// The zero value is not usable; always construct via NewUnboundedMailbox.
//
// Reference: https://concurrencyfreaks.blogspot.com/2014/04/multi-producer-single-consumer-queue.html
type UnboundedMailbox struct {
	// head and tail are padded to opposite cache lines so concurrent
	// producers and the single consumer do not thrash each other.
	head unsafe.Pointer // *ReceiveContext
	_    CacheLinePadding
	tail unsafe.Pointer // *ReceiveContext
	_    CacheLinePadding
}

// enforces compilation error
var _ Mailbox = (*UnboundedMailbox)(nil)

// NewUnboundedMailbox returns a new, initialized UnboundedMailbox.
// The zero value is not usable.
func NewUnboundedMailbox() *UnboundedMailbox {
	sentinel := new(ReceiveContext)
	return &UnboundedMailbox{
		head: unsafe.Pointer(sentinel),
		tail: unsafe.Pointer(sentinel),
	}
}

// Enqueue appends value to the tail of the mailbox. Safe to call
// concurrently from multiple producers. Always returns nil; the error
// is present to satisfy the Mailbox interface.
//
// The ReceiveContext must not already be linked into any mailbox; its
// `next` field is overwritten.
func (m *UnboundedMailbox) Enqueue(value *ReceiveContext) error {
	atomic.StorePointer(&value.next, nil)
	prev := (*ReceiveContext)(atomic.SwapPointer(&m.tail, unsafe.Pointer(value)))
	atomic.StorePointer(&prev.next, unsafe.Pointer(value))
	return nil
}

// Dequeue removes and returns the next message, or nil when empty.
// Must be called from a single consumer goroutine.
//
// The returned ReceiveContext becomes the new sentinel; the caller must
// not release it — the next Dequeue will. The previous sentinel is
// reset and returned to the shared context pool.
func (m *UnboundedMailbox) Dequeue() *ReceiveContext {
	head := (*ReceiveContext)(atomic.LoadPointer(&m.head))
	next := (*ReceiveContext)(atomic.LoadPointer(&head.next))

	if next == nil {
		return nil
	}

	atomic.StorePointer(&m.head, unsafe.Pointer(next))

	// A sibling worker that observed m.head before the StorePointer
	// above may still hold a pointer to the old head and atomically
	// load its next field via IsEmpty/Len. The reset here must match
	// that with an atomic store.
	head.reset()
	atomic.StorePointer(&head.next, nil)
	select {
	case contextCh <- head:
	default:
	}

	return next
}

// Len returns an approximate number of messages currently in the mailbox.
// O(n) traversal and racy with producers; use only outside hot paths.
func (m *UnboundedMailbox) Len() int64 {
	var count int64
	head := (*ReceiveContext)(atomic.LoadPointer(&m.head))
	current := (*ReceiveContext)(atomic.LoadPointer(&head.next))

	for current != nil {
		count++
		current = (*ReceiveContext)(atomic.LoadPointer(&current.next))
	}

	return count
}

// IsEmpty reports whether the mailbox currently holds no messages.
// The result is a racy snapshot; safe only from the consumer.
func (m *UnboundedMailbox) IsEmpty() bool {
	head := (*ReceiveContext)(atomic.LoadPointer(&m.head))
	next := atomic.LoadPointer(&head.next)
	return next == nil
}

// Dispose is a no-op for UnboundedMailbox; present for interface compliance.
func (m *UnboundedMailbox) Dispose() {}
