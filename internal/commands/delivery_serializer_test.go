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
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/remoteclient"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// TestReliableDeliverySerializerRoundTrip verifies every wire command.
func TestReliableDeliverySerializerRoundTrip(t *testing.T) {
	register, err := NewRegisterConsumer("nonce-1")
	require.NoError(t, err)
	registrationAck, err := NewRegistrationAck("session-1", 2, "nonce-1")
	require.NoError(t, err)
	request, err := NewRequest("session-1", "nonce-1", 1, 51, true)
	require.NoError(t, err)
	ack, err := NewAck("session-1", "nonce-1", 1)
	require.NoError(t, err)
	sequenced, err := NewSequencedMessage("session-1", "message-1", 2, []byte("payload"))
	require.NoError(t, err)

	commands := []any{
		register,
		registrationAck,
		request,
		ack,
		sequenced,
	}

	serializer := new(DeliverySerializer)

	for _, command := range commands {
		data, err := serializer.Serialize(command)
		require.NoError(t, err)
		require.False(t, resolvesAsProtoFrame(data))

		decoded, err := serializer.Deserialize(data)
		require.NoError(t, err)
		assert.Equal(t, command, decoded)
	}
}

// TestReliableDeliverySerializerDefensivePayloadCopy verifies that a decoded
// command does not retain the protobuf decoder's mutable byte slice.
func TestReliableDeliverySerializerDefensivePayloadCopy(t *testing.T) {
	command, err := NewSequencedMessage("session-1", "message-1", 1, []byte("payload"))
	require.NoError(t, err)

	serializer := new(DeliverySerializer)
	data, err := serializer.Serialize(command)
	require.NoError(t, err)
	decoded, err := serializer.Deserialize(data)
	require.NoError(t, err)

	sequenced := decoded.(*SequencedMessage)
	payload := sequenced.Payload()
	payload[0] = 'P'
	assert.Equal(t, []byte("payload"), sequenced.Payload())
}

// TestReliableDeliverySerializerErrors verifies malformed and foreign frames.
func TestReliableDeliverySerializerErrors(t *testing.T) {
	serializer := new(DeliverySerializer)

	t.Run("foreign command", func(t *testing.T) {
		_, err := serializer.Serialize(new(testpb.Reply))
		assert.ErrorIs(t, err, errNotDeliveryFrame)
	})

	t.Run("typed nil command", func(t *testing.T) {
		var command *RegisterConsumer

		_, err := serializer.Serialize(command)
		assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)
	})

	t.Run("foreign frame", func(t *testing.T) {
		_, err := serializer.Deserialize([]byte("not-a-delivery-frame"))
		assert.ErrorIs(t, err, errNotDeliveryFrame)
	})

	t.Run("invalid protobuf", func(t *testing.T) {
		_, err := serializer.Deserialize(append(deliveryFrameMagic[:], 0xff))
		require.Error(t, err)
	})

	t.Run("missing command", func(t *testing.T) {
		data, err := proto.MarshalOptions{}.MarshalAppend(deliveryFrameMagic[:], new(internalpb.DeliveryEnvelope))
		require.NoError(t, err)

		_, err = serializer.Deserialize(data)
		assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)
	})

	t.Run("missing sequenced payload", func(t *testing.T) {
		envelope := &internalpb.DeliveryEnvelope{
			Command: &internalpb.DeliveryEnvelope_SequencedMessage{
				SequencedMessage: &internalpb.SequencedMessage{
					SessionId: "session-1",
					MessageId: "message-1",
					Seq:       1,
				},
			},
		}

		data, err := proto.MarshalOptions{}.MarshalAppend(deliveryFrameMagic[:], envelope)
		require.NoError(t, err)

		_, err = serializer.Deserialize(data)
		assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)
	})

	t.Run("invalid command fields", func(t *testing.T) {
		for _, command := range []any{
			new(RegisterConsumer),
			new(RegistrationAck),
			new(Request),
			new(Ack),
			new(SequencedMessage),
		} {
			_, err := serializer.Serialize(command)
			assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)
		}
	})
}

