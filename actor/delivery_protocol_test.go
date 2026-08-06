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

package actor

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/log"
)

// reliableDeliveryBenchmarkWindow is the largest supported demand window.
const reliableDeliveryBenchmarkWindow = 10_000

// TestReliablePayload verifies immutable payload snapshots and equality.
func TestReliablePayload(t *testing.T) {
	source := []byte("payload")
	payload, err := NewReliablePayload(source)
	require.NoError(t, err)

	source[0] = 'P'
	assert.Equal(t, []byte("payload"), payload.Bytes())

	got := payload.Bytes()
	got[0] = 'P'
	assert.Equal(t, []byte("payload"), payload.Bytes())
	assert.Equal(t, []byte("payload"), payload.rawBytes())

	equal, err := NewReliablePayload([]byte("payload"))
	require.NoError(t, err)
	different, err := NewReliablePayload([]byte("other"))
	require.NoError(t, err)

	assert.True(t, payload.Equal(equal))
	assert.False(t, payload.Equal(different))
}

// TestNewReliablePayloadRejectsEmptyPayload verifies serialized-frame
// validation.
func TestNewReliablePayloadRejectsEmptyPayload(t *testing.T) {
	payload, err := NewReliablePayload(nil)

	assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)
	assert.Equal(t, ReliablePayload{}, payload)
}

// TestReliableProtocol verifies protocol correlation and authorization.
func TestReliableProtocol(t *testing.T) {
	producer, producerController, other := reliableProtocolPIDs()

	request, err := newRequestNext("session-1", "token-1", producer, producerController)
	require.NoError(t, err)
	assert.Equal(t, "session-1", request.SessionID())
	assert.Equal(t, "token-1", request.Token())
	assert.True(t, request.IsAuthorizedFor(producer, producerController))
	assert.False(t, request.IsAuthorizedFor(other, producerController))
	assert.False(t, request.IsAuthorizedFor(producer, other))
	assert.False(t, request.IsAuthorizedFor(nil, nil))

	message := &reliableProtocolMessage{value: "hello"}
	produced, err := NewProduced(request, "message-1", message)
	require.NoError(t, err)
	assert.Equal(t, request.SessionID(), produced.SessionID())
	assert.Equal(t, request.Token(), produced.Token())
	assert.Equal(t, "message-1", produced.MessageID())
	assert.Same(t, message, produced.Payload())

	stored, err := newStored(produced, 1, producer, producerController)
	require.NoError(t, err)
	assert.Equal(t, produced.SessionID(), stored.SessionID())
	assert.Equal(t, produced.Token(), stored.Token())
	assert.Equal(t, produced.MessageID(), stored.MessageID())
	assert.EqualValues(t, 1, stored.Seq())
	assert.True(t, stored.IsAuthorizedFor(producer, producerController))
	assert.False(t, stored.IsAuthorizedFor(other, producerController))
	assert.False(t, stored.IsAuthorizedFor(producer, other))

	storedAck, err := NewStoredAck(stored)
	require.NoError(t, err)
	assert.Equal(t, stored.SessionID(), storedAck.SessionID())
	assert.Equal(t, stored.Token(), storedAck.Token())
	assert.Equal(t, stored.MessageID(), storedAck.MessageID())

	delivery, err := newDelivery("session-1", "message-1", 1, message, producer, producerController)
	require.NoError(t, err)
	assert.Equal(t, "session-1", delivery.SessionID())
	assert.Equal(t, "message-1", delivery.MessageID())
	assert.EqualValues(t, 1, delivery.Seq())
	assert.Same(t, message, delivery.Payload())
	assert.True(t, delivery.IsAuthorizedFor(producer, producerController))
	assert.False(t, delivery.IsAuthorizedFor(other, producerController))
	assert.False(t, delivery.IsAuthorizedFor(producer, other))

	confirmed, err := NewConfirmed(delivery)
	require.NoError(t, err)
	assert.Equal(t, delivery.SessionID(), confirmed.SessionID())
	assert.Equal(t, delivery.MessageID(), confirmed.MessageID())
	assert.Equal(t, delivery.Seq(), confirmed.Seq())
}

