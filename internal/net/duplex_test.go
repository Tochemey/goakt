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
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/types"
)

func TestDuplexPingAnsweredWithPong(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), 1024)
	right := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), 1024)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	leftWait := left.pending.register(1)
	require.NoError(t, left.Submit(ctx, Frame{
		Type:        FrameTypePing,
		Lane:        LaneControl,
		Correlation: 1,
	}))

	select {
	case frame := <-leftWait:
		putPendingWaiter(leftWait)
		assert.Equal(t, FrameTypePong, frame.Type)
		assert.Equal(t, uint64(1), frame.Correlation)
	case <-ctx.Done():
		t.Fatal("timed out waiting for left PONG")
	}

	rightWait := right.pending.register(2)
	require.NoError(t, right.Submit(ctx, Frame{
		Type:        FrameTypePing,
		Lane:        LaneControl,
		Correlation: 2,
	}))

	select {
	case frame := <-rightWait:
		putPendingWaiter(rightWait)
		assert.Equal(t, FrameTypePong, frame.Type)
		assert.Equal(t, uint64(2), frame.Correlation)
	case <-ctx.Done():
		t.Fatal("timed out waiting for right PONG")
	}

	require.NoError(t, left.Close())
	require.NoError(t, right.Close())
}

func TestDuplexPongCompletesPendingWaiter(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), 1024)
	right := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), 1024)

	wait := left.pending.register(42)
	require.NoError(t, right.Submit(context.Background(), Frame{
		Type:        FrameTypePong,
		Lane:        LaneControl,
		Correlation: 42,
	}))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	select {
	case frame := <-wait:
		putPendingWaiter(wait)
		assert.Equal(t, FrameTypePong, frame.Type)
		assert.Equal(t, uint64(42), frame.Correlation)
	case <-ctx.Done():
		t.Fatal("timed out waiting for PONG")
	}

	require.NoError(t, left.Close())
	require.NoError(t, right.Close())
}

func TestDuplexLivenessClosesAfterTwoMissedPongs(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	const interval = 20 * time.Millisecond
	start := time.Now()
	conn := newDuplexConn(
		newTCPFramedConn(c1, defaultMaxFrameSize),
		1024,
		withDuplexReadIdleTimeout(interval),
	)

	require.Eventually(t, conn.IsClosed, time.Second, 5*time.Millisecond)
	// Two missed PONGs are required: the first silent interval only sends a
	// probe, and each of the next two counts one miss, so teardown cannot
	// happen before three intervals elapse. Timers never fire early, so this
	// lower bound is deterministic.
	assert.GreaterOrEqual(t, time.Since(start), 3*interval)
	require.NoError(t, c2.Close())
	_ = conn.Close()
}

func TestDuplexDefaultMaxOutBytes(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), 0)
	right := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), -1)
	assert.Equal(t, int64(defaultMaxFrameSize), left.maxOutBytes)
	assert.Equal(t, int64(defaultMaxFrameSize), right.maxOutBytes)

	require.NoError(t, left.Close())
	require.NoError(t, right.Close())
}

func TestDuplexBackpressure(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), FrameHeaderSize+1)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Use DATA: PING is admission-exempt so it can exceed the byte cap.
	payload := []byte{1, 2}
	err := left.Submit(ctx, Frame{
		Version: ProtocolVersion,
		Type:    FrameTypeData,
		Payload: payload,
		Length:  uint32(len(payload)),
	})
	require.ErrorIs(t, err, ErrDuplexBackpressure)

	_ = c2.Close()
	_ = left.Close()
}

