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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// workPullingProducerConfig builds a complete work-pulling producer configuration.
func workPullingProducerConfig() *reliableDeliveryConfig {
	return &reliableDeliveryConfig{
		producer: &reliableProducerConfig{
			workPulling:        true,
			localRetryInterval: DefaultLocalRetryInterval,
			queueRetry: &reliableQueueRetryConfig{
				maxAttempts:    DefaultQueueRetryAttempts,
				initialBackoff: DefaultQueueRetryBackoff,
			},
		},
	}
}

func TestWorkPullingProducerConfigValidate(t *testing.T) {
	t.Run("With a valid work-pulling producer", func(t *testing.T) {
		require.NoError(t, workPullingProducerConfig().Validate())
	})

	t.Run("With a consumer peer name", func(t *testing.T) {
		config := workPullingProducerConfig()
		config.producer.consumerName = "worker"
		require.ErrorContains(t, config.Validate(), "rejects a consumer endpoint name")
	})

	t.Run("With chunking", func(t *testing.T) {
		config := workPullingProducerConfig()
		config.producer.maxChunkBytes = MinChunkSize
		require.ErrorContains(t, config.Validate(), "rejects chunking")
	})

	t.Run("With a durable queue", func(t *testing.T) {
		config := workPullingProducerConfig()
		config.producer.durableQueueID = "queue"
		require.ErrorContains(t, config.Validate(), "rejects a durable producer queue")
	})
}

func TestWorkPullingProducerConfigWireRoundTrip(t *testing.T) {
	config := workPullingProducerConfig()
	config.producer.deliveryConfirmation = true

	wire := config.toProto()
	require.NotNil(t, wire.GetProducer())
	assert.Equal(t, internalpb.ReliableDeliveryPattern_RELIABLE_DELIVERY_PATTERN_WORK_PULLING, wire.GetProducer().GetPattern())
	assert.Empty(t, wire.GetProducer().GetConsumerName())

	restored, err := reliableDeliveryConfigFromProto(wire)
	require.NoError(t, err)
	assert.Equal(t, config, restored)

	spec := config.toRemoteSpec()
	require.NotNil(t, spec.Producer)
	assert.True(t, spec.Producer.WorkPulling)
	assert.Empty(t, spec.Producer.ConsumerName)
	require.NoError(t, spec.Validate())
}

func TestAsWorkPullingSpawnOptions(t *testing.T) {
	t.Run("With a valid producer", func(t *testing.T) {
		config := newSpawnConfig(AsWorkPullingProducer(WithDeliveryConfirmation()))
		require.NotNil(t, config.reliableDelivery)
		assert.True(t, config.reliableDelivery.producer.workPulling)
		assert.True(t, config.reliableDelivery.producer.deliveryConfirmation)
		require.NoError(t, config.Validate())
	})

	t.Run("With chunking rejected at spawn", func(t *testing.T) {
		config := newSpawnConfig(AsWorkPullingProducer(WithChunking(MinChunkSize)))
		require.ErrorContains(t, config.Validate(), "rejects chunking")
	})

	t.Run("With a worker", func(t *testing.T) {
		config := newSpawnConfig(AsWorkPullingWorker("jobs-producer", WithFlowControlWindow(10)))
		require.NotNil(t, config.reliableDelivery.consumer)
		assert.Equal(t, "jobs-producer", config.reliableDelivery.consumer.producerName)
		assert.Equal(t, 10, config.reliableDelivery.consumer.flowControlWindow)
		require.NoError(t, config.Validate())
	})
}

