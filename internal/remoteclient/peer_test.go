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

package remoteclient

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/remote"
)

func startProtoServer(t *testing.T, opts ...inet.ProtoServerOption) *inet.ProtoServer {
	t.Helper()

	ps, err := inet.NewProtoServer("127.0.0.1:0", opts...)
	require.NoError(t, err)
	require.NoError(t, ps.Listen())

	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	pause.For(100 * time.Millisecond)

	t.Cleanup(func() {
		require.NoError(t, ps.Shutdown(time.Second))
		<-done
	})

	return ps
}

func serverHostPort(t *testing.T, ps *inet.ProtoServer) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(ps.ListenAddr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	return host, port
}

func TestProtocolCacheAutoFallbackLegacy(t *testing.T) {
	ps := startProtoServer(t,
		inet.WithProtoServerAcceptProtocol(inet.AcceptProtocolLegacy),
		inet.WithProtoHandler("internalpb.RemoteLookupRequest", legacyLookupHandler(t)),
	)
	host, port := serverHostPort(t, ps)

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientProtocolPin(remote.ProtocolPinAuto),
	)
	defer r.Close()

	c := r.(*client)
	p := c.peerFor(host, port)
	assert.Equal(t, peerProtocolUnknown, p.cachedProtocol())

	addr, err := r.RemoteLookup(context.Background(), host, port, "actor")
	require.NoError(t, err)
	assert.Equal(t, "actor", addr.Name())

	assert.Equal(t, peerProtocolLegacy, p.cachedProtocol())
	assert.Nil(t, p.session)
}

func legacyLookupHandler(t *testing.T) inet.ProtoHandler {
	t.Helper()
	return func(_ context.Context, _ inet.Connection, msg proto.Message) (proto.Message, error) {
		req := msg.(*internalpb.RemoteLookupRequest)
		return &internalpb.RemoteLookupResponse{
			Address: address.New(req.GetName(), "test", req.GetHost(), int(req.GetPort())).String(),
		}, nil
	}
}

func TestProtocolPinLegacyOnDuplexCapableServer(t *testing.T) {
	ps := startProtoServer(t,
		inet.WithProtoHandler("internalpb.RemoteLookupRequest", legacyLookupHandler(t)),
	)
	host, port := serverHostPort(t, ps)

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientProtocolPin(remote.ProtocolPinLegacy),
	)
	defer r.Close()

	c := r.(*client)
	p := c.peerFor(host, port)

	addr, err := r.RemoteLookup(context.Background(), host, port, "actor")
	require.NoError(t, err)
	assert.Equal(t, "actor", addr.Name())
	assert.Equal(t, peerProtocolUnknown, p.cachedProtocol())
	assert.Nil(t, p.session)
}

func TestProtocolPinDuplexControlRPC(t *testing.T) {
	ps := startProtoServer(t,
		inet.WithProtoHandler("internalpb.RemoteLookupRequest", legacyLookupHandler(t)),
	)
	host, port := serverHostPort(t, ps)

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientProtocolPin(remote.ProtocolPinDuplex),
	)
	defer r.Close()

	c := r.(*client)
	p := c.peerFor(host, port)

	addr, err := r.RemoteLookup(context.Background(), host, port, "actor")
	require.NoError(t, err)
	assert.Equal(t, "actor", addr.Name())
	assert.Equal(t, peerProtocolDuplex, p.cachedProtocol())
	assert.NotNil(t, p.session)
}

func TestSwitchoverDrainOrder(t *testing.T) {
	ps := startProtoServer(t,
		inet.WithProtoHandler("internalpb.RemoteLookupRequest", legacyLookupHandler(t)),
	)
	host, port := serverHostPort(t, ps)

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientProtocolPin(remote.ProtocolPinAuto),
	)
	defer r.Close()

	c := r.(*client)
	p := c.peerFor(host, port)
	p.cache.set(peerProtocolLegacy)
	// Age the legacy mark so ensureDuplex re-probes and drains first.
	p.cache.markedAt = time.Now().Add(-peerLegacyReprobeInterval - time.Second)

	order := make(chan string, 2)
	p.beginLegacySend()
	go func() {
		time.Sleep(80 * time.Millisecond)
		order <- "legacy-done"
		p.endLegacySend()
	}()

	started := make(chan struct{})
	go func() {
		close(started)
		_, err := p.ensureDuplex(context.Background())
		require.NoError(t, err)
		order <- "duplex-ready"
	}()
	<-started

	require.Equal(t, "legacy-done", <-order)
	require.Equal(t, "duplex-ready", <-order)
	assert.Equal(t, peerProtocolDuplex, p.cachedProtocol())
}

