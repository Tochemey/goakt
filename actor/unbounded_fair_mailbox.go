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
	"unsafe"
)

// senderBox wraps a per‑sender mailbox and an active flag to avoid duplicate
// entries in the active senders queue.
type senderBox struct {
	mailbox Mailbox
	active  atomic.Bool
	pending int64
}

type senderNode struct {
	next  unsafe.Pointer
	value atomic.Pointer[senderBox]
}

// activeSenders is a lock‑free MPSC queue specialized for *senderNode entries.
type activeSenders struct {
	head atomic.Pointer[senderNode]
	tail atomic.Pointer[senderNode]
	pool sync.Pool
}

func newActiveSenders() *activeSenders {
	as := &activeSenders{pool: sync.Pool{New: func() any { return new(senderNode) }}}
	dummy := as.pool.Get().(*senderNode)
	atomic.StorePointer(&dummy.next, nil)
	dummy.value.Store(nil)
	as.head.Store(dummy)
	as.tail.Store(dummy)
	return as
}

func (as *activeSenders) enqueue(sq *senderBox) {
	n := as.pool.Get().(*senderNode)
	n.value.Store(sq)
	atomic.StorePointer(&n.next, nil)
	prev := as.tail.Swap(n)
	atomic.StorePointer(&prev.next, unsafe.Pointer(n))
}

func (as *activeSenders) dequeue() *senderBox {
	head := as.head.Load()
	next := (*senderNode)(atomic.LoadPointer(&head.next))
	if next == nil {
		return nil
	}
	as.head.Store(next)
	v := next.value.Load()
	atomic.StorePointer(&head.next, unsafe.Pointer(nil))
	head.value.Store(nil)
	as.pool.Put(head)
	return v
}

// UnboundedFairMailbox is an unbounded, multi-producer/single-consumer mailbox that
// enforces fairness across independent senders by giving each sender its own
// private sub-queue. Messages coming from the same sender remain FIFO-ordered,
// while the actor drains sub-queues in round-robin order so that a chatty sender
// cannot starve quieter peers. This makes the mailbox well-suited for multi-
// tenant actors, protocol handlers that serve many clients, or any actor that
// must provide predictable latency guarantees under bursty workloads.
//
// Compared to the default mailbox (a single global FIFO), UnboundedFairMailbox trades a
// small amount of bookkeeping overhead for stronger isolation. It is
// conceptually similar to "a mailbox where each sender has its sub-queue": the
// distinction is that UnboundedFairMailbox manages the activation lifecycle of those
// sub-queues automatically, re-queuing only the senders that still have pending
// work. Choose it whenever fairness is more important than the absolute
// throughput of a single hot sender. For latency-critical applications that
// prioritize raw throughput, prefer the lighter-weight UnboundedMailbox.
type UnboundedFairMailbox struct {
	// map of sender key -> per‑sender queue
	senders sync.Map // map[string]*senderBox

	// queue of active senders
	active *activeSenders

	length int64
}

// enforces compilation error
var _ Mailbox = (*UnboundedFairMailbox)(nil)

var (
	defaultSenderLoadOrStore = func(m *sync.Map, key string, value any) (any, bool) {
		return m.LoadOrStore(key, value)
	}
	senderLoadOrStoreFn atomic.Pointer[func(*sync.Map, string, any) (any, bool)]
)

func init() {
	senderLoadOrStoreFn.Store(&defaultSenderLoadOrStore)
}

func senderLoadOrStore(m *UnboundedFairMailbox, key string, value any) (any, bool) {
	fn := senderLoadOrStoreFn.Load()
	return (*fn)(&m.senders, key, value)
}

// NewUnboundedFairMailbox creates a new UnboundedFairMailbox instance with all bookkeeping
// structures initialised and ready for concurrent producers. The mailbox is
// unbounded, so memory consumption grows with the number of messages and active
// senders. Prefer this constructor when an actor processes requests from many
// independent peers (local or remote) and must deliver each peer's messages in
// order without letting any one of them monopolise the processing pipeline.
func NewUnboundedFairMailbox() *UnboundedFairMailbox {
	return &UnboundedFairMailbox{active: newActiveSenders()}
}

