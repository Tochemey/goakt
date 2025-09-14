/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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
)

type inboxNode struct {
	value *GrainContext
	next  atomic.Pointer[inboxNode]
}

var inboxNodePool = sync.Pool{New: func() any { return new(inboxNode) }}

type grainMailbox struct {
	// Separate cache lines to avoid false sharing between producers and consumer
	head  atomic.Pointer[inboxNode] // consumer only
	_pad1 [64]byte
	tail  atomic.Pointer[inboxNode] // producers only
	_pad2 [64]byte
}

func newGrainMailbox() *grainMailbox {
	dummy := inboxNodePool.Get().(*inboxNode)
	dummy.next.Store(nil)
	dummy.value = nil
	m := &grainMailbox{}
	m.head.Store(dummy)
	m.tail.Store(dummy)
	return m
}

// Enqueue places the given value in the mailbox
func (m *grainMailbox) Enqueue(value *GrainContext) {
	n := inboxNodePool.Get().(*inboxNode)
	n.value = value

	// For MPSC, we can use a simpler approach since only one consumer
	prev := m.tail.Swap(n)
	prev.next.Store(n)
}

// Dequeue takes the mail from the mailbox
// Returns nil if the mailbox is empty. Can be used in a single consumer (goroutine) only.
func (m *grainMailbox) Dequeue() *GrainContext {
	head := m.head.Load() // single consumer
	next := head.next.Load()

	if next == nil {
		return nil
	}

	// Single consumer - no synchronization needed
	m.head.Store(next)
	value := next.value

	// Return old head to pool for reuse
	head.next.Store(nil)
	mpscNodePool.Put(head)
	return value
}

// Len returns mailbox length
func (m *grainMailbox) Len() int64 {
	// Snapshot traversal: count nodes from head to end.
	h := m.head.Load()
	n := h.next.Load()
	var count int64
	for n != nil {
		count++
		n = n.next.Load()
	}
	return count
}

// IsEmpty returns true when the mailbox is empty
func (m *grainMailbox) IsEmpty() bool {
	head := m.head.Load()
	return head.next.Load() == nil
}
