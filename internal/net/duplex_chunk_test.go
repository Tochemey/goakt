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
	"crypto/sha256"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/size"
)

func TestDuplexChunkedTellRoundTrip(t *testing.T) {
	defer goleak.VerifyNone(t)

	left, right := newChunkingPair(t, 1024, DefaultMaxMessageSize, 4)
	defer func() {
		_ = left.Close()
		_ = right.Close()
	}()

	payload := make([]byte, 1*size.MB)
	for i := range payload {
		payload[i] = byte(i)
	}
	want := sha256.Sum256(payload)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	require.NoError(t, left.Tell(ctx, Frame{
		Type:    FrameTypeData,
		Lane:    LaneControl,
		Payload: payload,
	}))

	got, err := right.Recv(ctx)
	require.NoError(t, err)
	assert.Equal(t, FrameTypeData, got.Type)
	assert.Equal(t, want, sha256.Sum256(got.Payload))
}

func TestDuplexChunkedAskRoundTrip(t *testing.T) {
	defer goleak.VerifyNone(t)

	left, right := newChunkingPair(t, 1024, DefaultMaxMessageSize, 4)
	defer func() {
		_ = left.Close()
		_ = right.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go func() {
		req, err := right.Recv(ctx)
		if err != nil {
			return
		}
		_ = right.Submit(ctx, Frame{
			Type:        FrameTypeReply,
			Lane:        LaneControl,
			Correlation: req.Correlation,
			Payload:     []byte("pong-payload-that-is-also-fairly-long-to-force-chunking-ABCDEFGHIJKLMNOPQRSTUVWXYZ"),
		})
	}()

	// Force chunking on the request with a large payload.
	resp, err := left.Ask(ctx, Frame{
		Type:    FrameTypeData,
		Lane:    LaneControl,
		Payload: make([]byte, 3000),
	})
	require.NoError(t, err)
	assert.Equal(t, FrameTypeReply, resp.Type)
	assert.Contains(t, string(resp.Payload), "pong-payload")
}

func TestDuplexChunkSoftRejectCompletesAsk(t *testing.T) {
	defer goleak.VerifyNone(t)

	left, right := newChunkingPair(t, 512, 1024, 4)
	defer func() {
		_ = left.Close()
		_ = right.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := left.Ask(ctx, Frame{
		Type:    FrameTypeData,
		Lane:    LaneControl,
		Payload: make([]byte, 4000),
	})
	require.Error(t, err)
}

func TestDuplexChunkInboundFullBlocksUntilDrained(t *testing.T) {
	defer goleak.VerifyNone(t)

	left, right := newChunkingPair(t, 512, DefaultMaxMessageSize, 4)
	defer func() {
		_ = left.Close()
		_ = right.Close()
	}()

	for i := 0; i < cap(right.inbound); i++ {
		right.inbound <- Frame{Type: FrameTypeData, Payload: []byte{byte(i)}}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(t, left.Tell(ctx, Frame{
		Type:    FrameTypeData,
		Lane:    LaneControl,
		Payload: make([]byte, 4000),
	}))

	// The reassembled frame waits for queue space; a full inbound queue is
	// local consumer slowness, not a peer protocol violation.
	time.Sleep(50 * time.Millisecond)
	require.False(t, right.IsClosed())

	var got Frame
	require.Eventually(t, func() bool {
		frame, err := right.Recv(ctx)
		if err != nil {
			return false
		}

		if len(frame.Payload) == 4000 {
			got = frame
			return true
		}

		return false
	}, 5*time.Second, time.Millisecond)
	assert.Equal(t, FrameTypeData, got.Type)
	require.False(t, right.IsClosed())
}

func TestSoftRejectChunkFailsTransportWhenErrorNotAdmitted(t *testing.T) {
	defer goleak.VerifyNone(t)

	// ERROR is admission-exempt for the byte cap, so the only way soft-reject
	// cannot admit is a full writer queue. Kill the writer first so nothing
	// drains `out`, then fill the channel to capacity.
	framed := &writeErrFramedConn{err: assert.AnError, closed: make(chan struct{})}
	conn := newDuplexConn(framed, FrameHeaderSize)
	defer func() { _ = conn.Close() }()

	require.True(t, conn.trySubmit(Frame{
		Version: ProtocolVersion,
		Type:    FrameTypePing,
		Lane:    conn.Lane(),
	}))
	require.Eventually(t, func() bool {
		return conn.writeErr.Load() != nil
	}, time.Second, time.Millisecond)

	for conn.trySubmit(Frame{
		Version: ProtocolVersion,
		Type:    FrameTypePing,
		Lane:    conn.Lane(),
	}) {
	}

	// trySubmit can return false on a contended mutex; require sustained
	// failures so the queue is actually full when soft-reject runs.
	require.Eventually(t, func() bool {
		for range 32 {
			if conn.trySubmit(Frame{
				Version: ProtocolVersion,
				Type:    FrameTypePing,
				Lane:    conn.Lane(),
			}) {
				return false
			}
		}
		return true
	}, time.Second, time.Millisecond)

	conn.softRejectChunk(7, "max concurrent large transfers exceeded")

	require.Eventually(t, func() bool {
		return conn.IsClosed()
	}, 2*time.Second, 10*time.Millisecond)
}

func TestDuplexRevisionOneRejectsInboundChunk(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	baseline := &internalpb.Hello{
		Revision:                    CapabilityRevisionBaseline,
		MaxFrameSize:                defaultMaxFrameSize,
		MaxMessageSize:              DefaultMaxMessageSize,
		MaxConcurrentLargeTransfers: 4,
	}
	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), int64(defaultMaxFrameSize),
		withDuplexChunkSize(1024),
		withDuplexNegotiated(baseline),
	)
	right := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), int64(defaultMaxFrameSize),
		withDuplexChunkSize(1024),
		withDuplexNegotiated(baseline),
	)
	defer func() {
		_ = left.Close()
		_ = right.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, left.submitRaw(ctx, Frame{
		Type:        FrameTypeChunk,
		Flags:       FrameFlagFirstChunk | FrameFlagLastChunk,
		Lane:        LaneControl,
		Correlation: 1,
		Payload:     encodeChunkPayload(0, 32, true, make([]byte, 16)),
	}))

	require.Eventually(t, func() bool {
		return right.IsClosed()
	}, 2*time.Second, 10*time.Millisecond)
}

