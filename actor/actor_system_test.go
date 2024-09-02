/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package actor

import (
	"context"
	"net"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/log"
	mocksdiscovery "github.com/tochemey/goakt/v2/mocks/discovery"
	"github.com/tochemey/goakt/v2/telemetry"
	"github.com/tochemey/goakt/v2/test/data/testpb"
)

func TestEventsSubscriptions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	server := startNatsServer(t)

	node1, sd1 := startNode(t, "node1", server.Addr().String())
	peerAddress1 := node1.PeerAddress()
	require.NotEmpty(t, peerAddress1)
	// create a subscriber to node 1
	subscriber1, err := node1.Subscribe()
	require.NoError(t, err)
	require.NotNil(t, subscriber1)

	node2, sd2 := startNode(t, "node2", server.Addr().String())
	peerAddress2 := node2.PeerAddress()
	require.NotEmpty(t, peerAddress2)

	// create a subscriber to node 2
	subscriber2, err := node2.Subscribe()
	require.NoError(t, err)
	require.NotNil(t, subscriber2)

	// wait for some time
	time.Sleep(time.Second)

	// capture the joins
	var joins []*goaktpb.NodeJoined
	for event := range subscriber1.Iterator() {
		// get the event payload
		payload := event.Payload()
		// only listening to cluster event
		nodeJoined, ok := payload.(*goaktpb.NodeJoined)
		require.True(t, ok)
		joins = append(joins, nodeJoined)
	}

	// assert the joins list
	require.NotEmpty(t, joins)
	require.Len(t, joins, 1)
	require.Equal(t, peerAddress2, joins[0].GetAddress())

	// wait for some time
	time.Sleep(time.Second)

	// stop the node
	require.NoError(t, node1.Unsubscribe(subscriber1))
	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, sd1.Close())

	// wait for some time
	time.Sleep(time.Second)

	var lefts []*goaktpb.NodeLeft
	for event := range subscriber2.Iterator() {
		payload := event.Payload()

		// only listening to cluster event
		nodeLeft, ok := payload.(*goaktpb.NodeLeft)
		require.True(t, ok)
		lefts = append(lefts, nodeLeft)
	}

	require.NotEmpty(t, lefts)
	require.Len(t, lefts, 1)
	require.Equal(t, peerAddress1, lefts[0].GetAddress())

	require.NoError(t, node2.Unsubscribe(subscriber2))

	t.Cleanup(func() {
		assert.NoError(t, node2.Stop(ctx))
		// stop the discovery engines
		assert.NoError(t, sd2.Close())
		// shutdown the nats server gracefully
		server.Shutdown()
	})
}

func TestMetrics(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(r))
	// create an instance of telemetry
	tel := telemetry.New(telemetry.WithMeterProvider(mp))

	actorSystem, _ := NewActorSystem("testSys",
		WithMetric(),
		WithTelemetry(tel),
		WithStash(),
		WithLogger(log.DiscardLogger))
	// start the actor system
	err := actorSystem.Start(ctx)
	assert.NoError(t, err)

	pid, err := actorSystem.Spawn(ctx, "Test", new(testActor))
	assert.NoError(t, err)
	assert.NotNil(t, pid)

	// create a message to send to the test actor
	message := new(testpb.TestTell)
	// send the message to the actor
	err = Tell(ctx, pid, message)
	// perform some assertions
	require.NoError(t, err)

	// Should collect 4 metrics, 3 for the actor and 1 for the actor system
	got := &metricdata.ResourceMetrics{}
	err = r.Collect(ctx, got)
	require.NoError(t, err)
	require.Len(t, got.ScopeMetrics, 1)
	require.Len(t, got.ScopeMetrics[0].Metrics, 6)

	expected := []string{
		"actor_child_count",
		"actor_stash_count",
		"actor_restart_count",
		"actors_count",
		"actor_processed_count",
		"actor_received_duration",
	}
	// sort the array
	sort.Strings(expected)
	// get the metrics names
	actual := make([]string, len(got.ScopeMetrics[0].Metrics))
	for i, metric := range got.ScopeMetrics[0].Metrics {
		actual[i] = metric.Name
	}
	sort.Strings(actual)

	assert.ElementsMatch(t, expected, actual)

	// stop the actor after some time
	time.Sleep(time.Second)

	t.Cleanup(func() {
		err = actorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestNewActorSystemWithDefaults(t *testing.T) {
	t.Parallel()
	actorSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, actorSystem)
	assert.Equal(t, "testSys", actorSystem.Name())
	assert.Empty(t, actorSystem.Actors())
}

func TestNewActorSystem_WhenMissingName_ReturnsError(t *testing.T) {
	t.Parallel()
	actorSystem, err := NewActorSystem("")
	require.Error(t, err)
	assert.Nil(t, actorSystem)
	assert.EqualError(t, err, ErrNameRequired.Error())
}

func TestNewActorSystem_WhenNameIsInvalid_ReturnsError(t *testing.T) {
	t.Parallel()
	actorSystem, err := NewActorSystem("$omeN@me")
	require.Error(t, err)
	assert.Nil(t, actorSystem)
	assert.EqualError(t, err, ErrInvalidActorSystemName.Error())
}

func TestActorSystem_Spawn_WhenNotStarted_ReturnsError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	pid, err := actorSystem.Spawn(ctx, "Test", new(testActor))
	require.Error(t, err)
	assert.EqualError(t, err, ErrActorSystemNotStarted.Error())
	assert.Nil(t, pid)
}

