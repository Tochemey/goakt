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

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/passivation"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// newCompanionTestSystem starts a cluster-disabled actor system for
// companion-resolution tests and stops it when the test finishes.
func newCompanionTestSystem(t *testing.T) (context.Context, *actorSystem) {
	t.Helper()

	ctx := context.TODO()
	system, err := NewActorSystem("companionTest", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, system.Start(ctx))

	t.Cleanup(func() {
		require.NoError(t, system.Stop(context.WithoutCancel(ctx)))
	})

	return ctx, system.(*actorSystem)
}

func TestReliableCompanionName(t *testing.T) {
	incarnationID := uuid.NewString()

	assert.Equal(t, reliableProducerControllerNamePrefix+incarnationID, reliableCompanionName(ReliableControllerRoleProducer, incarnationID))
	assert.Equal(t, reliableConsumerControllerNamePrefix+incarnationID, reliableCompanionName(ReliableControllerRoleConsumer, incarnationID))
	assert.Empty(t, reliableCompanionName(reliableControllerRoleUnknown, incarnationID))
	assert.True(t, isReliableDeliveryControllerName(reliableCompanionName(ReliableControllerRoleProducer, incarnationID)))
	assert.True(t, isReliableDeliveryControllerName(reliableCompanionName(ReliableControllerRoleConsumer, incarnationID)))
	assert.True(t, isSystemName(reliableCompanionName(ReliableControllerRoleProducer, incarnationID)))
}

func TestNewReliableCompanionSpec(t *testing.T) {
	t.Run("With valid inputs", func(t *testing.T) {
		spec, err := newReliableCompanionSpec(ReliableControllerRoleProducer, "endpoint", uuid.NewString())
		require.NoError(t, err)
		require.NotNil(t, spec)
		assert.Equal(t, ReliableControllerRoleProducer, spec.role)
		assert.Equal(t, "endpoint", spec.endpointName)
	})

	t.Run("With unsupported role", func(t *testing.T) {
		spec, err := newReliableCompanionSpec(reliableControllerRoleUnknown, "endpoint", uuid.NewString())
		require.Error(t, err)
		assert.Nil(t, spec)
	})

	t.Run("With blank endpoint name", func(t *testing.T) {
		spec, err := newReliableCompanionSpec(ReliableControllerRoleConsumer, "  ", uuid.NewString())
		require.Error(t, err)
		assert.Nil(t, spec)
	})

	t.Run("With invalid incarnation ID", func(t *testing.T) {
		spec, err := newReliableCompanionSpec(ReliableControllerRoleConsumer, "endpoint", "not-a-uuid")
		require.Error(t, err)
		assert.Nil(t, spec)
	})
}

func TestReliableCompanionSpecWireRoundTrip(t *testing.T) {
	t.Run("With a valid spec", func(t *testing.T) {
		spec, err := newReliableCompanionSpec(ReliableControllerRoleProducer, "endpoint", uuid.NewString())
		require.NoError(t, err)

		restored, err := reliableCompanionSpecFromProto(spec.toProto())
		require.NoError(t, err)
		assert.Equal(t, spec, restored)
	})

	t.Run("With a nil spec", func(t *testing.T) {
		var spec *reliableCompanionSpec
		assert.Nil(t, spec.toProto())

		restored, err := reliableCompanionSpecFromProto(nil)
		require.Error(t, err)
		assert.Nil(t, restored)
	})

	t.Run("With an unspecified role", func(t *testing.T) {
		spec, err := newReliableCompanionSpec(ReliableControllerRoleConsumer, "endpoint", uuid.NewString())
		require.NoError(t, err)

		tampered := spec.toProto()
		tampered.Role = 0

		restored, err := reliableCompanionSpecFromProto(tampered)
		require.Error(t, err)
		assert.Nil(t, restored)
	})

	t.Run("With a tampered incarnation", func(t *testing.T) {
		spec, err := newReliableCompanionSpec(ReliableControllerRoleConsumer, "endpoint", uuid.NewString())
		require.NoError(t, err)

		tampered := spec.toProto()
		tampered.EndpointIncarnationId = "not-a-uuid"

		restored, err := reliableCompanionSpecFromProto(tampered)
		require.Error(t, err)
		assert.Nil(t, restored)
	})
}