func TestBatchAskOrderDuplex(t *testing.T) {
	protoSer := remote.NewProtoSerializer()
	duplexAsk := func(_ context.Context, env inet.DataEnvelope) (inet.ReplyEnvelope, error) {
		msg, err := protoSer.Deserialize(env.Payload)
		require.NoError(t, err)
		req := msg.(*durationpb.Duration)
		payload, err := proto.Marshal(req)
		require.NoError(t, err)
		return inet.ReplyEnvelope{
			TypeName:     string(proto.MessageName(req)),
			SerializerID: inet.SerializerIDInternalProto,
			Payload:      payload,
		}, nil
	}

	ps := startProtoServer(t, inet.WithProtoServerDuplexAskHandler(duplexAsk))
	host, port := serverHostPort(t, ps)

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientProtocolPin(remote.ProtocolPinDuplex),
	)
	defer r.Close()

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)

	responses, err := r.RemoteBatchAsk(
		context.Background(),
		from,
		to,
		[]any{durationpb.New(time.Second), durationpb.New(2 * time.Second), durationpb.New(3 * time.Second)},
		time.Second,
	)
	require.NoError(t, err)
	require.Len(t, responses, 3)

	for i, resp := range responses {
		d, ok := resp.(*durationpb.Duration)
		require.True(t, ok)
		assert.Equal(t, int64(i+1), d.GetSeconds())
	}
}

func TestDuplexTellAskRoundTrip(t *testing.T) {
	duplexAsk := func(_ context.Context, env inet.DataEnvelope) (inet.ReplyEnvelope, error) {
		reply := durationpb.New(5 * time.Second)
		payload, err := proto.Marshal(reply)
		require.NoError(t, err)
		return inet.ReplyEnvelope{
			TypeName:     string(proto.MessageName(reply)),
			SerializerID: inet.SerializerIDInternalProto,
			Payload:      payload,
		}, nil
	}

	tellCount := atomic.Int32{}
	duplexTell := func(_ context.Context, env inet.DataEnvelope) {
		tellCount.Add(1)
	}

	ps := startProtoServer(t,
		inet.WithProtoServerDuplexAskHandler(duplexAsk),
		inet.WithProtoServerDuplexTellHandler(duplexTell),
	)
	host, port := serverHostPort(t, ps)

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientProtocolPin(remote.ProtocolPinDuplex),
	)
	defer r.Close()

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)

	require.NoError(t, r.RemoteTell(context.Background(), from, to, durationpb.New(time.Second)))
	pause.For(50 * time.Millisecond)
	assert.Equal(t, int32(1), tellCount.Load())

	resp, err := r.RemoteAsk(context.Background(), from, to, durationpb.New(time.Second), time.Second)
	require.NoError(t, err)
	d, ok := resp.(*durationpb.Duration)
	require.True(t, ok)
	assert.Equal(t, int64(5), d.GetSeconds())
}

func TestMapDuplexBackpressure(t *testing.T) {
	err := mapDuplexErr(inet.ErrDuplexBackpressure)
	require.ErrorIs(t, err, gerrors.ErrRemoteSendBackpressure)
}

func TestShouldRetireDuplexSession(t *testing.T) {
	assert.False(t, shouldRetireDuplexSession(nil, inet.Frame{}))
	assert.False(t, shouldRetireDuplexSession(context.Canceled, inet.Frame{}))
	assert.False(t, shouldRetireDuplexSession(context.DeadlineExceeded, inet.Frame{}))
	assert.False(t, shouldRetireDuplexSession(inet.ErrDuplexBackpressure, inet.Frame{}))
	assert.False(t, shouldRetireDuplexSession(errors.New("handler"), inet.Frame{
		Type:        inet.FrameTypeError,
		Correlation: 7,
	}))
	assert.True(t, shouldRetireDuplexSession(inet.ErrDuplexClosed, inet.Frame{}))
}

func TestIsLegacyHandshakeFailure(t *testing.T) {
	assert.True(t, isLegacyHandshakeFailure(io.EOF))
	assert.True(t, isLegacyHandshakeFailure(io.ErrUnexpectedEOF))
	assert.False(t, isLegacyHandshakeFailure(nil))
	assert.False(t, isLegacyHandshakeFailure(context.DeadlineExceeded))
}

func TestCompressionCodec(t *testing.T) {
	assert.Equal(t, internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, compressionCodec(remote.NoCompression))
	assert.Equal(t, internalpb.CompressionCodec_COMPRESSION_CODEC_GZIP, compressionCodec(remote.GzipCompression))
	assert.Equal(t, internalpb.CompressionCodec_COMPRESSION_CODEC_ZSTD, compressionCodec(remote.ZstdCompression))
	assert.Equal(t, internalpb.CompressionCodec_COMPRESSION_CODEC_BROTLI, compressionCodec(remote.BrotliCompression))
}