func TestAuthenticateWorkPullingWorker(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)

	producer, err := system.Spawn(ctx, "jobs-producer", &reliableProducerMock{}, AsWorkPullingProducer())
	require.NoError(t, err)

	_, err = system.Spawn(ctx, "jobs-worker", &reliableConsumerMock{autoConfirm: true}, AsWorkPullingWorker("jobs-producer"))
	require.NoError(t, err)

	companion, err := system.resolveReliableCompanion(ctx, "jobs-worker", ReliableControllerRoleConsumer)
	require.NoError(t, err)

	verified, endpointName, err := system.authenticateWorkPullingWorker(ctx, companion, producer.Name())
	require.NoError(t, err)
	assert.True(t, verified.Equals(companion))
	assert.Equal(t, "jobs-worker", endpointName)

	t.Run("With a worker naming another producer", func(t *testing.T) {
		other, err := system.Spawn(ctx, "other-worker", &reliableConsumerMock{autoConfirm: true}, AsWorkPullingWorker("other-producer"))
		require.NoError(t, err)

		otherCompanion, err := system.resolveReliableCompanion(ctx, other.Name(), ReliableControllerRoleConsumer)
		require.NoError(t, err)

		_, _, err = system.authenticateWorkPullingWorker(ctx, otherCompanion, producer.Name())
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.ErrorContains(t, err, "does not name producer")
	})

	t.Run("With a non-companion sender", func(t *testing.T) {
		plain, err := system.Spawn(ctx, "plain", NewMockActor())
		require.NoError(t, err)

		_, _, err = system.authenticateWorkPullingWorker(ctx, plain, producer.Name())
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
	})

	t.Run("With a point-to-point consumer naming the producer", func(t *testing.T) {
		// fencing accepts any consumer configuration that names this producer,
		// including a point-to-point consumer spawned against it
		consumer, err := system.Spawn(ctx, "p2p-consumer", &reliableConsumerMock{autoConfirm: true}, AsReliableConsumer(producer.Name()))
		require.NoError(t, err)

		consumerCompanion, err := system.resolveReliableCompanion(ctx, consumer.Name(), ReliableControllerRoleConsumer)
		require.NoError(t, err)

		verified, endpointName, err := system.authenticateWorkPullingWorker(ctx, consumerCompanion, producer.Name())
		require.NoError(t, err)
		assert.True(t, verified.Equals(consumerCompanion))
		assert.Equal(t, consumer.Name(), endpointName)
	})
}

func TestWorkPullingDeliveryEndToEnd(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)

	producer, err := system.Spawn(ctx, "jobs-producer", &reliableProducerMock{}, AsWorkPullingProducer(WithLocalRetryInterval(100*time.Millisecond)))
	require.NoError(t, err)

	worker1, err := system.Spawn(ctx, "jobs-worker-1", &reliableConsumerMock{autoConfirm: true},
		AsWorkPullingWorker("jobs-producer", WithResendInterval(200*time.Millisecond), WithFlowControlWindow(2)))
	require.NoError(t, err)

	worker2, err := system.Spawn(ctx, "jobs-worker-2", &reliableConsumerMock{autoConfirm: true},
		AsWorkPullingWorker("jobs-producer", WithResendInterval(200*time.Millisecond), WithFlowControlWindow(2)))
	require.NoError(t, err)

	_, err = system.resolveReliableCompanion(ctx, "jobs-producer", ReliableControllerRoleProducer)
	require.NoError(t, err)

	// give both workers time to register and grant demand before submitting
	// work so round-robin has two eligible bindings
	pause.For(time.Second)

	for i := 1; i <= 6; i++ {
		id := fmt.Sprintf("job-%d", i)
		require.NoError(t, Tell(ctx, producer, &produceSubmission{messageID: id, payload: &testpb.Reply{Content: id}}))
	}

	require.Eventually(t, func() bool {
		left := awaitDeliveriesSnapshot(t, ctx, worker1)
		right := awaitDeliveriesSnapshot(t, ctx, worker2)
		return len(distinctDeliveries(left)) > 0 && len(distinctDeliveries(right)) > 0 &&
			len(distinctDeliveries(append(left, right...))) >= 6
	}, 20*time.Second, 20*time.Millisecond)

	all := distinctDeliveries(append(awaitDeliveriesSnapshot(t, ctx, worker1), awaitDeliveriesSnapshot(t, ctx, worker2)...))
	require.Len(t, all, 6)

	seen := make(map[string]bool, len(all))
	for _, delivery := range all {
		seen[delivery.MessageID()] = true
		assert.GreaterOrEqual(t, delivery.Seq(), int64(1))
	}

	for i := 1; i <= 6; i++ {
		assert.True(t, seen[fmt.Sprintf("job-%d", i)])
	}
}