func TestResolveReliableCompanion(t *testing.T) {
	t.Run("With cluster-disabled local resolution", func(t *testing.T) {
		ctx, system := newCompanionTestSystem(t)

		endpoint, err := system.Spawn(ctx, "endpoint", NewMockActor())
		require.NoError(t, err)

		spec, err := newReliableCompanionSpec(ReliableControllerRoleProducer, "endpoint", endpoint.IncarnationID())
		require.NoError(t, err)

		companionName := reliableCompanionName(ReliableControllerRoleProducer, endpoint.IncarnationID())
		companion, err := system.Spawn(ctx, companionName, NewMockActor(), asSystem(), asReliableCompanion(spec))
		require.NoError(t, err)

		resolved, err := system.resolveReliableCompanion(ctx, "endpoint", ReliableControllerRoleProducer)
		require.NoError(t, err)
		assert.True(t, companion.Equals(resolved))
	})

	t.Run("With unsupported role", func(t *testing.T) {
		ctx, system := newCompanionTestSystem(t)

		resolved, err := system.resolveReliableCompanion(ctx, "endpoint", reliableControllerRoleUnknown)
		require.Error(t, err)
		assert.Nil(t, resolved)
		assert.NotErrorIs(t, err, errReliableCompanionUnavailable)
	})

	t.Run("With missing endpoint", func(t *testing.T) {
		ctx, system := newCompanionTestSystem(t)

		resolved, err := system.resolveReliableCompanion(ctx, "endpoint", ReliableControllerRoleProducer)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.Nil(t, resolved)
	})

	t.Run("With missing companion", func(t *testing.T) {
		ctx, system := newCompanionTestSystem(t)

		_, err := system.Spawn(ctx, "endpoint", NewMockActor())
		require.NoError(t, err)

		resolved, err := system.resolveReliableCompanion(ctx, "endpoint", ReliableControllerRoleConsumer)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.Nil(t, resolved)
	})

	t.Run("With unmarked companion", func(t *testing.T) {
		ctx, system := newCompanionTestSystem(t)

		endpoint, err := system.Spawn(ctx, "endpoint", NewMockActor())
		require.NoError(t, err)

		companionName := reliableCompanionName(ReliableControllerRoleProducer, endpoint.IncarnationID())
		_, err = system.Spawn(ctx, companionName, NewMockActor(), asSystem())
		require.NoError(t, err)

		resolved, err := system.resolveReliableCompanion(ctx, "endpoint", ReliableControllerRoleProducer)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.Nil(t, resolved)
	})

	t.Run("With mismatched role metadata", func(t *testing.T) {
		ctx, system := newCompanionTestSystem(t)

		endpoint, err := system.Spawn(ctx, "endpoint", NewMockActor())
		require.NoError(t, err)

		spec, err := newReliableCompanionSpec(ReliableControllerRoleConsumer, "endpoint", endpoint.IncarnationID())
		require.NoError(t, err)

		companionName := reliableCompanionName(ReliableControllerRoleProducer, endpoint.IncarnationID())
		_, err = system.Spawn(ctx, companionName, NewMockActor(), asSystem(), asReliableCompanion(spec))
		require.NoError(t, err)

		resolved, err := system.resolveReliableCompanion(ctx, "endpoint", ReliableControllerRoleProducer)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.Nil(t, resolved)
	})

	t.Run("With foreign endpoint metadata", func(t *testing.T) {
		ctx, system := newCompanionTestSystem(t)

		endpoint, err := system.Spawn(ctx, "endpoint", NewMockActor())
		require.NoError(t, err)

		spec, err := newReliableCompanionSpec(ReliableControllerRoleProducer, "other", endpoint.IncarnationID())
		require.NoError(t, err)

		companionName := reliableCompanionName(ReliableControllerRoleProducer, endpoint.IncarnationID())
		_, err = system.Spawn(ctx, companionName, NewMockActor(), asSystem(), asReliableCompanion(spec))
		require.NoError(t, err)

		resolved, err := system.resolveReliableCompanion(ctx, "endpoint", ReliableControllerRoleProducer)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.Nil(t, resolved)
	})

	t.Run("With stale incarnation metadata", func(t *testing.T) {
		ctx, system := newCompanionTestSystem(t)

		endpoint, err := system.Spawn(ctx, "endpoint", NewMockActor())
		require.NoError(t, err)

		spec, err := newReliableCompanionSpec(ReliableControllerRoleProducer, "endpoint", uuid.NewString())
		require.NoError(t, err)

		companionName := reliableCompanionName(ReliableControllerRoleProducer, endpoint.IncarnationID())
		_, err = system.Spawn(ctx, companionName, NewMockActor(), asSystem(), asReliableCompanion(spec))
		require.NoError(t, err)

		resolved, err := system.resolveReliableCompanion(ctx, "endpoint", ReliableControllerRoleProducer)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.Nil(t, resolved)
	})

	t.Run("With stopped companion", func(t *testing.T) {
		ctx, system := newCompanionTestSystem(t)

		endpoint, err := system.Spawn(ctx, "endpoint", NewMockActor())
		require.NoError(t, err)

		spec, err := newReliableCompanionSpec(ReliableControllerRoleProducer, "endpoint", endpoint.IncarnationID())
		require.NoError(t, err)

		companion, err := system.Spawn(ctx, "companionStandIn", NewMockActor(), asReliableCompanion(spec))
		require.NoError(t, err)
		require.NoError(t, companion.Shutdown(ctx))

		err = validateReliableCompanion(endpoint, companion, ReliableControllerRoleProducer)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
	})
}

