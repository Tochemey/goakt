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
	"math"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/types"
)

func TestEncodeConsumeCreditPayload(t *testing.T) {
	defer goleak.VerifyNone(t)

	payload := encodeCreditPayload(12345)
	grant, ok := consumeCreditPayload(payload)
	require.True(t, ok)
	assert.Equal(t, uint64(12345), grant)

	_, ok = consumeCreditPayload(nil)
	assert.False(t, ok)

	_, ok = consumeCreditPayload(append(payload, 0x00))
	assert.False(t, ok)
}

func TestApplyCreditGrantRestoresNegativeWindow(t *testing.T) {
	defer goleak.VerifyNone(t)

	left, right := newCreditsPair(t, 64)
	defer closeCreditsPair(t, left, right)

	left.sendWindow.Store(-80)
	left.applyCreditGrant(144)
	assert.Equal(t, int64(64), left.sendWindow.Load())
}

func TestApplyCreditGrantClampsHostileGrant(t *testing.T) {
	defer goleak.VerifyNone(t)

	left, right := newCreditsPair(t, 64)
	defer closeCreditsPair(t, left, right)

	// A maximal grant against a negative window must clamp instead of
	// wrapping the signed counter downward: the window can only grow.
	left.sendWindow.Store(-80)
	left.applyCreditGrant(math.MaxUint64)
	assert.Equal(t, int64(math.MaxInt64)-80, left.sendWindow.Load())
}

func TestCreditDeferredGrantFlushedByWriterDrain(t *testing.T) {
	defer goleak.VerifyNone(t)

	window := int64(400)
	left, right := newCreditsPair(t, window)
	defer closeCreditsPair(t, left, right)

	// Simulate a grant whose CREDIT admit failed on a full writer queue: the
	// accumulator holds a full threshold and no further inbound windowed frame
	// will arrive to retry it (the peer is parked waiting for this credit).
	right.grantMu.Lock()
	right.grantAccum = window
	right.grantMu.Unlock()

	// Any writer drain on the granting side must flush the deferred grant.
	require.True(t, right.trySubmit(Frame{
		Version: ProtocolVersion,
		Type:    FrameTypePing,
		Lane:    right.Lane(),
	}))

	require.Eventually(t, func() bool {
		return left.sendWindow.Load() == window*2
	}, time.Second, 5*time.Millisecond)
}

func TestCreditWindowExhaustionParksAndCreditResumes(t *testing.T) {
	defer goleak.VerifyNone(t)

	payload := []byte("credit-park")
	cost := int64(FrameHeaderSize) + int64(len(payload))
	left, right := newCreditsPair(t, cost)
	defer closeCreditsPair(t, left, right)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(t, left.Tell(ctx, Frame{
		Type:    FrameTypeData,
		Payload: payload,
	}))

	frame, err := right.Recv(ctx)
	require.NoError(t, err)
	assert.Equal(t, payload, frame.Payload)
	right.ReleasePayload(frame)

	assert.Equal(t, int64(0), left.sendWindow.Load())

	second := []byte("second")
	require.NoError(t, left.Tell(ctx, Frame{
		Type:    FrameTypeData,
		Payload: second,
	}))

	// Submit returns on admission; the writer must stay parked until CREDIT.
	// Assert via sender-side counters only — never call Recv here: testify's
	// Never runs the condition in a goroutine and a stray Recv would steal
	// the frame after the grant.
	require.Eventually(t, func() bool {
		return left.outBytes == int64(FrameHeaderSize)+int64(len(second)) && left.sendWindow.Load() == 0
	}, time.Second, 5*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, int64(0), left.sendWindow.Load())
	assert.Equal(t, int64(FrameHeaderSize)+int64(len(second)), left.outBytes)

	right.noteOwnedBytes(cost)

	frame, err = right.Recv(ctx)
	require.NoError(t, err)
	assert.Equal(t, second, frame.Payload)
	right.ReleasePayload(frame)
	right.noteOwnedFrame(frame)
}

