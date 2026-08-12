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

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	dynaport "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// newRemotingOnlySystem starts a cluster-disabled, remoting-enabled actor
// system bound to the given local port and stops it when the test finishes.
func newRemotingOnlySystem(t *testing.T, ctx context.Context, name string, port int) *actorSystem {
	system, err := NewActorSystem(name,
		WithLogger(log.DiscardLogger),
		WithRemote(remote.NewConfig("127.0.0.1", port)))
	require.NoError(t, err)
	require.NoError(t, system.Start(ctx))

	t.Cleanup(func() {
		assert.NoError(t, system.Stop(context.WithoutCancel(ctx)))
	})

	return system.(*actorSystem)
}

func TestReliablePeerTopologyGuard(t *testing.T) {
	t.Run("peer address without remoting is rejected at spawn", func(t *testing.T) {
		ctx := context.TODO()

		system, err := NewActorSystem("no-remoting", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, system.Start(ctx))
		t.Cleanup(func() {
			assert.NoError(t, system.Stop(ctx))
		})

		pid, err := system.Spawn(ctx, "orders-producer", &reliableProducerMock{},
			AsReliableProducer("orders-consumer", WithReliableRemoteConsumer("127.0.0.1", 2280)))
		require.ErrorIs(t, err, gerrors.ErrReliablePeerRemotingRequired)
		assert.Nil(t, pid)

		pid, err = system.Spawn(ctx, "orders-consumer", &reliableConsumerMock{},
			AsReliableConsumer("orders-producer", WithReliableRemoteProducer("127.0.0.1", 2280)))
		require.ErrorIs(t, err, gerrors.ErrReliablePeerRemotingRequired)
		assert.Nil(t, pid)
	})

	t.Run("peer address with remote placement is rejected", func(t *testing.T) {
		// a remote-placement spawn on a clustered caller must reject the peer
		// address instead of silently dropping it from the placement wire
		ctx := context.TODO()
		ports := dynaport.Get(1)

		system := newRemotingOnlySystem(t, ctx, "placement-node", ports[0])
		system.clusterEnabled.Store(true)
		t.Cleanup(func() {
			system.clusterEnabled.Store(false)
		})

		pid, err := system.Spawn(ctx, "orders-producer", &reliableProducerMock{},
			AsReliableProducer("orders-consumer", WithReliableRemoteConsumer("127.0.0.1", 2280)),
			WithHostAndPort("127.0.0.1", ports[0]))
		require.ErrorIs(t, err, gerrors.ErrReliablePeerClusterConflict)
		assert.Nil(t, pid)
	})

	t.Run("peer address with clustering is rejected", func(t *testing.T) {
		system, err := NewActorSystem("cluster-conflict", WithLogger(log.DiscardLogger))
		require.NoError(t, err)

		sys := system.(*actorSystem)
		sys.remotingEnabled.Store(true)
		sys.clusterEnabled.Store(true)

		config := producerDeliveryConfig("orders-consumer")
		config.producer.consumerAddress = &reliablePeerAddress{host: "127.0.0.1", port: 2280}
		require.ErrorIs(t, sys.rejectReliablePeerTopology(config), gerrors.ErrReliablePeerClusterConflict)

		consumer := consumerDeliveryConfig("orders-producer")
		consumer.consumer.producerAddress = &reliablePeerAddress{host: "127.0.0.1", port: 2280}
		require.ErrorIs(t, sys.rejectReliablePeerTopology(consumer), gerrors.ErrReliablePeerClusterConflict)

		// a flow without a peer address keeps the registry authority untouched
		require.NoError(t, sys.rejectReliablePeerTopology(producerDeliveryConfig("orders-consumer")))
	})
}

