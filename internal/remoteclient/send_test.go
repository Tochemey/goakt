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
	"errors"
	"fmt"
	"io"
	"sync"
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
	resp := internalpb.RemoteLookupResponse_builder{Address: "actor"}.Build()
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

	errPayload, err := proto.Marshal(internalpb.Error_builder{
		Code:    internalpb.Code_CODE_NOT_FOUND,
		Message: "missing",
	}.Build())
	require.NoError(t, err)

	_, err = decodeControlReply(inet.Frame{
		Type:    inet.FrameTypeError,
		Payload: errPayload,
	}, inet.DecodeReplyEnvelope)
	require.ErrorIs(t, err, gerrors.ErrAddressNotFound)
}

// TestDuplexErrorFrameDecodesLikeLegacy checks that an internalpb.Error a
// server sends as a duplex ERROR frame becomes the same Go error the legacy
// path builds from the same internalpb.Error, so callers that classify errors
// with errors.Is behave the same on both protocols.
func TestDuplexErrorFrameDecodesLikeLegacy(t *testing.T) {
	sentinels := []error{
		gerrors.ErrRemoteSendFailure,
		gerrors.ErrRemotingDisabled,
		gerrors.ErrSystemShuttingDown,
		gerrors.ErrMailboxFull,
		gerrors.ErrAddressNotFound,
		gerrors.ErrRequestTimeout,
		gerrors.ErrActorAlreadyExists,
		gerrors.ErrTypeNotRegistered,
		gerrors.ErrClusterDisabled,
		gerrors.ErrDead,
	}

	testCases := []struct {
		name    string
		code    internalpb.Code
		message string
	}{
		{name: "unavailable", code: internalpb.Code_CODE_UNAVAILABLE, message: "tcp: worker pool is stopped"},
		{name: "failed precondition remoting disabled", code: internalpb.Code_CODE_FAILED_PRECONDITION, message: gerrors.ErrRemotingDisabled.Error()},
		{name: "failed precondition shutting down", code: internalpb.Code_CODE_FAILED_PRECONDITION, message: gerrors.ErrSystemShuttingDown.Error()},
		{name: "resource exhausted mailbox full", code: internalpb.Code_CODE_RESOURCE_EXHAUSTED, message: gerrors.ErrMailboxFull.Error()},
		{name: "not found", code: internalpb.Code_CODE_NOT_FOUND, message: "missing"},
		{name: "deadline exceeded", code: internalpb.Code_CODE_DEADLINE_EXCEEDED, message: "too late"},
		{name: "already exists", code: internalpb.Code_CODE_ALREADY_EXISTS, message: "actor=(a) " + gerrors.ErrActorAlreadyExists.Error()},
		{name: "invalid argument", code: internalpb.Code_CODE_INVALID_ARGUMENT, message: "bad field"},
		{name: "internal error", code: internalpb.Code_CODE_INTERNAL_ERROR, message: "boom"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			errMsg := internalpb.Error_builder{Code: tc.code, Message: tc.message}.Build()
			payload, err := proto.Marshal(errMsg)
			require.NoError(t, err)

			legacy := checkProtoError(errMsg)
			require.Error(t, legacy)

			frame := inet.Frame{Type: inet.FrameTypeError, Correlation: 7, Payload: payload}
			_, controlErr := decodeControlReply(frame, inet.DecodeReplyEnvelope)
			require.Error(t, controlErr)

			// duplexConn.Ask returns the ERROR frame together with a plain
			// "<code>: <message>" error; the Ask call sites decode the frame.
			askErr := duplexAskError(fmt.Errorf("%s: %s", tc.code, tc.message), frame)
			require.Error(t, askErr)

			for _, sentinel := range sentinels {
				assert.Equal(t, errors.Is(legacy, sentinel), errors.Is(controlErr, sentinel), "decodeControlReply: errors.Is(%v)", sentinel)
				assert.Equal(t, errors.Is(legacy, sentinel), errors.Is(askErr, sentinel), "duplexAskError: errors.Is(%v)", sentinel)
			}

			assert.EqualError(t, controlErr, legacy.Error())
			assert.EqualError(t, askErr, legacy.Error())
		})
	}
}