// TestReliableProtocolRepliesCopyCorrelationFields verifies that replies do
// not retain mutable trigger state.
func TestReliableProtocolRepliesCopyCorrelationFields(t *testing.T) {
	endpoint, controller, _ := reliableProtocolPIDs()

	request, err := newRequestNext("session-1", "token-1", endpoint, controller)
	require.NoError(t, err)
	produced, err := NewProduced(request, "message-1", "payload")
	require.NoError(t, err)

	request.sessionID = "changed-session"
	request.token = "changed-token"
	assert.Equal(t, "session-1", produced.SessionID())
	assert.Equal(t, "token-1", produced.Token())

	stored, err := newStored(produced, 1, endpoint, controller)
	require.NoError(t, err)
	storedAck, err := NewStoredAck(stored)
	require.NoError(t, err)

	stored.sessionID = "changed-session"
	stored.token = "changed-token"
	stored.messageID = "changed-message"
	assert.Equal(t, "session-1", storedAck.SessionID())
	assert.Equal(t, "token-1", storedAck.Token())
	assert.Equal(t, "message-1", storedAck.MessageID())

	delivery, err := newDelivery("session-1", "message-1", 1, "payload", endpoint, controller)
	require.NoError(t, err)
	confirmed, err := NewConfirmed(delivery)
	require.NoError(t, err)

	delivery.sessionID = "changed-session"
	delivery.messageID = "changed-message"
	delivery.seq = 2
	assert.Equal(t, "session-1", confirmed.SessionID())
	assert.Equal(t, "message-1", confirmed.MessageID())
	assert.EqualValues(t, 1, confirmed.Seq())
}