func TestCreditExemptFramesBypassParkedWriter(t *testing.T) {
	defer goleak.VerifyNone(t)

	payload := []byte("parked-data")
	cost := int64(FrameHeaderSize) + int64(len(payload))
	left, right := newCreditsPair(t, cost)
	defer closeCreditsPair(t, left, right)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(t, left.Tell(ctx, Frame{
		Type:    FrameTypeData,
		Payload: payload,
	}))
	frame, err := right.Recv(ctx)
	require.NoError(t, err)
	right.ReleasePayload(frame)

	require.NoError(t, left.Submit(ctx, Frame{
		Type:    FrameTypeData,
		Payload: payload,
	}))

	require.Eventually(t, func() bool {
		return left.sendWindow.Load() <= 0 && left.outBytes == cost
	}, time.Second, 5*time.Millisecond)

	before := right.lastInbound.Load()
	require.True(t, left.trySubmit(Frame{
		Type:        FrameTypePing,
		Correlation: 42,
	}))

	require.Eventually(t, func() bool {
		return right.lastInbound.Load() > before
	}, time.Second, 5*time.Millisecond)

	right.noteOwnedBytes(cost)

	frame, err = right.Recv(ctx)
	require.NoError(t, err)
	assert.Equal(t, payload, frame.Payload)
	right.ReleasePayload(frame)
	right.noteOwnedFrame(frame)
}

func TestCreditOversizedFrameAtFullWindow(t *testing.T) {
	defer goleak.VerifyNone(t)

	window := int64(64)
	left, right := newCreditsPair(t, window)
	defer closeCreditsPair(t, left, right)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	payload := make([]byte, 128)
	cost := int64(FrameHeaderSize) + int64(len(payload))
	require.Greater(t, cost, window)

	require.NoError(t, left.Tell(ctx, Frame{
		Type:    FrameTypeData,
		Payload: payload,
	}))

	frame, err := right.Recv(ctx)
	require.NoError(t, err)
	assert.Len(t, frame.Payload, len(payload))
	right.ReleasePayload(frame)

	assert.Less(t, left.sendWindow.Load(), int64(0))

	right.noteOwnedBytes(cost)
	require.Eventually(t, func() bool {
		return left.sendWindow.Load() >= 0
	}, time.Second, 5*time.Millisecond)
}

func TestCreditRevisionThreeIgnoresCredit(t *testing.T) {
	defer goleak.VerifyNone(t)

	left, right := newRevisionPair(t, CapabilityRevisionTables, int64(defaultInitialCredits))
	defer closeCreditsPair(t, left, right)

	assert.False(t, left.creditEnabled)
	assert.False(t, right.creditEnabled)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(t, left.Tell(ctx, Frame{
		Type:    FrameTypeData,
		Payload: []byte("rev3"),
	}))

	frame, err := right.Recv(ctx)
	require.NoError(t, err)
	assert.Equal(t, []byte("rev3"), frame.Payload)
	right.ReleasePayload(frame)

	require.True(t, left.trySubmit(Frame{
		Type:    FrameTypeCredit,
		Payload: encodeCreditPayload(100),
	}))

	recvCtx, recvCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer recvCancel()
	_, err = right.Recv(recvCtx)
	require.Error(t, err)
}

func TestCreditGrantBatchingQuarterWindow(t *testing.T) {
	defer goleak.VerifyNone(t)

	window := int64(400)
	left, right := newCreditsPair(t, window)
	defer closeCreditsPair(t, left, right)

	threshold := window / 4
	right.noteOwnedBytes(threshold - 1)
	assert.Equal(t, threshold-1, right.grantAccum)
	assert.Equal(t, window, left.sendWindow.Load())

	right.noteOwnedBytes(1)
	require.Eventually(t, func() bool {
		return left.sendWindow.Load() == window+threshold
	}, time.Second, 5*time.Millisecond)
	assert.Equal(t, int64(0), right.grantAccum)
}

func TestCreditChunkGrantsPerWireFrame(t *testing.T) {
	defer goleak.VerifyNone(t)

	chunkSize := uint32(64)
	// Keep the window small so CHUNK ownership crosses the quarter-window
	// batching threshold and emits CREDIT without an extra logical grant.
	window := int64(256)
	left, right := newCreditsChunkPair(t, window, chunkSize)
	defer closeCreditsPair(t, left, right)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	payload := make([]byte, int(chunkSize)*3)
	require.NoError(t, left.Tell(ctx, Frame{
		Type:    FrameTypeData,
		Payload: payload,
	}))

	frame, err := right.Recv(ctx)
	require.NoError(t, err)
	assert.Len(t, frame.Payload, len(payload))
	right.ReleasePayload(frame)

	// Batched CREDIT may retain up to a quarter-window of owned bytes in the
	// receiver accumulator; the sender window must still recover into that band.
	require.Eventually(t, func() bool {
		sw := left.sendWindow.Load()
		return sw >= window-window/4 && sw <= window
	}, time.Second, 5*time.Millisecond)
}

