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
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/internal/internalpb"
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

// disposeLease drives one received tell frame's lease through the terminal
// disposition selected by variant, mirroring every path handleDuplexData and
// the actor dispatch surface can take. Each variant must repay the frame's full
// cost, so the sender's window is conserved regardless of which one runs.
func disposeLease(lease *CreditLease, variant int) {
	switch variant % 5 {
	case 0:
		// Single-message consume: split into one share and release it.
		shares := lease.Split(1)

		for i := range shares {
			shares[i].Release()
		}

	case 1:
		// Handler ignored the lease entirely (foreign/legacy tell handler):
		// the frame-level grant repays the full cost.
		lease.releaseUnclaimed()

	case 2:
		// Handler panicked immediately after splitting: nothing released, the
		// recovered dispatch surface repays every outstanding share.
		_ = lease.Split(3)
		lease.releaseOutstanding()

	case 3:
		// Batch consume: split across several messages, release each.
		shares := lease.Split(4)

		for i := range shares {
			shares[i].Release()
		}

	case 4:
		// Partial consume then panic: release one share, then the recovered
		// dispatch surface repays the rest via releaseOutstanding (idempotent
		// over the already-released share).
		shares := lease.Split(3)

		if len(shares) > 0 {
			shares[0].Release()
		}

		lease.releaseOutstanding()
	}
}

// TestCreditLeaseConservationUnderConcurrentDispositions is the credit-lease
// leak guard for the release campaign. It streams many windowed tell frames
// through a real credit-negotiated connection pair while the receiver disposes
// each frame's lease through a rotating mix of every terminal path, from
// several goroutines at once. The invariant is conservation: every byte the
// sender debited from its window is repaid, either restored to the sender's
// available window or still pending in the receiver's grant accumulator. If any
// disposition failed to repay, the sum falls below the negotiated window and
// this test fails. Running under -race additionally guards the concurrent
// noteOwnedBytes/flushGrants accounting.
func TestCreditLeaseConservationUnderConcurrentDispositions(t *testing.T) {
	const (
		credits = int64(1 << 16)
		senders = 4
		perSend = 500
		total   = senders * perSend
		payload = 512
	)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	hello := internalpb.Hello_builder{
		Revision:                    CapabilityRevisionCredits,
		MaxFrameSize:                defaultMaxFrameSize,
		MaxMessageSize:              DefaultMaxMessageSize,
		MaxConcurrentLargeTransfers: 4,
		InitialCredits:              uint64(credits),
	}.Build()

	// A generous write timeout keeps the sender parked on a full window rather
	// than failing when the receiver's batched grant has not yet flushed; the
	// in-memory receiver drains fast, so a real stall still surfaces well
	// inside it.
	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), credits,
		withDuplexWriteTimeout(5*time.Second),
		withDuplexNegotiated(hello),
	)
	right := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), credits,
		withDuplexWriteTimeout(5*time.Second),
		withDuplexNegotiated(hello),
	)
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	sendErr := make(chan error, senders)

	for range senders {
		go func() {
			for range perSend {
				frame := Frame{
					Version: ProtocolVersion,
					Type:    FrameTypeData,
					Lane:    LaneControl,
					Payload: make([]byte, payload),
				}

				if err := left.Tell(ctx, frame); err != nil {
					sendErr <- err
					return
				}
			}

			sendErr <- nil
		}()
	}

	var dispatched sync.WaitGroup

	for received := 0; received < total; received++ {
		frame, err := right.Recv(ctx)
		require.NoError(t, err, "receiver stalled after %d frames", received)

		lease := right.newTellLease(frame)
		right.ReleasePayload(frame)

		dispatched.Add(1)
		go func(l *CreditLease, variant int) {
			defer dispatched.Done()
			disposeLease(l, variant)
		}(lease, received)
	}

	dispatched.Wait()

	for range senders {
		require.NoError(t, <-sendErr, "a sender failed before delivering its share")
	}

	// Conservation: available window plus not-yet-flushed grants equals the
	// full negotiated window. A leak on any disposition drops the sum below it.
	require.Eventually(t, func() bool {
		return left.sendWindow.Load()+right.grantAccum.Load() == credits
	}, 5*time.Second, 5*time.Millisecond,
		"window not fully reconciled: sendWindow=%d grantAccum=%d want sum=%d",
		left.sendWindow.Load(), right.grantAccum.Load(), credits)
}
