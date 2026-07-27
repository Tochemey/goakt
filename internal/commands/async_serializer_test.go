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

package commands

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// resolvesAsProtoFrame mirrors the remoting dispatcher's frameTypeName check.
// The dispatcher decodes any frame whose embedded type name resolves in the
// protobuf registry with the proto serializer, regardless of registration
// order. Async envelope frames must never satisfy this parse or they would be
// decoded into their internalpb form and bypass every envelope branch in the
// runtime.
func resolvesAsProtoFrame(data []byte) bool {
	if len(data) < 8 {
		return false
	}

	totalLen := int(binary.BigEndian.Uint32(data[:4]))
	nameLen := int(binary.BigEndian.Uint32(data[4:8]))

	return totalLen >= 8 && len(data) >= totalLen && nameLen > 0 && 8+nameLen <= totalLen
}

// invalidUTF8String yields a string that proto rejects at marshal time.
func invalidUTF8String() string {
	return string([]byte{0xff})
}

func TestAsyncEnvelopeFrameIdentity(t *testing.T) {
	payload := &testpb.Reply{Content: "ok"}

	t.Run("request frame is not a proto registry frame", func(t *testing.T) {
		data, err := new(AsyncRequestSerializer).Serialize(&AsyncRequest{
			CorrelationID: "corr",
			ReplyTo:       &AsyncReplyTo{Kind: ReplyToActor, Actor: address.New("actor", "sys", "127.0.0.1", 9000)},
			Message:       payload,
		})
		require.NoError(t, err)
		require.False(t, resolvesAsProtoFrame(data))
	})

	t.Run("response frame is not a proto registry frame", func(t *testing.T) {
		data, err := new(AsyncResponseSerializer).Serialize(&AsyncResponse{
			CorrelationID: "corr",
			Message:       payload,
		})
		require.NoError(t, err)
		require.False(t, resolvesAsProtoFrame(data))
	})
}

func TestAsyncRequestSerializerRoundTrip(t *testing.T) {
	serializer := new(AsyncRequestSerializer)

	t.Run("actor reply target", func(t *testing.T) {
		addr := address.New("actor", "sys", "127.0.0.1", 9000)
		data, err := serializer.Serialize(&AsyncRequest{
			CorrelationID: "corr-actor",
			ReplyTo:       &AsyncReplyTo{Kind: ReplyToActor, Actor: addr},
			Message:       &testpb.Reply{Content: "ok"},
		})
		require.NoError(t, err)

		decoded, err := serializer.Deserialize(data)
		require.NoError(t, err)

		request, ok := decoded.(*AsyncRequest)
		require.True(t, ok)
		require.Equal(t, "corr-actor", request.CorrelationID)
		require.Equal(t, ReplyToActor, request.ReplyTo.Kind)
		require.Equal(t, addr.String(), request.ReplyTo.Actor.String())
		require.Empty(t, request.ReplyTo.Grain)

		reply, ok := request.Message.(*testpb.Reply)
		require.True(t, ok)
		require.Equal(t, "ok", reply.GetContent())
	})

	t.Run("grain reply target", func(t *testing.T) {
		const identity = "TestGrain/grain-1"

		data, err := serializer.Serialize(&AsyncRequest{
			CorrelationID: "corr-grain",
			ReplyTo:       &AsyncReplyTo{Kind: ReplyToGrain, Grain: identity},
			Message:       &testpb.Reply{Content: "ok"},
		})
		require.NoError(t, err)

		decoded, err := serializer.Deserialize(data)
		require.NoError(t, err)

		request, ok := decoded.(*AsyncRequest)
		require.True(t, ok)
		require.Equal(t, ReplyToGrain, request.ReplyTo.Kind)
		require.Equal(t, identity, request.ReplyTo.Grain)
		require.Nil(t, request.ReplyTo.Actor)
	})

	t.Run("client reply target", func(t *testing.T) {
		data, err := serializer.Serialize(&AsyncRequest{
			CorrelationID: "corr-client",
			Message:       &testpb.Reply{Content: "ok"},
		})
		require.NoError(t, err)

		decoded, err := serializer.Deserialize(data)
		require.NoError(t, err)

		request, ok := decoded.(*AsyncRequest)
		require.True(t, ok)
		require.Equal(t, "corr-client", request.CorrelationID)
		require.Nil(t, request.ReplyTo)
	})
}