func TestDuplexRevisionOneOversizeFailsFast(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	baseline := &internalpb.Hello{
		Revision:                    CapabilityRevisionBaseline,
		MaxFrameSize:                2048,
		MaxMessageSize:              2048,
		MaxConcurrentLargeTransfers: 4,
	}
	left := newDuplexConn(newTCPFramedConn(c1, 2048), 64*1024,
		withDuplexChunkSize(512),
		withDuplexNegotiated(baseline),
	)
	right := newDuplexConn(newTCPFramedConn(c2, 2048), 64*1024,
		withDuplexChunkSize(512),
		withDuplexNegotiated(baseline),
	)
	defer func() {
		_ = left.Close()
		_ = right.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := left.Tell(ctx, Frame{
		Type:    FrameTypeData,
		Lane:    LaneControl,
		Payload: make([]byte, 4000),
	})
	require.ErrorIs(t, err, ErrMessageTooLarge)
}

func TestDuplexChunkSenderGating(t *testing.T) {
	defer goleak.VerifyNone(t)

	left, right := newChunkingPair(t, 256, DefaultMaxMessageSize, 1)
	defer func() {
		_ = left.Close()
		_ = right.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// Hold the sole large-transfer slot so a concurrent chunked send must wait
	// and then fail when its deadline expires.
	require.NoError(t, left.acquireLarge(ctx))
	defer left.releaseLarge()

	shortCtx, shortCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer shortCancel()

	err := left.Tell(shortCtx, Frame{
		Type:    FrameTypeData,
		Lane:    LaneControl,
		Payload: make([]byte, 8000),
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDuplexBackpressure)
}

func TestDuplexChunkAbortFreesSlot(t *testing.T) {
	defer goleak.VerifyNone(t)

	left, right := newChunkingPair(t, 1024, DefaultMaxMessageSize, 1)
	defer func() {
		_ = left.Close()
		_ = right.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	logical, err := encodeLogicalFrame(Frame{
		Type:    FrameTypeData,
		Lane:    LaneControl,
		Payload: make([]byte, 3000),
	})
	require.NoError(t, err)

	chunks, err := splitLogicalChunks(logical, 7, LaneControl, 1024, false)
	require.NoError(t, err)
	require.Greater(t, len(chunks), 1)

	require.NoError(t, left.submitRaw(ctx, chunks[0]))
	require.Eventually(t, func() bool {
		right.reassembler.mu.Lock()
		defer right.reassembler.mu.Unlock()
		return len(right.reassembler.groups) == 1
	}, time.Second, 5*time.Millisecond)

	left.emitChunkAbort(ctx, 7, 1)
	require.Eventually(t, func() bool {
		right.reassembler.mu.Lock()
		defer right.reassembler.mu.Unlock()
		return len(right.reassembler.groups) == 0
	}, time.Second, 5*time.Millisecond)

	// Slot freed under maxConcurrent=1: a later transfer must succeed.
	require.NoError(t, left.Tell(ctx, Frame{
		Type:    FrameTypeData,
		Lane:    LaneControl,
		Payload: make([]byte, 3000),
	}))

	got, err := right.Recv(ctx)
	require.NoError(t, err)
	assert.Equal(t, FrameTypeData, got.Type)
}

func TestDuplexChunkedTellOrdering(t *testing.T) {
	defer goleak.VerifyNone(t)

	left, right := newChunkingPair(t, 1024, DefaultMaxMessageSize, 4)
	defer func() {
		_ = left.Close()
		_ = right.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(t, left.Tell(ctx, Frame{Type: FrameTypeData, Lane: LaneControl, Payload: []byte("a")}))
	require.NoError(t, left.Tell(ctx, Frame{Type: FrameTypeData, Lane: LaneControl, Payload: make([]byte, 3000)}))
	require.NoError(t, left.Tell(ctx, Frame{Type: FrameTypeData, Lane: LaneControl, Payload: []byte("c")}))

	first, err := right.Recv(ctx)
	require.NoError(t, err)
	assert.Equal(t, []byte("a"), first.Payload)

	second, err := right.Recv(ctx)
	require.NoError(t, err)
	assert.Len(t, second.Payload, 3000)

	third, err := right.Recv(ctx)
	require.NoError(t, err)
	assert.Equal(t, []byte("c"), third.Payload)
}

func TestDuplexChunkedTellRoundTrip20MiB(t *testing.T) {
	defer goleak.VerifyNone(t)

	left, right := newChunkingPair(t, DefaultChunkSize, 32*size.MB, 4)
	defer func() {
		_ = left.Close()
		_ = right.Close()
	}()

	payload := make([]byte, 20*size.MB)
	for i := range payload {
		payload[i] = byte(i)
	}
	want := sha256.Sum256(payload)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	require.NoError(t, left.Tell(ctx, Frame{
		Type:    FrameTypeData,
		Lane:    LaneControl,
		Payload: payload,
	}))

	got, err := right.Recv(ctx)
	require.NoError(t, err)
	assert.Equal(t, want, sha256.Sum256(got.Payload))
}

func TestDuplexMixedRevisionWholeFrame(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	hello := &internalpb.Hello{
		Revision:                    CapabilityRevisionBaseline,
		MaxFrameSize:                defaultMaxFrameSize,
		MaxMessageSize:              DefaultMaxMessageSize,
		MaxConcurrentLargeTransfers: 4,
	}
	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), int64(defaultMaxFrameSize),
		withDuplexChunkSize(DefaultChunkSize),
		withDuplexNegotiated(hello),
	)
	right := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), int64(defaultMaxFrameSize),
		withDuplexChunkSize(DefaultChunkSize),
		withDuplexNegotiated(hello),
	)
	defer func() {
		_ = left.Close()
		_ = right.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	payload := make([]byte, 64*1024)
	require.NoError(t, left.Tell(ctx, Frame{
		Type:    FrameTypeData,
		Lane:    LaneControl,
		Payload: payload,
	}))

	got, err := right.Recv(ctx)
	require.NoError(t, err)
	assert.Equal(t, payload, got.Payload)
}

func TestDuplexChunked100MiBNoOrdinaryLatencyImpact(t *testing.T) {
	defer goleak.VerifyNone(t)

	const (
		largePayload  = 100 * size.MB
		samples       = 64
		latencyBudget = 5 * time.Millisecond
	)

	largeLeft, largeRight := newChunkingPair(t, DefaultChunkSize, 128*size.MB, 4)
	ordLeft, ordRight := newChunkingPair(t, DefaultChunkSize, DefaultMaxMessageSize, 4)
	defer func() {
		_ = largeLeft.Close()
		_ = largeRight.Close()
		_ = ordLeft.Close()
		_ = ordRight.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	baseline := measureTellLatencies(ctx, t, ordLeft, ordRight, samples)
	baselineP99 := percentile(baseline, 0.99)

	payload := make([]byte, largePayload)
	for i := range payload {
		payload[i] = byte(i * 31)
	}
	want := sha256.Sum256(payload)

	// Start the 100 MiB send without a receiver so the large lane saturates its
	// outbound credit window while ordinary-lane latency is sampled.
	tellErr := make(chan error, 1)
	go func() {
		tellErr <- largeLeft.Tell(ctx, Frame{
			Type:    FrameTypeData,
			Lane:    LaneLarge,
			Payload: payload,
		})
	}()
	time.Sleep(50 * time.Millisecond)

	underLoad := measureTellLatencies(ctx, t, ordLeft, ordRight, samples)
	underLoadP99 := percentile(underLoad, 0.99)

	recvErr := make(chan error, 1)
	var got Frame
	go func() {
		var err error
		got, err = largeRight.Recv(ctx)
		recvErr <- err
	}()

	require.NoError(t, <-tellErr)
	require.NoError(t, <-recvErr)
	assert.Equal(t, want, sha256.Sum256(got.Payload))

	assert.LessOrEqual(t, underLoadP99, latencyBudget,
		"ordinary-lane p99 under 100 MiB load %v exceeds budget %v (baseline p99 %v)", underLoadP99, latencyBudget, baselineP99)
	assert.LessOrEqual(t, underLoadP99, baselineP99+latencyBudget,
		"ordinary-lane p99 under load %v rose too far above baseline %v", underLoadP99, baselineP99)
}

func TestSplitLogicalChunksAllocsIndependentOfChunkCount(t *testing.T) {
	logical, err := encodeLogicalFrame(Frame{
		Type:    FrameTypeData,
		Payload: make([]byte, 1*size.MB),
	})
	require.NoError(t, err)

	allocs := func(chunkSize uint32) float64 {
		return testing.AllocsPerRun(50, func() {
			frames, err := splitLogicalChunks(logical, 1, LaneOrdinary, chunkSize, false)
			if err != nil {
				t.Fatal(err)
			}
			_ = frames
		})
	}

	coarse := allocs(256 * size.KB)
	fine := allocs(16 * size.KB)
	assert.InDelta(t, coarse, fine, 1.0,
		"split allocs must not grow with chunk count: coarse=%v fine=%v", coarse, fine)
	assert.LessOrEqual(t, coarse, 3.0, "expected frames slice + prefix arena only, got %v", coarse)
}

func TestReassemblyAllocatesOneBufferPerMessage(t *testing.T) {
	logical, err := encodeLogicalFrame(Frame{
		Type:    FrameTypeData,
		Payload: make([]byte, 3000),
	})
	require.NoError(t, err)

	frames, err := splitLogicalChunks(logical, 1, LaneOrdinary, 1024, false)
	require.NoError(t, err)
	require.Greater(t, len(frames), 1)

	// Materialize wire bodies so Push sees Prefix-empty frames (as on the
	// real receive path) and chunkWireBody does not allocate per call.
	wire := make([]Frame, len(frames))
	for i, frame := range frames {
		body := chunkWireBody(frame)
		wire[i] = Frame{
			Type:        frame.Type,
			Flags:       frame.Flags,
			Lane:        frame.Lane,
			Length:      uint32(len(body)),
			Correlation: frame.Correlation,
			Payload:     body,
		}
	}

	firstAllocs := testing.AllocsPerRun(20, func() {
		r := newChunkReassembler(DefaultMaxMessageSize, 4)
		_ = r.Push(wire[0])
	})
	assert.GreaterOrEqual(t, firstAllocs, 1.0, "first chunk must allocate the reassembly buffer")

	fullAllocs := testing.AllocsPerRun(20, func() {
		r := newChunkReassembler(DefaultMaxMessageSize, 4)
		for _, frame := range wire {
			_ = r.Push(frame)
		}
	})

	// Continuation chunks append in place: completing the group must not add
	// a per-chunk allocation on top of the first-chunk buffer.
	assert.InDelta(t, firstAllocs, fullAllocs, 2.0,
		"full reassembly allocs %v must stay near first-chunk allocs %v", fullAllocs, firstAllocs)
}

func TestControlLaneLatencyDuringLargeChunkTransfer(t *testing.T) {
	defer goleak.VerifyNone(t)

	const latencyBudget = 5 * time.Millisecond

	largeLeft, largeRight := newChunkingPair(t, DefaultChunkSize, 64*size.MB, 4)
	ctrlLeft, ctrlRight := newChunkingPair(t, DefaultChunkSize, DefaultMaxMessageSize, 4)
	defer func() {
		_ = largeLeft.Close()
		_ = largeRight.Close()
		_ = ctrlLeft.Close()
		_ = ctrlRight.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	payload := make([]byte, 20*size.MB)
	tellErr := make(chan error, 1)
	go func() {
		tellErr <- largeLeft.Tell(ctx, Frame{
			Type:    FrameTypeData,
			Lane:    LaneLarge,
			Payload: payload,
		})
	}()
	time.Sleep(50 * time.Millisecond)

	latencies := measureAskLatencies(ctx, t, ctrlLeft, ctrlRight, 32)
	p99 := percentile(latencies, 0.99)

	go func() { _, _ = largeRight.Recv(ctx) }()
	require.NoError(t, <-tellErr)
	assert.LessOrEqual(t, p99, latencyBudget,
		"control-lane ask p99 %v during large transfer exceeds budget %v", p99, latencyBudget)
}

func BenchmarkSplitLogicalChunksAllocs(b *testing.B) {
	logical, err := encodeLogicalFrame(Frame{
		Type:    FrameTypeData,
		Payload: make([]byte, 1*size.MB),
	})
	require.NoError(b, err)

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		frames, err := splitLogicalChunks(logical, 1, LaneOrdinary, DefaultChunkSize, false)
		if err != nil {
			b.Fatal(err)
		}
		_ = frames
	}
}

func BenchmarkDuplexChunkedTellAllocs(b *testing.B) {
	left, right := newChunkingPairB(b, DefaultChunkSize, 32*size.MB, 4)
	defer func() {
		_ = left.Close()
		_ = right.Close()
	}()

	payload := make([]byte, 1*size.MB)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		if err := left.Tell(ctx, Frame{
			Type:    FrameTypeData,
			Lane:    LaneLarge,
			Payload: payload,
		}); err != nil {
			b.Fatal(err)
		}

		frame, err := right.Recv(ctx)
		if err != nil {
			b.Fatal(err)
		}

		right.ReleasePayload(frame)
	}
}

func BenchmarkDuplexChunked100MiB(b *testing.B) {
	left, right := newChunkingPairB(b, DefaultChunkSize, 128*size.MB, 4)
	defer func() {
		_ = left.Close()
		_ = right.Close()
	}()

	payload := make([]byte, 100*size.MB)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		if err := left.Tell(ctx, Frame{
			Type:    FrameTypeData,
			Lane:    LaneLarge,
			Payload: payload,
		}); err != nil {
			b.Fatal(err)
		}
		if _, err := right.Recv(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func measureTellLatencies(ctx context.Context, t *testing.T, left, right *duplexConn, samples int) []time.Duration {
	t.Helper()

	out := make([]time.Duration, samples)
	for i := range out {
		start := time.Now()
		require.NoError(t, left.Tell(ctx, Frame{
			Type:    FrameTypeData,
			Lane:    LaneOrdinary,
			Payload: []byte("x"),
		}))
		_, err := right.Recv(ctx)
		require.NoError(t, err)
		out[i] = time.Since(start)
	}
	return out
}

func measureAskLatencies(ctx context.Context, t *testing.T, left, right *duplexConn, samples int) []time.Duration {
	t.Helper()

	replyCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	replyDone := make(chan struct{})
	go func() {
		defer close(replyDone)
		for {
			req, err := right.Recv(replyCtx)
			if err != nil {
				return
			}
			_ = right.Submit(ctx, Frame{
				Type:        FrameTypeReply,
				Lane:        LaneControl,
				Correlation: req.Correlation,
				Payload:     []byte("ok"),
			})
		}
	}()

	out := make([]time.Duration, samples)
	for i := range out {
		start := time.Now()
		_, err := left.Ask(ctx, Frame{
			Type:    FrameTypeData,
			Lane:    LaneControl,
			Payload: []byte("ping"),
		})
		require.NoError(t, err)
		out[i] = time.Since(start)
	}

	cancel()
	<-replyDone
	return out
}

func percentile(samples []time.Duration, p float64) time.Duration {
	if len(samples) == 0 {
		return 0
	}

	sorted := append([]time.Duration(nil), samples...)
	for i := 1; i < len(sorted); i++ {
		v := sorted[i]
		j := i
		for j > 0 && sorted[j-1] > v {
			sorted[j] = sorted[j-1]
			j--
		}
		sorted[j] = v
	}

	idx := int(float64(len(sorted)-1) * p)
	if idx < 0 {
		idx = 0
	}
	return sorted[idx]
}

func newChunkingPair(t *testing.T, chunkSize uint32, maxMsg uint64, maxConcurrent uint32) (*duplexConn, *duplexConn) {
	t.Helper()
	left, right := openChunkingPair(chunkSize, maxMsg, maxConcurrent)
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})
	return left, right
}

func newChunkingPairB(b *testing.B, chunkSize uint32, maxMsg uint64, maxConcurrent uint32) (*duplexConn, *duplexConn) {
	b.Helper()
	left, right := openChunkingPair(chunkSize, maxMsg, maxConcurrent)
	b.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})
	return left, right
}

