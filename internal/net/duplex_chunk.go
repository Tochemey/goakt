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
	"fmt"
	"math"

	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/types"
)

// ErrMessageTooLarge is returned when a logical frame exceeds the negotiated
// max message size, or exceeds max frame size when the peer cannot chunk.
var ErrMessageTooLarge = errors.New("tcp: logical message exceeds negotiated size limit")

// submitLogical sends frame as a whole DATA/REPLY frame or as a CHUNK group
// when the logical size exceeds the local chunk size and the peer supports
// chunking. Correlation-less tells allocate a fresh group correlation; asks
// and replies reuse the logical frame's correlation.
func (x *duplexConn) submitLogical(ctx context.Context, frame Frame) error {
	if frame.Version == 0 {
		frame.Version = ProtocolVersion
	}

	if int(frame.Length) != frame.bodyLen() {
		frame.Length = uint32(frame.bodyLen())
	}

	if frame.Lane == 0 && x.enforceLane {
		frame.Lane = x.lane
	}

	logicalLen := FrameHeaderSize + frame.bodyLen()
	if uint64(logicalLen) > math.MaxUint32 {
		// The logical frame header carries a 32-bit length, so no message
		// beyond 4 GiB is expressible on the wire regardless of config.
		return fmt.Errorf("%w: size %d exceeds the 32-bit frame length limit", ErrMessageTooLarge, logicalLen)
	}

	if !x.shouldChunk(logicalLen) {
		if x.maxFrameSize > 0 && uint32(logicalLen) > x.maxFrameSize {
			return fmt.Errorf("%w: size %d exceeds max frame size %d and this session cannot chunk (revision %d, chunk size %d)", ErrMessageTooLarge, logicalLen, x.maxFrameSize, x.revision, x.chunkSize)
		}

		if x.maxMessageSize > 0 && uint64(logicalLen) > x.maxMessageSize {
			return fmt.Errorf("%w: size %d exceeds max message size %d", ErrMessageTooLarge, logicalLen, x.maxMessageSize)
		}

		return x.submitRaw(ctx, frame)
	}

	if x.maxMessageSize > 0 && uint64(logicalLen) > x.maxMessageSize {
		return fmt.Errorf("%w: size %d exceeds max message size %d", ErrMessageTooLarge, logicalLen, x.maxMessageSize)
	}

	logical, err := encodeLogicalFrame(frame)
	if err != nil {
		return err
	}

	corr := frame.Correlation
	if corr == 0 {
		corr = x.nextCorrelation()
	}

	if err := x.acquireLarge(ctx); err != nil {
		return err
	}
	defer x.releaseLarge()

	chunks, err := splitLogicalChunks(logical, corr, frame.Lane, x.chunkSize, frame.ExpectsReply())
	if err != nil {
		return err
	}

	for i, chunk := range chunks {
		if err := x.submitRaw(ctx, chunk); err != nil {
			if i > 0 {
				x.emitChunkAbort(ctx, corr, uint64(i))
			}

			return err
		}
	}

	return nil
}

// shouldChunk reports whether a logical frame of the given size must be sent
// as CHUNK frames on this session.
func (x *duplexConn) shouldChunk(logicalLen int) bool {
	return x.revision >= CapabilityRevisionChunking &&
		x.chunkSize > 0 &&
		uint32(logicalLen) > x.chunkSize
}

// acquireLarge takes one slot in the per-session large-transfer semaphore.
func (x *duplexConn) acquireLarge(ctx context.Context) error {
	if x.largeSem == nil {
		return nil
	}

	ctx, cancel := x.bindWriteDeadline(ctx)
	defer cancel()

	select {
	case x.largeSem <- types.Unit{}:
		return nil
	case <-ctx.Done():
		return submitContextError(ctx)
	case <-x.closed:
		return x.closedError()
	}
}

// releaseLarge returns one large-transfer slot.
func (x *duplexConn) releaseLarge() {
	if x.largeSem == nil {
		return
	}

	select {
	case <-x.largeSem:
	default:
	}
}

