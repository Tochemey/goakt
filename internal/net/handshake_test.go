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
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/internal/internalpb"
)

func TestHandshakeNegotiateAndPingPong(t *testing.T) {
	defer goleak.VerifyNone(t)

	transport := NewTCPTransport()
	acceptor, err := transport.Listen("127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = acceptor.Close() })

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)

		serverConn, err := acceptor.Accept(context.Background())
		if err != nil {
			return
		}
		defer serverConn.Close()

		local := testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 8<<20)
		result, err := acceptHello(serverConn, local)
		if err != nil {
			return
		}

		assert.Equal(t, uint32(4<<20), result.Effective.GetMaxFrameSize())

		for {
			frame, err := serverConn.ReadFrame()
			if err != nil {
				return
			}

			if frame.Type == FrameTypePing {
				_ = serverConn.WriteFrames(Frame{
					Version:     ProtocolVersion,
					Type:        FrameTypePong,
					Lane:        frame.Lane,
					Correlation: frame.Correlation,
				})
				return
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn, err := transport.Dial(ctx, acceptor.Addr().String(), LaneSpec{
		Role: internalpb.LaneRole_LANE_ROLE_CONTROL,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = clientConn.Close() })

	dialerHello := testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 4<<20)
	result, err := performHello(clientConn, dialerHello)
	require.NoError(t, err)
	assert.Equal(t, uint32(4<<20), result.Effective.GetMaxFrameSize())
	assert.Equal(t, internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, result.Effective.GetCompression())

	require.NoError(t, clientConn.WriteFrames(Frame{
		Version:     ProtocolVersion,
		Type:        FrameTypePing,
		Lane:        LaneControl,
		Correlation: 99,
	}))

	pong, err := clientConn.ReadFrame()
	require.NoError(t, err)
	assert.Equal(t, FrameTypePong, pong.Type)
	assert.Equal(t, uint64(99), pong.Correlation)

	_ = clientConn.Close()
	<-serverDone
}

func TestHandshakeCompressionAgreement(t *testing.T) {
	assert.Equal(t,
		internalpb.CompressionCodec_COMPRESSION_CODEC_ZSTD,
		selectCompression(
			internalpb.CompressionCodec_COMPRESSION_CODEC_ZSTD,
			internalpb.CompressionCodec_COMPRESSION_CODEC_ZSTD,
		),
	)
	assert.Equal(t,
		internalpb.CompressionCodec_COMPRESSION_CODEC_NONE,
		selectCompression(
			internalpb.CompressionCodec_COMPRESSION_CODEC_GZIP,
			internalpb.CompressionCodec_COMPRESSION_CODEC_ZSTD,
		),
	)
}

func TestHandshakeVersionMismatchInBandError(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	serverErr := make(chan error, 1)
	go func() {
		framed := newTCPFramedConn(c2, defaultMaxFrameSize)
		_, err := acceptHello(framed, testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 1<<20))
		serverErr <- err
		_ = framed.Close()
	}()

	var hdr [FrameHeaderSize]byte
	hdr[0] = 0x03
	hdr[1] = FrameTypeHello
	_, err := c1.Write(hdr[:])
	require.NoError(t, err)

	framed := newTCPFramedConn(c1, defaultMaxFrameSize)
	frame, err := framed.ReadFrame()
	require.NoError(t, err)
	assert.Equal(t, FrameTypeError, frame.Type)
	assert.Equal(t, ProtocolVersion, frame.Version)
	assert.Equal(t, uint64(0), frame.Correlation)
	require.ErrorIs(t, <-serverErr, ErrUnsupportedProtocolVersion)
	_ = framed.Close()
}

func TestNegotiateHelloPairwiseMinimum(t *testing.T) {
	local := testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 8<<20)
	local.MaxMessageSize = 32 << 20
	local.InitialCredits = 16 << 20
	local.Revision = 4

	remote := testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 4<<20)
	remote.MaxMessageSize = 64 << 20
	remote.InitialCredits = 8 << 20
	remote.Revision = 2

	got := negotiateHello(local, remote)
	assert.Equal(t, uint32(2), got.GetRevision())
	assert.Equal(t, uint32(4<<20), got.GetMaxFrameSize())
	assert.Equal(t, uint64(32<<20), got.GetMaxMessageSize())
	assert.Equal(t, uint64(8<<20), got.GetInitialCredits())
}

func TestNegotiateHelloFloorsZeroMaxFrameSize(t *testing.T) {
	local := testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 8<<20)
	remote := testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 0)

	got := negotiateHello(local, remote)
	assert.Equal(t, minMaxFrameSize, got.GetMaxFrameSize())
}