func openChunkingPair(chunkSize uint32, maxMsg uint64, maxConcurrent uint32) (*duplexConn, *duplexConn) {
	c1, c2 := net.Pipe()

	hello := &internalpb.Hello{
		Revision:                    CapabilityRevisionChunking,
		MaxFrameSize:                defaultMaxFrameSize,
		MaxMessageSize:              maxMsg,
		MaxConcurrentLargeTransfers: maxConcurrent,
	}

	// Size the outbound queue to the credit window so large transfers stream.
	credits := int64(defaultInitialCredits)
	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), credits,
		withDuplexChunkSize(chunkSize),
		withDuplexNegotiated(hello),
	)
	right := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), credits,
		withDuplexChunkSize(chunkSize),
		withDuplexNegotiated(hello),
	)
	return left, right
}

func TestDuplexChunkSizeClampedToNegotiatedFrameLimit(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	// The peer negotiated a frame limit below the local chunk threshold; the
	// session must clamp so no chunk frame can exceed what the peer accepts.
	negotiated := &internalpb.Hello{
		Revision:                    CapabilityRevisionChunking,
		MaxFrameSize:                32 * 1024,
		MaxMessageSize:              DefaultMaxMessageSize,
		MaxConcurrentLargeTransfers: 4,
	}
	conn := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), int64(defaultMaxFrameSize),
		withDuplexChunkSize(1024*1024),
		withDuplexNegotiated(negotiated),
	)
	defer func() { _ = conn.Close() }()

	assert.Equal(t, uint32(32*1024), conn.ChunkSize())

	// A threshold already below the negotiated limit is left alone.
	c3, c4 := net.Pipe()
	t.Cleanup(func() {
		_ = c3.Close()
		_ = c4.Close()
	})

	unclamped := newDuplexConn(newTCPFramedConn(c3, defaultMaxFrameSize), int64(defaultMaxFrameSize),
		withDuplexChunkSize(16*1024),
		withDuplexNegotiated(negotiated),
	)
	defer func() { _ = unclamped.Close() }()

	assert.Equal(t, uint32(16*1024), unclamped.ChunkSize())
}

