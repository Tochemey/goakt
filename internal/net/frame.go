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
	"fmt"

	"github.com/tochemey/goakt/v4/internal/size"
)

// Duplex protocol frame header constants. The first header byte is also the
// dual-protocol listener discriminator against legacy length-prefixed frames.
const (
	// FrameHeaderSize is the fixed duplex frame header length in bytes.
	FrameHeaderSize = 16

	// ProtocolVersion is the duplex wire version and the first-byte sniff
	// value that routes a connection onto the new-protocol acceptor.
	ProtocolVersion byte = 0x02

	// Frame type identifiers.
	FrameTypeHello    byte = 0x01
	FrameTypeHelloAck byte = 0x02
	FrameTypeData     byte = 0x03
	FrameTypeReply    byte = 0x04
	FrameTypeError    byte = 0x05
	FrameTypeChunk    byte = 0x06
	FrameTypeCredit   byte = 0x07
	FrameTypeTable    byte = 0x08
	FrameTypePing     byte = 0x09
	FrameTypePong     byte = 0x0A

	// Frame flag bits (byte 2 of the header).
	FrameFlagHasMetadata  byte = 1 << 0
	FrameFlagExpectsReply byte = 1 << 1
	FrameFlagFirstChunk   byte = 1 << 2
	FrameFlagLastChunk    byte = 1 << 3
	frameFlagReservedMask byte = 0xF0

	// Lane header values used for validation and diagnostics.
	LaneControl  byte = 0x00
	LaneOrdinary byte = 0x01 // ordinary lanes occupy 1..N (header byte = index+1)
	LaneLarge    byte = 0xFF

	// maxOrdinaryLaneIndex is the largest ordinary lane index that encodes as
	// a header byte below [LaneLarge] (index+1 <= 0xFE).
	maxOrdinaryLaneIndex uint32 = 0xFE - 1

	// MaxOrdinaryLanes is the largest OrdinaryLanes count that can be encoded
	// on the wire (indexes 0..maxOrdinaryLaneIndex → header bytes 1..0xFE).
	MaxOrdinaryLanes uint32 = maxOrdinaryLaneIndex + 1

	// minMaxFrameSize is the floor for negotiated max_frame_size, matching the
	// legacy remote.Config validation lower bound (16 KiB).
	minMaxFrameSize uint32 = 16 * size.KB

	// defaultMaxConcurrentLargeTransfers is the HELLO default for concurrent
	// large-message transfers (see wire-reference defaults table).
	defaultMaxConcurrentLargeTransfers uint32 = 4

	// defaultInitialCredits is the HELLO default credit window and duplex
	// outbound queue byte cap (16 MiB).
	defaultInitialCredits uint64 = 16 * size.MB

	// CapabilityRevisionBaseline is the minimum duplex capability revision.
	// It covers DATA, REPLY, ERROR, PING, and PONG.
	CapabilityRevisionBaseline uint32 = 1

	// CapabilityRevisionChunking is the revision that enables CHUNK frames and
	// logical messages larger than the negotiated max frame size.
	CapabilityRevisionChunking uint32 = 2

	// CapabilityRevisionTables is the revision that enables TABLE frames and
	// compressed actor-path / type-name refs in DATA and REPLY envelopes.
	CapabilityRevisionTables uint32 = 3

	// CapabilityRevisionCredits is the revision that enables receiver-granted
	// credit windows on duplex connections. Below this revision the send
	// window is unlimited (pre-credit behavior).
	CapabilityRevisionCredits uint32 = 4

	// DefaultChunkSize is the default logical-frame threshold above which a
	// sender splits into CHUNK frames (256 KiB).
	DefaultChunkSize uint32 = 256 * size.KB

	// DefaultMaxMessageSize is the default cap on a reassembled logical frame
	// (16 MiB). Operators may raise it above the max frame size.
	DefaultMaxMessageSize uint64 = 16 * size.MB

	// DefaultTableCapacity is the per-kind per-connection bound on compression
	// table entries (actor paths or type names).
	DefaultTableCapacity = 8192

	// TableKindActorPath identifies an actor-address literal in a TABLE frame.
	TableKindActorPath byte = 0

	// TableKindTypeName identifies a message type-name literal in a TABLE frame.
	TableKindTypeName byte = 1
)

// Frame is one duplex-protocol unit: a fixed 16-byte header plus an optional
// payload. On the wire the header layout is:
//
//	byte 0        1        2        3        4..7          8..15
//	+--------+--------+--------+--------+--------------+------------------+
//	| ver    | type   | flags  | lane   | length (BE)  | correlation (BE) |
//	+--------+--------+--------+--------+--------------+------------------+
//
// Encode and decode paths validate Version, Type, reserved flag bits, and
// Correlation requirements before the frame is accepted.
type Frame struct {
	// Version is the protocol version byte. It must be [ProtocolVersion]
	// (0x02). The same value is the dual-protocol listener discriminator
	// against legacy length-prefixed frames.
	Version byte

	// Type identifies the frame kind. Valid values are the FrameType*
	// constants (HELLO, HELLO_ACK, DATA, REPLY, ERROR, CHUNK, CREDIT,
	// TABLE, PING, PONG).
	Type byte

	// Flags is a bitset of FrameFlag* values. Bits 0 to 3 carry hasMetadata,
	// expectsReply, firstChunk, and lastChunk; bits 4 to 7 are reserved and
	// must be zero on the wire.
	Flags byte

	// Lane is the sender's lane role/index for validation and diagnostics:
	// [LaneControl] (0), ordinary lanes 1..N (header byte = index+1), or
	// [LaneLarge] (0xFF). Receivers check it against the connection's
	// negotiated role.
	Lane byte

	// Length is the number of body bytes following the header, encoded as a
	// big-endian uint32. On write it must equal len(Prefix)+len(Payload). On
	// read the full body is placed in Payload (Prefix is unused) and Length
	// is bounded by the negotiated max frame size.
	Length uint32

	// Correlation links requests to replies, errors, and chunk groups.
	// Zero means none. It must be nonzero for REPLY, CHUNK, and any DATA
	// frame with the expectsReply flag. Connection-scoped ERROR frames use
	// correlation 0; request-scoped ERROR frames echo the failed request.
	Correlation uint64

	// Prefix is an optional body prefix written before Payload. CHUNK senders
	// use it for the index/totalSize uvarints so Payload can be a subslice of
	// the once-serialized logical buffer without a per-chunk copy. Readers
	// always leave Prefix nil and place the full body in Payload.
	Prefix []byte

	// Payload is the frame body (or the body suffix when Prefix is set). It
	// may be nil or empty when Length is zero (for example PING/PONG).
	// Callers must not retain a Payload slice past the lifetime of a pooled
	// frame buffer when one is in use.
	Payload []byte
}