func TestReliableCompanionHiddenFromPublicAPIs(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)

	endpoint, err := system.Spawn(ctx, "endpoint", NewMockActor())
	require.NoError(t, err)

	spec, err := newReliableCompanionSpec(ReliableControllerRoleConsumer, "endpoint", endpoint.IncarnationID())
	require.NoError(t, err)

	companionName := reliableCompanionName(ReliableControllerRoleConsumer, endpoint.IncarnationID())
	_, err = system.Spawn(ctx, companionName, NewMockActor(), asSystem(), asReliableCompanion(spec))
	require.NoError(t, err)

	_, err = system.ActorOf(ctx, companionName)
	assert.ErrorIs(t, err, gerrors.ErrActorNotFound)

	actors, err := system.Actors(ctx, time.Second)
	require.NoError(t, err)

	for _, pid := range actors {
		assert.NotEqual(t, companionName, pid.Name())
	}

	assert.ErrorIs(t, system.Kill(ctx, companionName), gerrors.ErrActorNotFound)

	_, err = system.ReSpawn(ctx, companionName)
	assert.ErrorIs(t, err, gerrors.ErrActorNotFound)
}

func TestReliableEndpointDefaults(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)

	config := producerDeliveryConfig("consumer")
	endpoint, err := system.Spawn(ctx, "endpoint", NewMockActor(), asReliableEndpoint(config))
	require.NoError(t, err)

	assert.IsType(t, new(passivation.LongLivedStrategy), endpoint.passivationStrategy)
	assert.Equal(t, config, endpoint.reliableDelivery)
	assert.NotSame(t, config, endpoint.reliableDelivery)

	// the spawn transaction created the controller companion automatically
	companion, err := system.resolveReliableCompanion(ctx, "endpoint", ReliableControllerRoleProducer)
	require.NoError(t, err)

	assert.IsType(t, new(passivation.LongLivedStrategy), companion.passivationStrategy)

	// both stay on their spawn node until relocation reconstruction lands
	assert.False(t, endpoint.IsRelocatable())
	assert.False(t, companion.IsRelocatable())
}

// produceSubmission commands the reliable producer mock to submit one
// application message through its controller.
type produceSubmission struct {
	messageID string
	payload   any
}