func TestDuplexAskOutOfOrder(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	client := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), 1<<20)
	server := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), 1<<20)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type result struct {
		corr uint64
		err  error
	}
	results := make(chan result, 2)

	go func() {
		frame, err := client.Ask(ctx, Frame{Type: FrameTypeData, Lane: LaneOrdinary, Payload: []byte("a")})
		results <- result{corr: frame.Correlation, err: err}
	}()
	go func() {
		frame, err := client.Ask(ctx, Frame{Type: FrameTypeData, Lane: LaneOrdinary, Payload: []byte("b")})
		results <- result{corr: frame.Correlation, err: err}
	}()

	req1, err := server.Recv(ctx)
	require.NoError(t, err)
	req2, err := server.Recv(ctx)
	require.NoError(t, err)

	// Reply out of order: second request first.
	require.NoError(t, server.Submit(ctx, Frame{
		Type:        FrameTypeReply,
		Lane:        LaneOrdinary,
		Correlation: req2.Correlation,
		Payload:     []byte("rb"),
	}))
	require.NoError(t, server.Submit(ctx, Frame{
		Type:        FrameTypeReply,
		Lane:        LaneOrdinary,
		Correlation: req1.Correlation,
		Payload:     []byte("ra"),
	}))

	r1 := <-results
	r2 := <-results
	require.NoError(t, r1.err)
	require.NoError(t, r2.err)
	assert.ElementsMatch(t, []uint64{req1.Correlation, req2.Correlation}, []uint64{r1.corr, r2.corr})
	assert.Equal(t, 0, client.pending.len())

	require.NoError(t, client.Close())
	require.NoError(t, server.Close())
}

func TestDuplexAskTimeoutClearsPending(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	client := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), 1<<20)
	server := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), 1<<20)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, err := client.Ask(ctx, Frame{Type: FrameTypeData, Lane: LaneOrdinary, Payload: []byte("x")})
	require.Error(t, err)
	assert.Equal(t, 0, client.pending.len())

	// Late reply must not panic, re-populate the table, or fill inbound.
	req, err := server.Recv(context.Background())
	require.NoError(t, err)
	require.NoError(t, server.Submit(context.Background(), Frame{
		Type:        FrameTypeReply,
		Correlation: req.Correlation,
	}))
	assert.Equal(t, 0, client.pending.len())

	// A subsequent correlated ask must still complete after many late replies
	// (inbound capacity is 64; dropping keeps the reader unblocked).
	for i := 0; i < 80; i++ {
		require.NoError(t, server.Submit(context.Background(), Frame{
			Type:        FrameTypeReply,
			Correlation: req.Correlation + uint64(i) + 1000,
		}))
	}

	askCtx, askCancel := context.WithTimeout(context.Background(), time.Second)
	defer askCancel()

	go func() {
		req2, recvErr := server.Recv(askCtx)
		if recvErr != nil {
			return
		}
		_ = server.Submit(askCtx, Frame{
			Type:        FrameTypeReply,
			Correlation: req2.Correlation,
			Payload:     []byte("ok"),
		})
	}()

	reply, err := client.Ask(askCtx, Frame{Type: FrameTypeData, Lane: LaneOrdinary, Payload: []byte("y")})
	require.NoError(t, err)
	assert.Equal(t, []byte("ok"), reply.Payload)

	require.NoError(t, client.Close())
	_ = server.Close()
}

func TestDuplexSubmitCanceledContext(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), FrameHeaderSize+1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Use DATA: PING is admission-exempt and would admit past the byte cap
	// instead of blocking until the canceled context surfaces.
	payload := []byte{1, 2}
	err := left.Submit(ctx, Frame{
		Version: ProtocolVersion,
		Type:    FrameTypeData,
		Payload: payload,
		Length:  uint32(len(payload)),
	})
	require.ErrorIs(t, err, context.Canceled)

	_ = c2.Close()
	_ = left.Close()
}

func TestDuplexSubmitWakesMultipleWaiters(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	// Cap admits exactly one FrameHeaderSize+1 frame at a time.
	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), FrameHeaderSize+1)
	right := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), 1024)

	payload := []byte{1}
	frame := Frame{
		Version: ProtocolVersion,
		Type:    FrameTypeData,
		Lane:    LaneControl,
		Payload: payload,
		Length:  uint32(len(payload)),
	}

	require.NoError(t, left.Submit(context.Background(), frame))

	errCh := make(chan error, 2)
	for range 2 {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			errCh <- left.Submit(ctx, frame)
		}()
	}

	// Drain the blocked first frame so both waiters can proceed after releases.
	for range 3 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := right.Recv(ctx)
		cancel()
		require.NoError(t, err)
	}

	require.NoError(t, <-errCh)
	require.NoError(t, <-errCh)

	require.NoError(t, left.Close())
	require.NoError(t, right.Close())
}

