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
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
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

	payload := []byte{1, 2}
	err := left.Submit(ctx, Frame{
		Version: ProtocolVersion,
		Type:    FrameTypePing,
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

	payload := []byte{1, 2}
	err := left.Submit(ctx, Frame{
		Version: ProtocolVersion,
		Type:    FrameTypePing,
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

	// One byte of outbound budget cannot admit any frame: the admit reports
	// backpressure immediately instead of waiting for capacity.
	err := starved.admitFrame(Frame{Type: FrameTypePing, Lane: starved.Lane()})
	require.ErrorIs(t, err, ErrDuplexBackpressure)

	// A frame within budget is admitted without error.
	require.NoError(t, healthy.admitFrame(Frame{Type: FrameTypePing, Lane: healthy.Lane()}))

	_ = healthy.Close()
	err = healthy.admitFrame(Frame{Type: FrameTypePing, Lane: healthy.Lane()})
	require.ErrorIs(t, err, ErrDuplexClosed)
}
