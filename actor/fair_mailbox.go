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
	"fmt"
	"sync"
	"sync/atomic"
)

// FairMailbox is an unbounded, fair MPSC mailbox for actors that prevents a hot
// sender from monopolizing the receiver. It achieves fairness by routing each
// sender to a dedicated sub‑queue and serving active senders in round‑robin.
//
// Design
//   - Per‑sender sub‑queues: Each distinct sender is assigned a sub‑queue that
//     preserves FIFO ordering for that sender’s messages.
//   - Active senders queue: When a sender transitions from empty → non‑empty, it
//     is added to a global queue of “active senders”. The single consumer dequeues
//     exactly one message per active sender (round‑robin) and re‑queues the sender
//     if there are more messages, preventing starvation.
//
// Guarantees
// - Per‑sender FIFO ordering
// - Cross‑sender fairness (no single sender dominates)
//
// Use cases (actor framework)
//   - Fan‑in actors (aggregators, reducers, topic hubs, router routees) receiving
//     from many producers concurrently.
//   - Multi‑tenant actors where fairness across tenants (senders) is required.
//   - Gateways/bridges (HTTP/gRPC/NATS) where one client can burst without
//     starving other clients’ messages.
type FairMailbox struct {
	// map of sender key -> per‑sender queue
	senders sync.Map // map[string]*senderQ

	// queue of active senders (MPSC)
	active *activeSenders

	length int64
}

// enforce compilation error when interface contract changes
var _ Mailbox = (*FairMailbox)(nil)

// senderQ wraps a per‑sender mailbox and an active flag to avoid duplicate
// entries in the active senders queue.
type senderQ struct {
	q      Mailbox
	active atomic.Bool
}

// activeSenders is a lock‑free MPSC queue specialized for *senderQ entries.
type activeSenders struct {
	head atomic.Pointer[aNode]
	tail atomic.Pointer[aNode]
	pool sync.Pool
}

type aNode struct {
	next atomic.Pointer[aNode]
	val  *senderQ
}

func newActiveSenders() *activeSenders {
	as := &activeSenders{pool: sync.Pool{New: func() any { return new(aNode) }}}
	dummy := as.pool.Get().(*aNode)
	dummy.next.Store(nil)
	dummy.val = nil
	as.head.Store(dummy)
	as.tail.Store(dummy)
	return as
}

func (as *activeSenders) enqueue(sq *senderQ) {
	n := as.pool.Get().(*aNode)
	n.val = sq
	n.next.Store(nil)
	prev := as.tail.Swap(n)
	prev.next.Store(n)
}

func (as *activeSenders) dequeue() *senderQ {
	head := as.head.Load()
	next := head.next.Load()
	if next == nil {
		return nil
	}
	as.head.Store(next)
	v := next.val
	head.next.Store(nil)
	head.val = nil
	as.pool.Put(head)
	return v
}

// NewFairMailbox creates a new FairMailbox.
//
// The mailbox is unbounded: it grows with the number of messages and active
// senders. Choose this mailbox when fairness across senders is more important
// than absolute peak throughput of a single FIFO.
func NewFairMailbox() *FairMailbox {
	return &FairMailbox{active: newActiveSenders()}
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
func (m *FairMailbox) Enqueue(msg *ReceiveContext) error {
	// derive sender key
	key := deriveSenderKey(msg)
	// load or create sender queue
	var sq *senderQ
	if v, ok := m.senders.Load(key); ok {
		sq = v.(*senderQ)
	} else {
		// use DefaultMailbox per sender (unbounded MPSC, thread‑safe)
		nsq := &senderQ{q: NewDefaultMailbox()}
		if actual, loaded := m.senders.LoadOrStore(key, nsq); loaded {
			sq = actual.(*senderQ)
		} else {
			sq = nsq
		}
	}

	_ = sq.q.Enqueue(msg)
	atomic.AddInt64(&m.length, 1)

	// attempt to activate this sender when transitioning from inactive
	if sq.active.CompareAndSwap(false, true) {
		m.active.enqueue(sq)
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
func (m *FairMailbox) Dequeue() *ReceiveContext {
	sq := m.active.dequeue()
	if sq == nil {
		return nil
	}

	msg := sq.q.Dequeue()
	if msg == nil {
		// per‑sender queue was drained concurrently; mark inactive
		sq.active.Store(false)
		return nil
	}

	atomic.AddInt64(&m.length, -1)

	// if still has messages, keep it active (re‑queue); otherwise mark inactive
	if !sq.q.IsEmpty() {
		m.active.enqueue(sq)
	} else {
		sq.active.Store(false)
	}
	return msg
}

// IsEmpty reports whether the mailbox currently has no messages.
//
// This is an O(1) snapshot based on an atomic counter and is best‑effort under
// concurrency. It is intended for observability and fast checks, not for hard
// synchronization.
func (m *FairMailbox) IsEmpty() bool {
	return atomic.LoadInt64(&m.length) == 0
}

// Len returns an approximate number of messages across all sub‑queues.
//
// The value is maintained as an atomic counter and may be approximate under
// concurrency. Use for metrics/observability rather than coordination.
func (m *FairMailbox) Len() int64 {
	return atomic.LoadInt64(&m.length)
}

// Dispose releases resources held by the mailbox. This implementation does not
// spawn background goroutines, so Dispose is a no‑op; messages already enqueued
// will remain until dequeued or garbage‑collected.
func (m *FairMailbox) Dispose() {}

func deriveSenderKey(rc *ReceiveContext) string {
	if s := rc.Sender(); s != nil {
		return fmt.Sprintf("pid:%s", s.ID())
	}
	if rs := rc.RemoteSender(); rs != nil {
		return "remote:" + rs.String()
	}
	return "nosender"
}