func TestAsyncResponseSerializerRoundTrip(t *testing.T) {
	serializer := new(AsyncResponseSerializer)

	t.Run("message response", func(t *testing.T) {
		data, err := serializer.Serialize(&AsyncResponse{
			CorrelationID: "corr",
			Message:       &testpb.Reply{Content: "ok"},
		})
		require.NoError(t, err)

		decoded, err := serializer.Deserialize(data)
		require.NoError(t, err)

		response, ok := decoded.(*AsyncResponse)
		require.True(t, ok)
		require.Equal(t, "corr", response.CorrelationID)
		require.Empty(t, response.Error)

		reply, ok := response.Message.(*testpb.Reply)
		require.True(t, ok)
		require.Equal(t, "ok", reply.GetContent())
	})

	t.Run("error response restores known identity", func(t *testing.T) {
		data, err := serializer.Serialize(&AsyncResponse{
			CorrelationID: "corr",
			Error:         gerrors.ErrRequestTimeout.Error(),
		})
		require.NoError(t, err)

		decoded, err := serializer.Deserialize(data)
		require.NoError(t, err)

		response, ok := decoded.(*AsyncResponse)
		require.True(t, ok)
		require.Nil(t, response.Message)
		require.Equal(t, gerrors.ErrRequestTimeout.Error(), response.Error)
	})
}