func TestCreditBidirectionalExhaustionResumes(t *testing.T) {
	defer goleak.VerifyNone(t)

	payload := []byte("bidir")
	cost := int64(FrameHeaderSize) + int64(len(payload))
	left, right := newCreditsPair(t, cost)
	defer closeCreditsPair(t, left, right)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(t, left.Tell(ctx, Frame{Type: FrameTypeData, Payload: payload}))
	require.NoError(t, right.Tell(ctx, Frame{Type: FrameTypeData, Payload: payload}))

	leftFrame, err := left.Recv(ctx)
	require.NoError(t, err)
	rightFrame, err := right.Recv(ctx)
	require.NoError(t, err)

	left.noteOwnedFrame(leftFrame)
	right.noteOwnedFrame(rightFrame)
	left.ReleasePayload(leftFrame)
	right.ReleasePayload(rightFrame)

	require.Eventually(t, func() bool {
		return left.sendWindow.Load() == cost && right.sendWindow.Load() == cost
	}, time.Second, 5*time.Millisecond)

	require.NoError(t, left.Tell(ctx, Frame{Type: FrameTypeData, Payload: payload}))
	require.NoError(t, right.Tell(ctx, Frame{Type: FrameTypeData, Payload: payload}))

	leftFrame, err = left.Recv(ctx)
	require.NoError(t, err)
	rightFrame, err = right.Recv(ctx)
	require.NoError(t, err)
	left.noteOwnedFrame(leftFrame)
	right.noteOwnedFrame(rightFrame)
	left.ReleasePayload(leftFrame)
	right.ReleasePayload(rightFrame)
}

func TestCreditStalledReceiverBackpressure(t *testing.T) {
	defer goleak.VerifyNone(t)

	payload := []byte("stall")
	cost := int64(FrameHeaderSize) + int64(len(payload))
	left, right := newCreditsPair(t, cost)
	defer closeCreditsPair(t, left, right)

	require.NoError(t, left.Tell(context.Background(), Frame{
		Type:    FrameTypeData,
		Payload: payload,
	}))

	frame, err := right.Recv(context.Background())
	require.NoError(t, err)
	right.ReleasePayload(frame)

	require.NoError(t, left.Tell(context.Background(), Frame{
		Type:    FrameTypeData,
		Payload: payload,
	}))

	require.Eventually(t, func() bool {
		return left.outBytes == cost && left.sendWindow.Load() == 0
	}, time.Second, 5*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err = left.Tell(ctx, Frame{
		Type:    FrameTypeData,
		Payload: payload,
	})
	require.ErrorIs(t, err, ErrDuplexBackpressure)
	assert.LessOrEqual(t, left.outBytes, left.maxOutBytes+cost)

	// Unblock the parked writer so Close can drain without stalling goleak.
	right.noteOwnedBytes(cost)
	frame, err = right.Recv(context.Background())
	require.NoError(t, err)
	right.ReleasePayload(frame)
	right.noteOwnedFrame(frame)
}

func TestCreditMessageLargerThanWindowCompletes(t *testing.T) {
	defer goleak.VerifyNone(t)

	chunkSize := uint32(64)
	window := int64(128)
	left, right := newCreditsChunkPair(t, window, chunkSize)
	defer closeCreditsPair(t, left, right)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	payload := make([]byte, 400)
	for i := range payload {
		payload[i] = byte(i)
	}
	require.Greater(t, int64(len(payload)), window)

	require.NoError(t, left.Tell(ctx, Frame{
		Type:    FrameTypeData,
		Payload: payload,
	}))

	frame, err := right.Recv(ctx)
	require.NoError(t, err)
	assert.Equal(t, payload, frame.Payload)
	right.ReleasePayload(frame)
}

func TestCreditReceiverMemoryBoundedUnderFlood(t *testing.T) {
	defer goleak.VerifyNone(t)

	payload := []byte("flood")
	cost := int64(FrameHeaderSize) + int64(len(payload))
	window := cost * 4
	left, right := newCreditsPair(t, window)
	defer closeCreditsPair(t, left, right)

	// Flood without receiver grants: the writer parks at window exhaustion and
	// Submit then hits admission backpressure. Bytes that reached the peer are
	// bounded by the send window (plus one oversized-frame allowance).
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		err := left.Tell(ctx, Frame{Type: FrameTypeData, Payload: payload})
		cancel()
		if err != nil {
			require.ErrorIs(t, err, ErrDuplexBackpressure)
			break
		}
	}

	assert.LessOrEqual(t, left.outBytes, left.maxOutBytes+cost)
	assert.LessOrEqual(t, left.sendWindow.Load(), int64(0))

	var received int64
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		frame, err := right.Recv(ctx)
		cancel()
		if err != nil {
			break
		}
		received += frameWireCost(frame)
		right.ReleasePayload(frame)
		// Deliberately no grant: measure the unreclaimed receive footprint.
	}

	// Windowed DATA written without grants cannot exceed the send window; with
	// no CHUNK traffic the reassembly buffer stays empty.
	assert.LessOrEqual(t, received, window)
	if right.reassembler != nil {
		right.reassembler.mu.Lock()
		groups := len(right.reassembler.groups)
		right.reassembler.mu.Unlock()
		assert.Zero(t, groups)
	}
}

