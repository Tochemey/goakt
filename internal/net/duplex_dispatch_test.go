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
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v4/internal/internalpb"
)

// TestDispatchDuplexUserAskViaLegacyCarriesCallerDeadline pins the bridge
// half of the wire-deadline fix: the caller's metadata deadline must ride the
// bridged RemoteAskRequest as Timeout so a legacy-only ask handler honors it,
// instead of falling back to its own ask timeout. The second subtest pins the
// fallback: no metadata, no timeout on the request.
func TestDispatchDuplexUserAskViaLegacyCarriesCallerDeadline(t *testing.T) {
	srv := &RemotingServer{}

	t.Run("wire deadline becomes request timeout", func(t *testing.T) {
		var got time.Duration
		handler := func(_ context.Context, _ Connection, req proto.Message) (proto.Message, error) {
			ask, ok := req.(*internalpb.RemoteAskRequest)
			require.True(t, ok)
			require.NotNil(t, ask.GetTimeout())
			got = ask.GetTimeout().AsDuration()
			return &internalpb.RemoteAskResponse{}, nil
		}

		md := NewMetadata()
		md.SetDeadline(time.Now().Add(5 * time.Second))

		reply, err := srv.dispatchDuplexUserAskViaLegacy(context.Background(), handler, DataEnvelope{
			Sender:   "goakt://sys@127.0.0.1:9000/sender",
			Receiver: "goakt://sys@127.0.0.1:9001/receiver",
			Metadata: md.MarshalBinary(),
		})
		require.NoError(t, err)
		assert.Equal(t, SerializerIDCustom, reply.SerializerID)
		assert.Greater(t, got, 4*time.Second, "the bridged timeout must reflect the caller's remaining deadline")
		assert.LessOrEqual(t, got, 5*time.Second)
	})

	t.Run("no metadata leaves timeout unset", func(t *testing.T) {
		handler := func(_ context.Context, _ Connection, req proto.Message) (proto.Message, error) {
			ask, ok := req.(*internalpb.RemoteAskRequest)
			require.True(t, ok)
			assert.Nil(t, ask.GetTimeout(), "without a wire deadline the handler's own ask timeout must apply")
			return &internalpb.RemoteAskResponse{}, nil
		}

		_, err := srv.dispatchDuplexUserAskViaLegacy(context.Background(), handler, DataEnvelope{
			Sender:   "goakt://sys@127.0.0.1:9000/sender",
			Receiver: "goakt://sys@127.0.0.1:9001/receiver",
		})
		require.NoError(t, err)
	})
}

func TestFrameTypeNameFromPayload(t *testing.T) {
	name := "google.protobuf.Duration"
	payload := make([]byte, 8+len(name)+2)
	binary.BigEndian.PutUint32(payload[0:4], uint32(len(payload)))
	binary.BigEndian.PutUint32(payload[4:8], uint32(len(name)))
	copy(payload[8:], name)

	got, ok := frameTypeNameFromPayload(payload)
	require.True(t, ok)
	assert.Equal(t, name, string(got))

	_, ok = frameTypeNameFromPayload([]byte{0x00})
	assert.False(t, ok)

	bad := make([]byte, 8)
	binary.BigEndian.PutUint32(bad[4:8], 100)
	_, ok = frameTypeNameFromPayload(bad)
	assert.False(t, ok)
}

func TestMetadataMapFromEnvelope(t *testing.T) {
	assert.Nil(t, metadataMapFromEnvelope(nil))
	assert.Nil(t, metadataMapFromEnvelope([]byte{0xFF, 0xFF}))

	md := NewMetadata()
	md.Set("trace", "abc")
	wire := md.MarshalBinary()

	out := metadataMapFromEnvelope(wire)
	require.NotNil(t, out)
	assert.Equal(t, "abc", out["trace"])
}

func TestSubmitErrorFrame(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	conn := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), 1024)
	peer := newTCPFramedConn(c2, defaultMaxFrameSize)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	require.NoError(t, submitErrorFrame(ctx, conn, 7, internalpb.Code_CODE_INVALID_ARGUMENT, "bad arg"))

	frame, err := peer.ReadFrame()
	require.NoError(t, err)
	assert.Equal(t, FrameTypeError, frame.Type)
	assert.Equal(t, uint64(7), frame.Correlation)

	var e internalpb.Error
	require.NoError(t, proto.Unmarshal(frame.Payload, &e))
	assert.Equal(t, internalpb.Code_CODE_INVALID_ARGUMENT, e.GetCode())
	assert.Equal(t, "bad arg", e.GetMessage())

	require.NoError(t, conn.Close())
	_ = peer.Close()
}

