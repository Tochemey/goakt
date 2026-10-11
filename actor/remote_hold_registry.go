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
	"unsafe"

	inet "github.com/tochemey/goakt/v4/internal/net"
)

// remoteHoldCompactBudget caps how many spent registry entries one dispatch
// retires, keeping per-message latency smooth when a burst of parked
// messages drains at once. Retirement is amortized: entries retire at least
// as fast as they accumulate over any sustained run.
const remoteHoldCompactBudget = 8

// remoteHoldNode is one tracked credit share in a [remoteHoldRegistry]. It
// references the share, never the pooled ReceiveContext, so context reuse
// can never alias a tracking entry.
type remoteHoldNode struct {
	// next is the intrusive MPSC link, accessed atomically by producers and
	// the single consumer.
	next unsafe.Pointer
	// share is the tracked credit share; nil only on the sentinel.
	share *inet.CreditShare
}

// remoteHoldNodePool recycles registry nodes across all actors.
var remoteHoldNodePool = sync.Pool{New: func() any { return new(remoteHoldNode) }}

// remoteHoldRegistry tracks the flow-control credit shares of remote
// messages currently resident in an actor, independently of whichever
// mailbox implementation holds the messages themselves. It is the internal
// guarantee behind end-to-end backpressure teardown: when the actor is
// reset, every still-held share is granted back to its peer connection, so
// no mailbox (built-in or custom) can strand sender credit.
//
// The structure is the same intrusive MPSC queue the default mailbox uses:
// any goroutine may track (producers CAS the tail). The consumer side
// (compact and releaseAll) is serialized by consumerMu rather than by
// dispatcher ownership, because the shutdown path does not wait out an
// in-flight turn: Shutdown must complete even when the actor is wedged
// inside Receive, and a wedged Receive never holds consumerMu (compact
// finishes before user code runs). Its size is bounded by the number of
// messages still parked in the actor plus a small retirement lag: compact
// unlinks released entries wherever they sit, so a message that a priority
// mailbox leaves waiting behind newer ones pins one entry, not one per
// message dispatched after it.
type remoteHoldRegistry struct {
	// head and tail follow the sentinel scheme of UnboundedMailbox: head
	// points at the last consumed node, tail at the newest.
	head unsafe.Pointer
	_    CacheLinePadding
	tail unsafe.Pointer
	_    CacheLinePadding

	// consumerMu serializes the consumer side: a dispatcher turn's compact
	// against a concurrent shutdown releaseAll. Producers never take it.
	consumerMu sync.Mutex

	// cursor is the parked entry the previous compact pass stopped behind
	// when its budget ran out, or nil when that pass reached the end of the
	// registry. The next pass resumes behind it, so a run of parked entries
	// longer than the budget cannot hide released entries behind it. It is
	// consumer-owned, guarded by consumerMu, and only ever points at a node
	// the consumer has not recycled: a parked entry or the head sentinel.
	cursor *remoteHoldNode
}

// newRemoteHoldRegistry returns an initialized registry.
func newRemoteHoldRegistry() *remoteHoldRegistry {
	sentinel := remoteHoldNodePool.Get().(*remoteHoldNode)
	sentinel.share = nil
	atomic.StorePointer(&sentinel.next, nil)

	return &remoteHoldRegistry{
		head: unsafe.Pointer(sentinel),
		tail: unsafe.Pointer(sentinel),
	}
}

// track records share so teardown can release it if the message it belongs
// to never leaves the mailbox. Safe for concurrent producers.
func (x *remoteHoldRegistry) track(share *inet.CreditShare) {
	node := remoteHoldNodePool.Get().(*remoteHoldNode)
	node.share = share
	atomic.StorePointer(&node.next, nil)

	prev := (*remoteHoldNode)(atomic.SwapPointer(&x.tail, unsafe.Pointer(node)))
	atomic.StorePointer(&prev.next, unsafe.Pointer(node))
}

