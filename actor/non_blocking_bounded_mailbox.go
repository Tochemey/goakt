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

	gerrors "github.com/tochemey/goakt/v4/errors"
)

// nbSlot is a single ring-buffer cell. seq gates access so that producers
// and the consumer never touch the same cell concurrently, and ctx holds
// the stored message. The pair fits in a cache line to keep contention on
// a single cell local.
type nbSlot struct {
	seq atomic.Uint64
	ctx *ReceiveContext
}

// NonBlockingBoundedMailbox is a lock-free, bounded multi-producer,
// single-consumer (MPSC) mailbox backed by a fixed-size ring buffer.
//
// It implements the Dmitry Vyukov bounded-queue algorithm: every cell owns
// a sequence number that producers and the consumer advance to hand the cell
// off to one another without locks. Many producers may call Enqueue
// concurrently; a single consumer calls Dequeue.
//
// Characteristics
//   - Bounded capacity: memory is fixed at construction time and never grows.
//     The capacity is rounded up to the next power of two so the ring can mask
//     instead of divide.
//   - Non-blocking: Enqueue never blocks. When the ring is full it returns
//     gerrors.ErrMailboxFull, which the dispatcher routes to the dead-letter
//     stream. The excess message is discarded rather than stalling the
//     producer goroutine.
//   - GC-friendly: no per-message node allocation. Dequeue clears the cell's
//     pointer so a drained message becomes collectable immediately.
//   - FIFO ordering with respect to arrival.
//
// Use this mailbox when you want a hard memory ceiling with drop-on-overflow
// backpressure and no producer blocking. The zero value is not usable; always
// construct via NewNonBlockingBoundedMailbox.
type NonBlockingBoundedMailbox struct {
	// ring and mask are immutable after construction and read-only on the hot
	// path, so they can share a cache line.
	ring []nbSlot
	mask uint64
	_    CacheLinePadding
	// enqueuePos is the next ticket producers claim; it sits on its own cache
	// line so producer CAS traffic does not thrash the consumer.
	enqueuePos atomic.Uint64
	_          CacheLinePadding
	// dequeuePos is the next ticket the consumer claims; padded on both sides
	// to avoid false sharing with enqueuePos and neighboring allocations.
	dequeuePos atomic.Uint64
	_          CacheLinePadding
	// prev is the context handed out by the last Dequeue; the consumer recycles
	// it on the next Dequeue, once the dispatcher has finished processing it.
	prev *ReceiveContext
}

// enforce compilation error
var _ Mailbox = (*NonBlockingBoundedMailbox)(nil)

// NewNonBlockingBoundedMailbox creates a bounded, non-blocking mailbox.
//
// The capacity must be a positive integer and is rounded up to the next power
// of two (with a minimum of two) so the ring buffer can index with a bitmask.
// A capacity of zero or less is treated as the minimum.
func NewNonBlockingBoundedMailbox(capacity int) *NonBlockingBoundedMailbox {
	size := nextPowerOfTwo(capacity)
	ring := make([]nbSlot, size)
	for i := range ring {
		ring[i].seq.Store(uint64(i))
	}

	return &NonBlockingBoundedMailbox{
		ring: ring,
		mask: size - 1,
	}
}

// Enqueue inserts a message into the mailbox.
//
// Semantics
//   - Never blocks.
//   - Returns gerrors.ErrMailboxFull when the ring is at capacity; the message
//     is not stored.
//
// Concurrency
// - Safe for concurrent producers.
func (m *NonBlockingBoundedMailbox) Enqueue(msg *ReceiveContext) error {
	pos := m.enqueuePos.Load()

	for {
		cell := &m.ring[pos&m.mask]
		seq := cell.seq.Load()
		dif := int64(seq) - int64(pos)

		switch {
		case dif == 0:
			if m.enqueuePos.CompareAndSwap(pos, pos+1) {
				cell.ctx = msg
				cell.seq.Store(pos + 1)
				return nil
			}
		case dif < 0:
			return gerrors.ErrMailboxFull
		default:
			pos = m.enqueuePos.Load()
		}
	}
}

// Dequeue removes and returns the next message, or nil when the mailbox is
// empty. FIFO order is preserved. Intended for a single consumer goroutine.
func (m *NonBlockingBoundedMailbox) Dequeue() (msg *ReceiveContext) {
	if m.prev != nil {
		recycleContext(m.prev)
		m.prev = nil
	}

	pos := m.dequeuePos.Load()

	for {
		cell := &m.ring[pos&m.mask]
		seq := cell.seq.Load()
		dif := int64(seq) - int64(pos+1)

		switch {
		case dif == 0:
			if m.dequeuePos.CompareAndSwap(pos, pos+1) {
				msg = cell.ctx
				cell.ctx = nil
				cell.seq.Store(pos + m.mask + 1)
				m.prev = msg
				return msg
			}
		case dif < 0:
			return nil
		default:
			pos = m.dequeuePos.Load()
		}
	}
}

// IsEmpty reports whether the mailbox currently holds no messages. The result
// is a racy snapshot and may change immediately under concurrency.
func (m *NonBlockingBoundedMailbox) IsEmpty() bool {
	return m.Len() == 0
}

// Len returns an approximate number of messages currently in the mailbox. The
// value is a snapshot and may change immediately after the call under
// concurrency.
func (m *NonBlockingBoundedMailbox) Len() int64 {
	enq := m.enqueuePos.Load()
	deq := m.dequeuePos.Load()
	if enq <= deq {
		return 0
	}
	return int64(enq - deq)
}

// Dispose is a no-op for NonBlockingBoundedMailbox; present for interface
// compliance. The backing array is released once the mailbox is unreferenced.
func (m *NonBlockingBoundedMailbox) Dispose() {}

// nextPowerOfTwo returns the smallest power of two greater than or equal to n,
// with a floor of two.
func nextPowerOfTwo(n int) uint64 {
	if n <= 2 {
		return 2
	}

	v := uint64(n - 1)
	v |= v >> 1
	v |= v >> 2
	v |= v >> 4
	v |= v >> 8
	v |= v >> 16
	v |= v >> 32

	return v + 1
}