func TestWorkPullingWorkerLossRequeues(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)

	producer, err := system.Spawn(ctx, "jobs-producer", &reliableProducerMock{}, AsWorkPullingProducer(WithLocalRetryInterval(100*time.Millisecond)))
	require.NoError(t, err)

	// worker-1 holds deliveries without confirming so unconfirmed work is
	// outstanding when its endpoint dies
	worker1, err := system.Spawn(ctx, "jobs-worker-1", &reliableConsumerMock{autoConfirm: false},
		AsWorkPullingWorker("jobs-producer", WithResendInterval(200*time.Millisecond), WithFlowControlWindow(4)))
	require.NoError(t, err)

	require.NoError(t, Tell(ctx, producer, &produceSubmission{messageID: "job-1", payload: &testpb.Reply{Content: "job-1"}}))

	require.Eventually(t, func() bool {
		return len(awaitDeliveriesSnapshot(t, ctx, worker1)) >= 1
	}, 10*time.Second, 20*time.Millisecond)

	worker2, err := system.Spawn(ctx, "jobs-worker-2", &reliableConsumerMock{autoConfirm: true},
		AsWorkPullingWorker("jobs-producer", WithResendInterval(200*time.Millisecond), WithFlowControlWindow(4)))
	require.NoError(t, err)

	// let the replacement worker register before ending the holding binding
	pause.For(time.Second)

	require.NoError(t, worker1.Shutdown(ctx))

	require.Eventually(t, func() bool {
		return !worker1.IsRunning()
	}, 5*time.Second, 20*time.Millisecond)

	// the surviving worker receives the requeued MessageID
	deliveries := awaitDeliveries(t, ctx, worker2, 1)
	require.Len(t, deliveries, 1)
	assert.Equal(t, "job-1", deliveries[0].MessageID())

	reply, ok := deliveries[0].Payload().(*testpb.Reply)
	require.True(t, ok)
	assert.Equal(t, "job-1", reply.GetContent())
}

func TestWorkPullingSilenceReregistrationKeepsDelivering(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)

	producer, err := system.Spawn(ctx, "jobs-producer", &reliableProducerMock{}, AsWorkPullingProducer(WithLocalRetryInterval(100*time.Millisecond)))
	require.NoError(t, err)

	worker, err := system.Spawn(ctx, "jobs-worker", &reliableConsumerMock{autoConfirm: true},
		AsWorkPullingWorker("jobs-producer", WithResendInterval(150*time.Millisecond), WithFlowControlWindow(4)))
	require.NoError(t, err)

	require.NoError(t, Tell(ctx, producer, &produceSubmission{messageID: "job-1", payload: &testpb.Reply{Content: "job-1"}}))

	require.Eventually(t, func() bool {
		return len(distinctDeliveries(awaitDeliveriesSnapshot(t, ctx, worker))) >= 1
	}, 10*time.Second, 20*time.Millisecond)

	// a quiet period spanning several worker ticks forces fresh-nonce
	// re-registrations while the worker holds a confirmed sequence; the
	// binding must keep its sequence space or every later demand grant
	// reads as a bounds violation and the worker never receives work again
	pause.For(time.Second)

	require.NoError(t, Tell(ctx, producer, &produceSubmission{messageID: "job-2", payload: &testpb.Reply{Content: "job-2"}}))

	require.Eventually(t, func() bool {
		for _, delivery := range distinctDeliveries(awaitDeliveriesSnapshot(t, ctx, worker)) {
			if delivery.MessageID() == "job-2" {
				return true
			}
		}

		return false
	}, 10*time.Second, 20*time.Millisecond)
}