func TestDispatchDuplexAskControlAndUser(t *testing.T) {
	ps, err := NewRemotingServer("127.0.0.1:0",
		WithProtoHandler("internalpb.RemoteLookupRequest", func(_ context.Context, _ Connection, msg proto.Message) (proto.Message, error) {
			req := msg.(*internalpb.RemoteLookupRequest)
			return internalpb.RemoteLookupResponse_builder{Address: req.GetName()}.Build(), nil
		}),
		WithRemotingServerDuplexAskHandler(func(_ context.Context, env DataEnvelope) (ReplyEnvelope, error) {
			return ReplyEnvelope{
				TypeName:     env.TypeName,
				SerializerID: SerializerIDInternalProto,
				Payload:      env.Payload,
			}, nil
		}),
	)
	require.NoError(t, err)

	lookup := internalpb.RemoteLookupRequest_builder{Name: "actor", Host: "127.0.0.1", Port: 1}.Build()
	payload, err := proto.Marshal(lookup)
	require.NoError(t, err)

	reply, err := ps.dispatchDuplexAsk(context.Background(), DataEnvelope{
		TypeName:     string(proto.MessageName(lookup)),
		SerializerID: SerializerIDInternalProto,
		Payload:      payload,
	})
	require.NoError(t, err)
	assert.Equal(t, string(proto.MessageName(&internalpb.RemoteLookupResponse{})), reply.TypeName)

	userPayload, err := proto.Marshal(durationpb.New(time.Second))
	require.NoError(t, err)

	userReply, err := ps.dispatchDuplexAsk(context.Background(), DataEnvelope{
		TypeName:     "google.protobuf.Duration",
		SerializerID: SerializerIDInternalProto,
		Payload:      userPayload,
	})
	require.NoError(t, err)
	assert.Equal(t, "google.protobuf.Duration", userReply.TypeName)
}

func TestDispatchDuplexAskLegacyBridge(t *testing.T) {
	ps, err := NewRemotingServer("127.0.0.1:0",
		WithProtoHandler("internalpb.RemoteAskRequest", func(_ context.Context, _ Connection, _ proto.Message) (proto.Message, error) {
			msg := durationpb.New(3 * time.Second)
			packed, packErr := proto.Marshal(msg)
			require.NoError(t, packErr)

			name := string(proto.MessageName(msg))
			frame := make([]byte, 8+len(name)+len(packed))
			binary.BigEndian.PutUint32(frame[0:4], uint32(len(frame)))
			binary.BigEndian.PutUint32(frame[4:8], uint32(len(name)))
			copy(frame[8:], name)
			copy(frame[8+len(name):], packed)

			return internalpb.RemoteAskResponse_builder{Messages: [][]byte{frame}}.Build(), nil
		}),
	)
	require.NoError(t, err)

	reply, err := ps.dispatchDuplexAsk(context.Background(), DataEnvelope{
		Sender:       "from",
		Receiver:     "to",
		TypeName:     "google.protobuf.Duration",
		SerializerID: SerializerIDPublicProto,
		Payload:      []byte{0x01},
	})
	require.NoError(t, err)
	assert.Equal(t, SerializerIDPublicProto, reply.SerializerID)
	assert.Equal(t, "google.protobuf.Duration", reply.TypeName)
}