// reliableProducerMock is a producer endpoint that answers the controller
// handshake the way a real application producer would: it queues submissions,
// spends one RequestNext grant per submission, idempotently resends the same
// Produced when a grant is retried, and acknowledges Stored. All state lives
// in the actor and is only touched inside its own mailbox turns.
type reliableProducerMock struct {
	controller   *PID
	request      *RequestNext
	pending      []*produceSubmission
	lastToken    string
	lastProduced *Produced
}

func (x *reliableProducerMock) PreStart(*Context) error { return nil }
func (x *reliableProducerMock) PostStop(*Context) error { return nil }

func (x *reliableProducerMock) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *PostStart:
	case *RequestNext:
		if !msg.IsAuthorizedFor(ctx.Self(), ctx.Sender()) {
			return
		}

		x.controller = ctx.Sender()

		if msg.Token() == x.lastToken && x.lastProduced != nil {
			ctx.Tell(x.controller, x.lastProduced)
			return
		}

		x.request = msg
		x.flush(ctx)
	case *Stored:
		ack, err := NewStoredAck(msg)
		if err != nil {
			ctx.Err(err)
			return
		}

		ctx.Tell(ctx.Sender(), ack)
	case *produceSubmission:
		x.pending = append(x.pending, msg)
		x.flush(ctx)
	default:
		ctx.Unhandled()
	}
}

// flush spends the held grant on the oldest queued submission.
func (x *reliableProducerMock) flush(ctx *ReceiveContext) {
	if x.request == nil || len(x.pending) == 0 {
		return
	}

	submission := x.pending[0]
	produced, err := NewProduced(x.request, submission.messageID, submission.payload)
	if err != nil {
		ctx.Err(err)
		return
	}

	x.pending = x.pending[1:]
	x.lastToken = x.request.Token()
	x.lastProduced = produced
	x.request = nil
	ctx.Tell(x.controller, produced)
}

// awaitDeliveries polls the consumer mock until it has recorded at least
// count deliveries and returns them collapsed to their first occurrence per
// sequence, since a slow confirmation legitimately allows a redelivery.
func awaitDeliveries(t *testing.T, ctx context.Context, consumer *PID, count int) []*Delivery {
	t.Helper()

	var distinct []*Delivery

	require.Eventually(t, func() bool {
		response, err := Ask(ctx, consumer, &getDeliveries{}, time.Second)
		if err != nil {
			return false
		}

		recorded, _ := response.([]*Delivery)
		seen := make(map[int64]bool, len(recorded))
		distinct = distinct[:0]

		for _, delivery := range recorded {
			if seen[delivery.Seq()] {
				continue
			}

			seen[delivery.Seq()] = true
			distinct = append(distinct, delivery)
		}

		return len(distinct) >= count
	}, 20*time.Second, 20*time.Millisecond)

	return distinct
}

func TestReliableDeliveryEndToEnd(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)

	producer, err := system.Spawn(ctx, "orders-producer", &reliableProducerMock{}, AsReliableProducer("orders-consumer"))
	require.NoError(t, err)

	consumer, err := system.Spawn(ctx, "orders-consumer", &reliableConsumerMock{autoConfirm: true}, AsReliableConsumer("orders-producer", WithResendInterval(200*time.Millisecond)))
	require.NoError(t, err)

	// both controller companions were created by the spawn transaction
	_, err = system.resolveReliableCompanion(ctx, "orders-producer", ReliableControllerRoleProducer)
	require.NoError(t, err)
	_, err = system.resolveReliableCompanion(ctx, "orders-consumer", ReliableControllerRoleConsumer)
	require.NoError(t, err)

	// ingress stays plain Tell: a message that never becomes Produced is not
	// part of the reliable flow and must not reach the consumer
	require.NoError(t, Tell(ctx, producer, new(testpb.TestSend)))

	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("m-%d", i)
		require.NoError(t, Tell(ctx, producer, &produceSubmission{messageID: id, payload: &testpb.Reply{Content: id}}))
	}

	deliveries := awaitDeliveries(t, ctx, consumer, 3)
	require.Len(t, deliveries, 3)

	for i, delivery := range deliveries {
		id := fmt.Sprintf("m-%d", i+1)
		assert.Equal(t, id, delivery.MessageID())
		assert.Equal(t, int64(i+1), delivery.Seq())

		reply, ok := delivery.Payload().(*testpb.Reply)
		require.True(t, ok)
		assert.Equal(t, id, reply.GetContent())
	}
}