// compact examines up to remoteHoldCompactBudget entries and retires the
// released ones. It first retires the released prefix, the common case since
// dispatch order follows attach order closely, then resumes behind the cursor
// of the previous pass and unlinks in place every released entry it finds
// behind a still-parked one. The parked entry stays until its own share is
// released, so a message that waits a long time in a priority mailbox,
// behind a steady flow of higher-priority messages, pins one entry and not
// one per message delivered after it. The tail entry is never unlinked: a
// producer may be about to publish its link, and only the head walk is safe
// against that. A pass that reaches the end, or a released tail, clears the
// cursor so the next pass starts from the head again and reaches the entries
// released since it walked past them.
func (x *remoteHoldRegistry) compact() {
	x.consumerMu.Lock()
	defer x.consumerMu.Unlock()

	budget := x.retireReleasedPrefix(remoteHoldCompactBudget)
	if budget == 0 {
		return
	}

	// the prefix walk stopped on a parked or missing entry after the head,
	// so this walk only skips parked entries and unlinks released ones
	// behind them
	prev := x.cursor
	if prev == nil {
		prev = (*remoteHoldNode)(atomic.LoadPointer(&x.head))
	}

	for ; budget > 0; budget-- {
		node := (*remoteHoldNode)(atomic.LoadPointer(&prev.next))
		if node == nil {
			x.cursor = nil
			return
		}

		if !node.share.Released() {
			prev = node
			continue
		}

		next := (*remoteHoldNode)(atomic.LoadPointer(&node.next))
		if next == nil {
			x.cursor = nil
			return
		}

		x.unlink(prev, node, next)
	}

	x.cursor = prev
}

// releaseAll walks the registry, releases every outstanding share, and
// retires the entries. It runs on actor teardown. Releasing is idempotent,
// so shares belonging to messages that survive a restart simply become
// no-ops when those messages are later dispatched; a terminal stop with a
// backlog grants everything back to the peers immediately, whatever mailbox
// held the messages.
func (x *remoteHoldRegistry) releaseAll() {
	x.consumerMu.Lock()
	defer x.consumerMu.Unlock()

	for {
		head := (*remoteHoldNode)(atomic.LoadPointer(&x.head))
		next := (*remoteHoldNode)(atomic.LoadPointer(&head.next))

		if next == nil {
			// An empty-looking chain is only truly drained when the tail has
			// come home: a producer that swung the tail but has not yet
			// published its link is mid-track, and returning now would
			// strand its share in a registry a terminal stop never walks
			// again. The publish is a single store away, so wait it out.
			if (*remoteHoldNode)(atomic.LoadPointer(&x.tail)) == head {
				// every node has been recycled: the next compact starts
				// from the head
				x.cursor = nil
				return
			}

			runtime.Gosched()
			continue
		}

		next.share.Release()
		x.retireHead(head, next)
	}
}

// retireReleasedPrefix retires released entries from the front of the
// registry, at most budget of them, and returns the budget left. It stops at
// the first parked entry or at the end. A retired entry that the cursor
// pointed at, a parked entry whose release made it the head sentinel, drops
// the cursor so it never names a recycled node. Callers must hold consumerMu.
func (x *remoteHoldRegistry) retireReleasedPrefix(budget int) int {
	for ; budget > 0; budget-- {
		head := (*remoteHoldNode)(atomic.LoadPointer(&x.head))
		next := (*remoteHoldNode)(atomic.LoadPointer(&head.next))

		if next == nil || !next.share.Released() {
			break
		}

		if x.cursor == head {
			x.cursor = nil
		}

		x.retireHead(head, next)
	}

	return budget
}

// retireHead advances the head past next and recycles the outgoing sentinel.
// Callers must hold consumerMu.
func (x *remoteHoldRegistry) retireHead(head, next *remoteHoldNode) {
	atomic.StorePointer(&x.head, unsafe.Pointer(next))
	next.share = nil
	head.share = nil
	atomic.StorePointer(&head.next, nil)
	remoteHoldNodePool.Put(head)
}

// unlink removes node, which sits between prev and next, and recycles it.
// node must not be the tail: next is its published successor, so no producer
// will touch node again, and prev.next is written by the consumer alone.
// Callers must hold consumerMu.
func (x *remoteHoldRegistry) unlink(prev, node, next *remoteHoldNode) {
	atomic.StorePointer(&prev.next, unsafe.Pointer(next))
	node.share = nil
	atomic.StorePointer(&node.next, nil)
	remoteHoldNodePool.Put(node)
}