// TestDuplexChunkedTellReachesInboundHandler pins the regression where a
// reassembled chunked frame was pushed straight onto the Recv queue and
// stranded on handler-mode sessions: server connections and client sessions
// with an inbound handler have no Recv consumer, so the message hung until
// unrelated traffic happened to wake a drainer.
func TestDuplexChunkedTellReachesInboundHandler(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	hello := &internalpb.Hello{
		Revision:                    CapabilityRevisionChunking,
		MaxFrameSize:                defaultMaxFrameSize,
		MaxMessageSize:              DefaultMaxMessageSize,
		MaxConcurrentLargeTransfers: 4,
	}

	credits := int64(defaultInitialCredits)
	sender := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), credits,
		withDuplexChunkSize(1024),
		withDuplexNegotiated(hello),
	)

	type delivered struct {
		frameType byte
		digest    [32]byte
	}

	got := make(chan delivered, 1)
	receiver := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), credits,
		withDuplexChunkSize(1024),
		withDuplexNegotiated(hello),
		withDuplexInboundHandler(func(session DuplexSession, frame Frame) {
			// Digest before release: the payload buffer returns to the pool.
			got <- delivered{frameType: frame.Type, digest: sha256.Sum256(frame.Payload)}
			session.ReleasePayload(frame)
		}),
	)

	defer func() {
		_ = sender.Close()
		_ = receiver.Close()
	}()

	payload := make([]byte, 64*1024)
	for i := range payload {
		payload[i] = byte(i)
	}
	want := sha256.Sum256(payload)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(t, sender.Tell(ctx, Frame{
		Type:    FrameTypeData,
		Lane:    LaneControl,
		Payload: payload,
	}))

	select {
	case frame := <-got:
		assert.Equal(t, FrameTypeData, frame.frameType)
		assert.Equal(t, want, frame.digest)
	case <-ctx.Done():
		t.Fatal("reassembled chunked frame never reached the inbound handler")
	}
}

