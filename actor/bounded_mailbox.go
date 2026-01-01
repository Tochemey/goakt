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
	gods "github.com/Workiva/go-datastructures/queue"
)

// BoundedMailbox is a bounded, blocking MPSC mailbox backed by a ring
// buffer.
//
// Characteristics
// - Bounded capacity: the queue has a fixed size.
// - Blocking semantics:
//   - Enqueue blocks when the mailbox is full until space becomes available
//     or the mailbox is disposed.
//   - Dequeue blocks when the mailbox is empty until a message is available
//     or the mailbox is disposed.
//
// - Concurrency: safe for multiple producers (MPSC) and a single consumer.
// - FIFO ordering: messages are dequeued in the order they were enqueued.
//
// Use this mailbox when you want strict, blocking backpressure with bounded
// capacity.
type BoundedMailbox struct {
	underlying *gods.RingBuffer
}

// enforce compilation error
var _ Mailbox = (*BoundedMailbox)(nil)

// NewBoundedMailbox creates a new bounded, blocking mailbox with the given
// capacity. Capacity must be a positive integer.
//
// Behavior
//   - When the mailbox reaches capacity, Enqueue blocks until space becomes
//     available (or the mailbox is disposed).
//   - When the mailbox is empty, Dequeue blocks until a message arrives (or the
//     mailbox is disposed).
func NewBoundedMailbox(capacity int) *BoundedMailbox {
	return &BoundedMailbox{
		underlying: gods.NewRingBuffer(uint64(capacity)),
	}
}

// Enqueue inserts a message into the mailbox.
//
// Semantics
//   - Blocks when the mailbox is full until space is available or the mailbox is
//     disposed.
//   - Returns an error when the mailbox has been disposed or the underlying
//     buffer reports a failure.
//
// Concurrency
// - Safe for concurrent producers.
func (mailbox *BoundedMailbox) Enqueue(msg *ReceiveContext) error {
	return mailbox.underlying.Put(msg)
}

// Dequeue removes and returns the next message from the mailbox.
//
// Semantics
//   - Blocks when the mailbox is empty until a message is available or the
//     mailbox is disposed.
//   - FIFO order is preserved.
//
// Concurrency
// - Intended for a single consumer; behavior follows the underlying ring buffer.
func (mailbox *BoundedMailbox) Dequeue() (msg *ReceiveContext) {
	if mailbox.underlying.Len() > 0 {
		item, _ := mailbox.underlying.Get()
		if v, ok := item.(*ReceiveContext); ok {
			return v
		}
	}
	return nil
}

// IsEmpty reports whether the mailbox currently has no messages.
// This check is a snapshot and may change immediately under concurrency.
func (mailbox *BoundedMailbox) IsEmpty() bool {
	return mailbox.underlying.Len() == 0
}

// Len returns the current number of messages in the mailbox.
// The value is a snapshot and may change immediately after the call under
// concurrency.
func (mailbox *BoundedMailbox) Len() int64 {
	return int64(mailbox.underlying.Len())
}

// Dispose releases resources held by the underlying ring buffer and unblocks
// any internal waiters maintained by it. Do not use the mailbox after
// calling Dispose.
func (mailbox *BoundedMailbox) Dispose() {
	mailbox.underlying.Dispose()
}