func TestPerformHelloNilLocal(t *testing.T) {
	_, err := performHello(nil, nil)
	require.Error(t, err)
}

func TestAcceptHelloNilLocal(t *testing.T) {
	_, err := acceptHello(nil, nil)
	require.Error(t, err)
}

func TestAcceptHelloRejectsNonHello(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	errCh := make(chan error, 1)
	go func() {
		framed := newTCPFramedConn(c2, defaultMaxFrameSize)
		_, err := acceptHello(framed, testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 1<<20))
		errCh <- err
		_ = framed.Close()
	}()

	client := newTCPFramedConn(c1, defaultMaxFrameSize)
	require.NoError(t, client.WriteFrames(Frame{
		Version: ProtocolVersion,
		Type:    FrameTypePing,
		Lane:    LaneControl,
	}))

	frame, err := client.ReadFrame()
	require.NoError(t, err)
	assert.Equal(t, FrameTypeError, frame.Type)

	require.Error(t, <-errCh)
	_ = client.Close()
}

func TestAcceptHelloRejectsInvalidPayload(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	errCh := make(chan error, 1)
	go func() {
		framed := newTCPFramedConn(c2, defaultMaxFrameSize)
		_, err := acceptHello(framed, testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 1<<20))
		errCh <- err
		_ = framed.Close()
	}()

	client := newTCPFramedConn(c1, defaultMaxFrameSize)
	require.NoError(t, client.WriteFrames(Frame{
		Version: ProtocolVersion,
		Type:    FrameTypeHello,
		Lane:    LaneControl,
		Length:  3,
		Payload: []byte{0x01, 0x02, 0x03},
	}))

	frame, err := client.ReadFrame()
	require.NoError(t, err)
	assert.Equal(t, FrameTypeError, frame.Type)

	require.Error(t, <-errCh)
	_ = client.Close()
}

func TestPerformHelloRejectedByErrorFrame(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	go func() {
		framed := newTCPFramedConn(c2, defaultMaxFrameSize)
		_, _ = framed.ReadFrame()
		_ = writeErrorFrame(framed, 0, internalpb.Code_CODE_FAILED_PRECONDITION, "nope")
		_ = framed.Close()
	}()

	client := newTCPFramedConn(c1, defaultMaxFrameSize)
	_, err := performHello(client, testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 1<<20))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "handshake rejected")
	_ = client.Close()
}

func TestPerformHelloUnexpectedAckType(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	go func() {
		framed := newTCPFramedConn(c2, defaultMaxFrameSize)
		_, _ = framed.ReadFrame()
		_ = framed.WriteFrames(Frame{
			Version: ProtocolVersion,
			Type:    FrameTypePong,
			Lane:    LaneControl,
		})
		_ = framed.Close()
	}()

	client := newTCPFramedConn(c1, defaultMaxFrameSize)
	_, err := performHello(client, testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 1<<20))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected HELLO_ACK")
	_ = client.Close()
}

func TestPerformHelloInvalidAckPayload(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	go func() {
		framed := newTCPFramedConn(c2, defaultMaxFrameSize)
		_, _ = framed.ReadFrame()
		_ = framed.WriteFrames(Frame{
			Version: ProtocolVersion,
			Type:    FrameTypeHelloAck,
			Lane:    LaneControl,
			Length:  2,
			Payload: []byte{0xff, 0xfe},
		})
		_ = framed.Close()
	}()

	client := newTCPFramedConn(c1, defaultMaxFrameSize)
	_, err := performHello(client, testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 1<<20))
	require.Error(t, err)
	_ = client.Close()
}

func TestDecodeErrorPayload(t *testing.T) {
	payload, err := proto.Marshal(&internalpb.Error{
		Code:    internalpb.Code_CODE_NOT_FOUND,
		Message: "missing",
	})
	require.NoError(t, err)

	decoded := decodeErrorPayload(payload)
	require.Error(t, decoded)
	assert.Contains(t, decoded.Error(), "missing")

	bad := decodeErrorPayload([]byte{0x01, 0x02})
	require.Error(t, bad)
	assert.Contains(t, bad.Error(), "unreadable error payload")
}

