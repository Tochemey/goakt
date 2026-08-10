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
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// deliveryForward commands a test double to send a message so that the
// double becomes the sender.
type deliveryForward struct {
	to      *PID
	message any
}

// getRecorded asks a test double for a snapshot of its recorded messages.
type getRecorded struct{}

// getDeliveries asks the consumer mock for a snapshot of its deliveries.
type getDeliveries struct{}

// deliveryRecorder is a test double that records every message it receives,
// forwards commanded sends, and answers snapshot queries. All state lives in
// the actor and is only touched inside its own mailbox turns.
type deliveryRecorder struct {
	messages []any
}

func (x *deliveryRecorder) PreStart(*Context) error { return nil }
func (x *deliveryRecorder) PostStop(*Context) error { return nil }

func (x *deliveryRecorder) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *PostStart:
	case *deliveryForward:
		ctx.Tell(msg.to, msg.message)
	case *getRecorded:
		ctx.Response(append([]any(nil), x.messages...))
	default:
		x.messages = append(x.messages, msg)
	}
}

// reliableConsumerMock records deliveries and optionally confirms them
// immediately, mimicking an idempotent consumer endpoint. Its state is only
// touched inside its own mailbox turns; tests query snapshots.
type reliableConsumerMock struct {
	autoConfirm bool
	deliveries  []*Delivery
}

func (x *reliableConsumerMock) PreStart(*Context) error { return nil }
func (x *reliableConsumerMock) PostStop(*Context) error { return nil }

func (x *reliableConsumerMock) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *Delivery:
		x.deliveries = append(x.deliveries, msg)

		if x.autoConfirm {
			confirmed, err := NewConfirmed(msg)
			if err != nil {
				ctx.Err(err)
				return
			}

			ctx.Tell(ctx.Sender(), confirmed)
		}
	case *deliveryForward:
		ctx.Tell(msg.to, msg.message)
	case *getDeliveries:
		ctx.Response(append([]*Delivery(nil), x.deliveries...))
	default:
		ctx.Unhandled()
	}
}

// consumerSettings builds the consumer-side configuration the controller
// constructor consumes, keeping test call sites compact.
func consumerSettings(producerName string, window int, resendInterval time.Duration) *reliableConsumerConfig {
	return &reliableConsumerConfig{
		producerName:      producerName,
		flowControlWindow: window,
		resendInterval:    resendInterval,
	}
}

// consumerControllerHarness wires a consumer controller under test to a recording producer
// controller stand-in and a mock consumer endpoint.
type consumerControllerHarness struct {
	ctx                       context.Context
	system                    *actorSystem
	producerControllerStandIn *PID
	consumer                  *PID
	consumerController        *PID
}

// newConsumerControllerHarness starts a cluster-disabled system with a
// producer endpoint, its recording controller stand-in, a mock consumer, and
// the consumer controller under test.
func newConsumerControllerHarness(t *testing.T, window int, resendInterval time.Duration, autoConfirm bool) *consumerControllerHarness {
	t.Helper()

	ctx, system := newCompanionTestSystem(t)

	producer, err := system.Spawn(ctx, "producer", NewMockActor())
	require.NoError(t, err)

	spec, err := newReliableCompanionSpec(ReliableControllerRoleProducer, "producer", producer.IncarnationID())
	require.NoError(t, err)

	producerControllerName := reliableCompanionName(ReliableControllerRoleProducer, producer.IncarnationID())
	producerControllerStandIn, err := system.Spawn(ctx, producerControllerName, &deliveryRecorder{}, asSystem(), asReliableCompanion(spec))
	require.NoError(t, err)

	consumer, err := system.Spawn(ctx, "consumer", &reliableConsumerMock{autoConfirm: autoConfirm})
	require.NoError(t, err)

	controller := newConsumerController(consumer, consumerSettings("producer", window, resendInterval))
	consumerController, err := system.Spawn(ctx, "consumer-controller", controller)
	require.NoError(t, err)

	return &consumerControllerHarness{
		ctx:                       ctx,
		system:                    system,
		producerControllerStandIn: producerControllerStandIn,
		consumer:                  consumer,
		consumerController:        consumerController,
	}
}

// producerControllerRecorded asks the producer controller stand-in for a message snapshot.
func (x *consumerControllerHarness) producerControllerRecorded() []any {
	response, err := Ask(x.ctx, x.producerControllerStandIn, &getRecorded{}, time.Second)
	if err != nil {
		return nil
	}

	snapshot, _ := response.([]any)
	return snapshot
}

// deliveries asks the consumer mock for its delivery snapshot.
func (x *consumerControllerHarness) deliveries() []*Delivery {
	response, err := Ask(x.ctx, x.consumer, &getDeliveries{}, time.Second)
	if err != nil {
		return nil
	}

	snapshot, _ := response.([]*Delivery)
	return snapshot
}

// latestRegistration waits for and returns the latest RegisterConsumer
// received by the producer controller stand-in.
func (x *consumerControllerHarness) latestRegistration(t *testing.T) *commands.RegisterConsumer {
	t.Helper()

	var latest *commands.RegisterConsumer

	require.Eventually(t, func() bool {
		for _, message := range x.producerControllerRecorded() {
			if register, ok := message.(*commands.RegisterConsumer); ok {
				latest = register
			}
		}
		return latest != nil
	}, 3*time.Second, 10*time.Millisecond)

	return latest
}

// fromProducerController sends a protocol message to the consumer controller with the
// producer controller stand-in as sender.
func (x *consumerControllerHarness) fromProducerController(t *testing.T, message any) {
	t.Helper()
	require.NoError(t, Tell(x.ctx, x.producerControllerStandIn, &deliveryForward{to: x.consumerController, message: message}))
}

