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
	"encoding/binary"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v4/internal/internalpb"
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/remote"
)

func TestSerializerWireID(t *testing.T) {
	msg := durationpb.New(time.Second)
	protoSer := remote.NewProtoSerializer()
	payload, err := protoSer.Serialize(msg)
	require.NoError(t, err)

	id, name := serializerWireID(protoSer, msg, payload)
	assert.Equal(t, inet.SerializerIDPublicProto, id)
	assert.Equal(t, string(proto.MessageName(msg)), name)

	id, name = serializerWireID(&remote.JSONSerializer{}, msg, payload)
	assert.Equal(t, inet.SerializerIDJSON, id)
	assert.NotEmpty(t, name)

	id, name = serializerWireID(&remote.CBORSerializer{}, msg, payload)
	assert.Equal(t, inet.SerializerIDCBOR, id)
	assert.NotEmpty(t, name)

	id, name = serializerWireID(nil, struct{}{}, nil)
	assert.Equal(t, inet.SerializerIDCustom, id)
	assert.Empty(t, name)
}

func TestAskMetadataFromContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	wire, flags := askMetadataFromContext(ctx)
	require.NotEmpty(t, wire)
	assert.Equal(t, byte(inet.FrameFlagHasMetadata), flags)

	md := inet.NewMetadata()
	require.NoError(t, md.UnmarshalBinary(wire))
	deadline, ok := md.GetDeadline()
	require.True(t, ok)
	assert.WithinDuration(t, time.Now().Add(time.Second), deadline, 200*time.Millisecond)
}

func TestMetadataWireFromContext(t *testing.T) {
	wire, flags := metadataWireFromContext(context.Background())
	assert.Nil(t, wire)
	assert.Equal(t, byte(0), flags)

	md := inet.NewMetadata()
	md.Set("k", "v")
	ctx := inet.ContextWithMetadata(context.Background(), md)

	wire, flags = metadataWireFromContext(ctx)
	require.NotEmpty(t, wire)
	assert.Equal(t, byte(inet.FrameFlagHasMetadata), flags)
}

func TestMetadataMapFromBytes(t *testing.T) {
	assert.Nil(t, metadataMapFromBytes(nil))
	assert.Nil(t, metadataMapFromBytes([]byte{0xFF}))

	md := inet.NewMetadata()
	md.Set("a", "1")
	out := metadataMapFromBytes(md.MarshalBinary())
	require.Equal(t, map[string]string{"a": "1"}, out)
}

func TestDecodeControlReply(t *testing.T) {
	resp := &internalpb.RemoteLookupResponse{Address: "actor"}
	payload, err := proto.Marshal(resp)
	require.NoError(t, err)

	replyPayload, err := inet.EncodeReplyEnvelope(inet.ReplyEnvelope{
		TypeName:     string(proto.MessageName(resp)),
		SerializerID: inet.SerializerIDInternalProto,
		Payload:      payload,
	})
	require.NoError(t, err)

	msg, err := decodeControlReply(inet.Frame{
		Type:    inet.FrameTypeReply,
		Payload: replyPayload,
	}, inet.DecodeReplyEnvelope)
	require.NoError(t, err)
	got, ok := msg.(*internalpb.RemoteLookupResponse)
	require.True(t, ok)
	assert.Equal(t, "actor", got.GetAddress())

	errPayload, err := proto.Marshal(&internalpb.Error{
		Code:    internalpb.Code_CODE_NOT_FOUND,
		Message: "missing",
	})
	require.NoError(t, err)

	_, err = decodeControlReply(inet.Frame{
		Type:    inet.FrameTypeError,
		Payload: errPayload,
	}, inet.DecodeReplyEnvelope)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "NOT_FOUND")
}

func TestDeserializeReplyEnvelope(t *testing.T) {
	msg, err := deserializeReplyEnvelope(inet.ReplyEnvelope{
		SerializerID: inet.SerializerIDCustom,
	}, remote.NewProtoSerializer())
	require.NoError(t, err)
	assert.Nil(t, msg)

	original := durationpb.New(2 * time.Second)
	ser := remote.NewProtoSerializer()
	payload, err := ser.Serialize(original)
	require.NoError(t, err)

	decoded, err := deserializeReplyEnvelope(inet.ReplyEnvelope{
		TypeName:     string(proto.MessageName(original)),
		SerializerID: inet.SerializerIDPublicProto,
		Payload:      payload,
	}, ser)
	require.NoError(t, err)
	assert.True(t, proto.Equal(original, decoded.(*durationpb.Duration)))

	raw, err := proto.Marshal(original)
	require.NoError(t, err)

	decoded, err = deserializeReplyEnvelope(inet.ReplyEnvelope{
		TypeName:     string(proto.MessageName(original)),
		SerializerID: inet.SerializerIDInternalProto,
		Payload:      raw,
	}, ser)
	require.NoError(t, err)
	assert.True(t, proto.Equal(original, decoded.(*durationpb.Duration)))
}