func TestLaneByte(t *testing.T) {
	got, err := laneByte(internalpb.LaneRole_LANE_ROLE_CONTROL, 0)
	require.NoError(t, err)
	assert.Equal(t, LaneControl, got)

	got, err = laneByte(internalpb.LaneRole_LANE_ROLE_LARGE, 0)
	require.NoError(t, err)
	assert.Equal(t, LaneLarge, got)

	got, err = laneByte(internalpb.LaneRole_LANE_ROLE_ORDINARY, 0)
	require.NoError(t, err)
	assert.Equal(t, LaneOrdinary, got)

	got, err = laneByte(internalpb.LaneRole_LANE_ROLE_ORDINARY, 1)
	require.NoError(t, err)
	assert.Equal(t, byte(2), got)

	got, err = laneByte(internalpb.LaneRole_LANE_ROLE_ORDINARY, 3)
	require.NoError(t, err)
	assert.Equal(t, byte(4), got)

	_, err = laneByte(internalpb.LaneRole_LANE_ROLE_ORDINARY, maxOrdinaryLaneIndex+1)
	require.ErrorIs(t, err, ErrInvalidLaneIndex)

	got, err = laneByte(internalpb.LaneRole(99), 0)
	require.Error(t, err)
	assert.Zero(t, got)
}

func TestAcceptHelloAckEchoesDialerLane(t *testing.T) {
	for _, lane := range []LaneSpec{
		{Role: internalpb.LaneRole_LANE_ROLE_ORDINARY, Index: 3},
		{Role: internalpb.LaneRole_LANE_ROLE_LARGE},
	} {
		c1, c2 := net.Pipe()

		serverResult := make(chan *HandshakeResult, 1)
		serverErr := make(chan error, 1)
		go func() {
			framed := newTCPFramedConn(c2, defaultMaxFrameSize)
			result, err := acceptHello(framed, testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 1<<20))
			serverResult <- result
			serverErr <- err
			_ = framed.Close()
		}()

		client := newTCPFramedConn(c1, defaultMaxFrameSize)
		hello := testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 1<<20)
		hello.LaneRole = lane.Role
		hello.LaneIndex = lane.Index
		payload, err := proto.Marshal(hello)
		require.NoError(t, err)
		laneByte, err := laneByte(lane.Role, lane.Index)
		require.NoError(t, err)
		require.NoError(t, client.WriteFrames(encodeHelloFrame(FrameTypeHello, laneByte, payload)))

		frame, err := client.ReadFrame()
		require.NoError(t, err)
		require.Equal(t, FrameTypeHelloAck, frame.Type)
		require.Equal(t, laneByte, frame.Lane)

		ack := new(internalpb.Hello)
		require.NoError(t, proto.Unmarshal(frame.Payload, ack))
		assert.Equal(t, lane.Role, ack.GetLaneRole())
		assert.Equal(t, lane.Index, ack.GetLaneIndex())

		result := <-serverResult
		require.NoError(t, <-serverErr)
		assert.Equal(t, lane.Role, result.Effective.GetLaneRole())
		assert.Equal(t, lane.Index, result.Effective.GetLaneIndex())
		require.NoError(t, client.Close())
	}
}

func TestAcceptHelloRejectsInvalidOrdinaryLaneIndex(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	serverErr := make(chan error, 1)
	go func() {
		framed := newTCPFramedConn(c2, defaultMaxFrameSize)
		_, err := acceptHello(framed, testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 1<<20))
		serverErr <- err
		_ = framed.Close()
	}()

	hello := testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 1<<20)
	hello.LaneRole = internalpb.LaneRole_LANE_ROLE_ORDINARY
	hello.LaneIndex = maxOrdinaryLaneIndex + 1
	payload, err := proto.Marshal(hello)
	require.NoError(t, err)

	client := newTCPFramedConn(c1, defaultMaxFrameSize)
	require.NoError(t, client.WriteFrames(encodeHelloFrame(FrameTypeHello, LaneOrdinary, payload)))
	frame, err := client.ReadFrame()
	require.NoError(t, err)
	assert.Equal(t, FrameTypeError, frame.Type)
	assert.Equal(t, uint64(0), frame.Correlation)
	require.ErrorIs(t, <-serverErr, ErrInvalidLaneIndex)
}

