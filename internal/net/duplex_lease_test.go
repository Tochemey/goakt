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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// leaseTestConn returns a credit-enabled connection whose grant accumulator
// observes lease releases (grants below the flush threshold accumulate).
func leaseTestConn(t *testing.T) *duplexConn {
	t.Helper()
	left, _ := newCreditsPair(t, 1<<20)
	return left
}

// TestCreditLeaseSplitApportionsExactly verifies the shares sum to the exact
// frame cost the sender debited, remainder on the first share.
func TestCreditLeaseSplitApportionsExactly(t *testing.T) {
	conn := leaseTestConn(t)
	frame := Frame{Type: FrameTypeData, Lane: LaneControl, Length: 100, Payload: make([]byte, 100)}
	cost := frameWireCost(frame)

	lease := conn.newTellLease(frame)
	require.NotNil(t, lease)

	shares := lease.Split(3)
	require.Len(t, shares, 3)

	var sum int64
	for i := range shares {
		sum += shares[i].cost.Load()
	}

	assert.Equal(t, cost, sum, "shares must sum to the debited frame cost")
	assert.GreaterOrEqual(t, shares[0].cost.Load(), shares[1].cost.Load(), "remainder rides the first share")

	before := conn.grantAccum.Load()
	for i := range shares {
		shares[i].Release()
	}

	assert.Equal(t, before+cost, conn.grantAccum.Load(), "every byte must be granted back")
}

// TestCreditShareReleaseIdempotent verifies a share grants exactly once no
// matter how many terminal paths release it defensively.
func TestCreditShareReleaseIdempotent(t *testing.T) {
	conn := leaseTestConn(t)
	lease := conn.newTellLease(Frame{Type: FrameTypeData, Lane: LaneControl, Length: 64, Payload: make([]byte, 64)})
	shares := lease.Split(1)
	require.Len(t, shares, 1)

	before := conn.grantAccum.Load()
	shares[0].Release()
	granted := conn.grantAccum.Load() - before

	shares[0].Release()
	shares[0].Release()
	assert.Equal(t, before+granted, conn.grantAccum.Load(), "repeat releases must not grant again")
}

// TestCreditLeaseUnclaimedGrantsInFull verifies a dispatch surface that never
// claims the lease falls back to the full grant-at-disposition behavior, and
// that a claimed lease's releaseUnclaimed is a no-op.
func TestCreditLeaseUnclaimedGrantsInFull(t *testing.T) {
	conn := leaseTestConn(t)
	frame := Frame{Type: FrameTypeData, Lane: LaneControl, Length: 32, Payload: make([]byte, 32)}
	cost := frameWireCost(frame)

	unclaimed := conn.newTellLease(frame)
	before := conn.grantAccum.Load()
	unclaimed.releaseUnclaimed()
	assert.Equal(t, before+cost, conn.grantAccum.Load(), "unclaimed lease must grant in full")

	claimed := conn.newTellLease(frame)
	shares := claimed.Split(2)
	require.Len(t, shares, 2)

	before = conn.grantAccum.Load()
	claimed.releaseUnclaimed()
	assert.Equal(t, before, conn.grantAccum.Load(), "claimed lease must not double grant")

	assert.Nil(t, claimed.Split(2), "a lease can be claimed only once")
}

// TestCreditLeaseSplitZeroGrantsImmediately verifies an empty batch settles
// the whole cost at claim time.
func TestCreditLeaseSplitZeroGrantsImmediately(t *testing.T) {
	conn := leaseTestConn(t)
	frame := Frame{Type: FrameTypeData, Lane: LaneControl, Length: 32, Payload: make([]byte, 32)}
	cost := frameWireCost(frame)

	lease := conn.newTellLease(frame)
	before := conn.grantAccum.Load()
	require.Nil(t, lease.Split(0))
	assert.Equal(t, before+cost, conn.grantAccum.Load())
}

