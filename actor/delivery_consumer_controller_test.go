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
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// ccHarness wires a consumer controller under test to a recording producer
// controller stand-in and a mock consumer endpoint.
type ccHarness struct {
	ctx      context.Context
	system   *actorSystem
	pc       *PID
	consumer *PID
	cc       *PID
}

// newConsumerControllerHarness starts a cluster-disabled system with a
// producer endpoint, its recording controller stand-in, a mock consumer, and
// the consumer controller under test.
func newConsumerControllerHarness(t *testing.T, window int, resendInterval time.Duration, autoConfirm bool) *ccHarness {
	t.Helper()

	ctx, system := newCompanionTestSystem(t)

	producer, err := system.Spawn(ctx, "producer", NewMockActor())
	require.NoError(t, err)

	spec, err := newReliableCompanionSpec(ReliableControllerRoleProducer, "producer", producer.IncarnationID())
	require.NoError(t, err)

	pcName := reliableCompanionName(ReliableControllerRoleProducer, producer.IncarnationID())
	pc, err := system.Spawn(ctx, pcName, &deliveryRecorder{}, asSystem(), asReliableCompanion(spec))
	require.NoError(t, err)

	consumer, err := system.Spawn(ctx, "consumer", &reliableConsumerMock{autoConfirm: autoConfirm})
	require.NoError(t, err)

	controller := newConsumerController(consumer, "producer", window, resendInterval)
	cc, err := system.Spawn(ctx, "consumer-controller", controller)
	require.NoError(t, err)

	return &ccHarness{
		ctx:      ctx,
		system:   system,
		pc:       pc,
		consumer: consumer,
		cc:       cc,
	}
}

// pcRecorded asks the producer controller stand-in for a message snapshot.
func (x *ccHarness) pcRecorded() []any {
	response, err := Ask(x.ctx, x.pc, &getRecorded{}, time.Second)
	if err != nil {
		return nil
	}

	snapshot, _ := response.([]any)
	return snapshot
}

// deliveries asks the consumer mock for its delivery snapshot.
func (x *ccHarness) deliveries() []*Delivery {
	response, err := Ask(x.ctx, x.consumer, &getDeliveries{}, time.Second)
	if err != nil {
		return nil
	}

	snapshot, _ := response.([]*Delivery)
	return snapshot
}

// latestRegistration waits for and returns the latest RegisterConsumer
// received by the producer controller stand-in.
func (x *ccHarness) latestRegistration(t *testing.T) *commands.RegisterConsumer {
	t.Helper()

	var latest *commands.RegisterConsumer

	require.Eventually(t, func() bool {
		for _, message := range x.pcRecorded() {
			if register, ok := message.(*commands.RegisterConsumer); ok {
				latest = register
			}
		}
		return latest != nil
	}, 3*time.Second, 10*time.Millisecond)

	return latest
}

// fromPC sends a protocol message to the consumer controller with the
// producer controller stand-in as sender.
func (x *ccHarness) fromPC(t *testing.T, message any) {
	t.Helper()
	require.NoError(t, Tell(x.ctx, x.pc, &deliveryForward{to: x.cc, message: message}))
}

