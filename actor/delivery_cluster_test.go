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
	"google.golang.org/protobuf/proto"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// newReliableClusterFixture starts a three-node NATS-backed cluster for
// reliable-delivery tests and stops every node when the test finishes. Three
// nodes place the endpoint pair on two members with one uninvolved member, so
// registry records regularly live on partitions owned by nodes that host
// neither endpoint.
func newReliableClusterFixture(t *testing.T) (context.Context, []*actorSystem) {
	t.Helper()

	ctx := context.TODO()
	server := startNatsServer(t)
	built, providers := testNATsConcurrent(t, server.Addr().String(), 3)

	systems := make([]*actorSystem, len(built))

	for i, system := range built {
		systems[i] = system.(*actorSystem)
	}

	// let membership settle before tests place actors
	pause.For(time.Second)

	t.Cleanup(func() {
		for i, system := range built {
			assert.NoError(t, system.Stop(context.WithoutCancel(ctx)))
			assert.NoError(t, providers[i].Close())
		}

		server.Shutdown()
	})

	return ctx, systems
}

func TestReliableDeliveryClusterFlow(t *testing.T) {
	ctx, systems := newReliableClusterFixture(t)
	node1, node2, node3 := systems[0], systems[1], systems[2]

	producer, err := node1.Spawn(ctx, "orders-producer", &reliableProducerMock{},
		AsReliableProducer("orders-consumer", WithLocalRetryInterval(200*time.Millisecond)))
	require.NoError(t, err)

	// an order submitted before the consumer exists stays queued: the
	// producer controller drops registrations while the consumer endpoint is
	// unresolvable, and the recurring tick recovers once it appears
	require.NoError(t, Tell(ctx, producer, &produceSubmission{messageID: "ord-1", payload: &testpb.Reply{Content: "ord-1"}}))

	consumer, err := node2.Spawn(ctx, "orders-consumer", &reliableConsumerMock{autoConfirm: true},
		AsReliableConsumer("orders-producer", WithResendInterval(200*time.Millisecond)))
	require.NoError(t, err)

	for i := 2; i <= 3; i++ {
		id := fmt.Sprintf("ord-%d", i)
		require.NoError(t, Tell(ctx, producer, &produceSubmission{messageID: id, payload: &testpb.Reply{Content: id}}))
	}

	deliveries := awaitDeliveries(t, ctx, consumer, 3)
	require.Len(t, deliveries, 3)

	for i, delivery := range deliveries {
		id := fmt.Sprintf("ord-%d", i+1)
		assert.Equal(t, id, delivery.MessageID())
		assert.Equal(t, int64(i+1), delivery.Seq())

		reply, ok := delivery.Payload().(*testpb.Reply)
		require.True(t, ok)
		assert.Equal(t, id, reply.GetContent())
	}

	// an uninvolved node resolves both companions as remote PIDs
	companionName := reliableCompanionName(ReliableControllerRoleProducer, producer.IncarnationID())
	resolved, err := node3.resolveReliableCompanion(ctx, "orders-producer", ReliableControllerRoleProducer)
	require.NoError(t, err)
	assert.True(t, resolved.IsRemote())
	assert.Equal(t, companionName, resolved.Name())

	resolved, err = node3.resolveReliableCompanion(ctx, "orders-consumer", ReliableControllerRoleConsumer)
	require.NoError(t, err)
	assert.True(t, resolved.IsRemote())

	// companions stay invisible to public management APIs cluster-wide
	_, err = node3.ActorOf(ctx, companionName)
	assert.ErrorIs(t, err, gerrors.ErrActorNotFound)

	actors, err := node3.Actors(ctx, time.Second)
	require.NoError(t, err)

	for _, pid := range actors {
		assert.False(t, isSystemName(pid.Name()))
	}

	// endpoint shutdown takes the companion and both registry records with it
	require.NoError(t, producer.Shutdown(ctx))

	require.Eventually(t, func() bool {
		_, endpointErr := node3.getCluster().GetActor(ctx, "orders-producer")
		_, companionErr := node3.getCluster().GetActor(ctx, companionName)
		return errors.Is(endpointErr, cluster.ErrActorNotFound) && errors.Is(companionErr, cluster.ErrActorNotFound)
	}, 10*time.Second, 100*time.Millisecond)
}

