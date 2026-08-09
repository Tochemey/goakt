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
	"encoding/binary"
	"math"

	"github.com/tochemey/goakt/v4/internal/types"
)

// creditGrantBatchDivisor sets grant batching to one CREDIT frame per quarter
// of the negotiated window: the accumulator flushes once it holds at least
// creditWindow / creditGrantBatchDivisor owned bytes.
const creditGrantBatchDivisor = 4

// appendCreditPayload appends the CREDIT body encoding of grant as a uvarint.
func appendCreditPayload(dst []byte, grant uint64) []byte {
	return binary.AppendUvarint(dst, grant)
}

// encodeCreditPayload returns a heap CREDIT body for grant. Encoding uses a
// stack scratch buffer so only the exact-sized result escapes; CREDIT frames
// are batched at a quarter window, so this allocation is not on the per-message
// hot path.
func encodeCreditPayload(grant uint64) []byte {
	var scratch [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(scratch[:], grant)
	out := make([]byte, n)
	copy(out, scratch[:n])
	return out
}

// consumeCreditPayload parses a CREDIT body as a uvarint grant. ok is false
// when the payload is empty, truncated, or contains trailing bytes.
func consumeCreditPayload(src []byte) (grant uint64, ok bool) {
	if len(src) == 0 {
		return 0, false
	}

	grant, n := binary.Uvarint(src)
	if n <= 0 || n != len(src) {
		return 0, false
	}

	return grant, true
}

// frameWireCost returns the admission and credit cost of frame: header plus
// payload length.
func frameWireCost(frame Frame) int64 {
	return int64(FrameHeaderSize) + int64(frame.Length)
}

// windowedFrameType reports whether frame type consumes the send window.
// Only DATA and CHUNK are windowed; every other type is exempt.
func windowedFrameType(frameType byte) bool {
	return frameType == FrameTypeData || frameType == FrameTypeChunk
}

// admissionExemptFrameType reports whether frame type may exceed the outbound
// admission byte cap. Control frames that must flow while the queue is filled
// with parked windowed DATA use this so CREDIT and liveness cannot stall.
func admissionExemptFrameType(frameType byte) bool {
	switch frameType {
	case FrameTypeCredit, FrameTypePing, FrameTypePong, FrameTypeError:
		return true
	default:
		return false
	}
}

// noteOwnedBytes records that the receiver has taken ownership of n wire
// bytes and flushes a batched CREDIT grant when the accumulator crosses the
// batching threshold. No-op when credits are disabled or n is non-positive.
func (x *duplexConn) noteOwnedBytes(n int64) {
	if !x.creditEnabled || n <= 0 {
		return
	}

	x.grantMu.Lock()
	x.grantAccum += n
	x.grantMu.Unlock()

	x.flushGrants()
}

// flushGrants emits batched CREDIT frames while the accumulator holds at
// least a quarter window. A grant that cannot be admitted (writer queue slot
// full) stays in the accumulator; the write loop calls this again whenever it
// frees queue slots, so a deferred grant cannot strand a parked peer waiting
// for credit that no later inbound frame would ever flush.
func (x *duplexConn) flushGrants() {
	if !x.creditEnabled {
		return
	}

	threshold := x.creditWindow / creditGrantBatchDivisor
	if threshold < 1 {
		threshold = 1
	}

	for {
		x.grantMu.Lock()
		if x.grantAccum < threshold {
			x.grantMu.Unlock()
			return
		}

		grant := x.grantAccum
		x.grantAccum = 0
		x.grantMu.Unlock()

		if !x.submitCreditFrame(grant) {
			x.grantMu.Lock()
			x.grantAccum += grant
			x.grantMu.Unlock()
			return
		}
	}
}

// noteOwnedFrame records ownership of one windowed wire frame. Non-windowed
// types are ignored so callers can invoke it at every terminal disposition.
func (x *duplexConn) noteOwnedFrame(frame Frame) {
	if !windowedFrameType(frame.Type) {
		return
	}

	x.noteOwnedBytes(frameWireCost(frame))
}

// submitCreditFrame best-effort admits a CREDIT frame for grant bytes. CREDIT
// is admission-exempt (see [duplexConn.canAdmitLocked]) so the byte cap never
// rejects it; the admit fails only when the writer queue slot is full or the
// connection is closed, leaving the grant in the accumulator for a later
// [duplexConn.flushGrants] retry.
func (x *duplexConn) submitCreditFrame(grant int64) bool {
	if grant <= 0 {
		return true
	}

	payload := encodeCreditPayload(uint64(grant))
	return x.admitFrame(Frame{
		Version: ProtocolVersion,
		Type:    FrameTypeCredit,
		Lane:    x.lane,
		Length:  uint32(len(payload)),
		Payload: payload,
	}) == nil
}

// handleInboundCredit applies a CREDIT frame to the local send window. The
// frame is always consumed here (never delivered to Recv). When credits are
// disabled the payload is discarded so a revision-3 peer is not killed by a
// spurious CREDIT.
func (x *duplexConn) handleInboundCredit(frame Frame) {
	defer x.releaseFramePayload(frame.Payload)

	if !x.creditEnabled {
		return
	}

	grant, ok := consumeCreditPayload(frame.Payload)
	if !ok || grant == 0 {
		return
	}

	x.applyCreditGrant(grant)
}

// applyCreditGrant adds grant bytes to the send window, clamping so the
// signed counter cannot overflow, and wakes a parked writer. Negative windows
// (oversized-frame allowance) are restored carefully: MaxInt64-sw overflows
// int64 when sw is negative, so room is computed in unsigned arithmetic, and
// the applied amount is capped at MaxInt64 so converting it back to the
// signed counter cannot wrap negative on a hostile oversized grant.
func (x *duplexConn) applyCreditGrant(grant uint64) {
	if grant == 0 {
		x.wakeCreditWaiter()
		return
	}

	for {
		sw := x.sendWindow.Load()
		if sw == math.MaxInt64 {
			break
		}

		var room uint64
		if sw >= 0 {
			room = uint64(math.MaxInt64 - sw)
		} else {
			room = uint64(math.MaxInt64) + uint64(-sw)
		}

		add := grant
		if add > room {
			add = room
		}

		if add > math.MaxInt64 {
			add = math.MaxInt64
		}

		if add == 0 {
			break
		}

		if x.sendWindow.CompareAndSwap(sw, sw+int64(add)) {
			break
		}
	}

	x.wakeCreditWaiter()
}

// wakeCreditWaiter signals the writer that the send window may have room.
func (x *duplexConn) wakeCreditWaiter() {
	if x.creditWake == nil {
		return
	}

	select {
	case x.creditWake <- types.Unit{}:
	default:
	}
}

// canChargeWindow reports whether a windowed frame of cost bytes may be
// written now. When cost is at least the full negotiated window and the
// window is at (or above) its full value, the write is allowed so one
// oversized frame can proceed (the counter may go negative). The at-or-above
// form keeps the guarantee intact when a peer over-grants past the window.
func (x *duplexConn) canChargeWindow(cost int64) bool {
	if !x.creditEnabled {
		return true
	}

	sw := x.sendWindow.Load()
	if sw >= cost {
		return true
	}

	return cost >= x.creditWindow && sw >= x.creditWindow && x.creditWindow > 0
}

// chargeWindow subtracts cost from the send window. The writer is the sole
// decrementor; the reader only adds via [applyCreditGrant].
func (x *duplexConn) chargeWindow(cost int64) {
	if !x.creditEnabled {
		return
	}

	x.sendWindow.Add(-cost)
}