func TestCreditFairnessTwoSenders(t *testing.T) {
	defer goleak.VerifyNone(t)

	payload := []byte("fair")
	cost := int64(FrameHeaderSize) + int64(len(payload))
	left, right := newCreditsPair(t, cost)
	defer closeCreditsPair(t, left, right)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan types.Unit, 1)
	go func() {
		defer close(done)
		for {
			frame, err := right.Recv(ctx)
			if err != nil {
				return
			}
			right.noteOwnedFrame(frame)
			right.ReleasePayload(frame)
		}
	}()

	const perSender = 20
	var first, second atomic.Int64
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range perSender {
			if err := left.Tell(ctx, Frame{Type: FrameTypeData, Payload: payload}); err != nil {
				errs <- err
				return
			}
			first.Add(1)
		}
	}()
	go func() {
		defer wg.Done()
		for range perSender {
			if err := left.Tell(ctx, Frame{Type: FrameTypeData, Payload: payload}); err != nil {
				errs <- err
				return
			}
			second.Add(1)
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	assert.Equal(t, int64(perSender), first.Load())
	assert.Equal(t, int64(perSender), second.Load())
	cancel()
	<-done
}

func TestCreditDropPathGrantRestoresWindow(t *testing.T) {
	defer goleak.VerifyNone(t)

	payload := []byte("drop-me")
	cost := int64(FrameHeaderSize) + int64(len(payload))
	left, right := newCreditsPair(t, cost)
	defer closeCreditsPair(t, left, right)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(t, left.Tell(ctx, Frame{Type: FrameTypeData, Payload: payload}))
	frame, err := right.Recv(ctx)
	require.NoError(t, err)
	// Decode-error / dead-letter disposition: grant once, then release — no
	// second grant on a later release path.
	right.noteOwnedFrame(frame)
	right.ReleasePayload(frame)

	require.Eventually(t, func() bool {
		return left.sendWindow.Load() == cost
	}, time.Second, 5*time.Millisecond)

	require.NoError(t, left.Tell(ctx, Frame{Type: FrameTypeData, Payload: payload}))
	frame, err = right.Recv(ctx)
	require.NoError(t, err)
	right.noteOwnedFrame(frame)
	right.ReleasePayload(frame)
}

func newCreditsPair(t *testing.T, credits int64) (*duplexConn, *duplexConn) {
	t.Helper()
	return newRevisionPair(t, CapabilityRevisionCredits, credits)
}

func newRevisionPair(t *testing.T, revision uint32, credits int64) (*duplexConn, *duplexConn) {
	t.Helper()

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	hello := &internalpb.Hello{
		Revision:                    revision,
		MaxFrameSize:                defaultMaxFrameSize,
		MaxMessageSize:              DefaultMaxMessageSize,
		MaxConcurrentLargeTransfers: 4,
		InitialCredits:              uint64(credits),
	}

	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), credits,
		withDuplexWriteTimeout(100*time.Millisecond),
		withDuplexNegotiated(hello),
	)
	right := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), credits,
		withDuplexWriteTimeout(100*time.Millisecond),
		withDuplexNegotiated(hello),
	)
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})
	return left, right
}

func newCreditsChunkPair(t *testing.T, credits int64, chunkSize uint32) (*duplexConn, *duplexConn) {
	t.Helper()

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	hello := &internalpb.Hello{
		Revision:                    CapabilityRevisionCredits,
		MaxFrameSize:                defaultMaxFrameSize,
		MaxMessageSize:              DefaultMaxMessageSize,
		MaxConcurrentLargeTransfers: 4,
		InitialCredits:              uint64(credits),
	}

	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), credits,
		withDuplexWriteTimeout(100*time.Millisecond),
		withDuplexChunkSize(chunkSize),
		withDuplexNegotiated(hello),
	)
	right := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), credits,
		withDuplexWriteTimeout(100*time.Millisecond),
		withDuplexChunkSize(chunkSize),
		withDuplexNegotiated(hello),
	)
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})
	return left, right
}

// closeCreditsPair closes both ends before goleak runs (defers run before
// t.Cleanup, so tests must Close explicitly like the other duplex suites).
func closeCreditsPair(t *testing.T, left, right *duplexConn) {
	t.Helper()
	_ = left.Close()
	_ = right.Close()
}
