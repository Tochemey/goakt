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
	"github.com/tochemey/goakt/v4/internal/pause"
)

func TestOpenDuplexRoundTripPing(t *testing.T) {
	defer goleak.VerifyNone(t)

	ps, err := NewRemotingServer("127.0.0.1:0", WithRemotingServerLoops(1))
	require.NoError(t, err)
	require.NoError(t, ps.Listen())

	serveDone := make(chan error, 1)
	go func() { serveDone <- ps.Serve() }()
	pause.For(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	session, result, err := OpenDuplex(
		ctx,
		NewTCPTransport(),
		ps.ListenAddr().String(),
		testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 1<<20),
		LaneSpec{Role: internalpb.LaneRole_LANE_ROLE_CONTROL},
		time.Second,
		0,
	)
	require.NoError(t, err)
	require.NotNil(t, result.Effective)

	// PONG completes a matching pending waiter in the reader.
	dc, ok := session.(*duplexConn)
	require.True(t, ok)

	wait := dc.pending.register(99)
	require.NoError(t, dc.Submit(ctx, Frame{
		Type:        FrameTypePing,
		Lane:        LaneControl,
		Correlation: 99,
	}))

	select {
	case pong := <-wait:
		putPendingWaiter(wait)
		assert.Equal(t, FrameTypePong, pong.Type)
	case <-ctx.Done():
		t.Fatal("timed out waiting for PONG")
	}

	require.NoError(t, session.Close())
	require.NoError(t, ps.Shutdown(time.Second))
	<-serveDone
}

func TestOpenDuplexHonorsContextDeadline(t *testing.T) {
	defer goleak.VerifyNone(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(io.Discard, conn)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _, err = OpenDuplex(
		ctx,
		NewTCPTransport(),
		ln.Addr().String(),
		testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 1<<20),
		LaneSpec{Role: internalpb.LaneRole_LANE_ROLE_CONTROL},
		time.Second,
		0,
	)
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.GreaterOrEqual(t, elapsed, 150*time.Millisecond)
	assert.Less(t, elapsed, 2*time.Second)

	require.NoError(t, ln.Close())
	<-acceptDone
}

func TestOpenDuplexDialFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, _, err := OpenDuplex(
		ctx,
		NewTCPTransport(),
		"127.0.0.1:1",
		testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 1<<20),
		LaneSpec{Role: internalpb.LaneRole_LANE_ROLE_CONTROL},
		time.Second,
		0,
	)
	require.Error(t, err)
}

func TestOpenDuplexAdoptsAckLaneFromBaselinePeer(t *testing.T) {
	defer goleak.VerifyNone(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	// A baseline acceptor that predates lane echo answers every dial with its
	// own CONTROL identity regardless of the requested lane.
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)

		raw, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}

		framed := newTCPFramedConn(raw, defaultMaxFrameSize)
		defer func() { _ = framed.Close() }()

		frame, readErr := framed.ReadFrame()
		if readErr != nil || frame.Type != FrameTypeHello {
			return
		}

		ack := testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 1<<20)
		payload, marshalErr := proto.Marshal(ack)
		if marshalErr != nil {
			return
		}

		if writeErr := framed.WriteFrames(encodeHelloFrame(FrameTypeHelloAck, LaneControl, payload)); writeErr != nil {
			return
		}

		// Hold the connection open until the dialer closes it.
		_, _ = framed.ReadFrame()
	}()

	session, result, err := OpenDuplex(
		context.Background(),
		NewTCPTransport(),
		ln.Addr().String(),
		testHello(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, 1<<20),
		LaneSpec{Role: internalpb.LaneRole_LANE_ROLE_ORDINARY, Index: 3},
		time.Second,
		0,
	)
	require.NoError(t, err)

	assert.Equal(t, LaneControl, session.Lane())
	assert.Equal(t, internalpb.LaneRole_LANE_ROLE_CONTROL, result.Effective.GetLaneRole())
	assert.Zero(t, result.Effective.GetLaneIndex())

	require.NoError(t, session.Close())
	<-acceptDone
}