// adopt completes a registration handshake for the given session and returns
// the nonce of the acknowledged registration. It re-acks the latest
// registration on every poll because a silent controller keeps re-registering
// with fresh nonces.
func (x *consumerControllerHarness) adopt(t *testing.T, sessionID string, nextSeq int64) string {
	t.Helper()

	var nonce string

	require.Eventually(t, func() bool {
		var register *commands.RegisterConsumer

		for _, message := range x.producerControllerRecorded() {
			if candidate, ok := message.(*commands.RegisterConsumer); ok {
				register = candidate
			}
		}

		if register == nil {
			return false
		}

		nonce = register.Nonce()
		ack, err := commands.NewRegistrationAck(sessionID, nextSeq, nonce)
		if err != nil {
			return false
		}

		if Tell(x.ctx, x.producerControllerStandIn, &deliveryForward{to: x.consumerController, message: ack}) != nil {
			return false
		}

		for _, request := range x.requests() {
			if request.SessionID() == sessionID {
				return true
			}
		}

		return false
	}, 5*time.Second, 20*time.Millisecond)

	return nonce
}

// sequenced builds a SequencedMessage carrying an encoded test payload.
func (x *consumerControllerHarness) sequenced(t *testing.T, sessionID string, seq int64) *commands.SequencedMessage {
	t.Helper()

	payload := &testpb.Reply{Content: fmt.Sprintf("message-%d", seq)}
	frame, err := x.system.getRemoting().Serializer(payload).Serialize(payload)
	require.NoError(t, err)

	message, err := commands.NewSequencedMessage(sessionID, fmt.Sprintf("id-%d", seq), seq, frame)
	require.NoError(t, err)
	return message
}

// chunk builds a chunked SequencedMessage carrying one part of a frame.
func (x *consumerControllerHarness) chunk(t *testing.T, sessionID, messageID string, seq int64, part []byte, first, last bool) *commands.SequencedMessage {
	t.Helper()

	message, err := commands.NewChunkedSequencedMessage(sessionID, messageID, seq, part, first, last)
	require.NoError(t, err)
	return message
}

// encodeReply serializes one test payload the way the producer controller
// would before splitting it into chunks.
func (x *consumerControllerHarness) encodeReply(t *testing.T, content string) []byte {
	t.Helper()

	payload := &testpb.Reply{Content: content}
	frame, err := x.system.getRemoting().Serializer(payload).Serialize(payload)
	require.NoError(t, err)
	return frame
}

// requests returns the Requests recorded by the producer controller stand-in.
func (x *consumerControllerHarness) requests() []*commands.Request {
	var requests []*commands.Request

	for _, message := range x.producerControllerRecorded() {
		if request, ok := message.(*commands.Request); ok {
			requests = append(requests, request)
		}
	}

	return requests
}

// acks returns the Acks recorded by the producer controller stand-in.
func (x *consumerControllerHarness) acks() []*commands.Ack {
	var acks []*commands.Ack

	for _, message := range x.producerControllerRecorded() {
		if ack, ok := message.(*commands.Ack); ok {
			acks = append(acks, ack)
		}
	}

	return acks
}

func TestConsumerControllerNoFaultOrdering(t *testing.T) {
	harness := newConsumerControllerHarness(t, 6, 200*time.Millisecond, true)
	harness.adopt(t, "s1", 1)

	initial := harness.requests()[0]
	assert.Equal(t, int64(0), initial.ConfirmedSeq())
	assert.Equal(t, int64(6), initial.RequestUpToSeq())
	assert.True(t, initial.ViaTimeout())

	for seq := int64(1); seq <= 3; seq++ {
		harness.fromProducerController(t, harness.sequenced(t, "s1", seq))
	}

	require.Eventually(t, func() bool {
		return len(harness.deliveries()) == 3
	}, 3*time.Second, 10*time.Millisecond)

	deliveries := harness.deliveries()

	for index, delivery := range deliveries {
		seq := int64(index + 1)
		assert.Equal(t, "s1", delivery.SessionID())
		assert.Equal(t, seq, delivery.Seq())
		assert.Equal(t, fmt.Sprintf("id-%d", seq), delivery.MessageID())

		reply, ok := delivery.Payload().(*testpb.Reply)
		require.True(t, ok)
		assert.Equal(t, fmt.Sprintf("message-%d", seq), reply.GetContent())
	}

	// confirming seq 3 consumes half the window: expect the top-up Request
	require.Eventually(t, func() bool {
		for _, request := range harness.requests() {
			if !request.ViaTimeout() && request.ConfirmedSeq() == 3 && request.RequestUpToSeq() == 9 {
				return true
			}
		}
		return false
	}, 3*time.Second, 10*time.Millisecond)

	// a single further message drains the stream: expect the idle Ack
	harness.fromProducerController(t, harness.sequenced(t, "s1", 4))

	require.Eventually(t, func() bool {
		for _, ack := range harness.acks() {
			if ack.ConfirmedSeq() == 4 {
				return true
			}
		}
		return false
	}, 3*time.Second, 10*time.Millisecond)
}

