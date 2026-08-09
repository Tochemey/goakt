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
	"fmt"
	"net"
	"time"

	"github.com/tochemey/goakt/v4/internal/internalpb"
)

// DuplexSession is the peer-facing API for one multiplexed duplex connection.
// Implementations enqueue frames on a byte-capped writer queue and correlate
// REPLY/ERROR frames for ask-style traffic. A session is safe for concurrent
// Tell/Ask from multiple goroutines; Close may be called once.
type DuplexSession interface {
	// Tell enqueues a fire-and-forget frame. Correlation is forced to zero and
	// the expectsReply flag is cleared. It returns when the frame is admitted
	// to the outbound queue, not when the peer processes it.
	Tell(ctx context.Context, frame Frame) error

	// Ask assigns a correlation ID, registers a waiter, submits frame with
	// expectsReply, and blocks until a REPLY/ERROR arrives, ctx is done, or
	// the session closes. Timeout abandons the waiter so a late reply is
	// dropped without error.
	Ask(ctx context.Context, frame Frame) (Frame, error)

	// Recv returns the next inbound frame that is not a correlated
	// REPLY/ERROR (those complete Ask waiters): connection-scoped ERROR and
	// any unsolicited traffic. It blocks until a frame
	// arrives, ctx is done, or the session closes.
	Recv(ctx context.Context) (Frame, error)

	// IsClosed reports whether the session has been signaled closed. A
	// closed session fails every subsequent Tell/Ask, so owners should
	// discard it and dial a fresh one.
	IsClosed() bool

	// Lane returns the negotiated connection lane byte.
	Lane() byte

	// Revision returns the negotiated capability revision for this session.
	Revision() uint32

	// MaxFrameSize returns the negotiated whole-frame ceiling.
	MaxFrameSize() uint32

	// MaxMessageSize returns the negotiated reassembled logical-frame ceiling.
	MaxMessageSize() uint64

	// MaxConcurrentLargeTransfers returns the negotiated concurrent CHUNK
	// group cap for this session.
	MaxConcurrentLargeTransfers() uint32

	// ChunkSize returns the local CHUNK send threshold used by this session.
	ChunkSize() uint32

	// Close drains admitted outbound frames, stops reader/writer loops, and
	// closes the underlying framed connection. It is safe to call more than
	// once; subsequent calls are no-ops that return the first close error.
	Close() error
}

// OpenDuplex dials addr with transport, completes HELLO/HELLO_ACK, applies the
// negotiated whole-connection compression wrapper, and starts reader/writer
// goroutines over the resulting [FramedConn].
//
// When ctx carries a deadline, that deadline bounds the HELLO exchange so a
// silent peer cannot stall dial forever. writeTimeout bounds [DuplexSession]
// submissions when a later caller's context carries no deadline. lane
// overrides the lane fields in localHello and readIdleTimeout enables
// connection-level PING/PONG liveness when nonzero. chunkSize is the local
// CHUNK send threshold (not negotiated). The session's effective lane identity
// comes from the HELLO_ACK, which may differ from lane when the acceptor
// predates lane echo and always answers CONTROL.
//
// On success the returned [HandshakeResult] holds local, remote, and effective
// negotiated parameters. On failure the underlying connection is closed.
func OpenDuplex(ctx context.Context, transport Transport, addr string, localHello *internalpb.Hello, lane LaneSpec, writeTimeout, readIdleTimeout time.Duration, chunkSize uint32) (DuplexSession, *HandshakeResult, error) {
	if _, err := laneByte(lane.Role, lane.Index); err != nil {
		return nil, nil, err
	}
	if localHello == nil {
		return nil, nil, fmt.Errorf("tcp: hello local parameters are required")
	}
	localHello.LaneRole = lane.Role
	localHello.LaneIndex = lane.Index

	framed, err := transport.Dial(ctx, addr, lane)
	if err != nil {
		return nil, nil, err
	}

	// Bound HELLO/HELLO_ACK by the caller's deadline so a silent peer cannot
	// stall dial forever. Cleared before the duplex loops start.
	if deadline, ok := ctx.Deadline(); ok {
		_ = framed.NetConn().SetDeadline(deadline)
		defer func() { _ = framed.NetConn().SetDeadline(time.Time{}) }()
	}

	// Cancellation without a deadline (ClosePeer) must still unblock ReadFrame.
	stopCancel := context.AfterFunc(ctx, func() {
		_ = framed.Close()
	})
	defer stopCancel()

	result, err := performHello(framed, localHello)
	if err != nil {
		_ = framed.Close()
		return nil, nil, err
	}

	// Stamp and enforce the negotiated lane from the ACK, not the requested
	// one. A baseline acceptor that predates lane echo answers CONTROL for
	// every dial; adopting its identity keeps frame stamping consistent with
	// the ERROR/REPLY lane bytes that peer emits instead of failing the lane
	// on its first server-originated frame.
	laneValue, err := laneByte(result.Effective.GetLaneRole(), result.Effective.GetLaneIndex())
	if err != nil {
		_ = framed.Close()
		return nil, nil, err
	}

	wrapped, err := wrapCompression(framed.NetConn(), result.Effective.GetCompression())
	if err != nil {
		_ = framed.Close()
		return nil, nil, err
	}

	if replacer, ok := framed.(interface{ ReplaceNetConn(net.Conn) }); ok {
		replacer.ReplaceNetConn(wrapped)
	}

	credits := int64(result.Effective.GetInitialCredits())
	if credits <= 0 {
		credits = int64(defaultInitialCredits)
	}

	conn := newDuplexConn(
		framed,
		credits,
		withDuplexWriteTimeout(writeTimeout),
		withDuplexReadIdleTimeout(readIdleTimeout),
		withDuplexLane(laneValue),
		withDuplexChunkSize(chunkSize),
		withDuplexNegotiated(result.Effective),
	)
	return conn, result, nil
}