// Enqueue pushes a message into the mailbox.
//
// Semantics
//   - Per‑sender FIFO: Messages from the same sender are delivered in order.
//   - Activation: The first message into an empty sub‑queue marks the sender
//     active and enqueues it into the active‑senders queue for round‑robin service.
//
// Concurrency
//   - Safe for concurrent producers (many senders). Each sub‑queue is internally
//     synchronized, and the active‑senders queue is an MPSC structure.
func (m *UnboundedFairMailbox) Enqueue(msg *ReceiveContext) error {
	// derive sender key
	key := deriveSenderKey(msg)
	// load or create sender queue
	var sq *senderBox
	if v, ok := m.senders.Load(key); ok {
		sq = v.(*senderBox)
	} else {
		// use DefaultMailbox per sender (unbounded MPSC, thread‑safe)
		nsq := &senderBox{mailbox: NewUnboundedMailbox()}
		if actual, loaded := senderLoadOrStore(m, key, nsq); loaded {
			sq = actual.(*senderBox)
		} else {
			sq = nsq
		}
	}

	_ = sq.mailbox.Enqueue(msg)
	atomic.AddInt64(&m.length, 1)

	if pending := atomic.AddInt64(&sq.pending, 1); pending == 1 {
		// transition from empty -> non-empty, try to activate sender
		if sq.active.CompareAndSwap(false, true) {
			m.active.enqueue(sq)
		}
	}
	return nil
}

// Dequeue fetches one message from the next active sender in round‑robin order.
//
// Semantics
//   - Returns nil when there is currently no active sender.
//   - If a sender remains non‑empty after a pop, it is re‑enqueued to the tail of
//     the active‑senders queue; otherwise it is marked inactive.
//
// Single consumer
// - Must be called by exactly one goroutine (the actor’s receiver loop).
func (m *UnboundedFairMailbox) Dequeue() (msg *ReceiveContext) {
	sq := m.active.dequeue()
	if sq == nil {
		return nil
	}

	msg = sq.mailbox.Dequeue()
	if msg == nil {
		// per‑sender queue was drained concurrently; mark inactive
		sq.active.Store(false)
		return
	}

	atomic.AddInt64(&m.length, -1)
	remaining := atomic.AddInt64(&sq.pending, -1)
	m.finalizeSender(sq, remaining)
	return
}

func (m *UnboundedFairMailbox) finalizeSender(sq *senderBox, remaining int64) {
	if remaining > 0 {
		m.active.enqueue(sq)
		return
	}

	if remaining < 0 {
		atomic.StoreInt64(&sq.pending, 0)
	}

	sq.active.Store(false)
	if atomic.LoadInt64(&sq.pending) > 0 && sq.active.CompareAndSwap(false, true) {
		m.active.enqueue(sq)
	}
}

// IsEmpty reports whether the mailbox currently has no messages.
//
// This is an O(1) snapshot based on an atomic counter and is best‑effort under
// concurrency. It is intended for observability and fast checks, not for hard
// synchronization.
func (m *UnboundedFairMailbox) IsEmpty() bool {
	return atomic.LoadInt64(&m.length) == 0
}

// Len returns an approximate number of messages across all sub‑queues.
//
// The value is maintained as an atomic counter and may be approximate under
// concurrency. Use for metrics/observability rather than coordination.
func (m *UnboundedFairMailbox) Len() int64 {
	return atomic.LoadInt64(&m.length)
}

// Dispose releases resources held by the mailbox. This implementation does not
// spawn background goroutines, so Dispose is a no‑op; messages already enqueued
// will remain until dequeued or garbage‑collected.
func (m *UnboundedFairMailbox) Dispose() {}

func deriveSenderKey(rc *ReceiveContext) string {
	return rc.SenderAddress().String()
}
