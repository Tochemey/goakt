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
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/internal/internalpb"
)

func TestTCPTransportConformance(t *testing.T) {
	transport := NewTCPTransport(
		WithTCPTransportDialTimeout(time.Second),
		WithTCPTransportKeepAlive(time.Second),
		WithTCPTransportMaxFrameSize(1<<20),
	)

	acceptor, err := transport.Listen("127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = acceptor.Close() })

	accepted := make(chan FramedConn, 1)
	go func() {
		conn, err := acceptor.Accept(context.Background())
		if err != nil {
			return
		}
		accepted <- conn
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	client, err := transport.Dial(ctx, acceptor.Addr().String(), LaneSpec{
		Role:  internalpb.LaneRole_LANE_ROLE_ORDINARY,
		Index: 1,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	require.NotNil(t, client.NetConn())
	require.NoError(t, client.WriteFrames(Frame{
		Version: ProtocolVersion,
		Type:    FrameTypePing,
		Lane:    LaneOrdinary,
	}))

	server := <-accepted
	t.Cleanup(func() { _ = server.Close() })

	frame, err := server.ReadFrame()
	require.NoError(t, err)
	assert.Equal(t, FrameTypePing, frame.Type)
	assert.Equal(t, uint32(1<<20), client.MaxFrameSize())
}

func TestTCPTransportDialFailure(t *testing.T) {
	transport := NewTCPTransport(WithTCPTransportDialTimeout(50 * time.Millisecond))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := transport.Dial(ctx, "127.0.0.1:1", LaneSpec{})
	require.Error(t, err)
}

func TestTCPTransportListenFailure(t *testing.T) {
	transport := NewTCPTransport()
	_, err := transport.Listen("127.0.0.1:999999")
	require.Error(t, err)
}

func TestTCPAcceptorAcceptCanceled(t *testing.T) {
	transport := NewTCPTransport()
	acceptor, err := transport.Listen("127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = acceptor.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err = acceptor.Accept(ctx)
	require.Error(t, err)
}

func TestTCPFramedConnWriteReadPayload(t *testing.T) {
	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	left := newTCPFramedConn(c1, defaultMaxFrameSize)
	right := newTCPFramedConn(c2, defaultMaxFrameSize)

	payload := []byte("hello-duplex")
	errCh := make(chan error, 1)
	go func() {
		errCh <- left.WriteFrames(Frame{
			Type:        FrameTypeData,
			Lane:        LaneOrdinary,
			Flags:       FrameFlagExpectsReply,
			Length:      uint32(len(payload)),
			Payload:     payload,
			Correlation: 7,
		})
	}()

	frame, err := right.ReadFrame()
	require.NoError(t, err)
	assert.Equal(t, ProtocolVersion, frame.Version)
	assert.Equal(t, FrameTypeData, frame.Type)
	assert.Equal(t, payload, frame.Payload)
	assert.Equal(t, uint64(7), frame.Correlation)
	require.NoError(t, <-errCh)
}

func TestTCPFramedConnReadPoolRelease(t *testing.T) {
	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	left := newTCPFramedConn(c1, defaultMaxFrameSize)
	right := newTCPFramedConn(c2, defaultMaxFrameSize)
	require.NotNil(t, right.readPool)

	payload := []byte("pooled-read-body")
	errCh := make(chan error, 1)
	go func() {
		errCh <- left.WriteFrames(Frame{
			Type:        FrameTypeChunk,
			Lane:        LaneOrdinary,
			Length:      uint32(len(payload)),
			Correlation: 1,
			Payload:     payload,
		})
	}()

	frame, err := right.ReadFrame()
	require.NoError(t, err)
	require.Equal(t, payload, frame.Payload)

	right.releaseReadPayload(frame.Payload)
	buf := right.getReadPayload(FrameTypeChunk, len(payload))
	require.Len(t, buf, len(payload))
	right.releaseReadPayload(buf)
	require.NoError(t, <-errCh)

	// DATA/REPLY/ERROR bodies also draw from the read pool and must be
	// released after Deserialize or drop.
	pooled := right.getReadPayload(FrameTypeData, 8)
	require.Len(t, pooled, 8)
	require.GreaterOrEqual(t, cap(pooled), 8)
	right.releaseReadPayload(pooled)

	// HELLO and other non-pooled types keep exact-size allocations.
	exact := right.getReadPayload(FrameTypeHello, 8)
	require.Len(t, exact, 8)
	require.Equal(t, 8, cap(exact))

	// Nil pool path stays allocation-based and release is a no-op.
	bare := &tcpFramedConn{}
	got := bare.getReadPayload(FrameTypeChunk, 8)
	require.Len(t, got, 8)
	bare.releaseReadPayload(got)
}

func TestGetReadPayloadPoolsDataReplyError(t *testing.T) {
	conn := newTCPFramedConn(nil, defaultMaxFrameSize)

	for _, frameType := range []byte{FrameTypeData, FrameTypeReply, FrameTypeError, FrameTypeChunk} {
		buf := conn.getReadPayload(frameType, 32)
		require.Len(t, buf, 32)
		require.GreaterOrEqual(t, cap(buf), 32)
		conn.releaseReadPayload(buf)
	}

	hello := conn.getReadPayload(FrameTypeHello, 32)
	require.Len(t, hello, 32)
	require.Equal(t, 32, cap(hello))
}

func TestTCPFramedConnLengthMismatch(t *testing.T) {
	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	left := newTCPFramedConn(c1, defaultMaxFrameSize)
	err := left.WriteFrames(Frame{
		Version: ProtocolVersion,
		Type:    FrameTypePing,
		Length:  5,
		Payload: []byte{1},
	})
	require.Error(t, err)
}

func TestTCPFramedConnReplaceNetConn(t *testing.T) {
	c1, c2 := net.Pipe()
	c3, c4 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
		_ = c3.Close()
		_ = c4.Close()
	})

	framed := newTCPFramedConn(c1, defaultMaxFrameSize)
	assert.Equal(t, c1, framed.NetConn())
	framed.ReplaceNetConn(c3)
	assert.Equal(t, c3, framed.NetConn())

	go func() {
		peer := newTCPFramedConn(c4, defaultMaxFrameSize)
		_, _ = peer.ReadFrame()
		_ = peer.Close()
	}()

	require.NoError(t, framed.WriteFrames(Frame{
		Version: ProtocolVersion,
		Type:    FrameTypePing,
	}))
}

func TestTCPFramedConnTruncatedPayload(t *testing.T) {
	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	go func() {
		var hdr [FrameHeaderSize]byte
		require.NoError(t, encodeFrameHeader(hdr[:], Frame{
			Version:     ProtocolVersion,
			Type:        FrameTypeData,
			Length:      4,
			Correlation: 1,
			Flags:       FrameFlagExpectsReply,
		}))
		_, _ = c1.Write(hdr[:])
		_, _ = c1.Write([]byte{1, 2}) // short payload
		_ = c1.Close()
	}()

	framed := newTCPFramedConn(c2, defaultMaxFrameSize)
	_, err := framed.ReadFrame()
	require.Error(t, err)
}

func TestEncodeHelloFrame(t *testing.T) {
	payload := []byte{1, 2, 3}
	frame := encodeHelloFrame(FrameTypeHello, LaneControl, payload)
	assert.Equal(t, ProtocolVersion, frame.Version)
	assert.Equal(t, FrameTypeHello, frame.Type)
	assert.Equal(t, LaneControl, frame.Lane)
	assert.Equal(t, uint32(3), frame.Length)
	assert.Equal(t, payload, frame.Payload)
}