func TestWorkPullingRegistrationFencingDropsUntrusted(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)

	producer, err := system.Spawn(ctx, "jobs-producer", &reliableProducerMock{}, AsWorkPullingProducer())
	require.NoError(t, err)

	companion, err := system.resolveReliableCompanion(ctx, producer.Name(), ReliableControllerRoleProducer)
	require.NoError(t, err)

	// a plain actor spoofing RegisterConsumer never receives an ack
	spoof, err := system.Spawn(ctx, "spoof", &deliveryRecorder{})
	require.NoError(t, err)

	register, err := commands.NewRegisterConsumer("nonce-1")
	require.NoError(t, err)
	require.NoError(t, Tell(ctx, spoof, &deliveryForward{to: companion, message: register}))

	pause.For(300 * time.Millisecond)

	for _, message := range recordedMessages(t, ctx, spoof) {
		_, isAck := message.(*commands.RegistrationAck)
		assert.False(t, isAck)
	}

	assert.True(t, companion.IsRunning())
}

func TestWorkPullingDeliveryConfirmation(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)

	producer, err := system.Spawn(ctx, "jobs-producer", &reliableProducerMockWithConfirm{},
		AsWorkPullingProducer(WithDeliveryConfirmation(), WithLocalRetryInterval(100*time.Millisecond)))
	require.NoError(t, err)

	_, err = system.Spawn(ctx, "jobs-worker", &reliableConsumerMock{autoConfirm: true},
		AsWorkPullingWorker("jobs-producer", WithResendInterval(200*time.Millisecond)))
	require.NoError(t, err)

	require.NoError(t, Tell(ctx, producer, &produceSubmission{messageID: "job-1", payload: &testpb.Reply{Content: "job-1"}}))

	require.Eventually(t, func() bool {
		response, err := Ask(ctx, producer, &getConfirmations{}, time.Second)
		if err != nil {
			return false
		}

		notices, _ := response.([]*DeliveryConfirmed)
		return len(notices) >= 1 && notices[0].MessageID() == "job-1"
	}, 10*time.Second, 20*time.Millisecond)
}

// awaitDeliveriesSnapshot returns the consumer's current delivery list without
// waiting for a count.
func awaitDeliveriesSnapshot(t *testing.T, ctx context.Context, consumer *PID) []*Delivery {
	t.Helper()

	response, err := Ask(ctx, consumer, &getDeliveries{}, time.Second)
	if err != nil {
		return nil
	}

	recorded, _ := response.([]*Delivery)
	return recorded
}

// distinctDeliveries collapses redeliveries to the first occurrence per MessageID.
func distinctDeliveries(recorded []*Delivery) []*Delivery {
	seen := make(map[string]bool, len(recorded))
	distinct := make([]*Delivery, 0, len(recorded))

	for _, delivery := range recorded {
		if seen[delivery.MessageID()] {
			continue
		}

		seen[delivery.MessageID()] = true
		distinct = append(distinct, delivery)
	}

	return distinct
}

// recordedMessages asks a recorder double for its message snapshot.
func recordedMessages(t *testing.T, ctx context.Context, pid *PID) []any {
	t.Helper()

	response, err := Ask(ctx, pid, &getRecorded{}, time.Second)
	if err != nil {
		return nil
	}

	snapshot, _ := response.([]any)
	return snapshot
}

// reliableProducerMockWithConfirm extends the producer mock with DeliveryConfirmed capture.
type reliableProducerMockWithConfirm struct {
	reliableProducerMock
	confirmations []*DeliveryConfirmed
}

func (x *reliableProducerMockWithConfirm) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *DeliveryConfirmed:
		x.confirmations = append(x.confirmations, msg)
	case *getConfirmations:
		ctx.Response(append([]*DeliveryConfirmed(nil), x.confirmations...))
	default:
		x.reliableProducerMock.Receive(ctx)
	}
}

// getConfirmations asks the producer mock for captured DeliveryConfirmed notices.
type getConfirmations struct{}