func TestDuplexSubmitAfterClose(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), 1024)
	require.NoError(t, left.Close())
	_ = c2.Close()

	err := left.Submit(context.Background(), Frame{
		Version: ProtocolVersion,
		Type:    FrameTypePing,
	})
	require.ErrorIs(t, err, ErrDuplexClosed)
}

func TestDuplexRecvAfterClose(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), 1024)
	_ = c2.Close()
	require.NoError(t, left.Close())

	_, err := left.Recv(context.Background())
	require.Error(t, err)
}

func TestDuplexRecvContextCanceled(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), 1024)
	right := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), 1024)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := right.Recv(ctx)
	require.ErrorIs(t, err, context.Canceled)

	require.NoError(t, left.Close())
	require.NoError(t, right.Close())
}

func TestDuplexRejectsMismatchedLanePing(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	left := newDuplexConn(
		newTCPFramedConn(c1, defaultMaxFrameSize),
		1<<20,
		withDuplexLane(LaneControl),
	)
	right := newTCPFramedConn(c2, defaultMaxFrameSize)

	require.NoError(t, right.WriteFrames(Frame{
		Version:     ProtocolVersion,
		Type:        FrameTypePing,
		Lane:        LaneOrdinary,
		Correlation: 9,
	}))

	frame, err := right.ReadFrame()
	require.NoError(t, err)
	assert.Equal(t, FrameTypeError, frame.Type)
	assert.Equal(t, LaneControl, frame.Lane)
	assert.Zero(t, frame.Correlation)

	require.Eventually(t, left.IsClosed, time.Second, 10*time.Millisecond)
	_ = left.Close()
}

func TestDuplexConnIdleTimeoutRefreshedByPing(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	left := newDuplexConn(
		newTCPFramedConn(c1, defaultMaxFrameSize),
		1<<20,
		withDuplexLane(LaneControl),
		withDuplexConnIdleTimeout(80*time.Millisecond),
	)
	right := newTCPFramedConn(c2, defaultMaxFrameSize)

	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		require.NoError(t, right.WriteFrames(Frame{
			Version:     ProtocolVersion,
			Type:        FrameTypePing,
			Lane:        LaneControl,
			Correlation: 1,
		}))
		pong, err := right.ReadFrame()
		require.NoError(t, err)
		require.Equal(t, FrameTypePong, pong.Type)
		time.Sleep(25 * time.Millisecond)
	}

	assert.False(t, left.IsClosed())
	require.NoError(t, left.Close())
}

func TestDuplexWriteErrorCloses(t *testing.T) {
	defer goleak.VerifyNone(t)

	boom := errors.New("write boom")
	d := newDuplexConn(&writeErrFramedConn{err: boom, closed: make(chan struct{})}, 1024)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	require.NoError(t, d.Submit(ctx, Frame{
		Version: ProtocolVersion,
		Type:    FrameTypePing,
	}))

	require.Eventually(t, func() bool {
		return d.isClosed()
	}, time.Second, 10*time.Millisecond)

	require.ErrorIs(t, d.Close(), boom)
}

// writeErrFramedConn fails every write with err while reads block until the
// connection closes, so the writer loop deterministically observes the error
// before the reader loop can race it to signal close.
type writeErrFramedConn struct {
	err    error
	closed chan struct{}
}

func (e *writeErrFramedConn) WriteFrames(...Frame) error { return e.err }

func (e *writeErrFramedConn) ReadFrame() (Frame, error) {
	<-e.closed
	return Frame{}, e.err
}

func (e *writeErrFramedConn) Close() error {
	select {
	case <-e.closed:
	default:
		close(e.closed)
	}
	return nil
}

func (e *writeErrFramedConn) NetConn() net.Conn      { return nil }
func (e *writeErrFramedConn) SetMaxFrameSize(uint32) {}
func (e *writeErrFramedConn) MaxFrameSize() uint32   { return 0 }