func TestGetReliableCompanionHandler(t *testing.T) {
	ctx := context.TODO()
	ports := dynaport.Get(1)
	port := ports[0]

	system := newRemotingOnlySystem(t, ctx, "handler-node", port)

	producer, err := system.Spawn(ctx, "orders-producer", &reliableProducerMock{},
		AsReliableProducer("orders-consumer"))
	require.NoError(t, err)

	request := func(endpointName string, role internalpb.ReliableControllerRole) *internalpb.GetReliableCompanionRequest {
		return internalpb.GetReliableCompanionRequest_builder{
			Host:         "127.0.0.1",
			Port:         int32(port),
			EndpointName: endpointName,
			Role:         role,
		}.Build()
	}

	t.Run("resolves the validated companion of a local endpoint", func(t *testing.T) {
		resp, err := system.getReliableCompanionHandler(ctx, nullConn, request("orders-producer", internalpb.ReliableControllerRole_RELIABLE_CONTROLLER_ROLE_PRODUCER))
		require.NoError(t, err)

		companionResp, ok := resp.(*internalpb.GetReliableCompanionResponse)
		require.True(t, ok)

		companion, err := system.resolveLocalReliableCompanion("orders-producer", ReliableControllerRoleProducer)
		require.NoError(t, err)
		assert.Equal(t, companion.ID(), companionResp.GetAddress())
		assert.Equal(t, reliableCompanionName(ReliableControllerRoleProducer, producer.IncarnationID()), companion.Name())
	})

	t.Run("unknown endpoint returns NOT_FOUND", func(t *testing.T) {
		resp, err := system.getReliableCompanionHandler(ctx, nullConn, request("missing", internalpb.ReliableControllerRole_RELIABLE_CONTROLLER_ROLE_PRODUCER))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_NOT_FOUND)
	})

	t.Run("role without a companion returns NOT_FOUND", func(t *testing.T) {
		resp, err := system.getReliableCompanionHandler(ctx, nullConn, request("orders-producer", internalpb.ReliableControllerRole_RELIABLE_CONTROLLER_ROLE_CONSUMER))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_NOT_FOUND)
	})

	t.Run("companion name probed directly returns NOT_FOUND", func(t *testing.T) {
		name := reliableCompanionName(ReliableControllerRoleProducer, producer.IncarnationID())
		resp, err := system.getReliableCompanionHandler(ctx, nullConn, request(name, internalpb.ReliableControllerRole_RELIABLE_CONTROLLER_ROLE_PRODUCER))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_NOT_FOUND)
	})

	t.Run("unspecified role returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		resp, err := system.getReliableCompanionHandler(ctx, nullConn, request("orders-producer", internalpb.ReliableControllerRole_RELIABLE_CONTROLLER_ROLE_UNSPECIFIED))
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("mismatched host returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		req := request("orders-producer", internalpb.ReliableControllerRole_RELIABLE_CONTROLLER_ROLE_PRODUCER)
		req.SetHost("10.0.0.9")
		resp, err := system.getReliableCompanionHandler(ctx, nullConn, req)
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("invalid request type returns CODE_INVALID_ARGUMENT", func(t *testing.T) {
		resp, err := system.getReliableCompanionHandler(ctx, nullConn, &internalpb.RemoteLookupRequest{})
		require.NoError(t, err)
		requireProtoError(t, resp, internalpb.Code_CODE_INVALID_ARGUMENT)
	})

	t.Run("client round trip resolves and misses", func(t *testing.T) {
		addr, err := system.getRemoting().GetReliableCompanion(ctx, "127.0.0.1", port, "orders-producer", internalpb.ReliableControllerRole_RELIABLE_CONTROLLER_ROLE_PRODUCER)
		require.NoError(t, err)
		assert.Equal(t, reliableCompanionName(ReliableControllerRoleProducer, producer.IncarnationID()), addr.Name())

		missing, err := system.getRemoting().GetReliableCompanion(ctx, "127.0.0.1", port, "missing", internalpb.ReliableControllerRole_RELIABLE_CONTROLLER_ROLE_PRODUCER)
		require.NoError(t, err)
		assert.True(t, missing.Equals(address.NoSender()))
	})
}

func TestReliableDeliveryRemotingOnlyFlow(t *testing.T) {
	ctx := context.TODO()
	ports := dynaport.Get(2)
	producerPort, consumerPort := ports[0], ports[1]

	producerNode := newRemotingOnlySystem(t, ctx, "producer-node", producerPort)
	consumerNode := newRemotingOnlySystem(t, ctx, "consumer-node", consumerPort)

	producer, err := producerNode.Spawn(ctx, "orders-producer", &reliableProducerMock{},
		AsReliableProducer("orders-consumer",
			WithReliableRetryInterval(200*time.Millisecond),
			WithReliableRemoteConsumer("127.0.0.1", consumerPort)))
	require.NoError(t, err)

	// an order submitted before the consumer exists stays queued: peer
	// resolution fails transiently and the recurring tick recovers once the
	// consumer endpoint appears on the addressed node
	require.NoError(t, Tell(ctx, producer, &produceSubmission{messageID: "ord-1", payload: testpb.Reply_builder{Content: "ord-1"}.Build()}))

	consumer, err := consumerNode.Spawn(ctx, "orders-consumer", &reliableConsumerMock{autoConfirm: true},
		AsReliableConsumer("orders-producer",
			WithReliableResendInterval(200*time.Millisecond),
			WithReliableRemoteProducer("127.0.0.1", producerPort)))
	require.NoError(t, err)

	for i := 2; i <= 3; i++ {
		id := fmt.Sprintf("ord-%d", i)
		require.NoError(t, Tell(ctx, producer, &produceSubmission{messageID: id, payload: testpb.Reply_builder{Content: id}.Build()}))
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

	// each side resolves its peer's controller as a remote PID through the
	// explicitly addressed node, without any registry
	resolved, err := producerNode.resolveReliableCompanion(ctx, "orders-consumer", ReliableControllerRoleConsumer, &reliablePeerAddress{host: "127.0.0.1", port: consumerPort})
	require.NoError(t, err)
	assert.True(t, resolved.IsRemote())

	resolved, err = consumerNode.resolveReliableCompanion(ctx, "orders-producer", ReliableControllerRoleProducer, &reliablePeerAddress{host: "127.0.0.1", port: producerPort})
	require.NoError(t, err)
	assert.True(t, resolved.IsRemote())
}

func TestReliableDeliveryRemotingOnlyConsumerRestartResync(t *testing.T) {
	ctx := context.TODO()
	ports := dynaport.Get(2)
	producerPort, consumerPort := ports[0], ports[1]

	producerNode := newRemotingOnlySystem(t, ctx, "producer-node", producerPort)
	consumerNode := newRemotingOnlySystem(t, ctx, "consumer-node", consumerPort)

	producer, err := producerNode.Spawn(ctx, "orders-producer", &reliableProducerMock{},
		AsReliableProducer("orders-consumer",
			WithReliableRetryInterval(200*time.Millisecond),
			WithReliableRemoteConsumer("127.0.0.1", consumerPort)))
	require.NoError(t, err)

	consumerOptions := []SpawnOption{
		AsReliableConsumer("orders-producer",
			WithReliableResendInterval(200*time.Millisecond),
			WithReliableRemoteProducer("127.0.0.1", producerPort)),
	}

	consumer, err := consumerNode.Spawn(ctx, "orders-consumer", &reliableConsumerMock{autoConfirm: true}, consumerOptions...)
	require.NoError(t, err)

	require.NoError(t, Tell(ctx, producer, &produceSubmission{messageID: "ord-1", payload: testpb.Reply_builder{Content: "ord-1"}.Build()}))

	deliveries := awaitDeliveries(t, ctx, consumer, 1)
	require.Len(t, deliveries, 1)

	// restarting the consumer endpoint creates a new incarnation with a new
	// companion identity; the producer must fence the fresh registration
	// through the peer-resolution RPC and resume delivery
	require.NoError(t, consumer.Shutdown(ctx))
	pause.For(time.Second)

	restarted, err := consumerNode.Spawn(ctx, "orders-consumer", &reliableConsumerMock{autoConfirm: true}, consumerOptions...)
	require.NoError(t, err)

	require.NoError(t, Tell(ctx, producer, &produceSubmission{messageID: "ord-2", payload: testpb.Reply_builder{Content: "ord-2"}.Build()}))

	redelivered := awaitDeliveries(t, ctx, restarted, 1)
	require.Len(t, redelivered, 1)
	assert.Equal(t, "ord-2", redelivered[0].MessageID())
}

func TestReliableDeliveryRemotingOnlyUnreachablePeerRetries(t *testing.T) {
	ctx := context.TODO()
	ports := dynaport.Get(2)
	producerPort, consumerPort := ports[0], ports[1]

	consumerNode := newRemotingOnlySystem(t, ctx, "consumer-node", consumerPort)

	// the producer node is not up yet: registration attempts fail transiently
	// while the consumer endpoint stays alive and keeps retrying on tick
	consumer, err := consumerNode.Spawn(ctx, "orders-consumer", &reliableConsumerMock{autoConfirm: true},
		AsReliableConsumer("orders-producer",
			WithReliableResendInterval(200*time.Millisecond),
			WithReliableRemoteProducer("127.0.0.1", producerPort)))
	require.NoError(t, err)

	pause.For(time.Second)
	require.True(t, consumer.IsRunning())

	producerNode := newRemotingOnlySystem(t, ctx, "producer-node", producerPort)

	producer, err := producerNode.Spawn(ctx, "orders-producer", &reliableProducerMock{},
		AsReliableProducer("orders-consumer",
			WithReliableRetryInterval(200*time.Millisecond),
			WithReliableRemoteConsumer("127.0.0.1", consumerPort)))
	require.NoError(t, err)

	require.NoError(t, Tell(ctx, producer, &produceSubmission{messageID: "ord-1", payload: testpb.Reply_builder{Content: "ord-1"}.Build()}))

	deliveries := awaitDeliveries(t, ctx, consumer, 1)
	require.Len(t, deliveries, 1)
	assert.Equal(t, "ord-1", deliveries[0].MessageID())
}