func TestDispatchDuplexAskNoHandler(t *testing.T) {
	ps, err := NewRemotingServer("127.0.0.1:0")
	require.NoError(t, err)

	_, err = ps.dispatchDuplexAsk(context.Background(), DataEnvelope{
		TypeName:     "google.protobuf.Duration",
		SerializerID: SerializerIDInternalProto,
		Payload:      []byte{0x00},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no duplex ask handler")
}

func TestRecoverDuplexAskPanic(t *testing.T) {
	var recovered any
	ps, err := NewRemotingServer("127.0.0.1:0",
		WithRemotingServerPanicHandler(func(_ protoreflect.FullName, r any) { recovered = r }),
		WithRemotingServerDuplexAskHandler(func(context.Context, DataEnvelope) (ReplyEnvelope, error) {
			panic("ask boom")
		}),
	)
	require.NoError(t, err)

	_, panicked, err := ps.recoverDuplexAsk(context.Background(), DataEnvelope{
		TypeName:     "google.protobuf.Duration",
		SerializerID: SerializerIDInternalProto,
	})
	require.True(t, panicked)
	require.NoError(t, err)
	assert.Equal(t, "ask boom", recovered)
}

func TestRecoverDuplexAskPanicWithoutHandler(t *testing.T) {
	ps, err := NewRemotingServer("127.0.0.1:0",
		WithRemotingServerDuplexAskHandler(func(context.Context, DataEnvelope) (ReplyEnvelope, error) {
			panic("ask boom")
		}),
	)
	require.NoError(t, err)

	_, panicked, err := ps.recoverDuplexAsk(context.Background(), DataEnvelope{
		TypeName:     "google.protobuf.Duration",
		SerializerID: SerializerIDInternalProto,
	})
	require.True(t, panicked)
	require.NoError(t, err)
}

func TestHandleDuplexDataDecodeErrorRepliesAndKeepsConnection(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	conn := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), 1024)
	peer := newTCPFramedConn(c2, defaultMaxFrameSize)

	ps, err := NewRemotingServer("127.0.0.1:0")
	require.NoError(t, err)

	// A truncated envelope fails decode before dispatch: the frame is
	// released pre-enqueue, a request-scoped ERROR is written, and the
	// connection stays up (nil return).
	err = ps.handleDuplexData(context.Background(), conn, Frame{
		Type:        FrameTypeData,
		Lane:        LaneOrdinary,
		Correlation: 7,
		Payload:     []byte{0},
	})
	require.NoError(t, err)

	frame, err := peer.ReadFrame()
	require.NoError(t, err)
	assert.Equal(t, FrameTypeError, frame.Type)
	assert.Equal(t, uint64(7), frame.Correlation)

	require.NoError(t, conn.Close())
	_ = peer.Close()
}

func TestHandleDuplexAskTaskWritesReply(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	conn := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), 1024)
	peer := newTCPFramedConn(c2, defaultMaxFrameSize)

	ps, err := NewRemotingServer("127.0.0.1:0",
		WithRemotingServerDuplexAskHandler(func(_ context.Context, env DataEnvelope) (ReplyEnvelope, error) {
			return ReplyEnvelope{
				TypeName:     env.TypeName,
				SerializerID: SerializerIDInternalProto,
				Payload:      env.Payload,
			}, nil
		}),
	)
	require.NoError(t, err)

	payload, err := proto.Marshal(durationpb.New(time.Second))
	require.NoError(t, err)

	handleDuplexAskTask(duplexAskTask{
		server: ps,
		conn:   conn,
		frame:  Frame{Correlation: 9, Lane: LaneOrdinary},
		env: DataEnvelope{
			TypeName:     "google.protobuf.Duration",
			SerializerID: SerializerIDInternalProto,
			Payload:      payload,
		},
		ctx: context.Background(),
	})

	frame, err := peer.ReadFrame()
	require.NoError(t, err)
	assert.Equal(t, FrameTypeReply, frame.Type)
	assert.Equal(t, uint64(9), frame.Correlation)

	require.NoError(t, conn.Close())
	_ = peer.Close()
}

func TestHandleDuplexAskTaskWritesErrorOnHandlerFailure(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	conn := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), 1024)
	peer := newTCPFramedConn(c2, defaultMaxFrameSize)

	ps, err := NewRemotingServer("127.0.0.1:0",
		WithRemotingServerDuplexAskHandler(func(context.Context, DataEnvelope) (ReplyEnvelope, error) {
			return ReplyEnvelope{}, errors.New("handler failed")
		}),
	)
	require.NoError(t, err)

	handleDuplexAskTask(duplexAskTask{
		server: ps,
		conn:   conn,
		frame:  Frame{Correlation: 3, Lane: LaneOrdinary},
		env: DataEnvelope{
			TypeName:     "google.protobuf.Duration",
			SerializerID: SerializerIDInternalProto,
		},
		ctx: context.Background(),
	})

	frame, err := peer.ReadFrame()
	require.NoError(t, err)
	assert.Equal(t, FrameTypeError, frame.Type)
	assert.Equal(t, uint64(3), frame.Correlation)

	require.NoError(t, conn.Close())
	_ = peer.Close()
}