func TestReliableDeliveryEndToEndDurable(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)

	queue := &mockDurableQueue{}
	producer, err := system.Spawn(ctx, "orders-producer", &reliableProducerMock{}, AsReliableProducer("orders-consumer", WithDurableQueue(queue)))
	require.NoError(t, err)

	consumer, err := system.Spawn(ctx, "orders-consumer", &reliableConsumerMock{autoConfirm: true}, AsReliableConsumer("orders-producer", WithResendInterval(200*time.Millisecond)))
	require.NoError(t, err)

	for i := 1; i <= 2; i++ {
		id := fmt.Sprintf("m-%d", i)
		require.NoError(t, Tell(ctx, producer, &produceSubmission{messageID: id, payload: &testpb.Reply{Content: id}}))
	}

	deliveries := awaitDeliveries(t, ctx, consumer, 2)
	require.Len(t, deliveries, 2)

	// every message went through the durable store-accept handshake
	require.Eventually(t, func() bool {
		_, operations, _ := queue.snapshot()
		return len(operations) >= 4
	}, 10*time.Second, 20*time.Millisecond)

	_, operations, _ := queue.snapshot()
	assert.Equal(t, []string{"store:m-1", "accept:m-1", "store:m-2", "accept:m-2"}, operations)
}

func TestReliableEndpointShutdownStopsCompanion(t *testing.T) {
	t.Run("With producer endpoint", func(t *testing.T) {
		ctx, system := newCompanionTestSystem(t)

		producer, err := system.Spawn(ctx, "orders-producer", &reliableProducerMock{}, AsReliableProducer("orders-consumer"))
		require.NoError(t, err)

		companion, err := system.resolveReliableCompanion(ctx, "orders-producer", ReliableControllerRoleProducer)
		require.NoError(t, err)

		require.NoError(t, producer.Shutdown(ctx))
		assert.False(t, companion.IsRunning())

		require.Eventually(t, func() bool {
			_, ok := system.actors.nodeByName(companion.Name())
			return !ok
		}, 3*time.Second, 10*time.Millisecond)
	})

	t.Run("With consumer endpoint", func(t *testing.T) {
		ctx, system := newCompanionTestSystem(t)

		consumer, err := system.Spawn(ctx, "orders-consumer", &reliableConsumerMock{autoConfirm: true}, AsReliableConsumer("orders-producer"))
		require.NoError(t, err)

		companion, err := system.resolveReliableCompanion(ctx, "orders-consumer", ReliableControllerRoleConsumer)
		require.NoError(t, err)

		require.NoError(t, consumer.Shutdown(ctx))
		assert.False(t, companion.IsRunning())

		require.Eventually(t, func() bool {
			_, ok := system.actors.nodeByName(companion.Name())
			return !ok
		}, 3*time.Second, 10*time.Millisecond)
	})
}

func TestReliableEndpointReSpawnRecreatesCompanion(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)

	producer, err := system.Spawn(ctx, "orders-producer", &reliableProducerMock{}, AsReliableProducer("orders-consumer"))
	require.NoError(t, err)

	companion, err := system.resolveReliableCompanion(ctx, "orders-producer", ReliableControllerRoleProducer)
	require.NoError(t, err)

	// simulate the controller's terminal self-stop, which the private stop
	// path permits while the system keeps running
	require.NoError(t, companion.Shutdown(ctx))

	require.Eventually(t, func() bool {
		_, ok := system.actors.nodeByName(companion.Name())
		return !ok
	}, 3*time.Second, 10*time.Millisecond)

	respawned, err := system.ReSpawn(ctx, "orders-producer")
	require.NoError(t, err)
	require.True(t, respawned.Equals(producer))

	recreated, err := system.resolveReliableCompanion(ctx, "orders-producer", ReliableControllerRoleProducer)
	require.NoError(t, err)
	assert.True(t, recreated.IsRunning())
	assert.Equal(t, companion.Name(), recreated.Name())
	assert.NotSame(t, companion, recreated)

	// a live companion is restarted with the endpoint subtree, never duplicated
	respawned, err = system.ReSpawn(ctx, "orders-producer")
	require.NoError(t, err)

	companions := 0

	for _, child := range system.tree().children(respawned) {
		if child.reliableCompanion != nil {
			companions++
		}
	}

	assert.Equal(t, 1, companions)
}

