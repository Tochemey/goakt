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

// embeddedMailbox is the default user mailbox, run directly on two words carried
// inside the PID (mailboxHead and mailboxTail) instead of on a separately
// allocated UnboundedMailbox. It is the same lock-free multi-producer,
// single-consumer FIFO queue: many producers Enqueue concurrently, exactly one
// consumer, the processing turn, Dequeues, and the retained head acts as the
// sentinel node so ordering is FIFO with respect to arrival.
//
// The type is a defined type over PID purely so (*embeddedMailbox)(pid) is a
// zero-allocation pointer reinterpretation: the Mailbox interface value the PID
// hands to runTurn points straight into the PID's own head and tail words, so
// the queue costs no extra heap object and the hot path stays an ordinary
// interface call. Every method operates on x.mailboxHead and x.mailboxTail with
// the identical sync/atomic pointer operations UnboundedMailbox uses, so the
// compiled dispatch is the same instruction sequence on either mailbox.
//
// It is unrelated to pidCompanion and systemQueue, which are separate shrinking
// steps: pidCompanion parks rarely set spawn settings off the PID, systemQueue
// embeds the control-plane queue, and this embeds the user mailbox.
//
// Placement of the two words is owned by the PID layout, not by this file: the
// head sits on the consumer-written line and the tail on the producer-written
// line, which is why they need no cache-line padding of their own (see the PID
// struct). A PID given a custom mailbox never becomes an embeddedMailbox and
// leaves both words nil.
type embeddedMailbox PID

// enforces compilation error
var _ Mailbox = (*embeddedMailbox)(nil)

// Enqueue appends value to the tail of the mailbox. Safe to call
// concurrently from multiple producers. Always returns nil; the error
// is present to satisfy the Mailbox interface.
//
// The ReceiveContext must not already be linked into any mailbox; its
// `next` field is overwritten.
func (x *embeddedMailbox) Enqueue(value *ReceiveContext) error {
	atomic.StorePointer(&value.next, nil)
	prev := (*ReceiveContext)(atomic.SwapPointer(&x.mailboxTail, unsafe.Pointer(value)))
	atomic.StorePointer(&prev.next, unsafe.Pointer(value))
	return nil
}

// Dequeue removes and returns the next message, or nil when empty.
// Must be called from a single consumer goroutine.
//
// The returned ReceiveContext becomes the new sentinel; the caller must
// not release it — the next Dequeue will. The previous sentinel is
// reset and returned to the shared context pool.
func (x *embeddedMailbox) Dequeue() *ReceiveContext {
	head := (*ReceiveContext)(atomic.LoadPointer(&x.mailboxHead))
	next := (*ReceiveContext)(atomic.LoadPointer(&head.next))

	if next == nil {
		return nil
	}

	atomic.StorePointer(&x.mailboxHead, unsafe.Pointer(next))

	// A sibling worker that observed x.mailboxHead before the StorePointer
	// above may still hold a pointer to the old head and atomically
	// load its next field via IsEmpty/Len. The reset here must match
	// that with an atomic store.
	head.reset()
	atomic.StorePointer(&head.next, nil)
	contextPool.put(head)

	return next
}

// Len returns an approximate number of messages currently in the mailbox.
// O(n) traversal and racy with producers; use only outside hot paths.
func (x *embeddedMailbox) Len() int64 {
	var count int64
	head := (*ReceiveContext)(atomic.LoadPointer(&x.mailboxHead))
	current := (*ReceiveContext)(atomic.LoadPointer(&head.next))

	for current != nil {
		count++
		current = (*ReceiveContext)(atomic.LoadPointer(&current.next))
	}

	return count
}

// IsEmpty reports whether the mailbox currently holds no messages.
// The result is a racy snapshot; safe only from the consumer.
func (x *embeddedMailbox) IsEmpty() bool {
	head := (*ReceiveContext)(atomic.LoadPointer(&x.mailboxHead))
	next := atomic.LoadPointer(&head.next)
	return next == nil
}

// Dispose is a no-op for embeddedMailbox; present for interface compliance.
// The mailbox owns no resources of its own: the two words live in the PID and
// the sentinel is recycled through the shared context pool.
func (x *embeddedMailbox) Dispose() {}