func TestConsumerControllerDuplicateRecovery(t *testing.T) {
	harness := newConsumerControllerHarness(t, 6, 200*time.Millisecond, true)
	harness.adopt(t, "s1", 1)

	harness.fromProducerController(t, harness.sequenced(t, "s1", 1))

	require.Eventually(t, func() bool {
		for _, ack := range harness.acks() {
			if ack.ConfirmedSeq() == 1 {
				return true
			}
		}
		return false
	}, 3*time.Second, 10*time.Millisecond)

	// a duplicate below expectedSeq is re-acked without redelivery
	confirmedAcks := len(harness.acks())
	harness.fromProducerController(t, harness.sequenced(t, "s1", 1))

	require.Eventually(t, func() bool {
		return len(harness.acks()) > confirmedAcks
	}, 3*time.Second, 10*time.Millisecond)

	assert.Len(t, harness.deliveries(), 1)
}

func TestConsumerControllerLostDeliveryAndConfirmed(t *testing.T) {
	harness := newConsumerControllerHarness(t, 6, 150*time.Millisecond, false)
	harness.adopt(t, "s1", 1)

	// an in-flight duplicate is dropped, and the unconfirmed delivery is
	// retried on the tick, permitting duplicate business processing
	harness.fromProducerController(t, harness.sequenced(t, "s1", 1))
	harness.fromProducerController(t, harness.sequenced(t, "s1", 1))

	require.Eventually(t, func() bool {
		return len(harness.deliveries()) >= 2
	}, 3*time.Second, 10*time.Millisecond)

	deliveries := harness.deliveries()
	assert.Equal(t, deliveries[0].MessageID(), deliveries[1].MessageID())
	assert.Equal(t, deliveries[0].Seq(), deliveries[1].Seq())

	// a stale Confirmed from the bound consumer is dropped
	require.NoError(t, Tell(harness.ctx, harness.consumer, &deliveryForward{to: harness.consumerController, message: &Confirmed{}}))

	// the true business confirmation ends the retry loop
	confirmed, err := NewConfirmed(deliveries[0])
	require.NoError(t, err)
	require.NoError(t, Tell(harness.ctx, harness.consumer, &deliveryForward{to: harness.consumerController, message: confirmed}))

	require.Eventually(t, func() bool {
		for _, ack := range harness.acks() {
			if ack.ConfirmedSeq() == 1 {
				return true
			}
		}
		return false
	}, 3*time.Second, 10*time.Millisecond)
}

func TestConsumerControllerGapRecovery(t *testing.T) {
	harness := newConsumerControllerHarness(t, 6, 200*time.Millisecond, true)
	harness.adopt(t, "s1", 1)

	// seq 2 arrives before seq 1: buffered, gap Request sent
	harness.fromProducerController(t, harness.sequenced(t, "s1", 2))

	require.Eventually(t, func() bool {
		timeoutRequests := 0
		for _, request := range harness.requests() {
			if request.ViaTimeout() {
				timeoutRequests++
			}
		}
		return timeoutRequests >= 2
	}, 3*time.Second, 10*time.Millisecond)

	assert.Empty(t, harness.deliveries())

	// the missing sequence closes the gap and both deliver in order
	harness.fromProducerController(t, harness.sequenced(t, "s1", 1))

	require.Eventually(t, func() bool {
		return len(harness.deliveries()) == 2
	}, 3*time.Second, 10*time.Millisecond)

	deliveries := harness.deliveries()
	assert.Equal(t, int64(1), deliveries[0].Seq())
	assert.Equal(t, int64(2), deliveries[1].Seq())
}

func TestConsumerControllerSequenceBounds(t *testing.T) {
	harness := newConsumerControllerHarness(t, 3, 200*time.Millisecond, true)
	harness.adopt(t, "s1", 1)

	// beyond the granted window: dropped
	harness.fromProducerController(t, harness.sequenced(t, "s1", 10))
	// stale session: dropped
	harness.fromProducerController(t, harness.sequenced(t, "s2", 1))

	pause.For(300 * time.Millisecond)
	assert.Empty(t, harness.deliveries())

	// the in-window sequence still flows
	harness.fromProducerController(t, harness.sequenced(t, "s1", 1))

	require.Eventually(t, func() bool {
		return len(harness.deliveries()) == 1
	}, 3*time.Second, 10*time.Millisecond)
}

func TestConsumerControllerRestartResync(t *testing.T) {
	harness := newConsumerControllerHarness(t, 6, 150*time.Millisecond, true)
	firstNonce := harness.adopt(t, "s1", 1)

	harness.fromProducerController(t, harness.sequenced(t, "s1", 1))

	require.Eventually(t, func() bool {
		return len(harness.deliveries()) == 1
	}, 3*time.Second, 10*time.Millisecond)

	require.NoError(t, harness.consumerController.Restart(harness.ctx))

	// the fresh incarnation registers with a fresh nonce
	require.Eventually(t, func() bool {
		register := harness.latestRegistration(t)
		return register.Nonce() != firstNonce
	}, 3*time.Second, 10*time.Millisecond)

	// traffic from the old session is dropped until adoption
	harness.fromProducerController(t, harness.sequenced(t, "s1", 2))
	pause.For(200 * time.Millisecond)
	assert.Len(t, harness.deliveries(), 1)

	// adopting the new session resumes delivery
	harness.adopt(t, "s2", 2)
	harness.fromProducerController(t, harness.sequenced(t, "s2", 2))

	require.Eventually(t, func() bool {
		return len(harness.deliveries()) == 2
	}, 3*time.Second, 10*time.Millisecond)

	assert.Equal(t, int64(2), harness.deliveries()[1].Seq())
}

func TestConsumerControllerConsumerTerminated(t *testing.T) {
	harness := newConsumerControllerHarness(t, 6, 200*time.Millisecond, true)
	harness.adopt(t, "s1", 1)

	require.NoError(t, harness.consumer.Shutdown(harness.ctx))

	require.Eventually(t, func() bool {
		return !harness.consumerController.IsRunning()
	}, 3*time.Second, 10*time.Millisecond)
}