// TestReliableProtocolValidation verifies protocol constructor invariants.
func TestReliableProtocolValidation(t *testing.T) {
	endpoint, controller, _ := reliableProtocolPIDs()
	remoteEndpoint := reliableProtocolPID("remote-endpoint", 9003)
	remoteEndpoint.setState(remoteState, true)
	remoteController := reliableProtocolPID("remote-controller", 9004)
	remoteController.setState(remoteState, true)

	request, err := newRequestNext("session-1", "token-1", endpoint, controller)
	require.NoError(t, err)
	produced, err := NewProduced(request, "message-1", "payload")
	require.NoError(t, err)

	var typedNil *reliableProtocolMessage

	tests := map[string]func() error{
		"request session": func() error {
			_, err := newRequestNext(" ", "token-1", endpoint, controller)
			return err
		},

		"request token": func() error {
			_, err := newRequestNext("session-1", " ", endpoint, controller)
			return err
		},

		"request endpoint": func() error {
			_, err := newRequestNext("session-1", "token-1", nil, controller)
			return err
		},

		"request controller": func() error {
			_, err := newRequestNext("session-1", "token-1", endpoint, nil)
			return err
		},

		"request remote endpoint": func() error {
			_, err := newRequestNext("session-1", "token-1", remoteEndpoint, controller)
			return err
		},

		"request remote controller": func() error {
			_, err := newRequestNext("session-1", "token-1", endpoint, remoteController)
			return err
		},

		"produced request": func() error {
			_, err := NewProduced(nil, "message-1", "payload")
			return err
		},

		"produced malformed request": func() error {
			_, err := NewProduced(&RequestNext{}, "message-1", "payload")
			return err
		},

		"produced request without ownership": func() error {
			_, err := NewProduced(
				&RequestNext{
					sessionID: "session-1",
					token:     "token-1",
				},
				"message-1",
				"payload",
			)
			return err
		},

		"produced message ID": func() error {
			_, err := NewProduced(request, " ", "payload")
			return err
		},

		"produced payload": func() error {
			_, err := NewProduced(request, "message-1", nil)
			return err
		},

		"produced typed nil payload": func() error {
			_, err := NewProduced(request, "message-1", typedNil)
			return err
		},

		"stored produced": func() error {
			_, err := newStored(nil, 1, endpoint, controller)
			return err
		},

		"stored malformed produced": func() error {
			_, err := newStored(&Produced{}, 1, endpoint, controller)
			return err
		},

		"stored produced without message ID": func() error {
			_, err := newStored(
				&Produced{
					sessionID: "session-1",
					token:     "token-1",
					payload:   "payload",
				},
				1,
				endpoint,
				controller,
			)
			return err
		},

		"stored produced without payload": func() error {
			_, err := newStored(
				&Produced{
					sessionID: "session-1",
					token:     "token-1",
					messageID: "message-1",
				},
				1,
				endpoint,
				controller,
			)
			return err
		},

		"stored sequence": func() error {
			_, err := newStored(produced, 0, endpoint, controller)
			return err
		},

		"stored endpoint": func() error {
			_, err := newStored(produced, 1, nil, controller)
			return err
		},

		"stored ack": func() error {
			_, err := NewStoredAck(nil)
			return err
		},

		"stored ack malformed message": func() error {
			_, err := NewStoredAck(&Stored{})
			return err
		},

		"stored ack without message ID": func() error {
			_, err := NewStoredAck(
				&Stored{
					sessionID: "session-1",
					token:     "token-1",
				},
			)
			return err
		},

		"stored ack without sequence": func() error {
			_, err := NewStoredAck(
				&Stored{
					sessionID: "session-1",
					token:     "token-1",
					messageID: "message-1",
				},
			)
			return err
		},

		"stored ack without ownership": func() error {
			_, err := NewStoredAck(
				&Stored{
					sessionID: "session-1",
					token:     "token-1",
					messageID: "message-1",
					seq:       1,
				},
			)
			return err
		},

		"delivery session": func() error {
			_, err := newDelivery(" ", "message-1", 1, "payload", endpoint, controller)
			return err
		},

		"delivery message ID": func() error {
			_, err := newDelivery("session-1", " ", 1, "payload", endpoint, controller)
			return err
		},

		"delivery sequence": func() error {
			_, err := newDelivery("session-1", "message-1", 0, "payload", endpoint, controller)
			return err
		},

		"delivery payload": func() error {
			_, err := newDelivery("session-1", "message-1", 1, nil, endpoint, controller)
			return err
		},

		"delivery typed nil payload": func() error {
			_, err := newDelivery("session-1", "message-1", 1, typedNil, endpoint, controller)
			return err
		},

		"delivery endpoint": func() error {
			_, err := newDelivery("session-1", "message-1", 1, "payload", nil, controller)
			return err
		},

		"confirmation": func() error {
			_, err := NewConfirmed(nil)
			return err
		},

		"confirmation malformed delivery": func() error {
			_, err := NewConfirmed(&Delivery{})
			return err
		},

		"confirmation without message ID": func() error {
			_, err := NewConfirmed(
				&Delivery{
					sessionID: "session-1",
				},
			)
			return err
		},

		"confirmation without sequence": func() error {
			_, err := NewConfirmed(
				&Delivery{
					sessionID: "session-1",
					messageID: "message-1",
				},
			)
			return err
		},

		"confirmation without payload": func() error {
			_, err := NewConfirmed(
				&Delivery{
					sessionID: "session-1",
					messageID: "message-1",
					seq:       1,
				},
			)
			return err
		},

		"confirmation without ownership": func() error {
			_, err := NewConfirmed(
				&Delivery{
					sessionID: "session-1",
					messageID: "message-1",
					seq:       1,
					payload:   "payload",
				},
			)
			return err
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			assert.ErrorIs(t, test(), gerrors.ErrInvalidMessage)
		})
	}
}