// TestReliablePayloadCodecRoundTrips verifies supported serializer families.
func TestReliablePayloadCodecRoundTrips(t *testing.T) {
	t.Run("protobuf", func(t *testing.T) {
		config := remote.DefaultConfig()
		payload := &testpb.Reply{Content: "protobuf"}
		assertReliablePayloadRoundTrip(t, config, payload)
	})

	t.Run("CBOR", func(t *testing.T) {
		config := remote.NewConfig("127.0.0.1", 0, remote.WithSerializables(new(deliveryCodecPayload)))
		payload := &deliveryCodecPayload{Value: "CBOR"}
		assertReliablePayloadRoundTrip(t, config, payload)
	})

	t.Run("JSON", func(t *testing.T) {
		config := remote.NewConfig("127.0.0.1", 0, remote.WithJSONSerializables(new(deliveryCodecPayload)))
		payload := &deliveryCodecPayload{Value: "JSON"}
		assertReliablePayloadRoundTrip(t, config, payload)
	})

	t.Run("custom", func(t *testing.T) {
		config := remote.NewConfig("127.0.0.1", 0, remote.WithSerializers(new(deliveryCodecPayload), new(deliveryCodecSerializer)))
		payload := &deliveryCodecPayload{Value: "custom"}
		assertReliablePayloadRoundTrip(t, config, payload)
	})
}

