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
	"runtime"

	gods "github.com/Workiva/go-datastructures/queue"

	gerrors "github.com/tochemey/goakt/v4/errors"
)

// BoundedMailbox is a bounded, blocking MPSC mailbox backed by a ring
// buffer.
//
// Characteristics
//   - Bounded capacity: the queue has a fixed size, the requested capacity
//     rounded up to the next power of two by the underlying ring buffer
//     (NewBoundedMailbox(10) holds 16 messages).
//   - Blocking enqueue for local senders: Enqueue blocks when the mailbox is
//     full until space becomes available or the mailbox is disposed. The
//     sender's goroutine spins meanwhile; when the sender is an actor, that is
//     a dispatcher worker.
//   - Non-blocking enqueue for remote messages: a message that arrived over
//     remoting is refused with ErrMailboxFull when the mailbox is full, like a
//     NonBlockingBoundedMailbox, and the runtime routes it to dead letters. Its
//     producer is the connection's inbound dispatcher, and blocking it would
//     stall every receiver sharing the connection.
//   - Non-blocking dequeue: Dequeue returns nil when the mailbox is empty, as
//     the dispatcher requires.
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
// capacity. Capacity must be a positive integer, and is rounded up to the next
// power of two.
//
// Behavior
//   - When the mailbox reaches capacity, Enqueue blocks a local sender until
//     space becomes available (or the mailbox is disposed) and refuses a
//     message that arrived over remoting with ErrMailboxFull.
//   - When the mailbox is empty, Dequeue returns nil.
func NewBoundedMailbox(capacity int) *BoundedMailbox {
	return &BoundedMailbox{
		underlying: gods.NewRingBuffer(uint64(capacity)),
	}
}

// Enqueue inserts a message into the mailbox.
//
// Semantics
//   - A message from a local sender blocks when the mailbox is full until
//     space is available or the mailbox is disposed.
//   - A message that arrived over remoting never blocks: when the mailbox is
//     full it is refused with ErrMailboxFull, and the runtime routes it to
//     dead letters and repays its credit. Blocking here would hold the
//     connection's inbound dispatcher, and with it every receiver sharing
//     the connection.
//   - Returns an error when the mailbox has been disposed or the underlying
//     buffer reports a failure.
//
// Concurrency
// - Safe for concurrent producers.
func (mailbox *BoundedMailbox) Enqueue(msg *ReceiveContext) error {
	if !msg.remote {
		return mailbox.underlying.Put(msg)
	}

	// The ring's Offer reports false when it lost the race for a free slot
	// to another producer as well as when the ring is full. Only a ring seen
	// full refuses the message; a lost race retries, as Put does.
	for {
		ok, err := mailbox.underlying.Offer(msg)
		if err != nil {
			return err
		}

		if ok {
			return nil
		}

		if mailbox.underlying.Len() >= mailbox.underlying.Cap() {
			return gerrors.ErrMailboxFull
		}

		runtime.Gosched()
	}
}

// Dequeue removes and returns the next message from the mailbox, or nil when
// the mailbox is empty. It never blocks: it reads the ring buffer only after
// seeing a message in it.
//
// Semantics
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
// any internal waiters maintained by it. It is final: every later Enqueue
// fails. The actor calls it on a terminal stop only, not when it restarts.
func (mailbox *BoundedMailbox) Dispose() {
	mailbox.underlying.Dispose()
}