type errFramedConn struct {
	err error
}

func (e *errFramedConn) WriteFrames(...Frame) error { return e.err }
func (e *errFramedConn) ReadFrame() (Frame, error)  { return Frame{}, e.err }
func (e *errFramedConn) Close() error               { return nil }
func (e *errFramedConn) NetConn() net.Conn          { return nil }
func (e *errFramedConn) SetMaxFrameSize(uint32)     {}
func (e *errFramedConn) MaxFrameSize() uint32       { return 0 }

func TestDuplexReadErrorSurfacedOnRecv(t *testing.T) {
	defer goleak.VerifyNone(t)

	boom := errors.New("read boom")
	d := newDuplexConn(&errFramedConn{err: boom}, 1024)

	require.Eventually(t, func() bool {
		select {
		case <-d.closed:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	_, err := d.Recv(context.Background())
	require.Error(t, err)
	require.NoError(t, d.Close())
}

func TestAdmitFrameFailureModes(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	starved := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), 1)
	healthy := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), int64(defaultMaxFrameSize))
	defer func() {
		_ = starved.Close()
		_ = healthy.Close()
	}()

	// One byte of outbound budget cannot admit a windowed frame: the admit
	// reports backpressure immediately instead of waiting for capacity.
	// PING is admission-exempt and would still fit past a tiny byte cap.
	err := starved.admitFrame(Frame{Type: FrameTypeData, Lane: starved.Lane()})
	require.ErrorIs(t, err, ErrDuplexBackpressure)

	// A frame within budget is admitted without error.
	require.NoError(t, healthy.admitFrame(Frame{Type: FrameTypePing, Lane: healthy.Lane()}))

	_ = healthy.Close()
	err = healthy.admitFrame(Frame{Type: FrameTypePing, Lane: healthy.Lane()})
	require.ErrorIs(t, err, ErrDuplexClosed)
}

func TestDuplexTellReleasePayloadReusable(t *testing.T) {
	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), 1<<20)
	right := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), 1<<20)
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})

	ctx := context.Background()
	body := []byte("zero-copy-tell")

	for range 8 {
		require.NoError(t, left.Tell(ctx, Frame{
			Type:    FrameTypeData,
			Lane:    LaneOrdinary,
			Payload: body,
		}))

		frame, err := right.Recv(ctx)
		require.NoError(t, err)
		require.Equal(t, body, frame.Payload)
		right.ReleasePayload(frame)
	}
}

// BenchmarkDuplexTellAllocs states the steady-state whole-frame tell
// allocation count for the #1301 milestone record: pooled read body, envelope
// bytes handed to Recv, released after consumption.
func BenchmarkDuplexTellAllocs(b *testing.B) {
	c1, c2 := net.Pipe()
	defer func() {
		_ = c1.Close()
		_ = c2.Close()
	}()

	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), 1<<20)
	right := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), 1<<20)
	defer func() {
		_ = left.Close()
		_ = right.Close()
	}()

	payload := make([]byte, 256)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		if err := left.Tell(ctx, Frame{
			Type:    FrameTypeData,
			Lane:    LaneOrdinary,
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

func TestLateCorrelatedReplyDoesNotStallReader(t *testing.T) {
	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), 1<<20)
	right := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), 1<<20)
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})

	ctx := context.Background()

	// A REPLY with no waiter must be dropped (and its pooled body released)
	// without filling inbound or stalling the reader.
	require.NoError(t, left.Submit(ctx, Frame{
		Type:        FrameTypeReply,
		Lane:        LaneOrdinary,
		Correlation: 99,
		Payload:     []byte("late-reply"),
	}))

	require.NoError(t, left.Tell(ctx, Frame{
		Type:    FrameTypeData,
		Lane:    LaneOrdinary,
		Payload: []byte("after-late"),
	}))

	frame, err := right.Recv(ctx)
	require.NoError(t, err)
	require.Equal(t, []byte("after-late"), frame.Payload)
	right.ReleasePayload(frame)

	require.Eventually(t, func() bool {
		return right.pending.len() == 0
	}, time.Second, 5*time.Millisecond)
}