func TestConsumerControllerProtocolDrops(t *testing.T) {
	harness := newConsumerControllerHarness(t, 6, 200*time.Millisecond, true)
	nonce := harness.adopt(t, "s1", 1)

	t.Run("With RegistrationAck from unexpected sender", func(t *testing.T) {
		ack, err := commands.NewRegistrationAck("s1", 1, nonce)
		require.NoError(t, err)
		require.NoError(t, Tell(harness.ctx, harness.consumer, &deliveryForward{to: harness.consumerController, message: ack}))
		pause.For(150 * time.Millisecond)
		assert.True(t, harness.consumerController.IsRunning())
	})

	t.Run("With RegistrationAck stale nonce", func(t *testing.T) {
		ack, err := commands.NewRegistrationAck("s1", 1, uuid.NewString())
		require.NoError(t, err)
		harness.fromProducerController(t, ack)
		pause.For(150 * time.Millisecond)
		assert.True(t, harness.consumerController.IsRunning())
	})

	t.Run("With SequencedMessage from unexpected sender", func(t *testing.T) {
		require.NoError(t, Tell(harness.ctx, harness.consumer, &deliveryForward{
			to:      harness.consumerController,
			message: harness.sequenced(t, "s1", 1),
		}))
		pause.For(150 * time.Millisecond)
		assert.Empty(t, harness.deliveries())
	})

	t.Run("With Confirmed from unexpected sender", func(t *testing.T) {
		harness.fromProducerController(t, &Confirmed{
			sessionID: "s1",
			messageID: "id-1",
			seq:       1,
		})
		pause.For(150 * time.Millisecond)
		assert.True(t, harness.consumerController.IsRunning())
	})

	t.Run("With unhandled message", func(t *testing.T) {
		require.NoError(t, Tell(harness.ctx, harness.consumerController, &testpb.Reply{Content: "noise"}))
		pause.For(150 * time.Millisecond)
		assert.True(t, harness.consumerController.IsRunning())
	})
}

func TestConsumerControllerDecodeFailure(t *testing.T) {
	harness := newConsumerControllerHarness(t, 6, 200*time.Millisecond, true)
	harness.adopt(t, "s1", 1)

	subscriber, err := harness.system.Subscribe()
	require.NoError(t, err)

	// a frame the remoting layer cannot decode is a terminal serializer asymmetry
	bad, err := commands.NewSequencedMessage("s1", "id-1", 1, []byte("not-a-serialized-frame"))
	require.NoError(t, err)
	harness.fromProducerController(t, bad)

	failure := awaitFailure(t, subscriber)
	assert.Equal(t, "consumer", failure.EndpointName())
	assert.Equal(t, ReliableControllerRoleConsumer, failure.ControllerRole())
	assert.Equal(t, ReliableDeliveryStageProtocol, failure.Stage())
	assert.ErrorContains(t, failure.Err(), "failed to decode")

	require.Eventually(t, func() bool {
		return !harness.consumerController.IsRunning()
	}, 3*time.Second, 10*time.Millisecond)
}

func TestConsumerControllerProducerControllerTerminated(t *testing.T) {
	harness := newConsumerControllerHarness(t, 6, 150*time.Millisecond, true)
	harness.adopt(t, "s1", 1)

	require.NoError(t, harness.producerControllerStandIn.Shutdown(harness.ctx))

	// silence after the peer dies triggers re-registration attempts against the
	// missing companion until a replacement appears
	require.Eventually(t, func() bool {
		return !harness.producerControllerStandIn.IsRunning()
	}, 3*time.Second, 10*time.Millisecond)

	pause.For(400 * time.Millisecond)
	assert.True(t, harness.consumerController.IsRunning())
}

