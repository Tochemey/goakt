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
	"errors"
	"fmt"
	gonet "net"

	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/internal/internalpb"
)

// HandshakeResult is the effective parameter set after HELLO/HELLO_ACK.
type HandshakeResult struct {
	Local     *internalpb.Hello
	Remote    *internalpb.Hello
	Effective *internalpb.Hello
}

// performHello runs the dialer side of the duplex handshake: send HELLO,
// read HELLO_ACK, compute effective limits.
func performHello(conn FramedConn, local *internalpb.Hello) (*HandshakeResult, error) {
	if local == nil {
		return nil, fmt.Errorf("tcp: hello local parameters are required")
	}

	payload, err := proto.Marshal(local)
	if err != nil {
		return nil, err
	}

	lane, err := laneByte(local.GetLaneRole(), local.GetLaneIndex())
	if err != nil {
		return nil, err
	}

	if err := conn.WriteFrames(encodeHelloFrame(FrameTypeHello, lane, payload)); err != nil {
		return nil, err
	}

	frame, err := conn.ReadFrame()
	if err != nil {
		writeProtocolVersionError(conn, err)
		return nil, err
	}

	if frame.Type == FrameTypeError {
		return nil, fmt.Errorf("tcp: handshake rejected: %v", decodeErrorPayload(frame.Payload))
	}

	if frame.Type != FrameTypeHelloAck {
		return nil, fmt.Errorf("tcp: expected HELLO_ACK, got frame type 0x%02x", frame.Type)
	}

	remote := new(internalpb.Hello)
	if err := proto.Unmarshal(frame.Payload, remote); err != nil {
		return nil, err
	}

	if remote.GetRevision() < CapabilityRevisionBaseline {
		_ = writeErrorFrame(conn, 0, internalpb.Code_CODE_INVALID_ARGUMENT, "capability revision below baseline")
		return nil, fmt.Errorf("%w: %d", ErrInvalidCapabilityRevision, remote.GetRevision())
	}

	effective := negotiateHello(local, remote)
	effective.Compression = remote.GetCompression()
	conn.SetMaxFrameSize(effective.GetMaxFrameSize())
	return &HandshakeResult{Local: local, Remote: remote, Effective: effective}, nil
}

// acceptHello runs the acceptor side of the duplex handshake: read HELLO,
// reply HELLO_ACK (or ERROR on protocol violation), compute effective limits.
func acceptHello(conn FramedConn, local *internalpb.Hello) (*HandshakeResult, error) {
	if local == nil {
		return nil, fmt.Errorf("tcp: hello local parameters are required")
	}

	frame, err := conn.ReadFrame()
	if err != nil {
		writeProtocolVersionError(conn, err)
		return nil, err
	}

	if frame.Type != FrameTypeHello {
		_ = writeErrorFrame(conn, 0, internalpb.Code_CODE_FAILED_PRECONDITION, "expected HELLO")
		return nil, fmt.Errorf("tcp: expected HELLO, got frame type 0x%02x", frame.Type)
	}

	remote := new(internalpb.Hello)
	if err := proto.Unmarshal(frame.Payload, remote); err != nil {
		_ = writeErrorFrame(conn, 0, internalpb.Code_CODE_INVALID_ARGUMENT, "invalid HELLO payload")
		return nil, err
	}

	if remote.GetRevision() < CapabilityRevisionBaseline {
		_ = writeErrorFrame(conn, 0, internalpb.Code_CODE_INVALID_ARGUMENT, "capability revision below baseline")
		return nil, fmt.Errorf("%w: %d", ErrInvalidCapabilityRevision, remote.GetRevision())
	}

	ack := negotiateHello(local, remote)
	ack.Compression = selectCompression(local.GetCompression(), remote.GetCompression())
	ack.SystemName = local.GetSystemName()
	ack.Host = local.GetHost()
	ack.Port = local.GetPort()
	ack.LaneRole = local.GetLaneRole()
	ack.LaneIndex = local.GetLaneIndex()

	payload, err := proto.Marshal(ack)
	if err != nil {
		return nil, err
	}

	lane, err := laneByte(ack.GetLaneRole(), ack.GetLaneIndex())
	if err != nil {
		return nil, err
	}

	if err := conn.WriteFrames(encodeHelloFrame(FrameTypeHelloAck, lane, payload)); err != nil {
		return nil, err
	}

	conn.SetMaxFrameSize(ack.GetMaxFrameSize())
	return &HandshakeResult{Local: local, Remote: remote, Effective: ack}, nil
}

