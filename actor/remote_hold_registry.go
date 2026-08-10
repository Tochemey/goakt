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
// finishes before user code runs). Its size is bounded by the credit
// windows feeding the actor: admission stops once a window's worth of
// shares is outstanding, so the registry can never outgrow the in-flight
// residency it protects, plus a small retirement lag.
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

// compact retires up to remoteHoldCompactBudget already-released entries
// from the front of the registry. Dispatch order tracks attach order closely
// enough that the front is almost always spent by the time the consumer
// looks; an unreleased front entry (a still-parked message) stops the pass,
// and everything behind it stays bounded by the credit window because
// admission has stopped granting new work.
func (x *remoteHoldRegistry) compact() {
	x.consumerMu.Lock()
	defer x.consumerMu.Unlock()

	for range remoteHoldCompactBudget {
		head := (*remoteHoldNode)(atomic.LoadPointer(&x.head))
		next := (*remoteHoldNode)(atomic.LoadPointer(&head.next))

		if next == nil || !next.share.Released() {
			return
		}

		x.retireHead(head, next)
	}
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
				return
			}

			runtime.Gosched()
			continue
		}

		next.share.Release()
		x.retireHead(head, next)
	}
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