// emitChunkAbort best-effort frees a partial reassembly group on the peer.
// When the abort frame cannot be admitted, the transport is failed so
// connection loss discards the partial group.
func (x *duplexConn) emitChunkAbort(ctx context.Context, corr uint64, index uint64) {
	abort := abortChunkFrame(corr, x.lane, index)
	if err := x.submitRaw(ctx, abort); err != nil {
		x.failTransport()
	}
}

// handleInboundChunk feeds a CHUNK frame into the reassembler and dispatches
// the completed logical frame or emits soft/hard errors. The wire payload is
// released after Push copies into the reassembly buffer.
func (x *duplexConn) handleInboundChunk(frame Frame) {
	if x.revision < CapabilityRevisionChunking {
		x.releaseFramePayload(frame.Payload)
		x.rejectProtocol("CHUNK requires capability revision 2")
		return
	}

	if x.reassembler == nil {
		x.releaseFramePayload(frame.Payload)
		x.rejectProtocol("CHUNK received but reassembler is not configured")
		return
	}

	out := x.reassembler.Push(frame)
	x.releaseFramePayload(frame.Payload)

	switch {
	case out.HardError != "":
		x.rejectProtocol(out.HardError)
	case out.SoftReject != "":
		x.softRejectChunk(frame.Correlation, out.SoftReject)
	case out.Complete:
		x.dispatchLogical(out.Frame)
	}
}

// releaseFramePayload returns a ReadFrame buffer to the framed connection's
// pool when the underlying conn supports it.
func (x *duplexConn) releaseFramePayload(payload []byte) {
	if releaser, ok := x.framed.(interface{ releaseReadPayload([]byte) }); ok {
		releaser.releaseReadPayload(payload)
	}
}

// softRejectChunk emits a request-scoped ERROR and completes any local Ask
// waiter so a rejected chunked reply does not strand the caller. When the
// ERROR cannot be admitted, the transport is failed so the peer observes
// connection loss instead of a silent drop.
func (x *duplexConn) softRejectChunk(corr uint64, message string) {
	payload, err := proto.Marshal(&internalpb.Error{
		Code:    internalpb.Code_CODE_FAILED_PRECONDITION,
		Message: message,
	})
	if err != nil {
		return
	}

	errFrame := Frame{
		Version:     ProtocolVersion,
		Type:        FrameTypeError,
		Lane:        x.lane,
		Length:      uint32(len(payload)),
		Correlation: corr,
		Payload:     payload,
	}
	if !x.trySubmit(errFrame) {
		x.failTransport()
	}

	_ = x.pending.complete(corr, errFrame)
}

// dispatchLogical delivers a reassembled DATA/REPLY/ERROR the same way
// readLoop handles an unchunked frame. CHUNK frames never touch pending
// completion directly; only the logical REPLY/ERROR does.
//
// Delivery to inbound blocks exactly like the unchunked DATA path: a slow
// consumer backpressures the reader instead of the connection being killed
// over local queue depth, which would punish a compliant peer. The pinned
// reassembly buffer is simply memory held while backpressuring, no different
// from the reassembler holding it one step earlier.
func (x *duplexConn) dispatchLogical(frame Frame) {
	if frame.Type == FrameTypeReply || frame.Type == FrameTypeError {
		if frame.Correlation != 0 {
			_ = x.pending.complete(frame.Correlation, frame)
			return
		}
	}

	select {
	case <-x.closed:
	case x.inbound <- frame:
	}
}

// rejectProtocol emits a connection-scoped ERROR then drains the writer and
// tears down the transport, matching [duplexConn.rejectWrongLane].
func (x *duplexConn) rejectProtocol(message string) {
	payload, err := proto.Marshal(&internalpb.Error{
		Code:    internalpb.Code_CODE_FAILED_PRECONDITION,
		Message: message,
	})
	if err == nil {
		_ = x.trySubmit(Frame{
			Version: ProtocolVersion,
			Type:    FrameTypeError,
			Lane:    x.lane,
			Length:  uint32(len(payload)),
			Payload: payload,
		})
	}

	x.drainAndCloseFramed()
}