// TestReliableDeliveryFailed verifies terminal failure event fields.
func TestReliableDeliveryFailed(t *testing.T) {
	cause := errors.New("queue unavailable")
	event, err := newReliableDeliveryFailed(
		"orders",
		ReliableControllerRoleProducer,
		ReliableDeliveryStageStore,
		cause,
	)
	require.NoError(t, err)

	assert.Equal(t, "orders", event.EndpointName())
	assert.Equal(t, ReliableControllerRoleProducer, event.ControllerRole())
	assert.Equal(t, ReliableDeliveryStageStore, event.Stage())
	assert.ErrorIs(t, event.Err(), cause)
	assert.False(t, event.Timestamp().IsZero())
	assert.Equal(t, time.UTC, event.Timestamp().Location())

	roles := []ReliableControllerRole{
		ReliableControllerRoleProducer,
		ReliableControllerRoleConsumer,
	}

	stages := []ReliableDeliveryStage{
		ReliableDeliveryStageLoad,
		ReliableDeliveryStageStore,
		ReliableDeliveryStageAccept,
		ReliableDeliveryStageConfirm,
		ReliableDeliveryStageProtocol,
	}

	for _, role := range roles {
		for _, stage := range stages {
			event, err := newReliableDeliveryFailed("orders", role, stage, cause)
			require.NoError(t, err)
			assert.Equal(t, role, event.ControllerRole())
			assert.Equal(t, stage, event.Stage())
		}
	}
}

// TestReliableDeliveryFailedValidation verifies terminal event invariants.
func TestReliableDeliveryFailedValidation(t *testing.T) {
	cause := errors.New("queue unavailable")

	tests := map[string]func() error{
		"endpoint name": func() error {
			_, err := newReliableDeliveryFailed(
				" ",
				ReliableControllerRoleProducer,
				ReliableDeliveryStageStore,
				cause,
			)
			return err
		},

		"controller role": func() error {
			_, err := newReliableDeliveryFailed(
				"orders",
				reliableControllerRoleUnknown,
				ReliableDeliveryStageStore,
				cause,
			)
			return err
		},

		"delivery stage": func() error {
			_, err := newReliableDeliveryFailed(
				"orders",
				ReliableControllerRoleProducer,
				reliableDeliveryStageUnknown,
				cause,
			)
			return err
		},

		"failure": func() error {
			_, err := newReliableDeliveryFailed(
				"orders",
				ReliableControllerRoleProducer,
				ReliableDeliveryStageStore,
				nil,
			)
			return err
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			assert.ErrorIs(t, test(), gerrors.ErrInvalidMessage)
		})
	}
}

// TestReliableDeliverySerializersRegistered verifies that remoting selects the
// built-in delivery serializer for every controller command.
func TestReliableDeliverySerializersRegistered(t *testing.T) {
	system, err := NewActorSystem("reliableDeliverySerializers", WithLogger(log.DiscardLogger))
	require.NoError(t, err)

	actorSystem := system.(*actorSystem)
	require.NoError(t, actorSystem.setupRemoting())
	t.Cleanup(actorSystem.remoting.Close)

	messages := []any{
		new(commands.RegisterConsumer),
		new(commands.RegistrationAck),
		new(commands.Request),
		new(commands.Ack),
		new(commands.SequencedMessage),
	}

	for _, message := range messages {
		require.IsType(t, new(commands.DeliverySerializer), actorSystem.remoting.Serializer(message))
	}

	register, err := commands.NewRegisterConsumer("nonce-1")
	require.NoError(t, err)
	data, err := actorSystem.remoting.Serializer(register).Serialize(register)
	require.NoError(t, err)

	decoded, err := actorSystem.remoting.Serializer(nil).Deserialize(data)
	require.NoError(t, err)
	require.Equal(t, register, decoded)
}

// BenchmarkReliablePayloadLifecycle measures allocations while an encoded
// frame enters and leaves the public reliable-delivery value types.
func BenchmarkReliablePayloadLifecycle(b *testing.B) {
	for _, size := range []int{1 << 10, 64 << 10, 1 << 20} {
		b.Run(fmt.Sprintf("%dB", size), func(b *testing.B) {
			data := make([]byte, size)
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()

			for b.Loop() {
				payload, err := NewReliablePayload(data)
				if err != nil {
					b.Fatal(err)
				}

				request, err := NewStoreRequest("message-1", 1, payload)
				if err != nil {
					b.Fatal(err)
				}

				_ = request.Payload().Bytes()
			}
		})
	}
}

