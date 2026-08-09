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

func TestDuplexStartStopNoLeak(t *testing.T) {
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

	require.NoError(t, left.Submit(ctx, Frame{
		Type: FrameTypePing,
		Lane: LaneControl,
	}))

	frame, err := right.Recv(ctx)
	require.NoError(t, err)
	assert.Equal(t, FrameTypePing, frame.Type)
	assert.Equal(t, ProtocolVersion, frame.Version)

	require.NoError(t, left.Close())
	require.NoError(t, right.Close())
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
		Type:    FrameTypePing,
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
	close(e.closed)
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