func TestReliableEndpointSpawnRollback(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)

	queue := &mockDurableQueue{loadErr: errors.New("backing store is unreachable")}
	pid, err := system.Spawn(ctx, "orders-producer", &reliableProducerMock{},
		AsReliableProducer("orders-consumer", WithDurableQueue(queue), WithQueueRetry(1, time.Millisecond)))
	require.Error(t, err)
	require.Nil(t, pid)

	// a failed spawn leaves nothing behind: the endpoint record disappears
	// and the same name spawns cleanly afterwards
	require.Eventually(t, func() bool {
		_, ok := system.actors.nodeByName("orders-producer")
		return !ok
	}, 3*time.Second, 10*time.Millisecond)

	fresh, err := system.Spawn(ctx, "orders-producer", &reliableProducerMock{}, AsReliableProducer("orders-consumer"))
	require.NoError(t, err)
	assert.True(t, fresh.IsRunning())
}

func TestReliableEndpointSpawnOnRejected(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)

	pid, err := system.SpawnOn(ctx, "orders-producer", &reliableProducerMock{}, AsReliableProducer("orders-consumer"))
	require.ErrorContains(t, err, "SpawnOn")
	assert.Nil(t, pid)
}

func TestReliableEndpointRemoteSpawnRejected(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)

	system.remotingEnabled.Store(true)
	t.Cleanup(func() { system.remotingEnabled.Store(false) })

	pid, err := system.Spawn(ctx, "orders-producer", &reliableProducerMock{},
		AsReliableProducer("orders-consumer"), WithHostAndPort("127.0.0.1", 8080))
	require.ErrorContains(t, err, "remote node")
	assert.Nil(t, pid)
}

func TestToSerializeCarriesReliableDelivery(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)

	config := producerDeliveryConfig("consumer")
	endpoint, err := system.Spawn(ctx, "endpoint", NewMockActor(), asReliableEndpoint(config))
	require.NoError(t, err)

	// mutating the caller-owned configuration must not leak into the PID snapshot
	config.producer.consumerName = "changed"

	serialized, err := endpoint.toSerialize()
	require.NoError(t, err)

	assert.Equal(t, endpoint.IncarnationID(), serialized.GetIncarnationId())
	assert.Equal(t, "consumer", serialized.GetReliableDelivery().GetProducer().GetConsumerName())

	// the companion record carries the ownership spec cluster resolution
	// validates and is pinned to its node
	companion, err := system.resolveReliableCompanion(ctx, "endpoint", ReliableControllerRoleProducer)
	require.NoError(t, err)

	companionSerialized, err := companion.toSerialize()
	require.NoError(t, err)

	spec := companionSerialized.GetReliableCompanion()
	require.NotNil(t, spec)
	assert.Equal(t, internalpb.ReliableControllerRole_RELIABLE_CONTROLLER_ROLE_PRODUCER, spec.GetRole())
	assert.Equal(t, "endpoint", spec.GetEndpointName())
	assert.Equal(t, endpoint.IncarnationID(), spec.GetEndpointIncarnationId())
	assert.False(t, companionSerialized.GetRelocatable())

	plain, err := system.Spawn(ctx, "plain", NewMockActor())
	require.NoError(t, err)

	plainSerialized, err := plain.toSerialize()
	require.NoError(t, err)
	assert.Nil(t, plainSerialized.GetReliableDelivery())
	assert.Nil(t, plainSerialized.GetReliableCompanion())
	assert.Equal(t, plain.IncarnationID(), plainSerialized.GetIncarnationId())
}