func TestAsyncEnvelopeSerializerErrors(t *testing.T) {
	requestSerializer := new(AsyncRequestSerializer)
	responseSerializer := new(AsyncResponseSerializer)

	t.Run("serialize rejects foreign types", func(t *testing.T) {
		_, err := requestSerializer.Serialize(new(testpb.Reply))
		require.ErrorIs(t, err, errNotAsyncRequestFrame)

		_, err = responseSerializer.Serialize(new(testpb.Reply))
		require.ErrorIs(t, err, errNotAsyncResponseFrame)
	})

	t.Run("serializers reject each other's frames", func(t *testing.T) {
		data, err := responseSerializer.Serialize(&AsyncResponse{CorrelationID: "corr", Error: "boom"})
		require.NoError(t, err)

		_, err = requestSerializer.Deserialize(data)
		require.ErrorIs(t, err, errNotAsyncRequestFrame)
	})

	t.Run("deserialize rejects short and foreign frames", func(t *testing.T) {
		_, err := requestSerializer.Deserialize([]byte{0x01, 0x02})
		require.ErrorIs(t, err, errNotAsyncRequestFrame)

		_, err = responseSerializer.Deserialize([]byte("not-a-frame-at-all"))
		require.ErrorIs(t, err, errNotAsyncResponseFrame)
	})

	t.Run("non-proto payload is rejected", func(t *testing.T) {
		_, err := requestSerializer.Serialize(&AsyncRequest{
			CorrelationID: "corr",
			ReplyTo:       &AsyncReplyTo{Kind: ReplyToActor, Actor: address.New("actor", "sys", "127.0.0.1", 9000)},
			Message:       "plain string",
		})
		require.ErrorIs(t, err, gerrors.ErrInvalidMessage)
	})

	t.Run("missing payload is rejected", func(t *testing.T) {
		_, err := requestSerializer.Serialize(&AsyncRequest{
			CorrelationID: "corr",
			ReplyTo:       &AsyncReplyTo{Kind: ReplyToActor, Actor: address.New("actor", "sys", "127.0.0.1", 9000)},
		})
		require.ErrorIs(t, err, gerrors.ErrInvalidMessage)
	})

	t.Run("unmarshalable payload is rejected", func(t *testing.T) {
		_, err := requestSerializer.Serialize(&AsyncRequest{
			CorrelationID: "corr",
			ReplyTo:       &AsyncReplyTo{Kind: ReplyToActor, Actor: address.New("actor", "sys", "127.0.0.1", 9000)},
			Message:       &testpb.Reply{Content: invalidUTF8String()},
		})
		require.Error(t, err)

		_, err = responseSerializer.Serialize(&AsyncResponse{
			CorrelationID: "corr",
			Message:       &testpb.Reply{Content: invalidUTF8String()},
		})
		require.Error(t, err)
	})

	t.Run("reply target missing its payload is rejected", func(t *testing.T) {
		_, err := requestSerializer.Serialize(&AsyncRequest{
			CorrelationID: "corr",
			ReplyTo:       &AsyncReplyTo{Kind: ReplyToActor},
			Message:       &testpb.Reply{Content: "ok"},
		})
		require.ErrorIs(t, err, gerrors.ErrInvalidMessage)

		_, err = requestSerializer.Serialize(&AsyncRequest{
			CorrelationID: "corr",
			ReplyTo:       &AsyncReplyTo{Kind: ReplyToGrain},
			Message:       &testpb.Reply{Content: "ok"},
		})
		require.ErrorIs(t, err, gerrors.ErrInvalidMessage)
	})

	t.Run("malformed reply target fails at the node boundary", func(t *testing.T) {
		payload, err := anypb.New(&testpb.Reply{Content: "ok"})
		require.NoError(t, err)

		frame, err := frameAsyncEnvelope(asyncRequestMagic, &internalpb.AsyncRequest{
			CorrelationId: "corr",
			ReplyTo:       "not-an-address",
			ReplyKind:     internalpb.ReplyKind_REPLY_KIND_ACTOR,
			Message:       payload,
		})
		require.NoError(t, err)

		_, err = requestSerializer.Deserialize(frame)
		require.Error(t, err)
	})

	t.Run("malformed grain identity fails at the node boundary", func(t *testing.T) {
		payload, err := anypb.New(&testpb.Reply{Content: "ok"})
		require.NoError(t, err)

		frame, err := frameAsyncEnvelope(asyncRequestMagic, &internalpb.AsyncRequest{
			CorrelationId: "corr",
			ReplyTo:       "missing-separator",
			ReplyKind:     internalpb.ReplyKind_REPLY_KIND_GRAIN,
			Message:       payload,
		})
		require.NoError(t, err)

		_, err = requestSerializer.Deserialize(frame)
		require.Error(t, err)
	})

	t.Run("unknown reply kind is rejected", func(t *testing.T) {
		_, err := decodeAsyncReplyTo(internalpb.ReplyKind(99), "whatever")
		require.ErrorIs(t, err, gerrors.ErrInvalidMessage)

		_, _, err = encodeAsyncReplyTo(&AsyncReplyTo{Kind: ReplyToKind(99)})
		require.ErrorIs(t, err, gerrors.ErrInvalidMessage)
	})

	t.Run("unmarshalable envelope field is rejected", func(t *testing.T) {
		// An invalid UTF-8 string field fails at proto.Marshal, which is the
		// framing failure the payload checks above cannot reach.
		_, err := requestSerializer.Serialize(&AsyncRequest{
			CorrelationID: invalidUTF8String(),
			ReplyTo:       &AsyncReplyTo{Kind: ReplyToActor, Actor: address.New("actor", "sys", "127.0.0.1", 9000)},
			Message:       &testpb.Reply{Content: "ok"},
		})
		require.Error(t, err)

		_, err = responseSerializer.Serialize(&AsyncResponse{
			CorrelationID: "corr",
			Error:         invalidUTF8String(),
		})
		require.Error(t, err)
	})

	t.Run("corrupt frame body is rejected", func(t *testing.T) {
		// 0x08 is a field-1 varint tag with no value: a truncated message.
		corruptRequest := append(asyncRequestMagic[:], 0x08)
		_, err := requestSerializer.Deserialize(corruptRequest)
		require.Error(t, err)
		require.NotErrorIs(t, err, errNotAsyncRequestFrame)

		corruptResponse := append(asyncResponseMagic[:], 0x08)
		_, err = responseSerializer.Deserialize(corruptResponse)
		require.Error(t, err)
		require.NotErrorIs(t, err, errNotAsyncResponseFrame)
	})

	t.Run("request frame without a payload is rejected", func(t *testing.T) {
		frame, err := frameAsyncEnvelope(asyncRequestMagic, &internalpb.AsyncRequest{
			CorrelationId: "corr",
			ReplyKind:     internalpb.ReplyKind_REPLY_KIND_CLIENT,
		})
		require.NoError(t, err)

		_, err = requestSerializer.Deserialize(frame)
		require.ErrorIs(t, err, gerrors.ErrInvalidMessage)
	})

	t.Run("undecodable payload is rejected", func(t *testing.T) {
		frame, err := frameAsyncEnvelope(asyncResponseMagic, &internalpb.AsyncResponse{
			CorrelationId: "corr",
			Message:       &anypb.Any{TypeUrl: "type.googleapis.com/nope.Nope", Value: []byte("bad")},
		})
		require.NoError(t, err)

		_, err = responseSerializer.Deserialize(frame)
		require.Error(t, err)
	})
}

func TestAsyncReplyToValid(t *testing.T) {
	testCases := []struct {
		name    string
		replyTo *AsyncReplyTo
		want    bool
	}{
		{name: "nil target", replyTo: nil, want: false},
		{
			name:    "actor with address",
			replyTo: &AsyncReplyTo{Kind: ReplyToActor, Actor: address.New("actor", "sys", "127.0.0.1", 9000)},
			want:    true,
		},
		{name: "actor without address", replyTo: &AsyncReplyTo{Kind: ReplyToActor}, want: false},
		{name: "grain with identity", replyTo: &AsyncReplyTo{Kind: ReplyToGrain, Grain: "TestGrain/one"}, want: true},
		{name: "grain without identity", replyTo: &AsyncReplyTo{Kind: ReplyToGrain}, want: false},
		{name: "unknown kind", replyTo: &AsyncReplyTo{Kind: ReplyToKind(99)}, want: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.want, testCase.replyTo.Valid())
		})
	}
}
