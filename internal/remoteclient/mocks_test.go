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
	nethttp "net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/remote"
)

// cborReplyMessage is a plain Go message serialized with CBOR. The tests pair
// it with a proto message to cross the serializer families between a request
// and its reply.
type cborReplyMessage struct {
	Amount int
}

// replyCount is a scalar message serialized with JSON. Its JSON encoding is
// also valid CBOR for another value, so it tells which serializer decoded it.
type replyCount int

// replyOther is a plain Go message serialized with CBOR.
type replyOther struct {
	Amount int
}

// crossedReply returns the serialized frame and the duplex serializer ID of a
// reply of the other serializer family than the one that encoded the request:
// a proto reply to a CBOR request and a CBOR reply to a proto request.
func crossedReply(t *testing.T, requestSerializerID byte) ([]byte, byte) {
	t.Helper()

	if requestSerializerID == inet.SerializerIDCBOR {
		frame, err := remote.NewProtoSerializer().Serialize(durationpb.New(3 * time.Second))
		require.NoError(t, err)
		return frame, inet.SerializerIDPublicProto
	}

	frame, err := remote.NewCBORSerializer().Serialize(&cborReplyMessage{Amount: 7})
	require.NoError(t, err)
	return frame, inet.SerializerIDCBOR
}

// requireCrossedReply asserts that got is the reply of the other serializer
// family than the one request belongs to.
func requireCrossedReply(t *testing.T, request, got any) {
	t.Helper()

	if _, ok := request.(*cborReplyMessage); ok {
		reply, ok := got.(*durationpb.Duration)
		require.True(t, ok, "expected a proto reply, got %T", got)
		require.True(t, proto.Equal(durationpb.New(3*time.Second), reply))
		return
	}

	require.Equal(t, &cborReplyMessage{Amount: 7}, got)
}

// newCrossedReplyClient builds a client that serializes cborReplyMessage with
// CBOR next to the default proto serializer.
func newCrossedReplyClient(t *testing.T, pin remote.ProtocolPin) Client {
	t.Helper()

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientProtocolPin(pin),
		WithClientSerializers(new(cborReplyMessage), remote.NewCBORSerializer()),
	)
	t.Cleanup(r.Close)
	return r
}

// crossedDuplexAskHandler answers every duplex ask with a reply of the other
// serializer family than the request's.
func crossedDuplexAskHandler(t *testing.T) func(context.Context, inet.DataEnvelope) (inet.ReplyEnvelope, error) {
	return func(_ context.Context, env inet.DataEnvelope) (inet.ReplyEnvelope, error) {
		frame, serializerID := crossedReply(t, env.SerializerID)
		typeName, ok := frameTypeName(frame)
		require.True(t, ok)
		return inet.ReplyEnvelope{TypeName: string(typeName), SerializerID: serializerID, Payload: frame}, nil
	}
}

// crossedLegacyAskHandler answers a legacy RemoteAskRequest with one reply per
// message, each of the other serializer family than its request's.
func crossedLegacyAskHandler(t *testing.T) inet.ProtoHandler {
	cbor := remote.NewCBORSerializer()

	return func(_ context.Context, _ inet.Connection, req proto.Message) (proto.Message, error) {
		messages := req.(*internalpb.RemoteAskRequest).GetRemoteMessages()
		replies := make([][]byte, 0, len(messages))

		for _, message := range messages {
			requestSerializerID := byte(inet.SerializerIDPublicProto)
			if _, err := cbor.Deserialize(message.GetMessage()); err == nil {
				requestSerializerID = inet.SerializerIDCBOR
			}

			frame, _ := crossedReply(t, requestSerializerID)
			replies = append(replies, frame)
		}

		return internalpb.RemoteAskResponse_builder{Messages: replies}.Build(), nil
	}
}

// crossedRequests lists one request per serializer family.
func crossedRequests() map[string]any {
	return map[string]any{
		"cbor request, proto reply": &cborReplyMessage{Amount: 1},
		"proto request, cbor reply": durationpb.New(time.Second),
	}
}

// testPeerState builds the snapshot the PersistPeerState tests send, and whose
// host and peers port the server-side handler asserts on.
func testPeerState() *internalpb.PeerState {
	return internalpb.PeerState_builder{
		Host:         "127.0.0.1",
		RemotingPort: 8080,
		PeersPort:    9000,
	}.Build()
}

// persistPeerStateHandler answers a PersistPeerStateRequest after checking that
// the snapshot built by testPeerState survived the round trip intact.
func persistPeerStateHandler(t *testing.T) inet.ProtoHandler {
	t.Helper()
	return func(_ context.Context, _ inet.Connection, msg proto.Message) (proto.Message, error) {
		req := msg.(*internalpb.PersistPeerStateRequest)
		assert.Equal(t, "127.0.0.1", req.GetPeerState().GetHost())
		assert.EqualValues(t, 9000, req.GetPeerState().GetPeersPort())
		return new(internalpb.PersistPeerStateResponse), nil
	}
}

// failingDependency implements extension.Dependency but MarshalBinary always fails.
// Used to exercise getGrainFromRequest's codec.EncodeDependencies error path.
type failingDependency struct{ err error }

func (f *failingDependency) ID() string { return "failing-dep" }

func (f *failingDependency) MarshalBinary() ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	return nil, errors.New("marshal failed")
}

func (f *failingDependency) UnmarshalBinary([]byte) error { return nil }

var _ extension.Dependency = (*failingDependency)(nil)

// nonProtoMsg is an arbitrary type with no registered serializer.
type nonProtoMsg struct{ value string }

// testInterface and nonProtoImpl are used to verify interface-based serializer
// registration and forwarding via ClientSerializerOptions.
type testInterface interface{ testMarker() }

type nonProtoImpl struct{}

func (nonProtoImpl) testMarker() {}

type mockPropagator struct{}

func (mockPropagator) Inject(context.Context, nethttp.Header) error {
	return nil
}

func (mockPropagator) Extract(ctx context.Context, _ nethttp.Header) (context.Context, error) {
	return ctx, nil
}

// errInjectPropagator is a ContextPropagator whose Inject always returns an error.
type errInjectPropagator struct{}

func (errInjectPropagator) Inject(context.Context, nethttp.Header) error {
	return errors.New("inject error")
}

func (errInjectPropagator) Extract(ctx context.Context, _ nethttp.Header) (context.Context, error) {
	return ctx, nil
}

// headerPropagator injects a fixed header so the header-copy loop in enrichContext is exercised.
type headerPropagator struct{ key, value string }

func (h headerPropagator) Inject(_ context.Context, headers nethttp.Header) error {
	headers.Set(h.key, h.value)
	return nil
}

func (h headerPropagator) Extract(ctx context.Context, _ nethttp.Header) (context.Context, error) {
	return ctx, nil
}

// mockDependencyForRemote is a minimal Dependency for RemoteDependencies tests.
// It can be registered with types.Registry and decoded from internalpb.Dependency.
type mockDependencyForRemote struct {
	id string
}

func (m *mockDependencyForRemote) MarshalBinary() ([]byte, error) {
	return []byte(m.id), nil
}

func (m *mockDependencyForRemote) UnmarshalBinary(data []byte) error {
	m.id = string(data)
	return nil
}

func (m *mockDependencyForRemote) ID() string { return m.id }

var _ extension.Dependency = (*mockDependencyForRemote)(nil)
