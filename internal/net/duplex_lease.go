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

package net

import (
	"context"
	"sync/atomic"
)

// CreditLease defers the credit grant for one windowed inbound tell frame
// until every message the frame carried reaches a terminal state. It extends
// credit-based flow control across receiver-side residency: the sender's
// window is repaid message by message as the receiving actor system consumes
// (or dead-letters) the frame's messages, so a stalled consumer stops the
// grants and senders observe backpressure instead of the receiver absorbing
// messages at wire speed into an unbounded mailbox.
//
// The dispatch surface claims a lease with [CreditLease.Split]; a lease left
// unclaimed when dispatch returns is granted in full by releaseUnclaimed,
// which keeps handlers that know nothing about leases (fixtures, foreign
// tell handlers) at today's grant-at-disposition behavior with no window
// leak.
type CreditLease struct {
	// conn is the connection whose flow-control window the lease repays.
	conn *duplexConn
	// cost is the frame's full wire cost, the exact amount the sender
	// debited from its window when it submitted the frame.
	cost int64
	// claimed flips exactly once, either by Split (the shares then own the
	// grant) or by releaseUnclaimed (the full cost is granted immediately).
	claimed atomic.Bool
	// shares is the apportionment produced by Split, retained so the
	// dispatch surface can repay everything still outstanding after a
	// recovered handler panic. Written by Split and read by
	// releaseOutstanding on the same dispatch goroutine, so it needs no
	// synchronization; the individual shares are themselves atomic.
	shares []CreditShare
}

// CreditShare is one message's portion of a [CreditLease]. Release grants
// the share's bytes back to the sender; it is idempotent and nil-safe so
// every terminal path can call it defensively.
type CreditShare struct {
	// conn is the connection whose flow-control window the share repays.
	conn *duplexConn
	// cost holds the share's unreleased bytes; Release swaps it to zero so
	// the grant happens exactly once.
	cost atomic.Int64
}

// creditLeaseContextKey keys the lease on the dispatch context handed to the
// registered tell handlers.
type creditLeaseContextKey struct{}

// newTellLease mints the credit lease for one inbound windowed tell frame.
// Returns nil when credits are disabled or the frame is a reassembled
// logical frame (its CHUNK frames were each granted at their own
// disposition); callers treat a nil lease as "grant already settled".
func (x *duplexConn) newTellLease(frame Frame) *CreditLease {
	if !x.creditEnabled || frame.Reassembled() || !windowedFrameType(frame.Type) {
		return nil
	}

	return &CreditLease{conn: x, cost: frameWireCost(frame)}
}

// Split claims the lease and apportions its cost across n message shares,
// remainder on the first share, so the shares sum exactly to the frame cost
// the sender debited. Nil-safe: a nil lease (credits disabled, reassembled
// frame, or no lease on the context) returns nil shares, and nil shares
// release as no-ops. Splitting an already-claimed lease returns nil; n <= 0
// grants the full cost immediately.
func (x *CreditLease) Split(n int) []CreditShare {
	if x == nil || !x.claimed.CompareAndSwap(false, true) {
		return nil
	}

	if n <= 0 {
		x.conn.noteOwnedBytes(x.cost)
		return nil
	}

	shares := make([]CreditShare, n)
	each := x.cost / int64(n)

	for i := range shares {
		shares[i].conn = x.conn
		shares[i].cost.Store(each)
	}

	shares[0].cost.Add(x.cost - each*int64(n))
	x.shares = shares
	return shares
}

// releaseOutstanding repays whatever the lease still owes, claimed or not.
// The dispatch surface calls it after a recovered tell-handler panic: a
// panic between Split and the per-share releases would otherwise strand the
// unreleased shares' cost and permanently shrink the sender's window. Share
// releases are idempotent, so repaying shares whose messages already reached
// a terminal state is a no-op. Nil-safe.
func (x *CreditLease) releaseOutstanding() {
	if x == nil {
		return
	}

	if x.claimed.CompareAndSwap(false, true) {
		x.conn.noteOwnedBytes(x.cost)
		return
	}

	for i := range x.shares {
		x.shares[i].Release()
	}
}

// releaseUnclaimed grants the lease's full cost when no handler claimed it,
// restoring grant-at-disposition behavior for dispatch surfaces that do not
// participate in leasing. A claimed lease is a no-op: its shares own the
// grant. Nil-safe.
func (x *CreditLease) releaseUnclaimed() {
	if x == nil {
		return
	}

	if x.claimed.CompareAndSwap(false, true) {
		x.conn.noteOwnedBytes(x.cost)
	}
}

// Released reports whether the share holds no outstanding bytes, either
// because Release ran or because the share was minted with a zero cost.
// Receiver-side bookkeeping uses it to retire tracking entries.
func (x *CreditShare) Released() bool {
	return x.cost.Load() == 0
}

// Release grants the share's bytes back to the sender's flow-control window.
// Idempotent and nil-safe: the first call grants, every later call is a
// no-op, so every terminal path may release defensively. A detached share
// (no connection) only clears its outstanding cost.
func (x *CreditShare) Release() {
	if x == nil {
		return
	}

	if cost := x.cost.Swap(0); cost > 0 && x.conn != nil {
		x.conn.noteOwnedBytes(cost)
	}
}

// DetachedCreditShare returns a share bound to no connection: releasing it
// only clears its outstanding cost. Runtime tests use it to model parked
// remote messages without a live transport.
func DetachedCreditShare(cost int64) *CreditShare {
	share := new(CreditShare)
	share.cost.Store(cost)
	return share
}

// WithCreditLease returns a context carrying lease for the duplex tell
// dispatch surface. A nil lease returns ctx unchanged.
func WithCreditLease(ctx context.Context, lease *CreditLease) context.Context {
	if lease == nil {
		return ctx
	}

	return context.WithValue(ctx, creditLeaseContextKey{}, lease)
}

// CreditLeaseFromContext returns the lease carried by ctx, or nil when the
// dispatch did not mint one.
func CreditLeaseFromContext(ctx context.Context) *CreditLease {
	lease, _ := ctx.Value(creditLeaseContextKey{}).(*CreditLease)
	return lease
}