func TestDuplexInboundHandlerReceivesFramesOnReadLoop(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	frames := make(chan Frame, 4)
	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), 1024,
		withDuplexInboundHandler(func(session DuplexSession, frame Frame) {
			session.ReleasePayload(frame)
			frames <- frame
		}),
	)
	right := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), 1024)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	require.NoError(t, right.Tell(ctx, Frame{
		Type:    FrameTypeData,
		Lane:    LaneControl,
		Payload: []byte("dispatched"),
	}))

	select {
	case frame := <-frames:
		assert.Equal(t, FrameTypeData, frame.Type)
	case <-ctx.Done():
		t.Fatal("timed out waiting for inbound handler dispatch")
	}

	require.NoError(t, left.Close())
	require.NoError(t, right.Close())
}

func TestDuplexClosedHandlerFiresOnLocalClose(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	closedSeen := make(chan bool, 1)
	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), 1024,
		withDuplexClosedHandler(func(session DuplexSession) {
			closedSeen <- session.IsClosed()
		}),
	)
	right := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), 1024)

	require.NoError(t, left.Close())

	select {
	case wasClosed := <-closedSeen:
		assert.True(t, wasClosed, "session must already be closed inside the handler")
	case <-time.After(time.Second):
		t.Fatal("closed handler did not fire on local close")
	}

	require.NoError(t, right.Close())
}

func TestDuplexClosedHandlerFiresOnPeerDeath(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	closedSeen := make(chan bool, 1)
	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), 1024,
		withDuplexClosedHandler(func(session DuplexSession) {
			closedSeen <- session.IsClosed()
		}),
	)

	// The peer vanishes without a duplex Close: the raw pipe end closes, the
	// left read loop fails the transport, and the handler must still fire.
	require.NoError(t, c2.Close())

	select {
	case wasClosed := <-closedSeen:
		assert.True(t, wasClosed, "session must already be closed inside the handler")
	case <-time.After(time.Second):
		t.Fatal("closed handler did not fire on peer death")
	}

	require.NoError(t, left.Close())
}

// TestDuplexPipelinedDispatchOrderAndPark exercises the adaptive inbound
// dispatch machinery directly: a burst of frames must all reach the handler
// in arrival order (whether dispatched inline or through the queue and its
// transient drainer), and once the connection goes quiet past the linger the
// drainer must hand the queue back and exit.
func TestDuplexPipelinedDispatchOrderAndPark(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	// Tell forces frame correlation to zero, so the arrival sequence rides
	// in the payload instead.
	const frames = 200
	seen := make(chan uint64, frames)
	slowFirst := make(chan struct{})
	var dispatched atomic.Int64

	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), 1<<20,
		withDuplexPipelinedInbound(),
		withDuplexInboundHandler(func(session DuplexSession, frame Frame) {
			// Stall the first dispatch so the read loop observes a busy
			// consumer and shifts the rest of the burst onto the queue.
			if dispatched.Add(1) == 1 {
				<-slowFirst
			}

			seen <- binary.BigEndian.Uint64(frame.Payload)
			session.ReleasePayload(frame)
		}),
	)
	right := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), 1<<20)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go func() {
		for i := 1; i <= frames; i++ {
			payload := make([]byte, 8)
			binary.BigEndian.PutUint64(payload, uint64(i))

			if err := right.Tell(ctx, Frame{
				Type:    FrameTypeData,
				Lane:    LaneControl,
				Payload: payload,
			}); err != nil {
				return
			}
		}
	}()

	// Give the burst time to pile up behind the stalled first dispatch, then
	// release it and require every frame in exact arrival order.
	pause.For(50 * time.Millisecond)
	close(slowFirst)

	for i := 1; i <= frames; i++ {
		select {
		case sequence := <-seen:
			require.Equal(t, uint64(i), sequence, "dispatch order broke at frame %d", i)
		case <-ctx.Done():
			t.Fatalf("frame %d never dispatched", i)
		}
	}

	// Quiet connection: the transient drainer must park within a few lingers.
	require.Eventually(t, func() bool {
		left.dispatchMu.Lock()
		defer left.dispatchMu.Unlock()
		return !left.dispatchRunning
	}, time.Second, 5*time.Millisecond, "drainer did not exit after the connection went quiet")

	require.NoError(t, left.Close())
	require.NoError(t, right.Close())
}