// negotiateHello returns pairwise minima and the lower capability revision.
// max_frame_size is floored at [minMaxFrameSize] so a peer advertising zero
// cannot disable length validation.
func negotiateHello(local, remote *internalpb.Hello) *internalpb.Hello {
	return &internalpb.Hello{
		Revision:                    minUint32(local.GetRevision(), remote.GetRevision()),
		SystemName:                  local.GetSystemName(),
		Host:                        local.GetHost(),
		Port:                        local.GetPort(),
		LaneRole:                    local.GetLaneRole(),
		LaneIndex:                   local.GetLaneIndex(),
		Compression:                 remote.GetCompression(),
		MaxFrameSize:                floorMaxFrameSize(minUint32(local.GetMaxFrameSize(), remote.GetMaxFrameSize())),
		MaxMessageSize:              minUint64(local.GetMaxMessageSize(), remote.GetMaxMessageSize()),
		InitialCredits:              minUint64(local.GetInitialCredits(), remote.GetInitialCredits()),
		MaxConcurrentLargeTransfers: minUint32(local.GetMaxConcurrentLargeTransfers(), remote.GetMaxConcurrentLargeTransfers()),
	}
}

// selectCompression picks the codec both sides will use. The acceptor answers
// with the dialer's proposal when it matches the acceptor's configured codec;
// otherwise it falls back to NONE.
func selectCompression(local, remote internalpb.CompressionCodec) internalpb.CompressionCodec {
	if local == remote {
		return remote
	}

	return internalpb.CompressionCodec_COMPRESSION_CODEC_NONE
}

// writeErrorFrame writes an ERROR frame with an [internalpb.Error] payload.
// Correlation may be zero for connection-scoped errors.
func writeErrorFrame(conn FramedConn, correlation uint64, code internalpb.Code, message string) error {
	payload, err := proto.Marshal(&internalpb.Error{
		Code:    code,
		Message: message,
	})
	if err != nil {
		return err
	}

	return conn.WriteFrames(Frame{
		Version:     ProtocolVersion,
		Type:        FrameTypeError,
		Lane:        LaneControl,
		Length:      uint32(len(payload)),
		Correlation: correlation,
		Payload:     payload,
	})
}

// writeProtocolVersionError emits a best-effort connection-scoped ERROR when
// err is a protocol-version mismatch; any other error is left untouched.
func writeProtocolVersionError(conn FramedConn, err error) {
	if !errors.Is(err, ErrUnsupportedProtocolVersion) {
		return
	}

	_ = writeErrorFrame(conn, 0, internalpb.Code_CODE_FAILED_PRECONDITION, "unsupported protocol version")
}

// decodeErrorPayload best-effort decodes an ERROR payload for diagnostics.
func decodeErrorPayload(payload []byte) error {
	var e internalpb.Error
	if err := proto.Unmarshal(payload, &e); err != nil {
		return fmt.Errorf("unreadable error payload: %w", err)
	}

	return fmt.Errorf("%s: %s", e.GetCode().String(), e.GetMessage())
}

// laneByte maps a lane role/index to the header lane byte. Ordinary lanes use
// index+1 so index 0 is [LaneOrdinary] and [LaneLarge] stays unambiguous.
func laneByte(role internalpb.LaneRole, index uint32) (byte, error) {
	switch role {
	case internalpb.LaneRole_LANE_ROLE_CONTROL:
		return LaneControl, nil
	case internalpb.LaneRole_LANE_ROLE_LARGE:
		return LaneLarge, nil
	case internalpb.LaneRole_LANE_ROLE_ORDINARY:
		if index > maxOrdinaryLaneIndex {
			return 0, fmt.Errorf("%w: %d", ErrInvalidLaneIndex, index)
		}

		return byte(index + 1), nil
	default:
		return LaneControl, nil
	}
}

// wrapCompression applies the negotiated whole-connection compression wrapper.
func wrapCompression(raw gonet.Conn, codec internalpb.CompressionCodec) (gonet.Conn, error) {
	switch codec {
	case internalpb.CompressionCodec_COMPRESSION_CODEC_NONE:
		return raw, nil
	case internalpb.CompressionCodec_COMPRESSION_CODEC_GZIP:
		w, err := NewGzipConnWrapper()
		if err != nil {
			return nil, err
		}

		return w.Wrap(raw)
	case internalpb.CompressionCodec_COMPRESSION_CODEC_ZSTD:
		w, err := NewZstdConnWrapper()
		if err != nil {
			return nil, err
		}

		return w.Wrap(raw)
	case internalpb.CompressionCodec_COMPRESSION_CODEC_BROTLI:
		return NewBrotliConnWrapper().Wrap(raw)
	default:
		return raw, nil
	}
}

func minUint32(a, b uint32) uint32 {
	if a < b {
		return a
	}

	return b
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}

	return b
}