func TestBuildUserTellParams(t *testing.T) {
	r := NewClient().(*client)
	msg := durationpb.New(time.Second)
	ser := remote.NewProtoSerializer()
	payload, err := ser.Serialize(msg)
	require.NoError(t, err)

	md := inet.NewMetadata()
	md.Set("trace", "1")
	ctx := inet.ContextWithMetadata(context.Background(), md)

	params, err := r.buildUserTellParams(ctx, "from", "to", msg, ser, payload)
	require.NoError(t, err)
	assert.Equal(t, "from", params.sender)
	assert.Equal(t, "to", params.receiver)
	assert.Equal(t, inet.SerializerIDPublicProto, params.serID)
	assert.Equal(t, string(proto.MessageName(msg)), params.typeName)
	assert.Equal(t, payload, params.payload)
	assert.NotEmpty(t, params.metadata)
}

func TestSendControlDuplexRoundTrip(t *testing.T) {
	ps := startRemotingServer(t,
		inet.WithProtoHandler("internalpb.RemoteLookupRequest", legacyLookupHandler(t)),
	)
	host, port := serverHostPort(t, ps)

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientProtocolPin(remote.ProtocolPinDuplex),
	)
	defer r.Close()

	ctx := context.Background()
	resp, err := r.(*client).sendControl(ctx, host, port, &internalpb.RemoteLookupRequest{
		Host: host,
		Port: int32(port),
		Name: "actor",
	})
	require.NoError(t, err)
	got, ok := resp.(*internalpb.RemoteLookupResponse)
	require.True(t, ok)
	assert.Contains(t, got.GetAddress(), "actor")
}

func TestSendAskLegacyPreservesTimeout(t *testing.T) {
	var got time.Duration
	ps := startRemotingServer(t,
		inet.WithProtoHandler("internalpb.RemoteAskRequest", func(_ context.Context, _ inet.Connection, msg proto.Message) (proto.Message, error) {
			req := msg.(*internalpb.RemoteAskRequest)
			if req.GetTimeout() != nil {
				got = req.GetTimeout().AsDuration()
			}
			reply := durationpb.New(time.Second)
			packed, err := proto.Marshal(reply)
			require.NoError(t, err)
			name := string(proto.MessageName(reply))
			frame := make([]byte, 8+len(name)+len(packed))
			binary.BigEndian.PutUint32(frame[0:4], uint32(len(frame)))
			binary.BigEndian.PutUint32(frame[4:8], uint32(len(name)))
			copy(frame[8:], name)
			copy(frame[8+len(name):], packed)
			return &internalpb.RemoteAskResponse{Messages: [][]byte{frame}}, nil
		}),
		inet.WithRemotingServerAcceptProtocol(inet.AcceptProtocolLegacy),
	)
	host, port := serverHostPort(t, ps)

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientProtocolPin(remote.ProtocolPinLegacy),
	).(*client)
	defer r.Close()

	msg := durationpb.New(time.Second)
	ser := remote.NewProtoSerializer()
	payload, err := ser.Serialize(msg)
	require.NoError(t, err)

	params, err := r.buildUserTellParams(context.Background(), "from", "to", msg, ser, payload)
	require.NoError(t, err)

	_, err = r.sendAsk(context.Background(), host, port, askParams{
		tellParams: params,
		timeout:    750 * time.Millisecond,
	}, ser)
	require.NoError(t, err)
	assert.Equal(t, 750*time.Millisecond, got)
}

func TestIsControlBulk(t *testing.T) {
	assert.True(t, isControlBulk(new(internalpb.RelocateBatchRequest)))
	assert.True(t, isControlBulk(new(internalpb.PersistPeerStateRequest)))
	assert.True(t, isControlBulk(new(internalpb.RemoteStateRequest)))
	assert.False(t, isControlBulk(new(internalpb.RemoteAskRequest)))
}

func TestSendTellAndAskDuplex(t *testing.T) {
	var tellCount atomic.Int32
	ps := startRemotingServer(t,
		inet.WithRemotingServerDuplexTellHandler(func(context.Context, inet.DataEnvelope) {
			tellCount.Add(1)
		}),
		inet.WithRemotingServerDuplexAskHandler(func(_ context.Context, env inet.DataEnvelope) (inet.ReplyEnvelope, error) {
			return inet.ReplyEnvelope{
				TypeName:     env.TypeName,
				SerializerID: inet.SerializerIDPublicProto,
				Payload:      env.Payload,
			}, nil
		}),
	)
	host, port := serverHostPort(t, ps)

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientProtocolPin(remote.ProtocolPinDuplex),
	).(*client)
	defer r.Close()

	msg := durationpb.New(time.Second)
	ser := remote.NewProtoSerializer()
	payload, err := ser.Serialize(msg)
	require.NoError(t, err)

	params, err := r.buildUserTellParams(context.Background(), "from", "to", msg, ser, payload)
	require.NoError(t, err)

	require.NoError(t, r.sendTell(context.Background(), host, port, params))
	pause.For(50 * time.Millisecond)
	assert.Equal(t, int32(1), tellCount.Load())

	resp, err := r.sendAsk(context.Background(), host, port, askParams{tellParams: params}, ser)
	require.NoError(t, err)
	assert.True(t, proto.Equal(msg, resp.(*durationpb.Duration)))
}
