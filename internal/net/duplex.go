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
	"sync"
	"sync/atomic"

	"github.com/tochemey/goakt/v4/internal/types"
)

// ErrDuplexClosed is returned when submitting to or reading from a closed
// duplex connection.
var ErrDuplexClosed = errors.New("tcp: duplex connection is closed")

// ErrDuplexBackpressure is returned when the outbound queue cannot accept a
// frame within the caller's deadline.
var ErrDuplexBackpressure = errors.New("tcp: duplex outbound queue full")

// duplexConn owns one reader goroutine, one writer goroutine, and a
// byte-bounded outbound queue over a [FramedConn]. Submit admits frames up
// to the byte cap; Recv delivers inbound frames from the reader loop.
type duplexConn struct {
	framed      FramedConn
	maxOutBytes int64

	mu       sync.Mutex
	space    *sync.Cond
	outBytes int64
	out      chan Frame
	inbound  chan Frame

	closeOnce sync.Once
	closed    chan types.Unit
	// closing records that the local side initiated Close, so loop errors
	// caused by tearing down our own connection are not recorded as peer
	// failures. Errors observed while closing is false are genuine.
	closing  atomic.Bool
	wg       sync.WaitGroup
	writeErr atomic.Pointer[error]
	readErr  atomic.Pointer[error]
}

// newDuplexConn starts reader and writer goroutines for framed.
// maxOutBytes caps admitted outbound payload+header bytes; values <= 0
// default to defaultMaxFrameSize.
func newDuplexConn(framed FramedConn, maxOutBytes int64) *duplexConn {
	if maxOutBytes <= 0 {
		maxOutBytes = int64(defaultMaxFrameSize)
	}

	x := &duplexConn{
		framed:      framed,
		maxOutBytes: maxOutBytes,
		out:         make(chan Frame, 64),
		inbound:     make(chan Frame, 64),
		closed:      make(chan types.Unit),
	}
	x.space = sync.NewCond(&x.mu)

	x.wg.Add(2)
	go x.writeLoop()
	go x.readLoop()
	return x
}

// Submit enqueues frame for writing. It blocks until there is byte capacity,
// ctx is done, or the duplex is closed. A canceled context returns ctx.Err();
// a deadline that expires while waiting for capacity returns
// [ErrDuplexBackpressure].
func (x *duplexConn) Submit(ctx context.Context, frame Frame) error {
	if frame.Version == 0 {
		frame.Version = ProtocolVersion
	}

	if int(frame.Length) != len(frame.Payload) {
		frame.Length = uint32(len(frame.Payload))
	}

	cost := int64(FrameHeaderSize) + int64(frame.Length)

	// The ctx wake-up hook is registered lazily, only when Submit must wait
	// for capacity, so the uncontended fast path stays allocation free.
	var stop func() bool

	defer func() {
		if stop != nil {
			stop()
		}
	}()

	x.mu.Lock()
	for {
		if x.isClosed() {
			x.mu.Unlock()
			return ErrDuplexClosed
		}

		if x.outBytes+cost <= x.maxOutBytes {
			x.outBytes += cost
			x.mu.Unlock()

			select {
			case x.out <- frame:
				return nil
			case <-x.closed:
				x.release(cost)
				return ErrDuplexClosed
			case <-ctx.Done():
				x.release(cost)
				return submitContextError(ctx)
			}
		}

		if err := ctx.Err(); err != nil {
			x.mu.Unlock()
			return submitContextError(ctx)
		}

		if stop == nil {
			stop = context.AfterFunc(ctx, func() {
				x.mu.Lock()
				x.space.Broadcast()
				x.mu.Unlock()
			})
		}

		x.space.Wait()
	}
}

// Recv returns the next inbound frame or an error when the reader stops.
// Buffered inbound frames are drained before a closed connection error so
// shutdown does not drop already-delivered frames.
func (x *duplexConn) Recv(ctx context.Context) (Frame, error) {
	select {
	case frame := <-x.inbound:
		return frame, nil
	default:
	}

	select {
	case frame := <-x.inbound:
		return frame, nil
	case <-ctx.Done():
		return Frame{}, ctx.Err()
	case <-x.closed:
		select {
		case frame := <-x.inbound:
			return frame, nil
		default:
			return Frame{}, x.closedError()
		}
	}
}

// Close stops both loops and closes the underlying framed connection.
// When the writer failed, that error is returned.
func (x *duplexConn) Close() error {
	x.closing.Store(true)
	closeErr := x.signalClose()
	x.wg.Wait()

	if errPtr := x.writeErr.Load(); errPtr != nil {
		return *errPtr
	}

	return closeErr
}

// signalClose closes the framed connection exactly once without waiting for
// the reader/writer goroutines. The loops call this on error; [Close] waits.
func (x *duplexConn) signalClose() error {
	var closeErr error
	x.closeOnce.Do(func() {
		close(x.closed)
		x.mu.Lock()
		x.space.Broadcast()
		x.mu.Unlock()
		closeErr = x.framed.Close()
	})
	return closeErr
}

// release returns cost bytes to the outbound budget and wakes every waiting
// Submit so none can miss a capacity change.
func (x *duplexConn) release(cost int64) {
	x.mu.Lock()
	x.outBytes -= cost
	if x.outBytes < 0 {
		x.outBytes = 0
	}
	x.space.Broadcast()
	x.mu.Unlock()
}

// writeLoop drains the outbound queue onto the framed connection until the
// duplex closes or a write fails.
func (x *duplexConn) writeLoop() {
	defer x.wg.Done()

	for {
		select {
		case <-x.closed:
			return
		case frame := <-x.out:
			cost := int64(FrameHeaderSize) + int64(frame.Length)
			if err := x.framed.WriteFrames(frame); err != nil {
				// A write error during a locally initiated Close is our own
				// teardown, not a peer failure, and is not recorded.
				if !x.closing.Load() {
					x.writeErr.Store(&err)
				}

				_ = x.signalClose()
				return
			}

			x.release(cost)
		}
	}
}

// readLoop pumps inbound frames from the framed connection into the inbound
// channel until the duplex closes or a read fails.
func (x *duplexConn) readLoop() {
	defer x.wg.Done()

	for {
		frame, err := x.framed.ReadFrame()
		if err != nil {
			// Same guard as the writer: a read failure triggered by our own
			// locally initiated Close must not masquerade as a peer error.
			if !x.closing.Load() {
				x.readErr.Store(&err)
			}

			_ = x.signalClose()
			return
		}

		select {
		case <-x.closed:
			return
		case x.inbound <- frame:
		}
	}
}

// isClosed reports whether the duplex has been signaled closed. It only reads
// the closed channel, so it is safe with or without the mutex held.
func (x *duplexConn) isClosed() bool {
	select {
	case <-x.closed:
		return true
	default:
		return false
	}
}

// closedError picks the most informative terminal error: the reader's, then
// the writer's, then the generic [ErrDuplexClosed].
func (x *duplexConn) closedError() error {
	if errPtr := x.readErr.Load(); errPtr != nil {
		return *errPtr
	}

	if errPtr := x.writeErr.Load(); errPtr != nil {
		return *errPtr
	}

	return ErrDuplexClosed
}

// submitContextError maps a done context to either cancellation or
// backpressure (deadline exceeded while waiting for queue capacity).
func submitContextError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return ctx.Err()
	}

	return ErrDuplexBackpressure
}
