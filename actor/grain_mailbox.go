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
	"sync/atomic"

	gerrors "github.com/tochemey/goakt/v4/errors"
)

// grainMailbox is a lock-free multi-producer, single-consumer (MPSC)
// FIFO queue used as the per-grain inbox. GrainContext itself serves as
// the intrusive list node via its `next` field, so a GrainContext may
// be linked into only one mailbox at a time.
type grainMailbox struct {
	head atomic.Pointer[GrainContext]
	_    CacheLinePadding
	tail atomic.Pointer[GrainContext]
	_    CacheLinePadding
	// len is the number of enqueued messages. In bounded mode, producers
	// reserve capacity by CAS-incrementing len before linking their node.
	len atomic.Int64

	// capacity:
	//   <= 0 : unbounded
	//   >  0 : bounded to capacity
	capacity int64
}

func newGrainMailbox(capacity int64) *grainMailbox {
	sentinel := new(GrainContext)
	mailbox := &grainMailbox{capacity: capacity}
	mailbox.head.Store(sentinel)
	mailbox.tail.Store(sentinel)
	return mailbox
}

// Enqueue places value at the tail of the mailbox. Safe for concurrent
// producers. Returns ErrMailboxFull when capacity is set and reached.
// The GrainContext must not already be linked into any mailbox; its
// `next` field is overwritten.
func (m *grainMailbox) Enqueue(value *GrainContext) error {
	if ok := m.tryEnqueue(value); !ok {
		return gerrors.ErrMailboxFull
	}
	return nil
}

// Dequeue removes and returns the next GrainContext, or nil when empty.
// Must be called from a single consumer goroutine.
//
// The returned GrainContext becomes the new sentinel; the caller must
// not release it — the next Dequeue will. The previous sentinel is
// reset and returned to the shared GrainContext pool.
func (m *grainMailbox) Dequeue() *GrainContext {
	head := m.head.Load()
	next := head.next.Load()

	// Avoid spurious empty: a producer may have swapped tail but not linked yet.
	if next == nil {
		if head == m.tail.Load() {
			return nil // truly empty
		}

		for next == nil {
			runtime.Gosched()
			next = head.next.Load()
		}
	}

	m.head.Store(next)

	// Decrement for both modes (bounded producers pre-incremented).
	m.len.Add(-1)

	// The previous sentinel (head) is no longer referenced by the mailbox.
	// Reset and recycle into the shared pool so the next producer can skip
	// mallocgc.
	head.next.Store(nil)
	head.reset()
	select {
	case grainContextCh <- head:
	default:
	}

	return next
}

// Len returns the current number of messages enqueued in the mailbox.
func (m *grainMailbox) Len() int64 {
	return m.len.Load()
}

// IsEmpty reports whether the mailbox currently holds no messages.
func (m *grainMailbox) IsEmpty() bool {
	return m.Len() == 0
}

// Capacity returns the mailbox capacity (<=0 means unbounded).
func (m *grainMailbox) Capacity() int64 { return m.capacity }

// tryEnqueue returns false if bounded and full.
func (m *grainMailbox) tryEnqueue(value *GrainContext) bool {
	// reserve capacity first (bounded only) so concurrent producers
	// can't overshoot the bound.
	if m.capacity > 0 {
		for {
			l := m.len.Load()
			if l >= m.capacity {
				return false
			}
			if m.len.CompareAndSwap(l, l+1) {
				break
			}
		}
	}

	value.next.Store(nil)

	// swap tail, then link prev.next.
	prev := m.tail.Swap(value)
	prev.next.Store(value)

	// unbounded increments after linking.
	if m.capacity <= 0 {
		m.len.Add(1)
	}

	return true
}