// TestDuplexAskError checks that only an ERROR frame answering the request is
// decoded as the peer's error; every other Ask failure keeps its transport
// error mapping.
func TestDuplexAskError(t *testing.T) {
	t.Run("transport failure without a reply frame", func(t *testing.T) {
		err := duplexAskError(io.EOF, inet.Frame{})
		require.ErrorIs(t, err, io.EOF)
	})

	t.Run("backpressure keeps its sentinel", func(t *testing.T) {
		err := duplexAskError(inet.ErrDuplexBackpressure, inet.Frame{})
		require.ErrorIs(t, err, gerrors.ErrRemoteSendBackpressure)
		require.ErrorIs(t, err, inet.ErrDuplexBackpressure)
	})

	t.Run("an ERROR frame without a correlation is not a reply", func(t *testing.T) {
		err := duplexAskError(inet.ErrDuplexClosed, inet.Frame{Type: inet.FrameTypeError})
		require.ErrorIs(t, err, inet.ErrDuplexClosed)
	})
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

type retainingReplySerializer struct {
	last []byte
}

func (x *retainingReplySerializer) Serialize(any) ([]byte, error) {
	return []byte("unused"), nil
}

func (x *retainingReplySerializer) Deserialize(data []byte) (any, error) {
	x.last = data
	return string(data), nil
}

func TestDeserializeReplyEnvelopeCustomCopiesPayload(t *testing.T) {
	ser := &retainingReplySerializer{}
	pooled := []byte("custom-payload")

	got, err := deserializeReplyEnvelope(inet.ReplyEnvelope{
		SerializerID: inet.SerializerIDCustom,
		Payload:      pooled,
	}, ser)
	require.NoError(t, err)
	require.Equal(t, "custom-payload", got)

	pooled[0] = 'X'
	require.Equal(t, []byte("custom-payload"), ser.last)
}

func TestDeserializeReplyEnvelopePublicDoesNotCopy(t *testing.T) {
	ser := &retainingReplySerializer{}
	pooled := []byte("public-payload")

	_, err := deserializeReplyEnvelope(inet.ReplyEnvelope{
		SerializerID: inet.SerializerIDPublicProto,
		Payload:      pooled,
	}, ser)
	require.NoError(t, err)

	pooled[0] = 'X'
	require.Equal(t, byte('X'), ser.last[0])
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
	resp, err := r.(*client).sendControl(ctx, host, port, internalpb.RemoteLookupRequest_builder{
		Host: host,
		Port: int32(port),
		Name: "actor",
	}.Build())
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
			return internalpb.RemoteAskResponse_builder{Messages: [][]byte{frame}}.Build(), nil
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

// TestDeserializeReplyOfAnotherSerializer covers the reply decoding rule: the
// request's serializer goes first, the dispatching serializer takes over when
// it fails, and the request serializer's error comes back when both fail.
func TestDeserializeReplyOfAnotherSerializer(t *testing.T) {
	cbor := remote.NewCBORSerializer()
	json := remote.NewJSONSerializer()
	protoSer := remote.NewProtoSerializer()

	// CBOR is registered ahead of JSON, so the dispatching serializer tries it first.
	r := NewClient(
		WithClientSerializers(new(replyOther), cbor),
		WithClientSerializers(new(replyCount), json),
	).(*client)
	defer r.Close()

	count := replyCount(5)
	jsonFrame, err := json.Serialize(&count)
	require.NoError(t, err)
	cborFrame, err := cbor.Serialize(&replyOther{Amount: 2})
	require.NoError(t, err)
	protoFrame, err := protoSer.Serialize(durationpb.New(time.Second))
	require.NoError(t, err)

	// envelope wraps a frame as the duplex REPLY envelope the server sends.
	envelope := func(frame []byte, serializerID byte) inet.ReplyEnvelope {
		name, ok := frameTypeName(frame)
		require.True(t, ok)
		return inet.ReplyEnvelope{TypeName: string(name), SerializerID: serializerID, Payload: frame}
	}

	t.Run("a reply of the request's serializer is decoded by it", func(t *testing.T) {
		// CBOR would decode the JSON frame to another value; the request's
		// serializer must win.
		got, err := r.deserializeReplyFrame(jsonFrame, json)
		require.NoError(t, err)
		assert.Equal(t, &count, got)

		got, err = r.deserializeUserReply(envelope(jsonFrame, inet.SerializerIDJSON), json)
		require.NoError(t, err)
		assert.Equal(t, &count, got)
	})

	t.Run("a reply of another serializer is decoded by the dispatching serializer", func(t *testing.T) {
		got, err := r.deserializeReplyFrame(protoFrame, cbor)
		require.NoError(t, err)
		assert.True(t, proto.Equal(durationpb.New(time.Second), got.(*durationpb.Duration)))

		got, err = r.deserializeReplyFrame(cborFrame, protoSer)
		require.NoError(t, err)
		assert.Equal(t, &replyOther{Amount: 2}, got)

		got, err = r.deserializeUserReply(envelope(protoFrame, inet.SerializerIDPublicProto), cbor)
		require.NoError(t, err)
		assert.True(t, proto.Equal(durationpb.New(time.Second), got.(*durationpb.Duration)))

		got, err = r.deserializeUserReply(envelope(cborFrame, inet.SerializerIDCBOR), protoSer)
		require.NoError(t, err)
		assert.Equal(t, &replyOther{Amount: 2}, got)
	})

	t.Run("a reply no serializer decodes returns the request serializer's error", func(t *testing.T) {
		garbage := []byte("not-a-frame")

		_, err := r.deserializeReplyFrame(garbage, protoSer)
		require.ErrorIs(t, err, remote.ErrInvalidFrame)

		_, err = r.deserializeUserReply(inet.ReplyEnvelope{SerializerID: inet.SerializerIDPublicProto, Payload: garbage}, protoSer)
		require.ErrorIs(t, err, remote.ErrInvalidFrame)
	})
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

// tellBatchRecorder collects every message the server receives in arrival
// order. Coalesced flushes arrive as internal-proto RemoteTellRequest frames
// dispatched through the ProtoHandler registry; each is flattened into the
// shared list.
type tellBatchRecorder struct {
	mu       sync.Mutex
	payloads [][]byte
}

func (x *tellBatchRecorder) handle(_ context.Context, _ inet.Connection, msg proto.Message) (proto.Message, error) {
	req, ok := msg.(*internalpb.RemoteTellRequest)
	if !ok {
		return &internalpb.RemoteTellResponse{}, nil
	}

	x.mu.Lock()
	defer x.mu.Unlock()

	for _, remoteMessage := range req.GetRemoteMessages() {
		payload := make([]byte, len(remoteMessage.GetMessage()))
		copy(payload, remoteMessage.GetMessage())
		x.payloads = append(x.payloads, payload)
	}

	return &internalpb.RemoteTellResponse{}, nil
}

func (x *tellBatchRecorder) snapshot() [][]byte {
	x.mu.Lock()
	defer x.mu.Unlock()

	out := make([][]byte, len(x.payloads))
	copy(out, x.payloads)
	return out
}

// TestRemoteTellAndBatchTellShareFIFO pins the cross-API ordering guarantee:
// batch tells ride the same coalescer shard as coalesced singles, so a
// RemoteTell followed by a RemoteBatchTell followed by another RemoteTell
// arrives at the receiver in exactly that order.
func TestRemoteTellAndBatchTellShareFIFO(t *testing.T) {
	recorder := &tellBatchRecorder{}
	ps := startRemotingServer(t,
		inet.WithProtoHandler("internalpb.RemoteTellRequest", recorder.handle),
	)
	host, port := serverHostPort(t, ps)

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientProtocolPin(remote.ProtocolPinDuplex),
		WithSendCoalescing(16),
	).(*client)
	defer r.Close()

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)

	sequenced := func(i int) *durationpb.Duration { return durationpb.New(time.Duration(i) * time.Millisecond) }

	require.NoError(t, r.RemoteTell(context.Background(), from, to, sequenced(1)))
	require.NoError(t, r.RemoteBatchTell(context.Background(), from, to, []any{sequenced(2), sequenced(3)}))
	require.NoError(t, r.RemoteTell(context.Background(), from, to, sequenced(4)))

	require.Eventually(t, func() bool { return len(recorder.snapshot()) == 4 }, 3*time.Second, 5*time.Millisecond,
		"all four tells must be delivered")

	ser := remote.NewProtoSerializer()

	for i, payload := range recorder.snapshot() {
		expected, err := ser.Serialize(sequenced(i + 1))
		require.NoError(t, err)
		assert.Equal(t, expected, payload, "delivery order must match submission order at position %d", i)
	}
}

// TestRemoteBatchTellSplitsOversizedFlush verifies that a coalesced batch
// whose aggregate wire size exceeds the negotiated max message size is split
// into size-bounded frames instead of being dead-lettered wholesale: every
// individually valid message must arrive.
func TestRemoteBatchTellSplitsOversizedFlush(t *testing.T) {
	const maxMessage = 256 * 1024

	recorder := &tellBatchRecorder{}
	ps := startRemotingServer(t,
		inet.WithRemotingServerMaxMessageSize(maxMessage),
		inet.WithProtoHandler("internalpb.RemoteTellRequest", recorder.handle),
	)
	host, port := serverHostPort(t, ps)

	var deadLettered atomic.Int64
	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientProtocolPin(remote.ProtocolPinDuplex),
		WithClientMaxMessageSize(maxMessage),
		WithSendCoalescing(64),
		WithTellFailureHandler(func(_ string, messages []*internalpb.RemoteMessage, _ error) {
			deadLettered.Add(int64(len(messages)))
		}),
	).(*client)
	defer r.Close()

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)

	// 40 x 32KiB of raw payload is ~5x the negotiated ceiling; individually
	// every message fits comfortably. RemoteMessage doubles as a convenient
	// vendored proto carrier for bulk bytes.
	const messages = 40
	batch := make([]any, 0, messages)

	for i := range messages {
		payload := make([]byte, 32*1024)
		for j := range payload {
			payload[j] = byte(i)
		}

		batch = append(batch, internalpb.RemoteMessage_builder{Message: payload}.Build())
	}

	require.NoError(t, r.RemoteBatchTell(context.Background(), from, to, batch))

	require.Eventually(t, func() bool { return len(recorder.snapshot()) == messages }, 5*time.Second, 10*time.Millisecond,
		"an oversized aggregate must be split and fully delivered, got %d of %d (dead-lettered %d)",
		len(recorder.snapshot()), messages, deadLettered.Load())
	assert.Zero(t, deadLettered.Load(), "no individually valid message may be dead-lettered")
}