func TestActorSystem_Spawn_WhenStarted_ReturnsNoError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

	// start the actor system
	err := actorSystem.Start(ctx)
	assert.NoError(t, err)

	actor := new(testActor)
	pid, err := actorSystem.Spawn(ctx, "Test", actor)
	assert.NoError(t, err)
	assert.NotNil(t, pid)

	t.Cleanup(func() {
		err = actorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestActorSystem_Spawn_WhenActorAlreadyExists_Returns_NoError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

	// start the actor system
	err := actorSystem.Start(ctx)
	assert.NoError(t, err)

	actor := new(testActor)
	pid, err := actorSystem.Spawn(ctx, "Test", actor)
	assert.NoError(t, err)
	assert.NotNil(t, pid)

	pidCopy, err := actorSystem.Spawn(ctx, "Test", actor)
	assert.NotNil(t, pidCopy)
	assert.NoError(t, err)

	// point to the same memory address
	assert.True(t, pid == pidCopy)
	assert.True(t, pid.Equals(pidCopy))

	t.Cleanup(func() {
		err = actorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestActorSystem_ReSpawn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

	// start the actor system
	err := actorSystem.Start(ctx)
	require.NoError(t, err)

	// create a deadletter subscriber
	consumer, err := actorSystem.Subscribe()
	require.NoError(t, err)
	require.NotNil(t, consumer)

	actorName := "testActor"
	pid, err := actorSystem.Spawn(ctx, actorName, &testActor{})
	require.NoError(t, err)
	require.NotNil(t, pid)

	// send a message to the actor
	reply, err := Ask(ctx, pid, new(testpb.TestAsk), time.Second)
	require.NoError(t, err)
	require.NotNil(t, reply)
	expected := new(testpb.TestAsk)
	require.True(t, proto.Equal(expected, reply))
	require.True(t, pid.IsRunning())

	// wait for a while for the system to stop
	time.Sleep(time.Second)
	// restart the actor
	_, err = actorSystem.ReSpawn(ctx, actorName)
	require.NoError(t, err)

	// wait for the actor to complete start
	time.Sleep(time.Second)
	require.True(t, pid.IsRunning())

	var items []*goaktpb.ActorRestarted
	for message := range consumer.Iterator() {
		payload := message.Payload()
		// only listening to deadletters
		restarted, ok := payload.(*goaktpb.ActorRestarted)
		if ok {
			items = append(items, restarted)
		}
	}

	require.Len(t, items, 1)

	// send a message to the actor
	reply, err = Ask(ctx, pid, new(testpb.TestAsk), time.Second)
	require.NoError(t, err)
	require.NotNil(t, reply)

	t.Cleanup(func() {
		err = actorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestActorSystem_ReSpawn_WhenPreStartReturnsError_ReturnsError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	actorSystem, _ := NewActorSystem("testSys",
		WithLogger(log.DiscardLogger),
		WithExpireActorAfter(time.Minute))

	// start the actor system
	err := actorSystem.Start(ctx)
	require.NoError(t, err)

	actorName := "actor"
	pid, err := actorSystem.Spawn(ctx, actorName, new(preStartActor))
	require.NoError(t, err)
	require.NotNil(t, pid)
	require.True(t, pid.IsRunning())

	// wait for a while for the system to stop
	time.Sleep(time.Second)

	// restart the actor
	pid, err = actorSystem.ReSpawn(ctx, actorName)
	require.Error(t, err)
	require.False(t, pid.IsRunning())

	t.Cleanup(func() {
		err = actorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestActorSystem_ReSpawn_WhenActorNotFound_ReturnsError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	actorSystem, _ := NewActorSystem("testSys",
		WithLogger(log.DiscardLogger),
		WithExpireActorAfter(time.Minute))

	// start the actor system
	err := actorSystem.Start(ctx)
	require.NoError(t, err)

	actorName := "exchanger"
	actorRef, err := actorSystem.Spawn(ctx, actorName, new(testActor))
	require.NoError(t, err)
	require.NotNil(t, actorRef)

	// send a message to the actor
	reply, err := Ask(ctx, actorRef, new(testpb.TestAsk), time.Second)
	require.NoError(t, err)
	require.NotNil(t, reply)
	expected := new(testpb.TestAsk)
	require.True(t, proto.Equal(expected, reply))
	require.True(t, actorRef.IsRunning())

	err = actorSystem.Kill(ctx, actorName)
	require.NoError(t, err)
	// wait for a while for the system to stop
	time.Sleep(time.Second)

	// restart the actor
	_, err = actorSystem.ReSpawn(ctx, actorName)
	require.Error(t, err)

	t.Cleanup(func() {
		err = actorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestActorSystem_ReSpawn_WhenSystemNotStarted_ReturnsError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	_, err := actorSystem.ReSpawn(ctx, "some-actor")
	require.Error(t, err)
	assert.EqualError(t, err, "actor system has not started yet")
}

func TestActorSystem_ActorsCount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	// start the actor system
	err := actorSystem.Start(ctx)
	require.NoError(t, err)

	actorName := "testActor"
	actorRef, err := actorSystem.Spawn(ctx, actorName, new(testActor))
	assert.NoError(t, err)
	assert.NotNil(t, actorRef)

	// wait for the start of the actor to be complete
	time.Sleep(time.Second)

	assert.EqualValues(t, 1, actorSystem.ActorsCount())

	t.Cleanup(func() {
		err = actorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestActorSystem_Kill_WhenSystemNotStarted_ReturnsError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	err := actorSystem.Kill(ctx, "Test")
	require.Error(t, err)
	assert.EqualError(t, err, "actor system has not started yet")
}

func TestActorSystem_Kill_WhenActorNotFound_ReturnsError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

	// start the actor system
	err := actorSystem.Start(ctx)
	assert.NoError(t, err)
	err = actorSystem.Kill(ctx, "Test")
	assert.Error(t, err)
	assert.EqualError(t, err, "actor=goakt://testSys@/Test not found")
	t.Cleanup(func() {
		err = actorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestActorSystem_Janitor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	actorSystem, _ := NewActorSystem("testSys",
		WithLogger(log.DiscardLogger),
		WithJanitorInterval(100*time.Millisecond),
		WithExpireActorAfter(passivateAfter))

	// start the actor system
	err := actorSystem.Start(ctx)
	assert.NoError(t, err)

	// wait for the system to properly start
	time.Sleep(time.Second)

	actorName := "testActor"
	pid, err := actorSystem.Spawn(ctx, actorName, new(testActor))
	assert.NoError(t, err)
	require.NotNil(t, pid)

	// wait for the actor to properly start
	time.Sleep(time.Second)

	assert.Zero(t, actorSystem.ActorsCount())

	// stop the actor after some time
	time.Sleep(time.Second)

	t.Cleanup(func() {
		err = actorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestActorSystem_GetPartition_WhenClusterDisabled_ReturnsZero(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger),
		WithExpireActorAfter(passivateAfter))

	// start the actor system
	err := actorSystem.Start(ctx)
	assert.NoError(t, err)

	// wait for the system to properly start
	time.Sleep(time.Second)

	partition := actorSystem.GetPartition("some-actor")
	assert.Zero(t, partition)

	t.Cleanup(func() {
		err = actorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestActorSystem_Stop_WhenActorPostStopReturnsError_Panics(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

	// start the actor system
	err := actorSystem.Start(ctx)
	require.NoError(t, err)

	pid, err := actorSystem.Spawn(ctx, "Test", new(postStopActor))
	require.NoError(t, err)
	require.NotNil(t, pid)

	// stop the actor after some time
	time.Sleep(time.Second)

	t.Cleanup(func() {
		assert.Panics(t, func() {
			_ = actorSystem.Stop(ctx)
		})
	})
}

func TestActorSystem_DeadletterSubscription(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

	// start the actor system
	err := actorSystem.Start(ctx)
	require.NoError(t, err)

	// wait for complete start
	time.Sleep(time.Second)

	// create a deadletter subscriber
	subscriber, err := actorSystem.Subscribe()
	require.NoError(t, err)
	require.NotNil(t, subscriber)

	// create the black hole actor
	pid, err := actorSystem.Spawn(ctx, "discarder", new(testActor))
	require.NoError(t, err)
	require.NotNil(t, pid)

	// wait a while
	time.Sleep(time.Second)

	// every message sent to the actor will result in deadletters
	for i := 0; i < 5; i++ {
		require.NoError(t, Tell(ctx, pid, &emptypb.Empty{}))
	}

	time.Sleep(time.Second)

	var items []*goaktpb.Deadletter
	for message := range subscriber.Iterator() {
		payload := message.Payload()
		// only listening to deadletters
		deadletter, ok := payload.(*goaktpb.Deadletter)
		if ok {
			items = append(items, deadletter)
		}
	}

	require.Len(t, items, 5)

	// unsubscribe the subscriber
	err = actorSystem.Unsubscribe(subscriber)
	require.NoError(t, err)

	t.Cleanup(func() {
		err = actorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestActorSystem_Subscription_WhenNotStarted_ReturnsError(t *testing.T) {
	t.Parallel()
	actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	subscriber, err := actorSystem.Subscribe()
	require.Error(t, err)
	require.Nil(t, subscriber)
}

func TestActorSystem_Unsubscribe_WhenShutdown_ReturnsError(t *testing.T) {
	ctx := context.Background()
	actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

	// start the actor system
	err := actorSystem.Start(ctx)
	assert.NoError(t, err)

	subscriber, err := actorSystem.Subscribe()
	require.NoError(t, err)
	require.NotNil(t, subscriber)

	// stop the actor system
	assert.NoError(t, actorSystem.Stop(ctx))

	time.Sleep(time.Second)

	// create a deadletter subscriber
	err = actorSystem.Unsubscribe(subscriber)
	require.Error(t, err)
}

func TestNewActorSystem_ActorOf_WhenActorPassivatedInCluster_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ports := dynaport.Get(3)
	host := "127.0.0.1"
	gossipPort := ports[0]
	peersPort := ports[1]
	remotingPort := ports[2]

	// define discovered addresses
	addrs := []string{
		net.JoinHostPort(host, strconv.Itoa(gossipPort)),
	}

	// mock the discovery provider
	provider := new(mocksdiscovery.Provider)
	actorSystem, err := NewActorSystem(
		"test",
		WithExpireActorAfter(passivateAfter),
		WithLogger(log.DiscardLogger),
		WithAskTimeout(time.Minute),
		WithHost(host),
		WithCluster(
			NewClusterConfig().
				WithKinds(new(testActor)).
				WithPartitionCount(10).
				WithReplicaCount(1).
				WithRemotingPort(remotingPort).
				WithGossipPort(gossipPort).
				WithPeersPort(peersPort).
				WithMinimumPeersQuorum(1).
				WithDiscovery(provider)))

	require.NoError(t, err)

	provider.EXPECT().ID().Return("testDisco")
	provider.EXPECT().Initialize().Return(nil)
	provider.EXPECT().Register().Return(nil)
	provider.EXPECT().Deregister().Return(nil)
	provider.EXPECT().DiscoverPeers().Return(addrs, nil)
	provider.EXPECT().Close().Return(nil)

	// start the actor system
	err = actorSystem.Start(ctx)
	assert.NoError(t, err)

	// wait for the cluster to start
	time.Sleep(time.Second)

	// create an actor
	actorName := uuid.NewString()
	actor := new(testActor)
	actorRef, err := actorSystem.Spawn(ctx, actorName, actor)
	assert.NoError(t, err)
	assert.NotNil(t, actorRef)

	// wait for a while for replication to take effect
	// otherwise the subsequent test will return actor not found
	time.Sleep(time.Second)

	// get the actor
	addr, pid, err := actorSystem.actorOf(ctx, actorName)
	require.Error(t, err)
	require.EqualError(t, err, ErrActorNotFound(actorName).Error())
	require.Nil(t, addr)
	require.Nil(t, pid)

	t.Cleanup(func() {
		err = actorSystem.Stop(ctx)
		assert.NoError(t, err)
		provider.AssertExpectations(t)
	})
}

func TestActorSystem_PeerAddress_WhenNoCluster_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

	// start the actor system
	err := actorSystem.Start(ctx)
	assert.NoError(t, err)
	require.Empty(t, actorSystem.PeerAddress())
	require.NoError(t, actorSystem.Stop(ctx))
}