// TestReliablePayloadCodecValidation verifies invalid codec inputs and
// serializer results.
func TestReliablePayloadCodecValidation(t *testing.T) {
	var typedNil *deliveryCodecPayload
	serializer := new(deliveryCodecSerializer)

	_, err := EncodeReliablePayload(new(deliveryCodecPayload), nil)
	assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)

	_, err = EncodeReliablePayload(typedNil, serializer)
	assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)

	_, err = DecodeReliablePayload([]byte("payload"), nil)
	assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)

	_, err = DecodeReliablePayload(nil, serializer)
	assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)

	_, err = EncodeReliablePayload(new(deliveryCodecPayload), deliveryEmptySerializer{})
	assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)

	_, err = EncodeReliablePayload(new(deliveryCodecPayload), deliveryFailingSerializer{})
	assert.ErrorContains(t, err, "encode boom")

	_, err = DecodeReliablePayload([]byte("payload"), deliveryFailingSerializer{})
	assert.ErrorContains(t, err, "decode boom")

	data, err := EncodeReliablePayload(new(deliveryCodecPayload), deliveryNilSerializer{})
	require.NoError(t, err)

	_, err = DecodeReliablePayload(data, deliveryNilSerializer{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil payload")
}

// TestReliablePayloadCodecSnapshotsBeforeAsyncWork verifies that later
// application and serializer-buffer mutation cannot change encoded data.
func TestReliablePayloadCodecSnapshotsBeforeAsyncWork(t *testing.T) {
	shared := []byte(`{"Value":"before"}`)
	serializer := &deliveryCodecSerializer{data: shared}
	config := remote.NewConfig("127.0.0.1", 0, remote.WithSerializers(new(deliveryCodecPayload), serializer))
	client := remoteclient.NewClient(remoteclient.ClientSerializerOptions(config)...)
	t.Cleanup(client.Close)
	payload := &deliveryCodecPayload{Value: "before"}

	data, err := EncodeReliablePayload(payload, client.Serializer(payload))
	require.NoError(t, err)

	payload.Value = "after"
	copy(shared, []byte(`{"Value":"after"}`))

	decoded, err := DecodeReliablePayload(data, client.Serializer(nil))
	require.NoError(t, err)
	assert.Equal(t, &deliveryCodecPayload{Value: "before"}, decoded)
}

// assertReliablePayloadRoundTrip verifies a fresh value is decoded on every
// call.
func assertReliablePayloadRoundTrip(t *testing.T, config *remote.Config, payload any) {
	t.Helper()

	client := remoteclient.NewClient(remoteclient.ClientSerializerOptions(config)...)
	t.Cleanup(client.Close)

	data, err := EncodeReliablePayload(payload, client.Serializer(payload))
	require.NoError(t, err)
	require.NotEmpty(t, data)

	first, err := DecodeReliablePayload(data, client.Serializer(nil))
	require.NoError(t, err)
	second, err := DecodeReliablePayload(data, client.Serializer(nil))
	require.NoError(t, err)

	assertReliablePayloadEqual(t, payload, first)
	assertReliablePayloadEqual(t, payload, second)
	assert.NotSame(t, first, second)
}

// assertReliablePayloadEqual compares protobuf messages by wire-visible fields
// and other payloads by value.
func assertReliablePayloadEqual(t *testing.T, expected, actual any) {
	t.Helper()

	expectedProto, ok := expected.(proto.Message)
	if ok {
		actualProto, matches := actual.(proto.Message)
		require.True(t, matches)
		assert.True(t, proto.Equal(expectedProto, actualProto))
		return
	}

	assert.Equal(t, expected, actual)
}

// deliveryCodecPayload is a message used across codec serializer tests.
type deliveryCodecPayload struct {
	Value string
}

// deliveryCodecSerializer provides a fixed-type JSON serializer for codec
// tests. When data is set, Serialize returns that shared buffer.
type deliveryCodecSerializer struct {
	data []byte
}

// Serialize encodes a deliveryCodecPayload.
func (x *deliveryCodecSerializer) Serialize(message any) ([]byte, error) {
	if x.data != nil {
		return x.data, nil
	}

	return json.Marshal(message)
}

// Deserialize decodes a deliveryCodecPayload.
func (x *deliveryCodecSerializer) Deserialize(data []byte) (any, error) {
	payload := new(deliveryCodecPayload)
	if err := json.Unmarshal(data, payload); err != nil {
		return nil, err
	}

	return payload, nil
}

// deliveryNilSerializer verifies that nil deserialization results are rejected.
type deliveryNilSerializer struct{}

// Serialize returns an empty JSON object.
func (x deliveryNilSerializer) Serialize(any) ([]byte, error) {
	return []byte("{}"), nil
}

// Deserialize returns a nil payload without an error.
func (x deliveryNilSerializer) Deserialize([]byte) (any, error) {
	return nil, nil
}

// deliveryNilSerializer implements remote.Serializer.
var _ remote.Serializer = deliveryNilSerializer{}

// deliveryEmptySerializer verifies that empty serialized frames are rejected.
type deliveryEmptySerializer struct{}

// Serialize returns an invalid empty frame.
func (x deliveryEmptySerializer) Serialize(any) ([]byte, error) {
	return nil, nil
}

// Deserialize is unused by the test.
func (x deliveryEmptySerializer) Deserialize([]byte) (any, error) {
	return nil, nil
}

// deliveryEmptySerializer implements remote.Serializer.
var _ remote.Serializer = deliveryEmptySerializer{}

// deliveryFailingSerializer propagates encode/decode errors through the codec helpers.
type deliveryFailingSerializer struct{}

func (x deliveryFailingSerializer) Serialize(any) ([]byte, error) {
	return nil, errors.New("encode boom")
}

func (x deliveryFailingSerializer) Deserialize([]byte) (any, error) {
	return nil, errors.New("decode boom")
}

var _ remote.Serializer = deliveryFailingSerializer{}

// deliveryCodecSerializer implements remote.Serializer.
var _ remote.Serializer = (*deliveryCodecSerializer)(nil)

// BenchmarkReliablePayloadCodec measures application payload snapshot and
// restoration costs for representative frame sizes.
func BenchmarkReliablePayloadCodec(b *testing.B) {
	serializer := remote.NewProtoSerializer()

	for _, size := range []int{1 << 10, 64 << 10, 1 << 20} {
		b.Run(fmt.Sprintf("%dB", size), func(b *testing.B) {
			message := &testpb.Reply{Content: string(make([]byte, size))}
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()

			for b.Loop() {
				data, err := EncodeReliablePayload(message, serializer)
				if err != nil {
					b.Fatal(err)
				}

				if _, err := DecodeReliablePayload(data, serializer); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkDeliverySerializerSequencedMessage measures delivery-envelope
// serialization for representative payload sizes.
func BenchmarkDeliverySerializerSequencedMessage(b *testing.B) {
	serializer := new(DeliverySerializer)

	for _, size := range []int{1 << 10, 64 << 10, 1 << 20} {
		b.Run(fmt.Sprintf("%dB", size), func(b *testing.B) {
			command, err := NewSequencedMessage("session-1", "message-1", 1, make([]byte, size))
			if err != nil {
				b.Fatal(err)
			}

			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()

			for b.Loop() {
				if _, err := serializer.Serialize(command); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