// TestCreditLeaseNilSafety verifies the nil lease and nil share paths used by
// legacy dispatch and credit-disabled sessions.
func TestCreditLeaseNilSafety(t *testing.T) {
	var lease *CreditLease
	assert.Nil(t, lease.Split(4))
	assert.NotPanics(t, lease.releaseUnclaimed)

	var share *CreditShare
	assert.NotPanics(t, share.Release)

	assert.Nil(t, CreditLeaseFromContext(context.Background()))
	assert.Equal(t, context.Background(), WithCreditLease(context.Background(), nil))
}

// TestNewTellLeaseSkipsReassembledFrames pins the double-grant regression:
// a reassembled logical frame's CHUNK frames were each granted at their own
// disposition, so the logical frame must mint no lease and noteOwnedFrame
// must ignore it.
func TestNewTellLeaseSkipsReassembledFrames(t *testing.T) {
	conn := leaseTestConn(t)

	frame := Frame{Type: FrameTypeData, Lane: LaneControl, Length: 64, Payload: make([]byte, 64)}
	frame.Flags |= frameFlagInternalReassembled

	assert.True(t, frame.Reassembled())
	assert.Nil(t, conn.newTellLease(frame), "reassembled frames must not mint a lease")

	before := conn.grantAccum.Load()
	conn.noteOwnedFrame(frame)
	assert.Equal(t, before, conn.grantAccum.Load(), "reassembled frames must not be granted at disposition")
}

// TestCreditLeaseContextRoundTrip verifies the dispatch-context plumbing.
func TestCreditLeaseContextRoundTrip(t *testing.T) {
	conn := leaseTestConn(t)
	lease := conn.newTellLease(Frame{Type: FrameTypeData, Lane: LaneControl, Length: 8, Payload: make([]byte, 8)})
	require.NotNil(t, lease)

	ctx := WithCreditLease(context.Background(), lease)
	assert.Same(t, lease, CreditLeaseFromContext(ctx))
}

// TestCreditLeaseReleaseOutstanding verifies the panic-path safety net: after
// a recovered tell-handler panic, the dispatch surface repays exactly the
// cost still outstanding, whether the handler claimed the lease or not, and
// without double-granting shares that already released.
func TestCreditLeaseReleaseOutstanding(t *testing.T) {
	conn := leaseTestConn(t)
	frame := Frame{Type: FrameTypeData, Lane: LaneControl, Length: 90, Payload: make([]byte, 90)}
	cost := frameWireCost(frame)

	// Claimed lease with a partial release: a panic mid-batch leaves some
	// shares repaid and the rest stranded. releaseOutstanding must repay the
	// remainder exactly once.
	claimed := conn.newTellLease(frame)
	shares := claimed.Split(3)
	require.Len(t, shares, 3)

	before := conn.grantAccum.Load()
	shares[0].Release()
	claimed.releaseOutstanding()
	assert.Equal(t, before+cost, conn.grantAccum.Load(), "the outstanding remainder must be repaid exactly once")

	claimed.releaseOutstanding()
	assert.Equal(t, before+cost, conn.grantAccum.Load(), "repeat repayment must not grant again")

	// Unclaimed lease: a panic before Split leaves the whole cost pending;
	// releaseOutstanding must grant it in full, and the later
	// releaseUnclaimed on the normal path must then be a no-op.
	unclaimed := conn.newTellLease(frame)
	before = conn.grantAccum.Load()
	unclaimed.releaseOutstanding()
	assert.Equal(t, before+cost, conn.grantAccum.Load(), "an unclaimed lease must be repaid in full")

	unclaimed.releaseUnclaimed()
	assert.Equal(t, before+cost, conn.grantAccum.Load(), "the settled lease must not grant again")

	// Nil lease: credit-disabled and reassembled frames dispatch with no
	// lease; the panic path must tolerate that.
	var none *CreditLease
	assert.NotPanics(t, none.releaseOutstanding)
}