// adopt completes a registration handshake for the given session and returns
// the nonce of the acknowledged registration. It re-acks the latest
// registration on every poll because a silent controller keeps re-registering
// with fresh nonces.
func (x *ccHarness) adopt(t *testing.T, sessionID string, nextSeq int64) string {
	t.Helper()

	var nonce string

	require.Eventually(t, func() bool {
		var register *commands.RegisterConsumer

		for _, message := range x.pcRecorded() {
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

		if Tell(x.ctx, x.pc, &deliveryForward{to: x.cc, message: ack}) != nil {
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
func (x *ccHarness) sequenced(t *testing.T, sessionID string, seq int64) *commands.SequencedMessage {
	t.Helper()

	payload := &testpb.Reply{Content: fmt.Sprintf("message-%d", seq)}
	frame, err := x.system.getRemoting().Serializer(payload).Serialize(payload)
	require.NoError(t, err)

	message, err := commands.NewSequencedMessage(sessionID, fmt.Sprintf("id-%d", seq), seq, frame)
	require.NoError(t, err)
	return message
}

// requests returns the Requests recorded by the producer controller stand-in.
func (x *ccHarness) requests() []*commands.Request {
	var requests []*commands.Request

	for _, message := range x.pcRecorded() {
		if request, ok := message.(*commands.Request); ok {
			requests = append(requests, request)
		}
	}

	return requests
}

// acks returns the Acks recorded by the producer controller stand-in.
func (x *ccHarness) acks() []*commands.Ack {
	var acks []*commands.Ack

	for _, message := range x.pcRecorded() {
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
		harness.fromPC(t, harness.sequenced(t, "s1", seq))
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
	harness.fromPC(t, harness.sequenced(t, "s1", 4))

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

	harness.fromPC(t, harness.sequenced(t, "s1", 1))

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
	harness.fromPC(t, harness.sequenced(t, "s1", 1))

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
	harness.fromPC(t, harness.sequenced(t, "s1", 1))
	harness.fromPC(t, harness.sequenced(t, "s1", 1))

	require.Eventually(t, func() bool {
		return len(harness.deliveries()) >= 2
	}, 3*time.Second, 10*time.Millisecond)

	deliveries := harness.deliveries()
	assert.Equal(t, deliveries[0].MessageID(), deliveries[1].MessageID())
	assert.Equal(t, deliveries[0].Seq(), deliveries[1].Seq())

	// a stale Confirmed from the bound consumer is dropped
	require.NoError(t, Tell(harness.ctx, harness.consumer, &deliveryForward{to: harness.cc, message: &Confirmed{}}))

	// the true business confirmation ends the retry loop
	confirmed, err := NewConfirmed(deliveries[0])
	require.NoError(t, err)
	require.NoError(t, Tell(harness.ctx, harness.consumer, &deliveryForward{to: harness.cc, message: confirmed}))

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
	harness.fromPC(t, harness.sequenced(t, "s1", 2))

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
	harness.fromPC(t, harness.sequenced(t, "s1", 1))

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
	harness.fromPC(t, harness.sequenced(t, "s1", 10))
	// stale session: dropped
	harness.fromPC(t, harness.sequenced(t, "s2", 1))

	pause.For(300 * time.Millisecond)
	assert.Empty(t, harness.deliveries())

	// the in-window sequence still flows
	harness.fromPC(t, harness.sequenced(t, "s1", 1))

	require.Eventually(t, func() bool {
		return len(harness.deliveries()) == 1
	}, 3*time.Second, 10*time.Millisecond)
}

func TestConsumerControllerRestartResync(t *testing.T) {
	harness := newConsumerControllerHarness(t, 6, 150*time.Millisecond, true)
	firstNonce := harness.adopt(t, "s1", 1)

	harness.fromPC(t, harness.sequenced(t, "s1", 1))

	require.Eventually(t, func() bool {
		return len(harness.deliveries()) == 1
	}, 3*time.Second, 10*time.Millisecond)

	require.NoError(t, harness.cc.Restart(harness.ctx))

	// the fresh incarnation registers with a fresh nonce
	require.Eventually(t, func() bool {
		register := harness.latestRegistration(t)
		return register.Nonce() != firstNonce
	}, 3*time.Second, 10*time.Millisecond)

	// traffic from the old session is dropped until adoption
	harness.fromPC(t, harness.sequenced(t, "s1", 2))
	pause.For(200 * time.Millisecond)
	assert.Len(t, harness.deliveries(), 1)

	// adopting the new session resumes delivery
	harness.adopt(t, "s2", 2)
	harness.fromPC(t, harness.sequenced(t, "s2", 2))

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
		return !harness.cc.IsRunning()
	}, 3*time.Second, 10*time.Millisecond)
}

func TestConsumerControllerEdgeBranches(t *testing.T) {
	// the controller under test is never spawned: its handlers run on the
	// test goroutine with stand-in PIDs, so no actor turn touches its state
	ctx, system := newCompanionTestSystem(t)

	consumer, err := system.Spawn(ctx, "consumer", &reliableConsumerMock{})
	require.NoError(t, err)

	pc, err := system.Spawn(ctx, "pc-shell", &deliveryRecorder{})
	require.NoError(t, err)

	shell, err := system.Spawn(ctx, "cc-shell", &deliveryRecorder{})
	require.NoError(t, err)

	controller := newConsumerController(consumer, "producer", 2, time.Hour)
	require.NoError(t, controller.PreStart(nil))

	t.Run("With stale tick generation", func(t *testing.T) {
		controller.sawValidTraffic = true
		stale := &consumerControllerTick{generation: controller.generation + 1}
		rctx := newReceiveContext(context.Background(), system.NoSender(), shell, stale)
		controller.handleTick(rctx, stale)
		assert.True(t, controller.sawValidTraffic)
	})

	t.Run("With full receive buffer", func(t *testing.T) {
		controller.producerController = pc
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
		rctx := newReceiveContext(context.Background(), pc, shell, five)
		controller.handleSequencedMessage(rctx, five)

		require.Len(t, controller.buffer, 2)
		assert.Equal(t, int64(3), controller.buffer[0].Seq())
		assert.Equal(t, int64(4), controller.buffer[1].Seq())

		// a duplicate of a buffered sequence leaves the buffer unchanged;
		// the gap request above shrank the demand window, so restore it
		controller.requestUpToSeq = 100
		duplicate, err := commands.NewSequencedMessage("s1", "id-3", 3, []byte("frame"))
		require.NoError(t, err)
		rctx = newReceiveContext(context.Background(), pc, shell, duplicate)
		controller.handleSequencedMessage(rctx, duplicate)
		require.Len(t, controller.buffer, 2)
	})

	t.Run("With producer controller terminated", func(t *testing.T) {
		controller.producerController = pc
		controller.sessionID = "s1"
		controller.registrationNonce = uuid.NewString()

		terminated := NewTerminated(pc.Path())
		rctx := newReceiveContext(context.Background(), system.NoSender(), shell, terminated)
		controller.handleTerminated(rctx, terminated)

		assert.Nil(t, controller.producerController)
		assert.Empty(t, controller.sessionID)
		assert.Empty(t, controller.registrationNonce)
	})
}