func TestReliableCompanionClusterResolution(t *testing.T) {
	ctx, systems := newReliableClusterFixture(t)
	node1, node2, node3 := systems[0], systems[1], systems[2]

	producer, err := node1.Spawn(ctx, "orders-producer", &reliableProducerMock{}, AsReliableProducer("orders-consumer"))
	require.NoError(t, err)

	companionName := reliableCompanionName(ReliableControllerRoleProducer, producer.IncarnationID())
	registry := node2.getCluster()

	var endpointRecord, companionRecord *internalpb.Actor

	require.Eventually(t, func() bool {
		endpointRecord, err = registry.GetActor(ctx, "orders-producer")
		if err != nil {
			return false
		}

		companionRecord, err = registry.GetActor(ctx, companionName)
		return err == nil
	}, 5*time.Second, 100*time.Millisecond)

	t.Run("With a published pair", func(t *testing.T) {
		resolved, err := node2.resolveReliableCompanion(ctx, "orders-producer", ReliableControllerRoleProducer)
		require.NoError(t, err)
		assert.True(t, resolved.IsRemote())
		assert.Equal(t, companionName, resolved.Name())
	})

	t.Run("With a stale companion incarnation", func(t *testing.T) {
		tampered := proto.Clone(companionRecord).(*internalpb.Actor)
		tampered.GetReliableCompanion().EndpointIncarnationId = uuid.NewString()
		require.NoError(t, registry.PutActor(ctx, tampered))

		_, err := node2.resolveReliableCompanion(ctx, "orders-producer", ReliableControllerRoleProducer)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)

		require.NoError(t, registry.PutActor(ctx, companionRecord))
	})

	t.Run("With the companion record missing", func(t *testing.T) {
		require.NoError(t, registry.RemoveActor(ctx, companionName))

		_, err := node2.resolveReliableCompanion(ctx, "orders-producer", ReliableControllerRoleProducer)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)

		require.NoError(t, registry.PutActor(ctx, companionRecord))
	})

	t.Run("With the pair split across nodes", func(t *testing.T) {
		tampered := proto.Clone(companionRecord).(*internalpb.Actor)
		tampered.Address = address.New(companionName, node1.Name(), node3.Host(), node3.Port()).String()
		require.NoError(t, registry.PutActor(ctx, tampered))

		_, err := node2.resolveReliableCompanion(ctx, "orders-producer", ReliableControllerRoleProducer)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)

		require.NoError(t, registry.PutActor(ctx, companionRecord))
	})

	t.Run("With records pointing at this node without a local pair", func(t *testing.T) {
		tamperedEndpoint := proto.Clone(endpointRecord).(*internalpb.Actor)
		tamperedEndpoint.Address = address.New("orders-producer", node1.Name(), node2.Host(), node2.Port()).String()
		tamperedCompanion := proto.Clone(companionRecord).(*internalpb.Actor)
		tamperedCompanion.Address = address.New(companionName, node1.Name(), node2.Host(), node2.Port()).String()
		require.NoError(t, registry.PutActor(ctx, tamperedEndpoint))
		require.NoError(t, registry.PutActor(ctx, tamperedCompanion))

		_, err := node2.resolveReliableCompanion(ctx, "orders-producer", ReliableControllerRoleProducer)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)

		require.NoError(t, registry.PutActor(ctx, endpointRecord))
		require.NoError(t, registry.PutActor(ctx, companionRecord))
	})
}

func TestReliableClusterSpawnRollback(t *testing.T) {
	ctx, systems := newReliableClusterFixture(t)
	node1, node2 := systems[0], systems[1]

	queue := &mockDurableQueue{loadErr: errors.New("backing store is unreachable")}
	pid, err := node1.Spawn(ctx, "orders-producer", &reliableProducerMock{},
		AsReliableProducer("orders-consumer", WithDurableQueue(queue), WithQueueRetry(1, time.Millisecond)))
	require.Error(t, err)
	require.Nil(t, pid)

	// the endpoint record was withdrawn cluster-wide and no companion record leaked
	require.Eventually(t, func() bool {
		_, err := node2.getCluster().GetActor(ctx, "orders-producer")
		return errors.Is(err, cluster.ErrActorNotFound)
	}, 5*time.Second, 100*time.Millisecond)

	records, err := node2.getCluster().Actors(ctx, time.Second)
	require.NoError(t, err)

	for _, record := range records {
		addr, err := address.Parse(record.GetAddress())
		require.NoError(t, err)
		assert.False(t, isReliableDeliveryControllerName(addr.Name()))
	}

	// nothing was left behind: the same name spawns cleanly under the atomic
	// uniqueness write afterwards
	require.Eventually(t, func() bool {
		_, ok := node1.actors.nodeByName("orders-producer")
		return !ok
	}, 5*time.Second, 100*time.Millisecond)

	fresh, err := node1.Spawn(ctx, "orders-producer", &reliableProducerMock{}, AsReliableProducer("orders-consumer"))
	require.NoError(t, err)
	assert.True(t, fresh.IsRunning())

	// a second reliable endpoint under the same name is rejected cluster-wide
	duplicate, err := node2.Spawn(ctx, "orders-producer", &reliableProducerMock{}, AsReliableProducer("orders-consumer"))
	require.Error(t, err)
	assert.Nil(t, duplicate)
}