// TestDuplexPipelinedDispatchJoinedBeforeClose pins the teardown contract:
// Close must not return, and the closed handler must not retire accounting,
// while a transient dispatch drainer is still delivering queued frames.
func TestDuplexPipelinedDispatchJoinedBeforeClose(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	gate := make(chan types.Unit)
	var handled atomic.Int32
	var handledAtClose atomic.Int32
	closedFired := make(chan types.Unit)

	conn := newDuplexConn(
		newTCPFramedConn(c1, defaultMaxFrameSize),
		1024,
		withDuplexInboundHandler(func(DuplexSession, Frame) {
			if handled.Add(1) == 1 {
				<-gate
			}
		}),
		withDuplexPipelinedInbound(),
		withDuplexClosedHandler(func(DuplexSession) {
			handledAtClose.Store(handled.Load())
			close(closedFired)
		}),
	)

	// Queue two frames and hand them to a drainer, exactly as the read loop
	// does under load; the drainer blocks in the handler on the first frame.
	conn.inbound <- Frame{Type: FrameTypeData}
	conn.inbound <- Frame{Type: FrameTypeData}
	conn.wakeDispatcher()

	closeDone := make(chan types.Unit)
	go func() {
		_ = conn.Close()
		close(closeDone)
	}()

	select {
	case <-closeDone:
		t.Fatal("Close returned while pipelined dispatch was still delivering")
	case <-closedFired:
		t.Fatal("closed handler ran while pipelined dispatch was still delivering")
	case <-time.After(100 * time.Millisecond):
	}

	close(gate)

	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after dispatch drained")
	}

	<-closedFired
	assert.EqualValues(t, 2, handledAtClose.Load(), "every queued frame must be delivered before the closed handler retires accounting")
}

// TestDuplexReadLoopAbandonsResumedFrameOnLivenessExit pins the resumable
// ReadFrame teardown: a partial frame parked by a read-deadline expiry holds
// a pooled payload, and a liveness exit never calls ReadFrame again, so the
// read loop must hand the buffer back on its way out.
func TestDuplexReadLoopAbandonsResumedFrameOnLivenessExit(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	const interval = 20 * time.Millisecond
	framed := newTCPFramedConn(c1, defaultMaxFrameSize)
	closedFired := make(chan types.Unit)

	conn := newDuplexConn(
		framed,
		1024,
		withDuplexReadIdleTimeout(interval),
		withDuplexClosedHandler(func(DuplexSession) { close(closedFired) }),
	)

	// The peer sends a partial DATA frame (header claims 64 bytes, only 8
	// arrive) and then stays silent while draining the probes it never
	// answers. net.Pipe writes complete only when the reader consumed the
	// bytes, so once Write returns the read loop provably holds the frame's
	// resume state.
	header := Frame{Version: ProtocolVersion, Type: FrameTypeData, Lane: LaneControl, Length: 64}
	wire := make([]byte, FrameHeaderSize+8)
	require.NoError(t, encodeFrameHeader(wire[:FrameHeaderSize], header))

	wrote := make(chan types.Unit)
	go func() {
		_, _ = c2.Write(wire)
		close(wrote)
		_, _ = io.Copy(io.Discard, c2)
	}()

	select {
	case <-wrote:
	case <-time.After(time.Second):
		t.Fatal("the read loop never consumed the partial frame")
	}

	select {
	case <-closedFired:
	case <-time.After(2 * time.Second):
		t.Fatal("liveness did not tear the connection down")
	}

	// The closed handler runs after the read loop's abandon (channel sync
	// orders this read after it), so the resume state must be gone and the
	// pooled payload back in the pool.
	assert.False(t, framed.pendingActive, "the resumed frame must be abandoned on liveness exit")
	assert.Nil(t, framed.pendingPayload, "the pooled payload must be handed back")
	_ = conn.Close()
}
