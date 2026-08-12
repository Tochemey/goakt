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
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// workPullingProducerConfig builds a complete work-pulling producer configuration.
func workPullingProducerConfig() *reliableDeliveryConfig {
	return &reliableDeliveryConfig{
		producer: &reliableProducerConfig{
			workPulling:   true,
			retryInterval: DefaultReliableProducerRetryInterval,
			queueRetry: &reliableQueueRetryConfig{
				maxAttempts:    DefaultReliableQueueRetryAttempts,
				initialBackoff: DefaultReliableQueueRetryBackoff,
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
		config.producer.maxChunkBytes = MinReliableChunkSize
		require.ErrorContains(t, config.Validate(), "rejects chunking")
	})

	t.Run("With a durable producer queue", func(t *testing.T) {
		config := workPullingProducerConfig()
		config.producer.queue = &mockDurableQueue{}
		require.ErrorContains(t, config.Validate(), "rejects a durable producer queue")
	})

	t.Run("With a durable work queue", func(t *testing.T) {
		config := workPullingProducerConfig()
		config.producer.workQueue = &mockDurableWorkQueue{}
		config.producer.durableQueueID = "mockDurableWorkQueue"
		require.NoError(t, config.Validate())
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

func TestAsReliableWorkPullingSpawnOptions(t *testing.T) {
	t.Run("With a valid producer", func(t *testing.T) {
		config := newSpawnConfig(AsReliableWorkPullingProducer(WithReliableDeliveryConfirmation()))
		require.NotNil(t, config.reliableDelivery)
		assert.True(t, config.reliableDelivery.producer.workPulling)
		assert.True(t, config.reliableDelivery.producer.deliveryConfirmation)
		require.NoError(t, config.Validate())
	})

	t.Run("With chunking rejected at spawn", func(t *testing.T) {
		config := newSpawnConfig(AsReliableWorkPullingProducer(WithReliableChunking(MinReliableChunkSize)))
		require.ErrorContains(t, config.Validate(), "rejects chunking")
	})

	t.Run("With a worker", func(t *testing.T) {
		config := newSpawnConfig(AsReliableWorkPullingWorker("jobs-producer", WithReliableFlowControlWindow(10)))
		require.NotNil(t, config.reliableDelivery.consumer)
		assert.Equal(t, "jobs-producer", config.reliableDelivery.consumer.producerName)
		assert.Equal(t, 10, config.reliableDelivery.consumer.flowControlWindow)
		require.NoError(t, config.Validate())
	})
}

func TestAuthenticateWorkPullingWorker(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)

	producer, err := system.Spawn(ctx, "jobs-producer", &reliableProducerMock{}, AsReliableWorkPullingProducer())
	require.NoError(t, err)

	_, err = system.Spawn(ctx, "jobs-worker", &reliableConsumerMock{autoConfirm: true}, AsReliableWorkPullingWorker("jobs-producer"))
	require.NoError(t, err)

	companion, err := system.resolveReliableCompanion(ctx, "jobs-worker", ReliableControllerRoleConsumer, nil)
	require.NoError(t, err)

	verified, endpointName, err := system.authenticateWorkPullingWorker(ctx, companion, producer.Name())
	require.NoError(t, err)
	assert.True(t, verified.Equals(companion))
	assert.Equal(t, "jobs-worker", endpointName)

	t.Run("With a worker naming another producer", func(t *testing.T) {
		other, err := system.Spawn(ctx, "other-worker", &reliableConsumerMock{autoConfirm: true}, AsReliableWorkPullingWorker("other-producer"))
		require.NoError(t, err)

		otherCompanion, err := system.resolveReliableCompanion(ctx, other.Name(), ReliableControllerRoleConsumer, nil)
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

		consumerCompanion, err := system.resolveReliableCompanion(ctx, consumer.Name(), ReliableControllerRoleConsumer, nil)
		require.NoError(t, err)

		verified, endpointName, err := system.authenticateWorkPullingWorker(ctx, consumerCompanion, producer.Name())
		require.NoError(t, err)
		assert.True(t, verified.Equals(consumerCompanion))
		assert.Equal(t, consumer.Name(), endpointName)
	})
}

func TestWorkPullingDeliveryEndToEnd(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)

	producer, err := system.Spawn(ctx, "jobs-producer", &reliableProducerMock{}, AsReliableWorkPullingProducer(WithReliableRetryInterval(100*time.Millisecond)))
	require.NoError(t, err)

	worker1, err := system.Spawn(ctx, "jobs-worker-1", &reliableConsumerMock{autoConfirm: true},
		AsReliableWorkPullingWorker("jobs-producer", WithReliableResendInterval(200*time.Millisecond), WithReliableFlowControlWindow(2)))
	require.NoError(t, err)

	worker2, err := system.Spawn(ctx, "jobs-worker-2", &reliableConsumerMock{autoConfirm: true},
		AsReliableWorkPullingWorker("jobs-producer", WithReliableResendInterval(200*time.Millisecond), WithReliableFlowControlWindow(2)))
	require.NoError(t, err)

	_, err = system.resolveReliableCompanion(ctx, "jobs-producer", ReliableControllerRoleProducer, nil)
	require.NoError(t, err)

	// give both workers time to register and grant demand before submitting
	// work so round-robin has two eligible bindings
	pause.For(time.Second)

	for i := 1; i <= 6; i++ {
		id := fmt.Sprintf("job-%d", i)
		require.NoError(t, Tell(ctx, producer, &produceSubmission{messageID: id, payload: testpb.Reply_builder{Content: id}.Build()}))
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

	producer, err := system.Spawn(ctx, "jobs-producer", &reliableProducerMock{}, AsReliableWorkPullingProducer(WithReliableRetryInterval(100*time.Millisecond)))
	require.NoError(t, err)

	// worker-1 holds deliveries without confirming so unconfirmed work is
	// outstanding when its endpoint dies
	worker1, err := system.Spawn(ctx, "jobs-worker-1", &reliableConsumerMock{autoConfirm: false},
		AsReliableWorkPullingWorker("jobs-producer", WithReliableResendInterval(200*time.Millisecond), WithReliableFlowControlWindow(4)))
	require.NoError(t, err)

	require.NoError(t, Tell(ctx, producer, &produceSubmission{messageID: "job-1", payload: testpb.Reply_builder{Content: "job-1"}.Build()}))

	require.Eventually(t, func() bool {
		return len(awaitDeliveriesSnapshot(t, ctx, worker1)) >= 1
	}, 10*time.Second, 20*time.Millisecond)

	worker2, err := system.Spawn(ctx, "jobs-worker-2", &reliableConsumerMock{autoConfirm: true},
		AsReliableWorkPullingWorker("jobs-producer", WithReliableResendInterval(200*time.Millisecond), WithReliableFlowControlWindow(4)))
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

	producer, err := system.Spawn(ctx, "jobs-producer", &reliableProducerMock{}, AsReliableWorkPullingProducer(WithReliableRetryInterval(100*time.Millisecond)))
	require.NoError(t, err)

	worker, err := system.Spawn(ctx, "jobs-worker", &reliableConsumerMock{autoConfirm: true},
		AsReliableWorkPullingWorker("jobs-producer", WithReliableResendInterval(150*time.Millisecond), WithReliableFlowControlWindow(4)))
	require.NoError(t, err)

	require.NoError(t, Tell(ctx, producer, &produceSubmission{messageID: "job-1", payload: testpb.Reply_builder{Content: "job-1"}.Build()}))

	require.Eventually(t, func() bool {
		return len(distinctDeliveries(awaitDeliveriesSnapshot(t, ctx, worker))) >= 1
	}, 10*time.Second, 20*time.Millisecond)

	// a quiet period spanning several worker ticks forces fresh-nonce
	// re-registrations while the worker holds a confirmed sequence; the
	// binding must keep its sequence space or every later demand grant
	// reads as a bounds violation and the worker never receives work again
	pause.For(time.Second)

	require.NoError(t, Tell(ctx, producer, &produceSubmission{messageID: "job-2", payload: testpb.Reply_builder{Content: "job-2"}.Build()}))

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

	producer, err := system.Spawn(ctx, "jobs-producer", &reliableProducerMock{}, AsReliableWorkPullingProducer())
	require.NoError(t, err)

	companion, err := system.resolveReliableCompanion(ctx, producer.Name(), ReliableControllerRoleProducer, nil)
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
		AsReliableWorkPullingProducer(WithReliableDeliveryConfirmation(), WithReliableRetryInterval(100*time.Millisecond)))
	require.NoError(t, err)

	_, err = system.Spawn(ctx, "jobs-worker", &reliableConsumerMock{autoConfirm: true},
		AsReliableWorkPullingWorker("jobs-producer", WithReliableResendInterval(200*time.Millisecond)))
	require.NoError(t, err)

	require.NoError(t, Tell(ctx, producer, &produceSubmission{messageID: "job-1", payload: testpb.Reply_builder{Content: "job-1"}.Build()}))

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

// testWorkPullingConfig builds the producer settings a directly constructed
// work-pulling controller needs, so a test states only the values it cares about.
func testWorkPullingConfig(retryAttempts int, retryBackoff, localRetryInterval time.Duration) *reliableProducerConfig {
	return &reliableProducerConfig{
		workPulling:   true,
		retryInterval: localRetryInterval,
		queueRetry: &reliableQueueRetryConfig{
			maxAttempts:    retryAttempts,
			initialBackoff: retryBackoff,
		},
	}
}

func TestWorkPullingControllerEdgeBranches(t *testing.T) {
	// the controller under test is never spawned: its handlers run on the
	// test goroutine with stand-in PIDs, so no actor turn touches its state
	ctx, system := newCompanionTestSystem(t)

	producer, err := system.Spawn(ctx, "jobs-producer", &deliveryRecorder{})
	require.NoError(t, err)

	workerStandIn, err := system.Spawn(ctx, "worker-stand-in", &deliveryRecorder{})
	require.NoError(t, err)

	// a genuine worker endpoint provides an authenticatable consumer companion
	_, err = system.Spawn(ctx, "jobs-worker-edge", &reliableConsumerMock{},
		AsReliableWorkPullingWorker("jobs-producer", WithReliableResendInterval(time.Hour), WithReliableFlowControlWindow(4)))
	require.NoError(t, err)

	workerCompanion, err := system.resolveReliableCompanion(ctx, "jobs-worker-edge", ReliableControllerRoleConsumer, nil)
	require.NoError(t, err)

	payload, err := NewReliablePayload([]byte("frame"))
	require.NoError(t, err)

	newController := func(t *testing.T, queue DurableWorkQueue) *workPullingProducerController {
		t.Helper()
		controller := newWorkPullingProducerController(producer, testWorkPullingConfig(1, time.Millisecond, time.Millisecond), queue)
		require.NoError(t, controller.PreStart(newContext(ctx, "wp-controller", system)))
		return controller
	}

	spawnHost := func(t *testing.T, name string) *PID {
		t.Helper()
		host, err := system.Spawn(ctx, name, &deliveryRecorder{})
		require.NoError(t, err)
		return host
	}

	rctxFor := func(sender, host *PID, message any) *ReceiveContext {
		return newReceiveContext(context.Background(), sender, host, message)
	}

	countRecorded := func(t *testing.T, pid *PID, match func(any) bool) int {
		t.Helper()

		total := 0
		for _, message := range recordedMessages(t, ctx, pid) {
			if match(message) {
				total++
			}
		}

		return total
	}

	t.Run("With PreStart validation", func(t *testing.T) {
		config := testWorkPullingConfig(1, time.Millisecond, time.Millisecond)
		assert.ErrorContains(t, newWorkPullingProducerController(nil, config, nil).PreStart(nil), "bound local producer")
		assert.ErrorContains(t, newWorkPullingProducerController(newRemotePID(address.New("remote", "sys", "127.0.0.1", 1), nil), config, nil).PreStart(nil), "bound local producer")
		assert.ErrorContains(t, newWorkPullingProducerController(producer, testWorkPullingConfig(1, time.Millisecond, 0), nil).PreStart(nil), "positive local retry interval")
		assert.ErrorContains(t, newWorkPullingProducerController(producer, testWorkPullingConfig(0, time.Millisecond, time.Millisecond), &mockDurableWorkQueue{}).PreStart(nil), "positive queue retry settings")
		assert.ErrorContains(t, newWorkPullingProducerController(producer, testWorkPullingConfig(1, 0, time.Millisecond), &mockDurableWorkQueue{}).PreStart(nil), "positive queue retry settings")
	})

	t.Run("With load rebuilding the pending pool", func(t *testing.T) {
		stored, err := NewUnconfirmedMessage("job-a", 1, payload)
		require.NoError(t, err)

		queue := &mockDurableWorkQueue{currentSeq: 1, entries: []workQueueEntry{{message: stored, accepted: true}}}
		controller := newController(t, queue)

		assert.EqualValues(t, 1, controller.storeSeq)
		require.Len(t, controller.pending, 1)
		assert.Equal(t, "job-a", controller.pending[0].messageID)
		assert.EqualValues(t, 1, controller.pending[0].storeSeq)
	})

	t.Run("With load failure on first incarnation", func(t *testing.T) {
		queue := &mockDurableWorkQueue{loadErr: errors.New("unreachable")}
		controller := newWorkPullingProducerController(producer, testWorkPullingConfig(1, time.Millisecond, time.Millisecond), queue)
		require.ErrorContains(t, controller.PreStart(newContext(ctx, "wp-load-fail", system)), "failed to load durable state")
		assert.EqualValues(t, 1, controller.generation)
	})

	t.Run("With load failure after restart publishes failure", func(t *testing.T) {
		queue := &mockDurableWorkQueue{}
		controller := newController(t, queue)

		queue.mu.Lock()
		queue.loadErr = errors.New("backing store is unreachable")
		queue.mu.Unlock()

		subscriber, err := system.Subscribe()
		require.NoError(t, err)

		require.Error(t, controller.PreStart(newContext(ctx, "wp-load-restart", system)))

		failure := awaitFailure(t, subscriber)
		assert.Equal(t, ReliableDeliveryStageLoad, failure.Stage())
		assert.Equal(t, ReliableControllerRoleProducer, failure.ControllerRole())
	})

	t.Run("With an unhandled message", func(t *testing.T) {
		controller := newController(t, nil)
		host := spawnHost(t, "host-unhandled")
		assert.NotPanics(t, func() { controller.Receive(rctxFor(system.NoSender(), host, "bogus")) })
	})

	t.Run("With an unverified registration dropped", func(t *testing.T) {
		controller := newController(t, nil)
		host := spawnHost(t, "host-register-drop")

		register, err := commands.NewRegisterConsumer("nonce-x")
		require.NoError(t, err)
		controller.handleRegisterConsumer(rctxFor(workerStandIn, host, register), register)

		assert.Empty(t, controller.bindings)
	})

	t.Run("With worker registration and nonce refresh", func(t *testing.T) {
		controller := newController(t, nil)
		host := spawnHost(t, "host-register")

		register, err := commands.NewRegisterConsumer("nonce-1")
		require.NoError(t, err)
		controller.handleRegisterConsumer(rctxFor(workerCompanion, host, register), register)

		binding := controller.bindings["jobs-worker-edge"]
		require.NotNil(t, binding)
		assert.True(t, binding.controller.Equals(workerCompanion))
		assert.Equal(t, "nonce-1", binding.registrationNonce)

		// the same companion with a fresh nonce keeps its sequence space
		binding.currentSeq = 5
		refresh, err := commands.NewRegisterConsumer("nonce-2")
		require.NoError(t, err)
		controller.handleRegisterConsumer(rctxFor(workerCompanion, host, refresh), refresh)

		binding = controller.bindings["jobs-worker-edge"]
		require.NotNil(t, binding)
		assert.Equal(t, "nonce-2", binding.registrationNonce)
		assert.EqualValues(t, 5, binding.currentSeq)
		assert.Len(t, controller.bindingOrder, 1)
	})

	t.Run("With a replaced companion requeueing unconfirmed work", func(t *testing.T) {
		controller := newController(t, nil)
		host := spawnHost(t, "host-replace")

		controller.bindings["jobs-worker-edge"] = &bindingWork{
			endpointName:      "jobs-worker-edge",
			controller:        workerStandIn,
			registrationNonce: "old",
			currentSeq:        2,
			demandUpTo:        4,
			unconfirmed: []dispatchedWork{
				{messageID: "job-1", workerSeq: 1, storeSeq: 1, payload: payload},
				{messageID: "job-2", workerSeq: 2, storeSeq: 2, payload: payload},
			},
		}
		controller.bindingOrder = []string{"jobs-worker-edge"}

		register, err := commands.NewRegisterConsumer("nonce-fresh")
		require.NoError(t, err)
		controller.handleRegisterConsumer(rctxFor(workerCompanion, host, register), register)

		binding := controller.bindings["jobs-worker-edge"]
		require.NotNil(t, binding)
		assert.True(t, binding.controller.Equals(workerCompanion))
		assert.Zero(t, binding.currentSeq)

		// the departed incarnation's work waits at the head of the pool
		require.Len(t, controller.pending, 2)
		assert.Equal(t, "job-1", controller.pending[0].messageID)
		assert.Equal(t, "job-2", controller.pending[1].messageID)
	})

	t.Run("With a RegistrationAck build failure", func(t *testing.T) {
		controller := newController(t, nil)
		host := spawnHost(t, "host-register-terminal")
		controller.sessionID = ""

		subscriber, err := system.Subscribe()
		require.NoError(t, err)

		register, err := commands.NewRegisterConsumer("nonce-terminal")
		require.NoError(t, err)
		controller.handleRegisterConsumer(rctxFor(workerCompanion, host, register), register)

		failure := awaitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "failed to build RegistrationAck")
	})

	t.Run("With stale worker traffic dropped", func(t *testing.T) {
		controller := newController(t, nil)
		host := spawnHost(t, "host-stale-traffic")
		controller.bindings["jobs-worker-edge"] = &bindingWork{endpointName: "jobs-worker-edge", controller: workerStandIn, registrationNonce: "n-1", demandUpTo: 3}
		controller.bindingOrder = []string{"jobs-worker-edge"}

		// a stale session is dropped before any binding lookup
		staleSession, err := commands.NewRequest("other-session", "n-1", 0, 5, false)
		require.NoError(t, err)
		controller.handleRequest(rctxFor(workerStandIn, host, staleSession), staleSession)

		// a bound sender with a stale nonce is dropped
		staleNonce, err := commands.NewRequest(controller.sessionID, "n-9", 0, 5, false)
		require.NoError(t, err)
		controller.handleRequest(rctxFor(workerStandIn, host, staleNonce), staleNonce)

		// an unbound sender is dropped
		unbound, err := commands.NewAck(controller.sessionID, "n-1", 1)
		require.NoError(t, err)
		controller.handleAck(rctxFor(workerCompanion, host, unbound), unbound)

		assert.EqualValues(t, 3, controller.bindings["jobs-worker-edge"].demandUpTo)
		assert.Zero(t, controller.bindings["jobs-worker-edge"].confirmedSeq)
	})

	t.Run("With an illegal demand range ending the binding", func(t *testing.T) {
		controller := newController(t, nil)
		host := spawnHost(t, "host-illegal-demand")
		controller.bindings["jobs-worker-edge"] = &bindingWork{
			endpointName:      "jobs-worker-edge",
			controller:        workerStandIn,
			registrationNonce: "n-1",
			currentSeq:        1,
			unconfirmed:       []dispatchedWork{{messageID: "job-1", workerSeq: 1, storeSeq: 1, payload: payload}},
		}
		controller.bindingOrder = []string{"jobs-worker-edge"}

		request, err := commands.NewRequest(controller.sessionID, "n-1", 5, 6, false)
		require.NoError(t, err)
		controller.handleRequest(rctxFor(workerStandIn, host, request), request)

		assert.Empty(t, controller.bindings)
		assert.Empty(t, controller.bindingOrder)
		require.Len(t, controller.pending, 1)
		assert.Equal(t, "job-1", controller.pending[0].messageID)
	})

	t.Run("With an illegal confirmation ending the binding", func(t *testing.T) {
		controller := newController(t, nil)
		host := spawnHost(t, "host-illegal-confirm")
		controller.bindings["jobs-worker-edge"] = &bindingWork{endpointName: "jobs-worker-edge", controller: workerStandIn, registrationNonce: "n-1", currentSeq: 1}
		controller.bindingOrder = []string{"jobs-worker-edge"}

		ack, err := commands.NewAck(controller.sessionID, "n-1", 7)
		require.NoError(t, err)
		controller.handleAck(rctxFor(workerStandIn, host, ack), ack)

		assert.Empty(t, controller.bindings)
	})

	t.Run("With confirmation watermark rules", func(t *testing.T) {
		controller := newController(t, nil)
		host := spawnHost(t, "host-confirm-rules")
		rctx := rctxFor(system.NoSender(), host, &PostStart{})

		binding := &bindingWork{endpointName: "jobs-worker-edge", controller: workerStandIn, registrationNonce: "n-1", currentSeq: 3, confirmedSeq: 2}

		// at or below the watermark is ignored
		controller.advanceConfirmed(rctx, binding, 1)
		assert.EqualValues(t, 2, binding.confirmedSeq)

		// above the watermark with nothing dispatched advances and returns
		controller.advanceConfirmed(rctx, binding, 3)
		assert.EqualValues(t, 3, binding.confirmedSeq)

		// a completed prefix is cut even without confirmation notices
		binding.currentSeq = 5
		binding.unconfirmed = []dispatchedWork{
			{messageID: "job-4", workerSeq: 4, storeSeq: 4, payload: payload},
			{messageID: "job-5", workerSeq: 5, storeSeq: 5, payload: payload},
		}
		controller.advanceConfirmed(rctx, binding, 4)
		require.Len(t, binding.unconfirmed, 1)
		assert.Equal(t, "job-5", binding.unconfirmed[0].messageID)
	})

	t.Run("With ViaTimeout resends capped by demand", func(t *testing.T) {
		controller := newController(t, nil)
		host := spawnHost(t, "host-resend")
		controller.bindings["jobs-worker-edge"] = &bindingWork{
			endpointName:      "jobs-worker-edge",
			controller:        workerStandIn,
			registrationNonce: "n-1",
			currentSeq:        2,
			unconfirmed: []dispatchedWork{
				{messageID: "job-1", workerSeq: 1, storeSeq: 1, payload: payload},
				{messageID: "job-2", workerSeq: 2, storeSeq: 2, payload: payload},
			},
		}
		controller.bindingOrder = []string{"jobs-worker-edge"}

		before := countRecorded(t, workerStandIn, func(message any) bool {
			_, ok := message.(*commands.SequencedMessage)
			return ok
		})

		// demand covers only the first unconfirmed message
		request, err := commands.NewRequest(controller.sessionID, "n-1", 0, 1, true)
		require.NoError(t, err)
		controller.handleRequest(rctxFor(workerStandIn, host, request), request)

		require.Eventually(t, func() bool {
			return countRecorded(t, workerStandIn, func(message any) bool {
				sequenced, ok := message.(*commands.SequencedMessage)
				return ok && sequenced.Seq() == 1
			}) > 0
		}, 3*time.Second, 20*time.Millisecond)

		after := countRecorded(t, workerStandIn, func(message any) bool {
			_, ok := message.(*commands.SequencedMessage)
			return ok
		})
		assert.Equal(t, before+1, after)
	})

	t.Run("With deferred emission beyond demand", func(t *testing.T) {
		controller := newController(t, nil)
		host := spawnHost(t, "host-deferred")
		rctx := rctxFor(system.NoSender(), host, &PostStart{})

		bound := &bindingWork{endpointName: "jobs-worker-edge", controller: workerStandIn, demandUpTo: 0}
		assert.NotPanics(t, func() {
			controller.emitSequenced(rctx, bound, dispatchedWork{messageID: "job-1", workerSeq: 1, storeSeq: 1, payload: payload})
		})

		unbound := &bindingWork{endpointName: "jobs-worker-edge", demandUpTo: 5}
		assert.NotPanics(t, func() {
			controller.emitSequenced(rctx, unbound, dispatchedWork{messageID: "job-1", workerSeq: 1, storeSeq: 1, payload: payload})
		})
	})

	t.Run("With dispatch waiting for eligible bindings", func(t *testing.T) {
		controller := newController(t, nil)
		host := spawnHost(t, "host-dispatch-wait")
		rctx := rctxFor(system.NoSender(), host, &PostStart{})

		controller.pending = []pendingWork{{messageID: "job-1", storeSeq: 1, payload: payload}}

		// no workers registered
		controller.dispatchPending(rctx)
		require.Len(t, controller.pending, 1)

		// a registered worker without free demand
		controller.bindings["jobs-worker-edge"] = &bindingWork{endpointName: "jobs-worker-edge", controller: workerStandIn, currentSeq: 2, demandUpTo: 2}
		controller.bindingOrder = []string{"jobs-worker-edge"}
		controller.dispatchPending(rctx)
		require.Len(t, controller.pending, 1)
	})

	t.Run("With producer handshake drops and duplicates", func(t *testing.T) {
		controller := newController(t, nil)
		host := spawnHost(t, "host-produced-drops")

		request, err := newRequestNext(controller.sessionID, "tok-1", producer, host)
		require.NoError(t, err)
		produced, err := NewProduced(request, "m-1", testpb.Reply_builder{Content: "m-1"}.Build())
		require.NoError(t, err)

		// wrong sender
		controller.handleProduced(rctxFor(workerStandIn, host, produced), produced)
		assert.Equal(t, producerHandshakeIdle, controller.handshake)

		// stale session
		staleRequest, err := newRequestNext("other-session", "tok-1", producer, host)
		require.NoError(t, err)
		staleProduced, err := NewProduced(staleRequest, "m-1", testpb.Reply_builder{Content: "m-1"}.Build())
		require.NoError(t, err)
		controller.handleProduced(rctxFor(producer, host, staleProduced), staleProduced)
		assert.Equal(t, producerHandshakeIdle, controller.handshake)

		// exact duplicate of the in-progress handshake
		controller.handshake = producerHandshakeStore
		controller.token = "tok-1"
		controller.pendingMessageID = "m-1"
		controller.handleProduced(rctxFor(producer, host, produced), produced)
		assert.Equal(t, producerHandshakeStore, controller.handshake)

		// late duplicate of an accepted handshake
		controller.resetHandshake()
		controller.lastCompletedToken = "tok-1"
		controller.lastCompletedMessageID = "m-1"
		controller.handleProduced(rctxFor(producer, host, produced), produced)
		assert.Equal(t, producerHandshakeIdle, controller.handshake)
	})

	t.Run("With an unexpected Produced terminal", func(t *testing.T) {
		controller := newController(t, nil)
		host := spawnHost(t, "host-produced-unexpected")

		subscriber, err := system.Subscribe()
		require.NoError(t, err)

		request, err := newRequestNext(controller.sessionID, "tok-x", producer, host)
		require.NoError(t, err)
		produced, err := NewProduced(request, "m-x", testpb.Reply_builder{Content: "m-x"}.Build())
		require.NoError(t, err)
		controller.handleProduced(rctxFor(producer, host, produced), produced)

		failure := awaitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "unexpected Produced")
	})

	t.Run("With a Produced token mismatch terminal", func(t *testing.T) {
		controller := newController(t, nil)
		host := spawnHost(t, "host-produced-mismatch")
		controller.handshake = producerHandshakeCredit
		controller.token = "tok-want"

		subscriber, err := system.Subscribe()
		require.NoError(t, err)

		request, err := newRequestNext(controller.sessionID, "tok-got", producer, host)
		require.NoError(t, err)
		produced, err := NewProduced(request, "m-x", testpb.Reply_builder{Content: "m-x"}.Build())
		require.NoError(t, err)
		controller.handleProduced(rctxFor(producer, host, produced), produced)

		failure := awaitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "Produced token mismatch")
	})

	t.Run("With an unregistered payload type terminal", func(t *testing.T) {
		controller := newController(t, nil)
		host := spawnHost(t, "host-produced-serializer")
		controller.handshake = producerHandshakeCredit
		controller.token = "tok-1"

		subscriber, err := system.Subscribe()
		require.NoError(t, err)

		request, err := newRequestNext(controller.sessionID, "tok-1", producer, host)
		require.NoError(t, err)
		produced, err := NewProduced(request, "m-1", struct{ name string }{name: "unregistered"})
		require.NoError(t, err)
		controller.handleProduced(rctxFor(producer, host, produced), produced)

		failure := awaitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "no serializer is registered")
	})

	t.Run("With store guards terminal", func(t *testing.T) {
		exhausted := newController(t, nil)
		host := spawnHost(t, "host-store-exhausted")
		exhausted.storeSeq = math.MaxInt64 - 1

		subscriber, err := system.Subscribe()
		require.NoError(t, err)

		exhausted.startStore(rctxFor(system.NoSender(), host, &PostStart{}))
		failure := awaitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "store sequence space exhausted")

		impossible := newController(t, nil)
		impossibleHost := spawnHost(t, "host-store-impossible")
		impossible.pendingMessageID = "m-1"

		subscriber, err = system.Subscribe()
		require.NoError(t, err)

		impossible.startStore(rctxFor(system.NoSender(), impossibleHost, &PostStart{}))
		failure = awaitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "failed to build store result")
	})

	t.Run("With impossible protocol values terminal", func(t *testing.T) {
		stored := newController(t, nil)
		host := spawnHost(t, "host-impossible-stored")
		stored.sessionID = ""
		stored.pendingMessageID = "m-1"
		stored.pendingStoreSeq = 1

		subscriber, err := system.Subscribe()
		require.NoError(t, err)

		stored.replyStored(rctxFor(system.NoSender(), host, &PostStart{}))
		failure := awaitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "failed to build Stored")

		requester := newController(t, nil)
		requesterHost := spawnHost(t, "host-impossible-request")
		requester.sessionID = ""

		subscriber, err = system.Subscribe()
		require.NoError(t, err)

		requester.sendRequestNext(rctxFor(system.NoSender(), requesterHost, &PostStart{}))
		failure = awaitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "failed to build RequestNext")

		emitter := newController(t, nil)
		emitterHost := spawnHost(t, "host-impossible-emit")
		emitter.sessionID = ""

		subscriber, err = system.Subscribe()
		require.NoError(t, err)

		binding := &bindingWork{endpointName: "jobs-worker-edge", controller: workerStandIn, demandUpTo: 5}
		emitter.emitSequenced(rctxFor(system.NoSender(), emitterHost, &PostStart{}), binding, dispatchedWork{messageID: "m-1", workerSeq: 1, storeSeq: 1, payload: payload})
		failure = awaitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "failed to build SequencedMessage")

		dispatcher := newController(t, nil)
		dispatcherHost := spawnHost(t, "host-worker-exhausted")
		dispatcher.pending = []pendingWork{{messageID: "m-1", storeSeq: 1, payload: payload}}
		dispatcher.bindings["jobs-worker-edge"] = &bindingWork{endpointName: "jobs-worker-edge", controller: workerStandIn, currentSeq: math.MaxInt64 - 1, demandUpTo: math.MaxInt64}
		dispatcher.bindingOrder = []string{"jobs-worker-edge"}

		subscriber, err = system.Subscribe()
		require.NoError(t, err)

		dispatcher.dispatchPending(rctxFor(system.NoSender(), dispatcherHost, &PostStart{}))
		failure = awaitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "worker sequence space exhausted")
	})

	t.Run("With StoredAck drops duplicates and violation", func(t *testing.T) {
		controller := newController(t, nil)
		host := spawnHost(t, "host-storedack")

		stored, err := newStoredFromState(controller.sessionID, "tok-1", "m-1", 1, producer, host)
		require.NoError(t, err)
		ack, err := NewStoredAck(stored)
		require.NoError(t, err)

		// wrong sender
		controller.handleStoredAck(rctxFor(workerStandIn, host, ack), ack)
		assert.Equal(t, producerHandshakeIdle, controller.handshake)

		// stale session
		staleStored, err := newStoredFromState("other-session", "tok-1", "m-1", 1, producer, host)
		require.NoError(t, err)
		staleAck, err := NewStoredAck(staleStored)
		require.NoError(t, err)
		controller.handleStoredAck(rctxFor(producer, host, staleAck), staleAck)
		assert.Equal(t, producerHandshakeIdle, controller.handshake)

		// duplicate while acceptance is pending
		controller.handshake = producerHandshakeAccept
		controller.token = "tok-1"
		controller.pendingMessageID = "m-1"
		controller.handleStoredAck(rctxFor(producer, host, ack), ack)
		assert.Equal(t, producerHandshakeAccept, controller.handshake)

		// late duplicate of an accepted handshake
		controller.resetHandshake()
		controller.lastCompletedToken = "tok-1"
		controller.lastCompletedMessageID = "m-1"
		controller.handleStoredAck(rctxFor(producer, host, ack), ack)
		assert.Equal(t, producerHandshakeIdle, controller.handshake)

		// anything else from the bound producer is terminal
		subscriber, err := system.Subscribe()
		require.NoError(t, err)

		controller.lastCompletedToken = ""
		controller.lastCompletedMessageID = ""
		controller.handleStoredAck(rctxFor(producer, host, ack), ack)
		failure := awaitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "unexpected StoredAck")
	})

	t.Run("With acceptance deduplicating owned messages", func(t *testing.T) {
		controller := newController(t, nil)
		host := spawnHost(t, "host-owns")
		rctx := rctxFor(system.NoSender(), host, &PostStart{})

		// the volatile happy path appends the accepted message once
		stored, err := newStoredFromState(controller.sessionID, "tok-1", "m-1", 1, producer, host)
		require.NoError(t, err)
		ack, err := NewStoredAck(stored)
		require.NoError(t, err)

		controller.handshake = producerHandshakeStoredAck
		controller.token = "tok-1"
		controller.pendingMessageID = "m-1"
		controller.pendingStoreSeq = 1
		controller.pendingPayload = payload
		controller.handleStoredAck(rctxFor(producer, host, ack), ack)
		require.Len(t, controller.pending, 1)
		assert.Equal(t, "tok-1", controller.lastCompletedToken)

		// a resubmit already pending is not appended again
		controller.pendingMessageID = "m-1"
		controller.pendingStoreSeq = 1
		controller.pendingPayload = payload
		controller.completeAccept(rctx)
		assert.Len(t, controller.pending, 1)

		// a resubmit already dispatched to a worker is not appended either
		controller.pending = nil
		controller.bindings["jobs-worker-edge"] = &bindingWork{
			endpointName: "jobs-worker-edge",
			controller:   workerStandIn,
			unconfirmed:  []dispatchedWork{{messageID: "m-2", workerSeq: 1, storeSeq: 2, payload: payload}},
		}
		controller.pendingMessageID = "m-2"
		controller.pendingStoreSeq = 2
		controller.pendingPayload = payload
		controller.completeAccept(rctx)
		assert.Empty(t, controller.pending)
	})

	t.Run("With tick retransmissions", func(t *testing.T) {
		controller := newController(t, nil)
		host := spawnHost(t, "host-tick")

		// a stale generation is ignored
		controller.handshake = producerHandshakeCredit
		stale := &producerControllerTick{generation: controller.generation + 1}
		controller.handleTick(rctxFor(system.NoSender(), host, stale), stale)

		// the StoredAck phase re-tells the retained Stored
		stored, err := newStoredFromState(controller.sessionID, "tok-1", "m-1", 1, producer, host)
		require.NoError(t, err)
		controller.handshake = producerHandshakeStoredAck
		controller.storedMessage = stored

		before := countRecorded(t, producer, func(message any) bool {
			_, ok := message.(*Stored)
			return ok
		})

		tick := &producerControllerTick{generation: controller.generation}
		controller.handleTick(rctxFor(system.NoSender(), host, tick), tick)

		require.Eventually(t, func() bool {
			return countRecorded(t, producer, func(message any) bool {
				_, ok := message.(*Stored)
				return ok
			}) > before
		}, 3*time.Second, 20*time.Millisecond)
	})

	t.Run("With terminated peers", func(t *testing.T) {
		controller := newController(t, nil)
		host := spawnHost(t, "host-terminated-producer")

		// the producer's death stops the controller host
		terminated := NewTerminated(producer.Path())
		controller.handleTerminated(rctxFor(system.NoSender(), host, terminated), terminated)

		require.Eventually(t, func() bool {
			return !host.IsRunning()
		}, 3*time.Second, 20*time.Millisecond)

		// a worker companion's death ends only its binding and requeues
		survivor := newController(t, nil)
		survivorHost := spawnHost(t, "host-terminated-worker")
		survivor.bindings["jobs-worker-edge"] = &bindingWork{
			endpointName: "jobs-worker-edge",
			controller:   workerStandIn,
			unconfirmed:  []dispatchedWork{{messageID: "job-1", workerSeq: 1, storeSeq: 1, payload: payload}},
		}
		survivor.bindingOrder = []string{"jobs-worker-edge"}

		workerGone := NewTerminated(workerStandIn.Path())
		survivor.handleTerminated(rctxFor(system.NoSender(), survivorHost, workerGone), workerGone)

		assert.Empty(t, survivor.bindings)
		require.Len(t, survivor.pending, 1)
		assert.Equal(t, "job-1", survivor.pending[0].messageID)
		assert.True(t, survivorHost.IsRunning())
	})

	t.Run("With delivery confirmation notices", func(t *testing.T) {
		controller := newController(t, nil)
		controller.deliveryConfirmation = true
		host := spawnHost(t, "host-confirmation")
		rctx := rctxFor(system.NoSender(), host, &PostStart{})

		before := countRecorded(t, producer, func(message any) bool {
			_, ok := message.(*DeliveryConfirmed)
			return ok
		})

		controller.sendConfirmation(rctx, []dispatchedWork{{messageID: "job-1", workerSeq: 1, storeSeq: 7, payload: payload}})

		require.Eventually(t, func() bool {
			return countRecorded(t, producer, func(message any) bool {
				notice, ok := message.(*DeliveryConfirmed)
				return ok && notice.MessageID() == "job-1" && notice.Seq() == 7
			}) > before
		}, 3*time.Second, 20*time.Millisecond)

		// an impossible notice is skipped without terminating
		controller.sessionID = ""
		assert.NotPanics(t, func() {
			controller.sendConfirmation(rctx, []dispatchedWork{{messageID: "job-2", workerSeq: 2, storeSeq: 8, payload: payload}})
		})
		assert.False(t, controller.failed)
	})

	t.Run("With queue results fenced by the lane", func(t *testing.T) {
		controller := newController(t, &mockDurableWorkQueue{})
		host := spawnHost(t, "host-lane-fence")
		rctx := rctxFor(system.NoSender(), host, &PostStart{})

		// no operation in flight: the result is stale
		controller.handleQueueOpResult(rctx, &queueOpResult{sessionID: controller.sessionID, operationID: 1, kind: queueOpStore})
		assert.Equal(t, producerHandshakeIdle, controller.handshake)

		// a mismatched operation ID is stale
		controller.opInFlight = true
		controller.nextOperationID = 2
		controller.handleQueueOpResult(rctx, &queueOpResult{sessionID: controller.sessionID, operationID: 1, kind: queueOpStore})
		assert.True(t, controller.opInFlight)
		controller.opInFlight = false
	})

	t.Run("With queue failure classification", func(t *testing.T) {
		stages := []struct {
			kind  int
			cause error
			stage ReliableDeliveryStage
		}{
			{kind: queueOpStore, cause: gerrors.ErrQueueFenced, stage: ReliableDeliveryStageStore},
			{kind: queueOpAccept, cause: gerrors.ErrQueueConflict, stage: ReliableDeliveryStageAccept},
			{kind: queueOpConfirmMessage, cause: gerrors.ErrQueueFenced, stage: ReliableDeliveryStageConfirm},
		}

		for index, entry := range stages {
			controller := newController(t, &mockDurableWorkQueue{})
			host := spawnHost(t, fmt.Sprintf("host-queue-failure-%d", index))
			controller.opInFlight = true
			controller.nextOperationID = 1

			subscriber, err := system.Subscribe()
			require.NoError(t, err)

			controller.handleQueueOpResult(rctxFor(system.NoSender(), host, &PostStart{}), &queueOpResult{sessionID: controller.sessionID, operationID: 1, kind: entry.kind, err: entry.cause})

			failure := awaitFailure(t, subscriber)
			assert.Equal(t, entry.stage, failure.Stage())
			assert.ErrorIs(t, failure.Err(), entry.cause)
		}

		// a retryable backend error escalates for a supervised restart
		controller := newController(t, &mockDurableWorkQueue{})
		host := spawnHost(t, "host-queue-retryable")
		controller.opInFlight = true
		controller.nextOperationID = 1

		rctx := rctxFor(system.NoSender(), host, &PostStart{})
		controller.handleQueueOpResult(rctx, &queueOpResult{sessionID: controller.sessionID, operationID: 1, kind: queueOpStore, err: errors.New("backend down")})

		require.Error(t, rctx.err)
		assert.ErrorIs(t, rctx.err, gerrors.ErrReliableStore)
		assert.False(t, controller.failed)
	})

	t.Run("With endBinding tolerating unknown workers", func(t *testing.T) {
		controller := newController(t, nil)
		host := spawnHost(t, "host-endbinding-unknown")

		assert.NotPanics(t, func() {
			controller.endBinding(rctxFor(system.NoSender(), host, &PostStart{}), "ghost-worker", "test")
		})
	})

	t.Run("With terminate publishing once", func(t *testing.T) {
		controller := newController(t, nil)
		host := spawnHost(t, "host-terminate-once")

		subscriber, err := system.Subscribe()
		require.NoError(t, err)

		rctx := rctxFor(system.NoSender(), host, &PostStart{})
		controller.terminate(rctx, ReliableDeliveryStageProtocol, errors.New("first"))
		require.True(t, controller.failed)

		failure := awaitFailure(t, subscriber)
		assert.ErrorContains(t, failure.Err(), "first")

		// the second violation is absorbed by the single-shot flag
		assert.NotPanics(t, func() {
			controller.terminate(rctx, ReliableDeliveryStageProtocol, errors.New("second"))
		})
	})

	t.Run("With a lost tell logged", func(t *testing.T) {
		controller := newController(t, nil)
		host := spawnHost(t, "host-lost-tell")

		dead, err := system.Spawn(ctx, "soon-dead", &deliveryRecorder{})
		require.NoError(t, err)
		require.NoError(t, dead.Shutdown(ctx))

		assert.NotPanics(t, func() {
			controller.tell(rctxFor(system.NoSender(), host, &PostStart{}), dead, uuid.NewString())
		})
	})
}
