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

// mpscNode defines a node for the MPSC queue specialized for *ReceiveContext.
type mpscNode struct {
	next atomic.Pointer[mpscNode]
	data *ReceiveContext
}

// Single global pool for mpscNode to avoid per-op allocations.
var mpscNodePool = sync.Pool{New: func() any { return new(mpscNode) }}

// DefaultMailbox is the default unbounded, lock-free mailbox used in goakt.
//
// Concurrency model:
//   - Multi-Producer, Single-Consumer (MPSC): many goroutines may call Enqueue concurrently,
//     but exactly one goroutine must call Dequeue.
//
// Characteristics:
// - FIFO ordering across all producers.
// - Lock-free operations via atomic pointer primitives.
// - Zero allocations per message by reusing nodes with a sync.Pool.
// - IsEmpty is O(1). Len performs a snapshot traversal (O(n)) and is intended for diagnostics.
//
// Notes:
//   - This mailbox is not safe for multiple concurrent Dequeue callers.
//   - Under heavy contention, IsEmpty can briefly report empty between tail swap and link.
//     The algorithm is designed so no messages are lost.
type DefaultMailbox struct {
	// Separate cache lines to avoid false sharing between producers and consumer
	head  atomic.Pointer[mpscNode] // consumer only
	_pad1 [64]byte
	tail  atomic.Pointer[mpscNode] // producers only
	_pad2 [64]byte
}

// enforce compilation error when interface contract changes
var _ Mailbox = (*DefaultMailbox)(nil)

// NewDefaultMailbox creates and initializes a DefaultMailbox instance.
// The mailbox starts with a dummy node so that producers can append by swapping tail
// and linking through the previous node.
func NewDefaultMailbox() *DefaultMailbox {
	dummy := mpscNodePool.Get().(*mpscNode)
	dummy.next.Store(nil)
	dummy.data = nil
	m := &DefaultMailbox{}
	m.head.Store(dummy)
	m.tail.Store(dummy)
	return m
}

// Enqueue places the given value in the mailbox. Never blocks; always returns nil.
// Safe for concurrent calls by multiple producers.
func (m *DefaultMailbox) Enqueue(value *ReceiveContext) error {
	n := mpscNodePool.Get().(*mpscNode)
	n.data = value

	// For MPSC, we can use a simpler approach since only one consumer
	prev := m.tail.Swap(n)
	prev.next.Store(n)
	return nil
}

// Dequeue removes and returns the value at the head of the mailbox.
// Returns nil if the mailbox is empty.
// Must be called by a single consumer goroutine.
func (m *DefaultMailbox) Dequeue() *ReceiveContext {
	head := m.head.Load() // single consumer
	next := head.next.Load()

	if next == nil {
		return nil
	}

	// Single consumer - no synchronization needed
	m.head.Store(next)
	value := next.data

	// Return old head to pool for reuse
	head.next.Store(nil)
	mpscNodePool.Put(head)
	return value
}

// Len returns a best-effort snapshot of the number of messages in the mailbox.
// It performs an O(n) traversal from head to tail with atomic loads.
// The value may be approximate under concurrent producers.
func (m *DefaultMailbox) Len() int64 {
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

// IsEmpty returns true when the mailbox is empty.
// This is an O(1) check and safe under concurrent producers.
func (m *DefaultMailbox) IsEmpty() bool {
	head := m.head.Load()
	return head.next.Load() == nil
}

// Dispose releases resources if needed. No-op for this mailbox.
func (m *DefaultMailbox) Dispose() {}