// BenchmarkDurableQueueStateLoad measures validation and snapshot allocation at
// the maximum supported flow-control window.
func BenchmarkDurableQueueStateLoad(b *testing.B) {
	payload, err := NewReliablePayload(make([]byte, 1<<10))
	if err != nil {
		b.Fatal(err)
	}

	messages := make([]UnconfirmedMessage, reliableDeliveryBenchmarkWindow)

	for index := range messages {
		messages[index], err = NewUnconfirmedMessage(fmt.Sprintf("message-%d", index), int64(index+1), payload)
		if err != nil {
			b.Fatal(err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		state, err := NewDurableQueueState(reliableDeliveryBenchmarkWindow, 0, messages)
		if err != nil {
			b.Fatal(err)
		}

		_ = state.Unconfirmed()
	}
}

func TestNewStoredFromState(t *testing.T) {
	endpoint, controller, _ := reliableProtocolPIDs()
	remoteEndpoint := reliableProtocolPID("remote-endpoint", 9010)
	remoteEndpoint.setState(remoteState, true)

	t.Run("With valid inputs", func(t *testing.T) {
		stored, err := newStoredFromState("session-1", "token-1", "message-1", 1, endpoint, controller)
		require.NoError(t, err)
		assert.Equal(t, "session-1", stored.SessionID())
		assert.Equal(t, "token-1", stored.Token())
		assert.Equal(t, "message-1", stored.MessageID())
		assert.EqualValues(t, 1, stored.Seq())
	})

	t.Run("With blank message ID", func(t *testing.T) {
		_, err := newStoredFromState("session-1", "token-1", " ", 1, endpoint, controller)
		assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)
	})

	t.Run("With non-positive sequence", func(t *testing.T) {
		_, err := newStoredFromState("session-1", "token-1", "message-1", 0, endpoint, controller)
		assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)
	})

	t.Run("With remote ownership", func(t *testing.T) {
		_, err := newStoredFromState("session-1", "token-1", "message-1", 1, remoteEndpoint, controller)
		assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)
	})
}

func TestReliableControllerRoleHelpers(t *testing.T) {
	assert.Equal(t, "producer", ReliableControllerRoleProducer.String())
	assert.Equal(t, "consumer", ReliableControllerRoleConsumer.String())
	assert.Equal(t, "unknown", reliableControllerRoleUnknown.String())

	assert.Equal(t, internalpb.ReliableControllerRole_RELIABLE_CONTROLLER_ROLE_PRODUCER, ReliableControllerRoleProducer.toProto())
	assert.Equal(t, internalpb.ReliableControllerRole_RELIABLE_CONTROLLER_ROLE_CONSUMER, ReliableControllerRoleConsumer.toProto())
	assert.Equal(t, internalpb.ReliableControllerRole_RELIABLE_CONTROLLER_ROLE_UNSPECIFIED, reliableControllerRoleUnknown.toProto())
}

func TestReliableDeliveryStageString(t *testing.T) {
	assert.Equal(t, "load", ReliableDeliveryStageLoad.String())
	assert.Equal(t, "store", ReliableDeliveryStageStore.String())
	assert.Equal(t, "accept", ReliableDeliveryStageAccept.String())
	assert.Equal(t, "confirm", ReliableDeliveryStageConfirm.String())
	assert.Equal(t, "protocol", ReliableDeliveryStageProtocol.String())
	assert.Equal(t, "unknown", reliableDeliveryStageUnknown.String())
	assert.Equal(t, "unknown", ReliableDeliveryStage(200).String())

	// the stage renders by name wherever a failure event is formatted
	assert.Equal(t, "stage=protocol", fmt.Sprintf("stage=%s", ReliableDeliveryStageProtocol))
}

type reliableProtocolMessage struct {
	value string
}

// reliableProtocolPIDs creates distinct local PIDs for authorization tests.
func reliableProtocolPIDs() (endpoint, controller, other *PID) {
	return reliableProtocolPID("endpoint", 9000),
		reliableProtocolPID("controller", 9001),
		reliableProtocolPID("other", 9002)
}

// reliableProtocolPID creates a local PID without starting an actor system.
func reliableProtocolPID(name string, port int) *PID {
	addr := address.New(name, "reliable-protocol", "127.0.0.1", port)
	return &PID{
		address: addr,
		path:    newPath(addr),
	}
}