func TestWrapCompression(t *testing.T) {
	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	none, err := wrapCompression(c1, internalpb.CompressionCodec_COMPRESSION_CODEC_NONE)
	require.NoError(t, err)
	assert.Equal(t, c1, none)

	gzipConn, err := wrapCompression(c1, internalpb.CompressionCodec_COMPRESSION_CODEC_GZIP)
	require.NoError(t, err)
	require.NotEqual(t, c1, gzipConn)
	go io.Copy(io.Discard, c2)
	require.NoError(t, gzipConn.Close())

	c3, c4 := net.Pipe()
	t.Cleanup(func() {
		_ = c3.Close()
		_ = c4.Close()
	})
	zstdConn, err := wrapCompression(c3, internalpb.CompressionCodec_COMPRESSION_CODEC_ZSTD)
	require.NoError(t, err)
	require.NotEqual(t, c3, zstdConn)
	go io.Copy(io.Discard, c4)
	require.NoError(t, zstdConn.Close())

	c5, c6 := net.Pipe()
	t.Cleanup(func() {
		_ = c5.Close()
		_ = c6.Close()
	})
	brotliConn, err := wrapCompression(c5, internalpb.CompressionCodec_COMPRESSION_CODEC_BROTLI)
	require.NoError(t, err)
	require.NotEqual(t, c5, brotliConn)
	go io.Copy(io.Discard, c6)
	require.NoError(t, brotliConn.Close())

	unknown, err := wrapCompression(c2, internalpb.CompressionCodec(99))
	require.NoError(t, err)
	assert.Equal(t, c2, unknown)
}

func TestWriteErrorFrameAllowsZeroCorrelation(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	go func() {
		framed := newTCPFramedConn(c2, defaultMaxFrameSize)
		_ = writeErrorFrame(framed, 0, internalpb.Code_CODE_INTERNAL, "x")
		_ = framed.Close()
	}()

	client := newTCPFramedConn(c1, defaultMaxFrameSize)
	frame, err := client.ReadFrame()
	require.NoError(t, err)
	assert.Equal(t, FrameTypeError, frame.Type)
	assert.Equal(t, uint64(0), frame.Correlation)
	_ = client.Close()
}

func TestAcceptHelloRejectsZeroRevision(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	serverErr := make(chan error, 1)
	go func() {
		framed := newTCPFramedConn(c2, defaultMaxFrameSize)
		_, err := acceptHello(framed, testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 1<<20))
		serverErr <- err
		_ = framed.Close()
	}()

	hello := testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 1<<20)
	hello.Revision = 0
	payload, err := proto.Marshal(hello)
	require.NoError(t, err)

	client := newTCPFramedConn(c1, defaultMaxFrameSize)
	require.NoError(t, client.WriteFrames(encodeHelloFrame(FrameTypeHello, LaneControl, payload)))

	frame, err := client.ReadFrame()
	require.NoError(t, err)
	assert.Equal(t, FrameTypeError, frame.Type)
	assert.Equal(t, uint64(0), frame.Correlation)

	require.ErrorIs(t, <-serverErr, ErrInvalidCapabilityRevision)
	_ = client.Close()
}

func TestPerformHelloRejectsZeroRevisionAck(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)

		framed := newTCPFramedConn(c2, defaultMaxFrameSize)

		frame, err := framed.ReadFrame()
		if err != nil || frame.Type != FrameTypeHello {
			return
		}

		ack := testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 1<<20)
		ack.Revision = 0

		payload, err := proto.Marshal(ack)
		if err != nil {
			return
		}

		_ = framed.WriteFrames(encodeHelloFrame(FrameTypeHelloAck, LaneControl, payload))
		// Drain the dialer's connection-scoped ERROR so its best-effort write
		// cannot block on the synchronous pipe.
		_, _ = framed.ReadFrame()
		_ = framed.Close()
	}()

	client := newTCPFramedConn(c1, defaultMaxFrameSize)
	_, err := performHello(client, testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 1<<20))
	require.ErrorIs(t, err, ErrInvalidCapabilityRevision)

	_ = client.Close()
	<-serverDone
}

func TestMinUintHelpers(t *testing.T) {
	assert.Equal(t, uint32(1), minUint32(1, 2))
	assert.Equal(t, uint32(1), minUint32(2, 1))
	assert.Equal(t, uint64(1), minUint64(1, 2))
	assert.Equal(t, uint64(1), minUint64(2, 1))
}

func testHello(codec internalpb.CompressionCodec, maxFrame uint32) *internalpb.Hello {
	return &internalpb.Hello{
		Revision:                    CapabilityRevisionBaseline,
		SystemName:                  "test",
		Host:                        "127.0.0.1",
		Port:                        1,
		LaneRole:                    internalpb.LaneRole_LANE_ROLE_CONTROL,
		Compression:                 codec,
		MaxFrameSize:                maxFrame,
		MaxMessageSize:              uint64(maxFrame),
		InitialCredits:              uint64(maxFrame),
		MaxConcurrentLargeTransfers: 4,
	}
}