func TestConsumerControllerEdgeBranches(t *testing.T) {
	// the controller under test is never spawned: its handlers run on the
	// test goroutine with stand-in PIDs, so no actor turn touches its state
	ctx, system := newCompanionTestSystem(t)

	consumer, err := system.Spawn(ctx, "consumer", &reliableConsumerMock{})
	require.NoError(t, err)

	producer, err := system.Spawn(ctx, "producer", NewMockActor())
	require.NoError(t, err)

	spec, err := newReliableCompanionSpec(ReliableControllerRoleProducer, "producer", producer.IncarnationID())
	require.NoError(t, err)

	producerControllerName := reliableCompanionName(ReliableControllerRoleProducer, producer.IncarnationID())
	producerControllerStandIn, err := system.Spawn(ctx, producerControllerName, &deliveryRecorder{}, asSystem(), asReliableCompanion(spec))
	require.NoError(t, err)

	spawnControllerHost := func(t *testing.T, name string) *PID {
		t.Helper()
		host, err := system.Spawn(ctx, name, &deliveryRecorder{})
		require.NoError(t, err)
		return host
	}

	t.Run("With PreStart validation", func(t *testing.T) {
		assert.ErrorContains(t, newConsumerController(nil, consumerSettings("producer", 1, time.Millisecond)).PreStart(nil), "bound local consumer")
		assert.ErrorContains(t, newConsumerController(newRemotePID(address.New("remote", "sys", "127.0.0.1", 1), nil), consumerSettings("producer", 1, time.Millisecond)).PreStart(nil), "bound local consumer")
		assert.ErrorContains(t, newConsumerController(consumer, consumerSettings("", 1, time.Millisecond)).PreStart(nil), "producer endpoint name")
		assert.ErrorContains(t, newConsumerController(consumer, consumerSettings("producer", 0, time.Millisecond)).PreStart(nil), "valid flow control window")
		assert.ErrorContains(t, newConsumerController(consumer, consumerSettings("producer", MaxReliableFlowControlWindow+1, time.Millisecond)).PreStart(nil), "valid flow control window")
		assert.ErrorContains(t, newConsumerController(consumer, consumerSettings("producer", 1, 0)).PreStart(nil), "positive resend interval")
	})

	t.Run("With stale tick generation", func(t *testing.T) {
		host := spawnControllerHost(t, "host-stale-tick")
		controller := newConsumerController(consumer, consumerSettings("producer", 2, time.Hour))
		require.NoError(t, controller.PreStart(nil))
		controller.sawValidTraffic = true

		stale := &consumerControllerTick{generation: controller.generation.Load() + 1}
		rctx := newReceiveContext(context.Background(), system.NoSender(), host, stale)
		controller.handleTick(rctx, stale)
		assert.True(t, controller.sawValidTraffic)
	})

	t.Run("With gap recovery on tick", func(t *testing.T) {
		host := spawnControllerHost(t, "host-gap-tick")
		controller := newConsumerController(consumer, consumerSettings("producer", 6, time.Millisecond))
		require.NoError(t, controller.PreStart(nil))
		controller.producerController = producerControllerStandIn
		controller.sessionID = "s1"
		controller.registrationNonce = uuid.NewString()
		controller.expectedSeq = 1
		controller.confirmedSeq = 0
		controller.requestUpToSeq = 6
		controller.sawValidTraffic = true

		three, err := commands.NewSequencedMessage("s1", "id-3", 3, []byte("frame"))
		require.NoError(t, err)
		controller.buffer = []*commands.SequencedMessage{three}

		tick := &consumerControllerTick{generation: controller.generation.Load()}
		rctx := newReceiveContext(context.Background(), system.NoSender(), host, tick)
		controller.handleTick(rctx, tick)

		assert.False(t, controller.sawValidTraffic)
		assert.False(t, controller.lastGapRequest.IsZero())
	})

	t.Run("With full receive buffer", func(t *testing.T) {
		host := spawnControllerHost(t, "host-full-buffer")
		controller := newConsumerController(consumer, consumerSettings("producer", 2, time.Hour))
		require.NoError(t, controller.PreStart(nil))
		controller.producerController = producerControllerStandIn
		controller.sessionID = "s1"
		controller.registrationNonce = uuid.NewString()
		controller.expectedSeq = 1
		controller.confirmedSeq = 0
		controller.requestUpToSeq = 100

		three, err := commands.NewSequencedMessage("s1", "id-3", 3, []byte("frame"))
		require.NoError(t, err)
		four, err := commands.NewSequencedMessage("s1", "id-4", 4, []byte("frame"))
		require.NoError(t, err)
		controller.buffer = []*commands.SequencedMessage{three, four}

		five, err := commands.NewSequencedMessage("s1", "id-5", 5, []byte("frame"))
		require.NoError(t, err)
		rctx := newReceiveContext(context.Background(), producerControllerStandIn, host, five)
		controller.handleSequencedMessage(rctx, five)

		require.Len(t, controller.buffer, 2)
		assert.Equal(t, int64(3), controller.buffer[0].Seq())
		assert.Equal(t, int64(4), controller.buffer[1].Seq())

		// a duplicate of a buffered sequence leaves the buffer unchanged;
		// the gap request above shrank the demand window, so restore it
		controller.requestUpToSeq = 100
		duplicate, err := commands.NewSequencedMessage("s1", "id-3", 3, []byte("frame"))
		require.NoError(t, err)
		rctx = newReceiveContext(context.Background(), producerControllerStandIn, host, duplicate)
		controller.handleSequencedMessage(rctx, duplicate)
		require.Len(t, controller.buffer, 2)
	})

	t.Run("With producer controller terminated", func(t *testing.T) {
		host := spawnControllerHost(t, "host-producer-controller-terminated")
		controller := newConsumerController(consumer, consumerSettings("producer", 2, time.Hour))
		require.NoError(t, controller.PreStart(nil))
		controller.producerController = producerControllerStandIn
		controller.sessionID = "s1"
		controller.registrationNonce = uuid.NewString()

		terminated := NewTerminated(producerControllerStandIn.Path())
		rctx := newReceiveContext(context.Background(), system.NoSender(), host, terminated)
		controller.handleTerminated(rctx, terminated)

		assert.Nil(t, controller.producerController)
		assert.Empty(t, controller.sessionID)
		assert.Empty(t, controller.registrationNonce)
	})

	t.Run("With register resolve failure", func(t *testing.T) {
		host := spawnControllerHost(t, "host-register-miss")
		controller := newConsumerController(consumer, consumerSettings("missing-producer", 2, time.Hour))
		require.NoError(t, controller.PreStart(nil))

		rctx := newReceiveContext(context.Background(), system.NoSender(), host, &PostStart{})
		controller.register(rctx)
		assert.Nil(t, controller.producerController)
	})

	t.Run("With purgeBuffer removing stale entries", func(t *testing.T) {
		controller := newConsumerController(consumer, consumerSettings("producer", 6, time.Hour))
		require.NoError(t, controller.PreStart(nil))
		controller.expectedSeq = 3

		one, err := commands.NewSequencedMessage("s1", "id-1", 1, []byte("frame"))
		require.NoError(t, err)
		two, err := commands.NewSequencedMessage("s1", "id-2", 2, []byte("frame"))
		require.NoError(t, err)
		four, err := commands.NewSequencedMessage("s1", "id-4", 4, []byte("frame"))
		require.NoError(t, err)
		controller.buffer = []*commands.SequencedMessage{one, two, four}

		controller.purgeBuffer()
		require.Len(t, controller.buffer, 1)
		assert.Equal(t, int64(4), controller.buffer[0].Seq())
	})

	t.Run("With sendRequest and sendAck guards", func(t *testing.T) {
		host := spawnControllerHost(t, "host-request-ack-guards")
		controller := newConsumerController(consumer, consumerSettings("producer", 6, time.Hour))
		require.NoError(t, controller.PreStart(nil))

		rctx := newReceiveContext(context.Background(), system.NoSender(), host, &PostStart{})
		controller.sendRequest(rctx, false)
		controller.sendAck(rctx)
		assert.Zero(t, controller.requestUpToSeq)

		controller.producerController = producerControllerStandIn
		controller.sessionID = ""
		controller.sendRequest(rctx, false)
		controller.sendAck(rctx)
		assert.Zero(t, controller.requestUpToSeq)
	})

	t.Run("With impossible Request construction", func(t *testing.T) {
		host := spawnControllerHost(t, "host-bad-request")
		controller := newConsumerController(consumer, consumerSettings("producer", 6, time.Hour))
		require.NoError(t, controller.PreStart(nil))
		controller.producerController = producerControllerStandIn
		controller.sessionID = "s1"
		controller.registrationNonce = ""

		subscriber, err := system.Subscribe()
		require.NoError(t, err)

		rctx := newReceiveContext(context.Background(), system.NoSender(), host, &PostStart{})
		controller.sendRequest(rctx, false)

		failure := awaitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "failed to build Request")
	})

	t.Run("With impossible Ack construction", func(t *testing.T) {
		host := spawnControllerHost(t, "host-bad-ack")
		controller := newConsumerController(consumer, consumerSettings("producer", 6, time.Hour))
		require.NoError(t, controller.PreStart(nil))
		controller.producerController = producerControllerStandIn
		controller.sessionID = "s1"
		controller.registrationNonce = ""

		subscriber, err := system.Subscribe()
		require.NoError(t, err)

		rctx := newReceiveContext(context.Background(), system.NoSender(), host, &PostStart{})
		controller.sendAck(rctx)

		failure := awaitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "failed to build Ack")
	})

	t.Run("With Delivery ownership failure", func(t *testing.T) {
		host := spawnControllerHost(t, "host-delivery-ownership")
		controller := newConsumerController(consumer, consumerSettings("producer", 6, time.Hour))
		require.NoError(t, controller.PreStart(nil))
		// PreStart requires a local consumer; swap afterwards so newDelivery rejects ownership
		controller.consumer = newRemotePID(address.New("remote-consumer", "sys", "127.0.0.1", 1), nil)

		payload := &testpb.Reply{Content: "x"}
		frame, err := system.getRemoting().Serializer(payload).Serialize(payload)
		require.NoError(t, err)
		msg, err := commands.NewSequencedMessage("s1", "id-1", 1, frame)
		require.NoError(t, err)

		rctx := newReceiveContext(context.Background(), system.NoSender(), host, msg)
		controller.deliver(rctx, msg)
		assert.True(t, controller.failed)
	})

	t.Run("With fail already published", func(t *testing.T) {
		host := spawnControllerHost(t, "host-fail-once")
		controller := newConsumerController(consumer, consumerSettings("producer", 6, time.Hour))
		require.NoError(t, controller.PreStart(nil))
		controller.failed = true

		rctx := newReceiveContext(context.Background(), system.NoSender(), host, &PostStart{})
		controller.fail(rctx, ReliableDeliveryStageProtocol, errors.New("ignored"))
		assert.True(t, host.IsRunning())
	})

	t.Run("With fail without event stream", func(t *testing.T) {
		host := spawnControllerHost(t, "host-fail-silent")
		// a synthetic local PID keeps eventsStream nil without racing a live
		// actor's turns; spawning and nilling the field on a running PID races
		// with Unhandled reads on the dispatcher goroutine
		consumerAddr := address.New("no-stream-consumer", system.Name(), "127.0.0.1", 1)
		consumerWithoutStream := &PID{address: consumerAddr, path: newPath(consumerAddr)}

		controller := newConsumerController(consumerWithoutStream, consumerSettings("producer", 6, time.Hour))
		require.NoError(t, controller.PreStart(nil))

		rctx := newReceiveContext(context.Background(), system.NoSender(), host, &PostStart{})
		controller.fail(rctx, ReliableDeliveryStageProtocol, errors.New("silent"))
		assert.True(t, controller.failed)
	})

	t.Run("With tell to dead peer", func(t *testing.T) {
		host := spawnControllerHost(t, "host-tell-dead")
		controller := newConsumerController(consumer, consumerSettings("producer", 6, time.Hour))
		require.NoError(t, controller.PreStart(nil))

		dead, err := system.Spawn(ctx, "dead-peer", &deliveryRecorder{})
		require.NoError(t, err)
		require.NoError(t, dead.Shutdown(ctx))
		require.Eventually(t, func() bool { return !dead.IsRunning() }, 3*time.Second, 10*time.Millisecond)

		register, err := commands.NewRegisterConsumer(uuid.NewString())
		require.NoError(t, err)

		rctx := newReceiveContext(context.Background(), system.NoSender(), host, &PostStart{})
		controller.tell(rctx, dead, register)
	})
}