// TestDuplexChunkedTellReachesPipelinedHandler covers the same regression for
// the server-side configuration: pipelined inbound dispatch, where the
// reassembled frame must wake the transient drainer instead of sitting in the
// queue until unrelated traffic arrives.
func TestDuplexChunkedTellReachesPipelinedHandler(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	hello := &internalpb.Hello{
		Revision:                    CapabilityRevisionChunking,
		MaxFrameSize:                defaultMaxFrameSize,
		MaxMessageSize:              DefaultMaxMessageSize,
		MaxConcurrentLargeTransfers: 4,
	}

	credits := int64(defaultInitialCredits)
	sender := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), credits,
		withDuplexChunkSize(1024),
		withDuplexNegotiated(hello),
	)

	const messages = 3
	digests := make(chan [32]byte, messages)
	receiver := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), credits,
		withDuplexChunkSize(1024),
		withDuplexNegotiated(hello),
		withDuplexPipelinedInbound(),
		withDuplexInboundHandler(func(session DuplexSession, frame Frame) {
			digests <- sha256.Sum256(frame.Payload)
			session.ReleasePayload(frame)
		}),
	)

	defer func() {
		_ = sender.Close()
		_ = receiver.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Space the sends past the drainer linger so each chunked message must
	// wake dispatch on its own rather than riding an already-hot drainer.
	for i := range messages {
		payload := make([]byte, 32*1024)
		for j := range payload {
			payload[j] = byte(i + j)
		}
		want := sha256.Sum256(payload)

		require.NoError(t, sender.Tell(ctx, Frame{
			Type:    FrameTypeData,
			Lane:    LaneControl,
			Payload: payload,
		}))

		select {
		case digest := <-digests:
			assert.Equal(t, want, digest, "message %d", i)
		case <-ctx.Done():
			t.Fatalf("chunked message %d never reached the pipelined handler", i)
		}

		pause.For(3 * duplexDispatchLinger)
	}
}
