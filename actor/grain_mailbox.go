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
	"sync"
	"sync/atomic"

	gerrors "github.com/tochemey/goakt/v3/errors"
)

type grainNode struct {
	value atomic.Pointer[GrainContext]
	next  atomic.Pointer[grainNode]
}

var grainNodePool = sync.Pool{New: func() any { return new(grainNode) }}

type grainMailbox struct {
	head atomic.Pointer[grainNode]
	_    CacheLinePadding
	tail atomic.Pointer[grainNode]
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
	item := new(grainNode)
	mailbox := &grainMailbox{capacity: capacity}
	mailbox.head.Store(item)
	mailbox.tail.Store(item)
	return mailbox
}

// Enqueue places the given GrainContext at the tail of the mailbox.
//
// It is safe for concurrent producers. Ordering is preserved (FIFO).
func (m *grainMailbox) Enqueue(value *GrainContext) error {
	if ok := m.tryEnqueue(value); !ok {
		return gerrors.ErrMailboxFull
	}
	return nil
}

// Dequeue removes and returns the next GrainContext from the mailbox.
//
// It returns nil when the mailbox is empty. Only a single consumer should
// invoke Dequeue concurrently.
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

	value := next.value.Load()
	next.value.Store(nil)

	// Decrement for both modes (bounded producers pre-incremented).
	m.len.Add(-1)

	// Recycle old head.
	head.next.Store(nil)
	head.value.Store(nil)
	grainNodePool.Put(head)

	return value
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

	n := grainNodePool.Get().(*grainNode)
	n.value.Store(value)
	n.next.Store(nil)

	// MPSC link step: swap tail, then link prev.next.
	prev := m.tail.Swap(n)
	prev.next.Store(n)

	// unbounded increments after linking.
	if m.capacity <= 0 {
		m.len.Add(1)
	}

	return true
}