func TestConsumerControllerChunkedDelivery(t *testing.T) {
	t.Run("With an in-order run assembled into one delivery", func(t *testing.T) {
		harness := newConsumerControllerHarness(t, 10, time.Second, true)
		nonce := harness.adopt(t, "session-1", 1)

		frame := harness.encodeReply(t, "chunked-hello")
		third := len(frame) / 3
		harness.fromProducerController(t, harness.chunk(t, "session-1", "m-1", 1, frame[:third], true, false))
		harness.fromProducerController(t, harness.chunk(t, "session-1", "m-1", 2, frame[third:2*third], false, false))
		harness.fromProducerController(t, harness.chunk(t, "session-1", "m-1", 3, frame[2*third:], false, true))

		require.Eventually(t, func() bool {
			return len(harness.deliveries()) == 1
		}, 3*time.Second, 10*time.Millisecond)

		delivery := harness.deliveries()[0]
		assert.Equal(t, "m-1", delivery.MessageID())
		assert.EqualValues(t, 3, delivery.Seq())

		reply, ok := delivery.Payload().(*testpb.Reply)
		require.True(t, ok)
		assert.Equal(t, "chunked-hello", reply.GetContent())

		// cumulative confirmation covers the interior chunk sequences
		require.Eventually(t, func() bool {
			for _, request := range harness.requests() {
				if request.ConfirmedSeq() == 3 {
					return true
				}
			}
			for _, ack := range harness.acks() {
				if ack.ConfirmedSeq() == 3 {
					return true
				}
			}
			return false
		}, 3*time.Second, 10*time.Millisecond)

		_ = nonce
	})

	t.Run("With an interior chunk missing recovered by a gap request", func(t *testing.T) {
		harness := newConsumerControllerHarness(t, 10, 150*time.Millisecond, true)
		harness.adopt(t, "session-1", 1)

		frame := harness.encodeReply(t, "gap-recovery")
		half := len(frame) / 2
		before := len(harness.requests())

		harness.fromProducerController(t, harness.chunk(t, "session-1", "m-1", 1, frame[:half], true, false))
		harness.fromProducerController(t, harness.chunk(t, "session-1", "m-1", 3, []byte("tail"), false, true))

		// the incomplete run opens a gap: a timeout request asks for resend
		require.Eventually(t, func() bool {
			for _, request := range harness.requests()[before:] {
				if request.ViaTimeout() {
					return true
				}
			}
			return false
		}, 3*time.Second, 10*time.Millisecond)

		require.Empty(t, harness.deliveries())

		harness.fromProducerController(t, harness.chunk(t, "session-1", "m-1", 2, frame[half:], false, false))

		require.Eventually(t, func() bool {
			return len(harness.deliveries()) == 1
		}, 3*time.Second, 10*time.Millisecond)

		reply, ok := harness.deliveries()[0].Payload().(*testpb.Reply)
		require.True(t, ok)
		assert.Equal(t, "gap-recovery", reply.GetContent())
	})

	t.Run("With a full buffer dropping a chunk recovered by resend", func(t *testing.T) {
		harness := newConsumerControllerHarness(t, 3, 150*time.Millisecond, false)
		harness.adopt(t, "session-1", 1)

		frameA := harness.encodeReply(t, "message-a")
		halfA := len(frameA) / 2
		harness.fromProducerController(t, harness.chunk(t, "session-1", "m-a", 1, frameA[:halfA], true, false))
		harness.fromProducerController(t, harness.chunk(t, "session-1", "m-a", 2, frameA[halfA:], false, true))

		require.Eventually(t, func() bool {
			return len(harness.deliveries()) == 1
		}, 3*time.Second, 10*time.Millisecond)

		// the assembled message is in flight, so its two chunks still occupy
		// the buffer; the next message's first chunk fills it and the second
		// one is dropped. While the first message is in flight that incomplete
		// run is not yet the head the controller can deliver, so no gap
		// request fires here.
		frameB := harness.encodeReply(t, "message-b")
		halfB := len(frameB) / 2
		harness.fromProducerController(t, harness.chunk(t, "session-1", "m-b", 3, frameB[:halfB], true, false))
		harness.fromProducerController(t, harness.chunk(t, "session-1", "m-b", 4, frameB[halfB:], false, true))

		beforeConfirm := len(harness.requests())

		confirmed, err := NewConfirmed(harness.deliveries()[0])
		require.NoError(t, err)
		require.NoError(t, Tell(harness.ctx, harness.consumer, &deliveryForward{to: harness.consumerController, message: confirmed}))

		// confirmation purges the first run and uncovers the incomplete head;
		// the controller must solicit a timeout resend itself. A non-timeout
		// top-up from batching is not enough: ordinary Requests never resend.
		var gapRequest *commands.Request

		require.Eventually(t, func() bool {
			for _, request := range harness.requests()[beforeConfirm:] {
				if request.ViaTimeout() && request.ConfirmedSeq() == 2 {
					gapRequest = request
					return true
				}
			}
			return false
		}, 3*time.Second, 10*time.Millisecond)

		require.NotNil(t, gapRequest)
		assert.EqualValues(t, 5, gapRequest.RequestUpToSeq())
		require.Empty(t, harness.deliveries()[1:])

		// the producer's timeout resend delivers the dropped chunk and the
		// second message assembles
		harness.fromProducerController(t, harness.chunk(t, "session-1", "m-b", 3, frameB[:halfB], true, false))
		harness.fromProducerController(t, harness.chunk(t, "session-1", "m-b", 4, frameB[halfB:], false, true))

		require.Eventually(t, func() bool {
			return len(harness.deliveries()) == 2
		}, 3*time.Second, 10*time.Millisecond)

		delivery := harness.deliveries()[1]
		assert.Equal(t, "m-b", delivery.MessageID())
		assert.EqualValues(t, 4, delivery.Seq())
	})

	t.Run("With a run not starting at a first chunk is terminal", func(t *testing.T) {
		harness := newConsumerControllerHarness(t, 10, time.Second, true)
		harness.adopt(t, "session-1", 1)

		subscriber, err := harness.system.Subscribe()
		require.NoError(t, err)

		harness.fromProducerController(t, harness.chunk(t, "session-1", "m-1", 1, []byte("part"), false, true))

		failure := awaitFailure(t, subscriber)
		assert.Equal(t, ReliableDeliveryStageProtocol, failure.Stage())
		assert.Equal(t, ReliableControllerRoleConsumer, failure.ControllerRole())
		assert.ErrorContains(t, failure.Err(), "does not start with a first chunk")
	})

	t.Run("With a first chunk inside a run is terminal", func(t *testing.T) {
		harness := newConsumerControllerHarness(t, 10, time.Second, true)
		harness.adopt(t, "session-1", 1)

		subscriber, err := harness.system.Subscribe()
		require.NoError(t, err)

		harness.fromProducerController(t, harness.chunk(t, "session-1", "m-1", 1, []byte("head"), true, false))
		harness.fromProducerController(t, harness.chunk(t, "session-1", "m-1", 2, []byte("head-again"), true, true))

		failure := awaitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "unexpected first chunk")
	})

	t.Run("With a whole message interleaved into a run is terminal", func(t *testing.T) {
		harness := newConsumerControllerHarness(t, 10, time.Second, true)
		harness.adopt(t, "session-1", 1)

		subscriber, err := harness.system.Subscribe()
		require.NoError(t, err)

		harness.fromProducerController(t, harness.chunk(t, "session-1", "m-1", 1, []byte("head"), true, false))
		harness.fromProducerController(t, harness.sequenced(t, "session-1", 2))
		harness.fromProducerController(t, harness.chunk(t, "session-1", "m-1", 3, []byte("tail"), false, true))

		failure := awaitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "interleaved into the chunk run")
	})

	t.Run("With a whole message completing the run coverage is terminal without resend", func(t *testing.T) {
		harness := newConsumerControllerHarness(t, 10, time.Second, true)
		harness.adopt(t, "session-1", 1)

		subscriber, err := harness.system.Subscribe()
		require.NoError(t, err)

		// the last chunk arrives before the interleaved whole message, so the
		// entry closing the sequence coverage is the whole message itself:
		// assembly must run from that arrival, not wait for another chunk
		harness.fromProducerController(t, harness.chunk(t, "session-1", "m-1", 1, []byte("head"), true, false))
		harness.fromProducerController(t, harness.chunk(t, "session-1", "m-1", 3, []byte("tail"), false, true))
		harness.fromProducerController(t, harness.sequenced(t, "session-1", 2))

		failure := awaitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "interleaved into the chunk run")
	})

	t.Run("With a whole message occupying the run continuation and no last chunk is terminal on the tick", func(t *testing.T) {
		harness := newConsumerControllerHarness(t, 10, 200*time.Millisecond, true)
		harness.adopt(t, "session-1", 1)

		subscriber, err := harness.system.Subscribe()
		require.NoError(t, err)

		// no last chunk ever arrives for the head run, so sequence coverage
		// never completes and assembly never classifies the stream: only the
		// tick's gap rule can raise the violation instead of resending forever
		harness.fromProducerController(t, harness.chunk(t, "session-1", "m-1", 1, []byte("head"), true, false))
		harness.fromProducerController(t, harness.chunk(t, "session-1", "m-1", 2, []byte("mid"), false, false))
		harness.fromProducerController(t, harness.sequenced(t, "session-1", 3))

		failure := awaitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "interleaved into the chunk run")
	})

	t.Run("With a foreign chunk continuing the run and no last chunk is terminal on the tick", func(t *testing.T) {
		harness := newConsumerControllerHarness(t, 10, 200*time.Millisecond, true)
		harness.adopt(t, "session-1", 1)

		subscriber, err := harness.system.Subscribe()
		require.NoError(t, err)

		// the foreign interior chunk occupies the run's continuation and
		// neither run ever presents a last chunk, so only the tick's gap rule
		// can classify the violated stream
		harness.fromProducerController(t, harness.chunk(t, "session-1", "m-1", 1, []byte("head"), true, false))
		harness.fromProducerController(t, harness.chunk(t, "session-1", "m-2", 2, []byte("mid"), false, false))

		failure := awaitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "changed message ID")
	})

	t.Run("With a message ID change inside a run is terminal", func(t *testing.T) {
		harness := newConsumerControllerHarness(t, 10, time.Second, true)
		harness.adopt(t, "session-1", 1)

		subscriber, err := harness.system.Subscribe()
		require.NoError(t, err)

		harness.fromProducerController(t, harness.chunk(t, "session-1", "m-1", 1, []byte("head"), true, false))
		harness.fromProducerController(t, harness.chunk(t, "session-1", "m-2", 2, []byte("tail"), false, true))

		failure := awaitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "changed message ID")
	})

	t.Run("With an undecodable reassembled frame is terminal", func(t *testing.T) {
		harness := newConsumerControllerHarness(t, 10, time.Second, true)
		harness.adopt(t, "session-1", 1)

		subscriber, err := harness.system.Subscribe()
		require.NoError(t, err)

		harness.fromProducerController(t, harness.chunk(t, "session-1", "m-1", 1, []byte("gar"), true, false))
		harness.fromProducerController(t, harness.chunk(t, "session-1", "m-1", 2, []byte("bage"), false, true))

		failure := awaitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "failed to decode")
	})
}