// bodyLen returns the on-wire body size of f (Prefix plus Payload).
func (x Frame) bodyLen() int {
	return len(x.Prefix) + len(x.Payload)
}

// HasMetadata reports whether the hasMetadata flag is set.
func (x Frame) HasMetadata() bool {
	return x.Flags&FrameFlagHasMetadata != 0
}

// ExpectsReply reports whether the expectsReply flag is set.
func (x Frame) ExpectsReply() bool {
	return x.Flags&FrameFlagExpectsReply != 0
}

// IsFirstChunk reports whether the firstChunk flag is set.
func (x Frame) IsFirstChunk() bool {
	return x.Flags&FrameFlagFirstChunk != 0
}

// IsLastChunk reports whether the lastChunk flag is set.
func (x Frame) IsLastChunk() bool {
	return x.Flags&FrameFlagLastChunk != 0
}

// encodeFrameHeader writes the 16-byte duplex header for f into dst.
// dst must have length at least [FrameHeaderSize].
func encodeFrameHeader(dst []byte, f Frame) error {
	if len(dst) < FrameHeaderSize {
		return fmt.Errorf("tcp: frame header buffer too small: need %d, got %d", FrameHeaderSize, len(dst))
	}

	if err := validateFrameHeader(f, 0); err != nil {
		return err
	}

	dst[0] = f.Version
	dst[1] = f.Type
	dst[2] = f.Flags
	dst[3] = f.Lane
	binary.BigEndian.PutUint32(dst[4:8], f.Length)
	binary.BigEndian.PutUint64(dst[8:16], f.Correlation)
	return nil
}

// decodeFrameHeader parses a 16-byte duplex header from src.
// maxFrameSize bounds Length. A zero value is raised to [minMaxFrameSize] so a
// peer cannot disable validation by advertising a zero limit.
func decodeFrameHeader(src []byte, maxFrameSize uint32) (Frame, error) {
	if len(src) < FrameHeaderSize {
		return Frame{}, fmt.Errorf("tcp: frame header truncated: need %d, got %d", FrameHeaderSize, len(src))
	}

	frame := Frame{
		Version:     src[0],
		Type:        src[1],
		Flags:       src[2],
		Lane:        src[3],
		Length:      binary.BigEndian.Uint32(src[4:8]),
		Correlation: binary.BigEndian.Uint64(src[8:16]),
	}

	if maxFrameSize == 0 {
		maxFrameSize = minMaxFrameSize
	}

	if err := validateFrameHeader(frame, maxFrameSize); err != nil {
		return Frame{}, err
	}

	return frame, nil
}

// validateFrameHeader checks version, type, reserved flags, correlation
// requirements, and optional length bound. A zero maxFrameSize skips the
// length check (encode path); decode always supplies a positive bound.
func validateFrameHeader(frame Frame, maxFrameSize uint32) error {
	if frame.Version != ProtocolVersion {
		return fmt.Errorf("%w: 0x%02x", ErrUnsupportedProtocolVersion, frame.Version)
	}

	if !isKnownFrameType(frame.Type) {
		return fmt.Errorf("tcp: unknown frame type 0x%02x", frame.Type)
	}

	if frame.Flags&frameFlagReservedMask != 0 {
		return fmt.Errorf("tcp: reserved frame flags set: 0x%02x", frame.Flags&frameFlagReservedMask)
	}

	if maxFrameSize > 0 && frame.Length > maxFrameSize {
		return fmt.Errorf("%w: length %d exceeds max %d", ErrFrameTooLarge, frame.Length, maxFrameSize)
	}

	if err := validateCorrelation(frame); err != nil {
		return err
	}

	return nil
}

// validateCorrelation enforces nonzero correlation where the wire reference
// requires it. Connection-scoped ERROR frames may use correlation 0.
func validateCorrelation(frame Frame) error {
	needsCorrelation := frame.Type == FrameTypeReply ||
		frame.Type == FrameTypeChunk ||
		(frame.Type == FrameTypeData && frame.ExpectsReply())

	if needsCorrelation && frame.Correlation == 0 {
		return fmt.Errorf("tcp: frame type 0x%02x requires a nonzero correlation id", frame.Type)
	}

	return nil
}

// isKnownFrameType reports whether t is a defined duplex frame type.
func isKnownFrameType(t byte) bool {
	switch t {
	case FrameTypeHello, FrameTypeHelloAck, FrameTypeData, FrameTypeReply,
		FrameTypeError, FrameTypeChunk, FrameTypeCredit, FrameTypeTable,
		FrameTypePing, FrameTypePong:
		return true
	default:
		return false
	}
}
