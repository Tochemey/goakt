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
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/address"
	"github.com/tochemey/goakt/v3/datacenter"
	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/cluster"
	"github.com/tochemey/goakt/v3/internal/datacentercontroller"
	"github.com/tochemey/goakt/v3/internal/pause"
	"github.com/tochemey/goakt/v3/log"
	mockcluster "github.com/tochemey/goakt/v3/mocks/cluster"
	mocks "github.com/tochemey/goakt/v3/mocks/discovery"
	mocksremote "github.com/tochemey/goakt/v3/mocks/remote"
	"github.com/tochemey/goakt/v3/passivation"
	"github.com/tochemey/goakt/v3/reentrancy"
	"github.com/tochemey/goakt/v3/remote"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

// nolint
func TestSpawn(t *testing.T) {
	t.Run("With Spawn an actor when not System started", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, "Test", actor)
		assert.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
		assert.Nil(t, sys.Metric(ctx))
		assert.Nil(t, actorRef)
		assert.Zero(t, sys.Uptime())
	})
	t.Run("With Spawn an actor when started", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, "Test", actor)
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		assert.NotZero(t, sys.Uptime())

		// stop the actor after some time
		pause.For(time.Second)
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Spawn an actor with invalid actor name", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		require.NoError(t, err)

		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, "$omeN@me", actor)
		require.Error(t, err)
		assert.EqualError(t, err, "must contain only word characters (i.e. [a-zA-Z0-9] plus non-leading '-' or '_')")
		assert.Nil(t, actorRef)

		// stop the actor after some time
		pause.For(time.Second)

		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("With Spawn an actor already exist", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("test", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actor := NewMockActor()
		ref1, err := sys.Spawn(ctx, "Test", actor)
		assert.NoError(t, err)
		assert.NotNil(t, ref1)

		ref2, err := sys.Spawn(ctx, "Test", actor)
		assert.NotNil(t, ref2)
		assert.NoError(t, err)

		// point to the same memory address
		assert.True(t, ref1 == ref2)

		// stop the actor after some time
		pause.For(time.Second)

		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})

	t.Run("With Spawn an actor with invalid spawn option", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("test", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actor := NewMockActor()
		pid, err := sys.Spawn(ctx, "Test", actor, WithDependencies(NewMockDependency("$omeN@me", "user", "email")))
		require.Error(t, err)
		assert.Nil(t, pid)

		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("With Spawn an actor with GoAkt system name", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("test", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actor := NewMockActor()
		pid, err := sys.Spawn(ctx, "GoAktTest", actor)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrReservedName)
		assert.Nil(t, pid)

		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("With Spawn with custom mailbox", func(t *testing.T) {
		ctx := context.TODO()
		actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := actorSystem.Start(ctx)
		assert.NoError(t, err)

		// wait for complete start
		pause.For(time.Second)

		// create the black hole actor
		actor := NewMockActor()
		pid, err := actorSystem.Spawn(ctx, "test", actor, WithMailbox(NewBoundedMailbox(10)))
		assert.NoError(t, err)
		assert.NotNil(t, pid)

		// wait a while
		pause.For(time.Second)
		assert.EqualValues(t, 1, pid.ProcessedCount())
		require.True(t, pid.IsRunning())

		counter := 0
		for i := 1; i <= 5; i++ {
			require.NoError(t, Tell(ctx, pid, new(testpb.TestSend)))
			counter = counter + 1
		}

		pause.For(time.Second)

		assert.EqualValues(t, counter, pid.ProcessedCount()-1)
		require.NoError(t, err)

		err = actorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Spawn an actor already exist in cluster mode", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start system cluster
		node1, sd1 := testNATs(t, srv.Addr().String())
		peerAddress1 := node1.PeersAddress()
		require.NotEmpty(t, peerAddress1)

		// create and start system cluster
		node2, sd2 := testNATs(t, srv.Addr().String())
		peerAddress2 := node2.PeersAddress()
		require.NotEmpty(t, peerAddress2)

		pause.For(time.Second)

		// create an actor on node1
		actor := NewMockActor()
		actorName := "actorID"
		_, err := node1.Spawn(ctx, actorName, actor)
		require.NoError(t, err)

		pause.For(200 * time.Millisecond)

		_, err = node2.Spawn(ctx, actorName, actor)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorAlreadyExists)

		// free resource
		require.NoError(t, node2.Stop(ctx))
		assert.NoError(t, node1.Stop(ctx))
		assert.NoError(t, sd2.Close())
		assert.NoError(t, sd1.Close())
		// shutdown the nats server gracefully
		srv.Shutdown()
	})

	t.Run("With Spawn with custom passivation", func(t *testing.T) {
		ctx := context.TODO()
		actorSystem, _ := NewActorSystem("testSys",
			WithLogger(log.DiscardLogger))

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the actor path
		pid, err := actorSystem.Spawn(ctx, "test", NewMockActor(),
			WithPassivationStrategy(passivation.NewTimeBasedStrategy(passivateAfter)))
		require.NoError(t, err)
		assert.NotNil(t, pid)

		// let us sleep for some time to make the actor idle
		wg := sync.WaitGroup{}
		wg.Go(func() {
			pause.For(receivingDelay)
		})
		// block until timer is up
		wg.Wait()
		// let us send a message to the actor
		err = Tell(ctx, pid, new(testpb.TestSend))
		assert.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrDead)
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With Spawn with long lived", func(t *testing.T) {
		ctx := context.TODO()
		actorSystem, _ := NewActorSystem("testSys",
			WithLogger(log.DiscardLogger))

		require.NoError(t, actorSystem.Start(ctx))

		pause.For(time.Second)

		// create the actor path
		pid, err := actorSystem.Spawn(ctx, "test", NewMockActor(),
			WithLongLived())
		require.NoError(t, err)
		assert.NotNil(t, pid)

		// let us sleep for some time to make the actor idle
		wg := sync.WaitGroup{}
		wg.Go(func() {
			pause.For(receivingDelay)
		})
		// block until timer is up
		wg.Wait()
		// let us send a message to the actor
		err = Tell(ctx, pid, new(testpb.TestSend))
		assert.NoError(t, err)
		assert.True(t, pid.IsRunning())
		assert.NoError(t, actorSystem.Stop(ctx))
	})

	t.Run("With SpawnNamedFromFunc when actor already exist", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)

		logger := log.DiscardLogger
		host := "127.0.0.1"

		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, ports[0])))

		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		require.NoError(t, err)

		receiveFn := func(_ context.Context, message proto.Message) error {
			expected := &testpb.Reply{Content: "test spawn from func"}
			assert.True(t, proto.Equal(expected, message))
			return nil
		}

		actorName := "name"
		actorRef, err := newActorSystem.SpawnNamedFromFunc(ctx, actorName, receiveFn)
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// stop the actor after some time
		pause.For(time.Second)

		// send a message to the actor
		require.NoError(t, Tell(ctx, actorRef, &testpb.Reply{Content: "test spawn from func"}))

		newInstance, err := newActorSystem.SpawnNamedFromFunc(ctx, actorName, receiveFn)
		require.NoError(t, err)
		require.NotNil(t, newInstance)
		require.True(t, newInstance.Equals(actorRef))

		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With SpawnNamedFromFunc when actor name is invalid", func(t *testing.T) {
		ctx := context.TODO()
		ports := dynaport.Get(1)

		logger := log.DiscardLogger
		host := "127.0.0.1"

		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, ports[0])))

		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		require.NoError(t, err)

		receiveFn := func(_ context.Context, message proto.Message) error {
			expected := &testpb.Reply{Content: "test spawn from func"}
			assert.True(t, proto.Equal(expected, message))
			return nil
		}

		actorName := strings.Repeat("a", 256)
		actorRef, err := newActorSystem.SpawnNamedFromFunc(ctx, actorName, receiveFn)
		assert.Error(t, err)
		assert.Nil(t, actorRef)

		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With SpawnFromFunc (cluster/remote enabled)", func(t *testing.T) {
		ctx := context.TODO()
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		logger := log.DiscardLogger
		host := "127.0.0.1"

		// define discovered addresses
		addrs := []string{
			net.JoinHostPort(host, strconv.Itoa(gossipPort)),
		}

		// mock the discovery provider
		provider := new(mocks.Provider)
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
			WithCluster(
				NewClusterConfig().
					WithKinds(new(MockActor)).
					WithPartitionCount(9).
					WithReplicaCount(1).
					WithPeersPort(clusterPort).
					WithMinimumPeersQuorum(1).
					WithDiscoveryPort(gossipPort).
					WithDiscovery(provider)),
		)
		require.NoError(t, err)

		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		// start the actor system
		err = newActorSystem.Start(ctx)
		require.NoError(t, err)

		receiveFn := func(_ context.Context, message proto.Message) error {
			expected := &testpb.Reply{Content: "test spawn from func"}
			assert.True(t, proto.Equal(expected, message))
			return nil
		}

		actorRef, err := newActorSystem.SpawnFromFunc(ctx, receiveFn)
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// stop the actor after some time
		pause.For(time.Second)

		// send a message to the actor
		require.NoError(t, Tell(ctx, actorRef, &testpb.Reply{Content: "test spawn from func"}))

		t.Cleanup(
			func() {
				err = newActorSystem.Stop(ctx)
				assert.NoError(t, err)
				provider.AssertExpectations(t)
			},
		)
	})

	t.Run("With SpawnNamedFromFunc (cluster/remote enabled) already exists", func(t *testing.T) {
		ctx := context.TODO()
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		logger := log.DiscardLogger
		host := "127.0.0.1"

		// define discovered addresses
		addrs := []string{
			net.JoinHostPort(host, strconv.Itoa(gossipPort)),
		}

		// mock the discovery provider
		provider := new(mocks.Provider)
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
			WithCluster(
				NewClusterConfig().
					WithKinds(new(MockActor)).
					WithPartitionCount(9).
					WithReplicaCount(1).
					WithPeersPort(clusterPort).
					WithMinimumPeersQuorum(1).
					WithDiscoveryPort(gossipPort).
					WithDiscovery(provider)),
		)
		require.NoError(t, err)

		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		// start the actor system
		err = newActorSystem.Start(ctx)
		require.NoError(t, err)

		receiveFn := func(_ context.Context, message proto.Message) error {
			expected := &testpb.Reply{Content: "test spawn from func"}
			assert.True(t, proto.Equal(expected, message))
			return nil
		}

		actorName := "name"
		actorRef, err := newActorSystem.SpawnNamedFromFunc(ctx, actorName, receiveFn)
		require.NoError(t, err)
		require.NotNil(t, actorRef)

		// stop the actor after some time
		pause.For(time.Second)

		// send a message to the actor
		require.NoError(t, Tell(ctx, actorRef, &testpb.Reply{Content: "test spawn from func"}))

		actorRef, err = newActorSystem.SpawnNamedFromFunc(ctx, actorName, receiveFn)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorAlreadyExists)
		require.Nil(t, actorRef)

		t.Cleanup(
			func() {
				err = newActorSystem.Stop(ctx)
				assert.NoError(t, err)
				provider.AssertExpectations(t)
			},
		)
	})

	t.Run("With SpawnFromFunc with PreStart error", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		receiveFn := func(_ context.Context, message proto.Message) error {
			expected := &testpb.Reply{Content: "test spawn from func"}
			assert.True(t, proto.Equal(expected, message))
			return nil
		}

		preStart := func(ctx context.Context) error {
			return errors.New("failed")
		}

		actorRef, err := sys.SpawnFromFunc(ctx, receiveFn, WithPreStart(preStart))
		assert.Error(t, err)
		assert.Nil(t, actorRef)

		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("With SpawnFromFunc with PreStop error", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		receiveFn := func(ctx context.Context, message proto.Message) error {
			expected := &testpb.Reply{Content: "test spawn from func"}
			assert.True(t, proto.Equal(expected, message))
			return nil
		}

		postStop := func(ctx context.Context) error {
			return errors.New("failed")
		}

		actorRef, err := sys.SpawnFromFunc(ctx, receiveFn, WithPostStop(postStop))
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// stop the actor after some time
		pause.For(time.Second)

		// send a message to the actor
		require.NoError(t, Tell(ctx, actorRef, &testpb.Reply{Content: "test spawn from func"}))

		t.Cleanup(
			func() {
				assert.Error(t, sys.Stop(ctx))
			},
		)
	})
	t.Run("With SpawnFromFunc with ReceiveFunc error", func(t *testing.T) {
		ctx := context.TODO()
		actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := actorSystem.Start(ctx)
		assert.NoError(t, err)

		// wait for complete start
		pause.For(time.Second)

		// create a deadletter subscriber
		consumer, err := actorSystem.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		mockErr := errors.New("failed to process message")
		receiveFn := func(ctx context.Context, message proto.Message) error { // nolint
			return mockErr
		}

		pid, err := actorSystem.SpawnFromFunc(ctx, receiveFn)
		assert.NoError(t, err)
		assert.NotNil(t, pid)

		pause.For(time.Second)

		msg := new(testpb.TestSend)
		// send a message to the actor
		require.NoError(t, Tell(ctx, pid, msg))

		pause.For(time.Second)

		// the actor will be suspended because there is no supervisor strategy
		require.True(t, pid.IsSuspended())

		var items []*goaktpb.ActorSuspended
		for message := range consumer.Iterator() {
			payload := message.Payload()
			suspended, ok := payload.(*goaktpb.ActorSuspended)
			if ok {
				items = append(items, suspended)
			}
		}

		require.Len(t, items, 1)
		item := items[0]
		addr, err := address.Parse(item.GetAddress())
		require.NoError(t, err)
		assert.True(t, pid.Address().Equals(addr))
		assert.Equal(t, mockErr.Error(), item.GetReason())

		// unsubscribe the consumer
		err = actorSystem.Unsubscribe(consumer)
		require.NoError(t, err)

		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With SpawnFromFunc with actorSystem not started", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		receiveFn := func(_ context.Context, message proto.Message) error {
			expected := &testpb.Reply{Content: "test spawn from func"}
			assert.True(t, proto.Equal(expected, message))
			return nil
		}

		preStart := func(_ context.Context) error {
			return errors.New("failed")
		}

		actorRef, err := sys.SpawnFromFunc(ctx, receiveFn, WithPreStart(preStart))
		assert.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
		assert.Nil(t, actorRef)
	})
	t.Run("SpawnOn with single node cluster", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start system cluster
		node, sd := testNATs(t, srv.Addr().String())
		peerAddress1 := node.PeersAddress()
		require.NotEmpty(t, peerAddress1)
		require.NotNil(t, sd)

		// create an actor on node1
		actor := NewMockActor()
		actorName := "actorID"
		err := node.SpawnOn(ctx, actorName, actor)
		require.NoError(t, err)

		// free resources
		require.NoError(t, node.Stop(ctx))
		require.NoError(t, sd.Close())

		// shutdown the nats server gracefully
		srv.Shutdown()
	})
	t.Run("SpawnOn happy path", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start system cluster
		node1, sd1 := testNATs(t, srv.Addr().String())
		peerAddress1 := node1.PeersAddress()
		require.NotEmpty(t, peerAddress1)
		require.NotNil(t, sd1)

		// create and start system cluster
		node2, sd2 := testNATs(t, srv.Addr().String())
		peerAddress2 := node2.PeersAddress()
		require.NotEmpty(t, peerAddress2)
		require.NotNil(t, sd2)

		// create and start system cluster
		node3, sd3 := testNATs(t, srv.Addr().String())
		peerAddress3 := node3.PeersAddress()
		require.NotEmpty(t, peerAddress3)
		require.NotNil(t, sd3)

		pause.For(time.Second)

		// create an actor on node1
		actor := NewMockActor()
		actorName := "actorID"
		err := node1.SpawnOn(ctx, actorName, actor)
		require.NoError(t, err)

		pause.For(200 * time.Millisecond)

		// either we can locate the actor or try to recreate it
		_, err = node2.Spawn(ctx, actorName, actor)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorAlreadyExists)

		err = node1.SpawnOn(ctx, actorName, actor)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorAlreadyExists)

		// free resources
		require.NoError(t, node2.Stop(ctx))
		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))

		require.NoError(t, sd3.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd1.Close())

		// shutdown the nats server gracefully
		srv.Shutdown()
	})
	t.Run("SpawnOn when actor system not started", func(t *testing.T) {
		// create a context
		ctx := context.TODO()

		actorSystem, _ := NewActorSystem("testSys",
			WithLogger(log.DiscardLogger))

		// create an actor on node1
		actor := NewMockActor()
		actorName := "actorID"
		err := actorSystem.SpawnOn(ctx, actorName, actor)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
	})
	t.Run("SpawnOn when actor name is invalid", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger

		// create the actor system
		actorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = actorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create an actor on node1
		actor := NewMockActor()
		actorName := strings.Repeat("a", 256)
		err = actorSystem.SpawnOn(ctx, actorName, actor)
		require.Error(t, err)

		err = actorSystem.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("SpawnOn when cluster peers lookup fails", func(t *testing.T) {
		ctx := context.TODO()
		clusterMock := new(mockcluster.Cluster)

		system := MockReplicationTestSystem(clusterMock)
		system.remoting = remote.NewRemoting()
		system.remotingEnabled.Store(true)

		actor := NewMockActor()
		actorName := "actorID"

		clusterMock.EXPECT().ActorExists(mock.Anything, actorName).Return(false, nil)
		clusterMock.EXPECT().Members(mock.Anything).Return(nil, assert.AnError)

		t.Cleanup(func() {
			system.remoting.Close()
			clusterMock.AssertExpectations(t)
		})

		err := system.SpawnOn(ctx, actorName, actor)
		require.Error(t, err)
		assert.ErrorIs(t, err, assert.AnError)
		assert.ErrorContains(t, err, "failed to fetch cluster nodes")
	})
	t.Run("SpawnOn remote includes reentrancy config", func(t *testing.T) {
		ctx := context.TODO()
		clusterMock := mockcluster.NewCluster(t)
		remotingMock := mocksremote.NewRemoting(t)

		system := MockReplicationTestSystem(clusterMock)
		system.remoting = remotingMock

		actor := NewMockActor()
		actorName := "actorID"
		peer := &cluster.Peer{Host: "127.0.0.1", RemotingPort: 9000}

		clusterMock.EXPECT().ActorExists(mock.Anything, actorName).Return(false, nil).Once()
		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{peer}, nil).Once()
		remotingMock.EXPECT().RemoteSpawn(mock.Anything, peer.Host, peer.RemotingPort, mock.Anything).
			Run(func(_ context.Context, _ string, _ int, request *remote.SpawnRequest) {
				require.NotNil(t, request.Reentrancy)
				require.Equal(t, reentrancy.AllowAll, request.Reentrancy.Mode())
				require.Equal(t, 7, request.Reentrancy.MaxInFlight())
			}).
			Return(nil).
			Once()

		err := system.SpawnOn(ctx, actorName, actor,
			WithPlacement(Random),
			WithReentrancy(reentrancy.New(
				reentrancy.WithMode(reentrancy.AllowAll),
				reentrancy.WithMaxInFlight(7),
			)),
		)
		require.NoError(t, err)
	})
	t.Run("SpawnOn when cluster has no members spawns locally", func(t *testing.T) {
		ctx := context.TODO()
		sys, err := NewActorSystem("spawn-no-peers", WithLogger(log.DiscardLogger))
		require.NoError(t, err)

		actorSystem := sys.(*actorSystem)
		err = actorSystem.Start(ctx)
		require.NoError(t, err)

		pause.For(time.Second)

		clusterMock := mockcluster.NewCluster(t)

		actorSystem.locker.Lock()
		actorSystem.cluster = clusterMock
		actorSystem.locker.Unlock()
		actorSystem.clusterEnabled.Store(true)

		t.Cleanup(func() {
			actorSystem.clusterEnabled.Store(false)
			actorSystem.locker.Lock()
			actorSystem.cluster = nil
			actorSystem.locker.Unlock()
			assert.NoError(t, actorSystem.Stop(ctx))
		})

		actor := NewMockActor()
		actorName := "actorID"

		clusterMock.EXPECT().ActorExists(mock.Anything, actorName).Return(false, nil).Twice()
		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{}, nil).Once()

		err = actorSystem.SpawnOn(ctx, actorName, actor)
		require.NoError(t, err)

		pause.For(200 * time.Millisecond)

		actors := actorSystem.Actors()
		require.Len(t, actors, 1)
		assert.Equal(t, actorName, actors[0].Name())
	})
	t.Run("SpawnOn when node metric fetch fails", func(t *testing.T) {
		ctx := context.TODO()
		clusterMock := new(mockcluster.Cluster)

		system := MockReplicationTestSystem(clusterMock)
		system.remoting = remote.NewRemoting()
		system.remotingEnabled.Store(true)

		client := system.remoting.HTTPClient()
		client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, assert.AnError
		})

		actor := NewMockActor()
		actorName := "actorID"
		peers := []*cluster.Peer{
			{Host: "127.0.0.1", RemotingPort: 10001},
			{Host: "127.0.0.1", RemotingPort: 10002},
		}

		clusterMock.EXPECT().ActorExists(mock.Anything, actorName).Return(false, nil)
		clusterMock.EXPECT().Members(mock.Anything).Return(peers, nil)

		t.Cleanup(func() {
			system.remoting.Close()
			clusterMock.AssertExpectations(t)
		})

		err := system.SpawnOn(ctx, actorName, actor, WithPlacement(LeastLoad))
		require.Error(t, err)
		assert.ErrorContains(t, err, "failed to fetch node metrics")
		assert.ErrorIs(t, err, assert.AnError)
	})
	t.Run("SpawnOn with random placement", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start system cluster
		node1, sd1 := testNATs(t, srv.Addr().String())
		peerAddress1 := node1.PeersAddress()
		require.NotEmpty(t, peerAddress1)
		require.NotNil(t, sd1)

		// create and start system cluster
		node2, sd2 := testNATs(t, srv.Addr().String())
		peerAddress2 := node2.PeersAddress()
		require.NotEmpty(t, peerAddress2)
		require.NotNil(t, sd2)

		// create and start system cluster
		node3, sd3 := testNATs(t, srv.Addr().String())
		peerAddress3 := node3.PeersAddress()
		require.NotEmpty(t, peerAddress3)
		require.NotNil(t, sd3)

		pause.For(time.Second)

		// create an actor on node1
		actor := NewMockActor()
		actorName := "actorID"
		err := node1.SpawnOn(ctx, actorName, actor, WithPlacement(Random))
		require.NoError(t, err)

		pause.For(200 * time.Millisecond)

		// either we can locate the actor or try to recreate it
		_, err = node2.Spawn(ctx, actorName, actor)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorAlreadyExists)

		// free resources
		require.NoError(t, node2.Stop(ctx))
		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))

		require.NoError(t, sd3.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd1.Close())

		// shutdown the nats server gracefully
		srv.Shutdown()
	})
	t.Run("SpawnOn with local placement", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start system cluster
		node1, sd1 := testNATs(t, srv.Addr().String())
		peerAddress1 := node1.PeersAddress()
		require.NotEmpty(t, peerAddress1)
		require.NotNil(t, sd1)

		// create and start system cluster
		node2, sd2 := testNATs(t, srv.Addr().String())
		peerAddress2 := node2.PeersAddress()
		require.NotEmpty(t, peerAddress2)
		require.NotNil(t, sd2)

		// create and start system cluster
		node3, sd3 := testNATs(t, srv.Addr().String())
		peerAddress3 := node3.PeersAddress()
		require.NotEmpty(t, peerAddress3)
		require.NotNil(t, sd3)

		pause.For(time.Second)

		// create an actor on node1
		actor := NewMockActor()
		actorName := "actorID"
		err := node1.SpawnOn(ctx, actorName, actor, WithPlacement(Local))
		require.NoError(t, err)

		pause.For(200 * time.Millisecond)

		// either we can locate the actor or try to recreate it
		_, err = node2.Spawn(ctx, actorName, actor)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorAlreadyExists)

		// free resources
		require.NoError(t, node2.Stop(ctx))
		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))

		require.NoError(t, sd3.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd1.Close())

		// shutdown the nats server gracefully
		srv.Shutdown()
	})
	t.Run("SpawnOn with least-load placement", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start system cluster
		node1, sd1 := testNATs(t, srv.Addr().String())
		peerAddress1 := node1.PeersAddress()
		require.NotEmpty(t, peerAddress1)
		require.NotNil(t, sd1)

		// create and start system cluster
		node2, sd2 := testNATs(t, srv.Addr().String())
		peerAddress2 := node2.PeersAddress()
		require.NotEmpty(t, peerAddress2)
		require.NotNil(t, sd2)

		// create and start system cluster
		node3, sd3 := testNATs(t, srv.Addr().String())
		peerAddress3 := node3.PeersAddress()
		require.NotEmpty(t, peerAddress3)
		require.NotNil(t, sd3)

		pause.For(time.Second)

		// we create three actors on node1 to simulate load
		for i := range 3 {
			_, err := node1.Spawn(ctx, fmt.Sprintf("actor1%d", i), NewMockActor())
			require.NoError(t, err)
		}

		pause.For(time.Second)

		// we create two actors on node2 to simulate load
		for i := range 2 {
			_, err := node2.Spawn(ctx, fmt.Sprintf("actor2%d", i), NewMockActor())
			require.NoError(t, err)
		}

		pause.For(time.Second)

		// we create two actors on node2 to simulate load
		for i := range 4 {
			_, err := node3.Spawn(ctx, fmt.Sprintf("actor3%d", i), NewMockActor())
			require.NoError(t, err)
		}

		pause.For(time.Second)

		// try creating an actor on node1 and it will be placed on node2
		actor := NewMockActor()
		actorName := "actorID"
		err := node1.SpawnOn(ctx, actorName, actor, WithPlacement(LeastLoad))
		require.NoError(t, err)

		pause.For(time.Second)

		metric := node2.Metric(ctx)
		require.Exactly(t, int64(3), metric.ActorsCount())

		_, err = node2.Spawn(ctx, actorName, actor)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorAlreadyExists)

		// free resources
		require.NoError(t, node2.Stop(ctx))
		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))

		require.NoError(t, sd3.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd1.Close())

		// shutdown the nats server gracefully
		srv.Shutdown()
	})
	t.Run("SpawnOn with Brotli compression", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start system cluster
		node1, sd1 := testNATs(t, srv.Addr().String(), withMockCompression(remote.BrotliCompression))
		peerAddress1 := node1.PeersAddress()
		require.NotEmpty(t, peerAddress1)
		require.NotNil(t, sd1)

		// create and start system cluster
		node2, sd2 := testNATs(t, srv.Addr().String(), withMockCompression(remote.BrotliCompression))
		peerAddress2 := node2.PeersAddress()
		require.NotEmpty(t, peerAddress2)
		require.NotNil(t, sd2)

		// create and start system cluster
		node3, sd3 := testNATs(t, srv.Addr().String(), withMockCompression(remote.BrotliCompression))
		peerAddress3 := node3.PeersAddress()
		require.NotEmpty(t, peerAddress3)
		require.NotNil(t, sd3)

		pause.For(time.Second)

		// we create three actors on node1 to simulate load
		for i := range 3 {
			_, err := node1.Spawn(ctx, fmt.Sprintf("actor1%d", i), NewMockActor())
			require.NoError(t, err)
		}

		// we create two actors on node2 to simulate load
		for i := range 2 {
			_, err := node2.Spawn(ctx, fmt.Sprintf("actor2%d", i), NewMockActor())
			require.NoError(t, err)
		}

		// we create two actors on node2 to simulate load
		for i := range 4 {
			_, err := node3.Spawn(ctx, fmt.Sprintf("actor3%d", i), NewMockActor())
			require.NoError(t, err)
		}

		pause.For(time.Second)

		// try creating an actor on node1 and it will be placed on node2
		actor := NewMockActor()
		actorName := "actorID"
		err := node1.SpawnOn(ctx, actorName, actor, WithPlacement(LeastLoad))
		require.NoError(t, err)

		pause.For(200 * time.Millisecond)

		metric := node2.Metric(ctx)
		require.Exactly(t, int64(3), metric.ActorsCount())

		_, err = node2.Spawn(ctx, actorName, actor)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorAlreadyExists)

		// free resources
		require.NoError(t, node2.Stop(ctx))
		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))

		require.NoError(t, sd3.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd1.Close())

		// shutdown the nats server gracefully
		srv.Shutdown()
	})
	t.Run("SpawnOn with Zstandard compression", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start system cluster
		node1, sd1 := testNATs(t, srv.Addr().String(), withMockCompression(remote.ZstdCompression))
		peerAddress1 := node1.PeersAddress()
		require.NotEmpty(t, peerAddress1)
		require.NotNil(t, sd1)

		// create and start system cluster
		node2, sd2 := testNATs(t, srv.Addr().String(), withMockCompression(remote.ZstdCompression))
		peerAddress2 := node2.PeersAddress()
		require.NotEmpty(t, peerAddress2)
		require.NotNil(t, sd2)

		// create and start system cluster
		node3, sd3 := testNATs(t, srv.Addr().String(), withMockCompression(remote.ZstdCompression))
		peerAddress3 := node3.PeersAddress()
		require.NotEmpty(t, peerAddress3)
		require.NotNil(t, sd3)

		pause.For(time.Second)

		// we create three actors on node1 to simulate load
		for i := range 3 {
			_, err := node1.Spawn(ctx, fmt.Sprintf("actor1%d", i), NewMockActor())
			require.NoError(t, err)
		}

		// we create two actors on node2 to simulate load
		for i := range 2 {
			_, err := node2.Spawn(ctx, fmt.Sprintf("actor2%d", i), NewMockActor())
			require.NoError(t, err)
		}

		// we create two actors on node2 to simulate load
		for i := range 4 {
			_, err := node3.Spawn(ctx, fmt.Sprintf("actor3%d", i), NewMockActor())
			require.NoError(t, err)
		}

		pause.For(time.Second)

		// try creating an actor on node1 and it will be placed on node2
		actor := NewMockActor()
		actorName := "actorID"
		err := node1.SpawnOn(ctx, actorName, actor, WithPlacement(LeastLoad))
		require.NoError(t, err)

		pause.For(200 * time.Millisecond)

		metric := node2.Metric(ctx)
		require.Exactly(t, int64(3), metric.ActorsCount())

		_, err = node2.Spawn(ctx, actorName, actor)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorAlreadyExists)

		// free resources
		require.NoError(t, node2.Stop(ctx))
		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))

		require.NoError(t, sd3.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd1.Close())

		// shutdown the nats server gracefully
		srv.Shutdown()
	})
	t.Run("SpawnOn with Gzip compression", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start system cluster
		node1, sd1 := testNATs(t, srv.Addr().String(), withMockCompression(remote.GzipCompression))
		peerAddress1 := node1.PeersAddress()
		require.NotEmpty(t, peerAddress1)
		require.NotNil(t, sd1)

		// create and start system cluster
		node2, sd2 := testNATs(t, srv.Addr().String(), withMockCompression(remote.GzipCompression))
		peerAddress2 := node2.PeersAddress()
		require.NotEmpty(t, peerAddress2)
		require.NotNil(t, sd2)

		// create and start system cluster
		node3, sd3 := testNATs(t, srv.Addr().String(), withMockCompression(remote.GzipCompression))
		peerAddress3 := node3.PeersAddress()
		require.NotEmpty(t, peerAddress3)
		require.NotNil(t, sd3)

		pause.For(time.Second)

		// we create three actors on node1 to simulate load
		for i := range 3 {
			_, err := node1.Spawn(ctx, fmt.Sprintf("actor1%d", i), NewMockActor())
			require.NoError(t, err)
		}

		// we create two actors on node2 to simulate load
		for i := range 2 {
			_, err := node2.Spawn(ctx, fmt.Sprintf("actor2%d", i), NewMockActor())
			require.NoError(t, err)
		}

		// we create two actors on node2 to simulate load
		for i := range 4 {
			_, err := node3.Spawn(ctx, fmt.Sprintf("actor3%d", i), NewMockActor())
			require.NoError(t, err)
		}

		pause.For(time.Second)

		// try creating an actor on node1 and it will be placed on node2
		actor := NewMockActor()
		actorName := "actorID"
		err := node1.SpawnOn(ctx, actorName, actor, WithPlacement(LeastLoad))
		require.NoError(t, err)

		pause.For(200 * time.Millisecond)

		metric := node2.Metric(ctx)
		require.Exactly(t, int64(3), metric.ActorsCount())

		_, err = node2.Spawn(ctx, actorName, actor)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorAlreadyExists)

		// free resources
		require.NoError(t, node2.Stop(ctx))
		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))

		require.NoError(t, sd3.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd1.Close())

		// shutdown the nats server gracefully
		srv.Shutdown()
	})
	t.Run("SpawnOn happy path with role-based", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		roles := []string{"backend", "api", "worker"}

		// create and start system cluster
		node1, sd1 := testNATs(t, srv.Addr().String(), withMockRoles(roles...))
		peerAddress1 := node1.PeersAddress()
		require.NotEmpty(t, peerAddress1)
		require.NotNil(t, sd1)

		// create and start system cluster
		node2, sd2 := testNATs(t, srv.Addr().String(), withMockRoles(roles...))
		peerAddress2 := node2.PeersAddress()
		require.NotEmpty(t, peerAddress2)
		require.NotNil(t, sd2)

		// create and start system cluster
		node3, sd3 := testNATs(t, srv.Addr().String(), withMockRoles(roles...))
		peerAddress3 := node3.PeersAddress()
		require.NotEmpty(t, peerAddress3)
		require.NotNil(t, sd3)

		pause.For(time.Second)

		// create an actor on node1
		actor := NewMockActor()
		actorName := "actorID"
		role := "api"
		err := node1.SpawnOn(ctx, actorName, actor, WithRole(role))
		require.NoError(t, err)

		pause.For(200 * time.Millisecond)

		_, err = node2.Spawn(ctx, actorName, actor)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorAlreadyExists)

		err = node1.SpawnOn(ctx, actorName, actor, WithRole(role))
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorAlreadyExists)

		// free resources
		require.NoError(t, node2.Stop(ctx))
		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))

		require.NoError(t, sd3.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd1.Close())

		// shutdown the nats server gracefully
		srv.Shutdown()
	})
	t.Run("SpawnOn when no peers found for the given role", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		roles := []string{"backend", "api", "worker"}

		// create and start system cluster
		node1, sd1 := testNATs(t, srv.Addr().String(), withMockRoles(roles...))
		peerAddress1 := node1.PeersAddress()
		require.NotEmpty(t, peerAddress1)
		require.NotNil(t, sd1)

		// create and start system cluster
		node2, sd2 := testNATs(t, srv.Addr().String(), withMockRoles(roles...))
		peerAddress2 := node2.PeersAddress()
		require.NotEmpty(t, peerAddress2)
		require.NotNil(t, sd2)

		// create and start system cluster
		node3, sd3 := testNATs(t, srv.Addr().String(), withMockRoles(roles...))
		peerAddress3 := node3.PeersAddress()
		require.NotEmpty(t, peerAddress3)
		require.NotNil(t, sd3)

		pause.For(time.Second)

		// create an actor on node1
		actor := NewMockActor()
		actorName := "actorID"
		role := "frontend"
		err := node1.SpawnOn(ctx, actorName, actor, WithRole(role))
		require.Error(t, err)
		assert.ErrorContains(t, err, fmt.Sprintf("no nodes with role %s found in the cluster", role))

		err = node3.SpawnOn(ctx, actorName, actor, WithRole(role))
		require.Error(t, err)
		assert.ErrorContains(t, err, fmt.Sprintf("no nodes with role %s found in the cluster", role))

		// free resources
		require.NoError(t, node2.Stop(ctx))
		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))

		require.NoError(t, sd3.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd1.Close())

		// shutdown the nats server gracefully
		srv.Shutdown()
	})

	t.Run("SpawnOn happy path with role-based and local placement", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		roles := []string{"backend", "api", "worker"}

		// create and start system cluster
		node1, sd1 := testNATs(t, srv.Addr().String(), withMockRoles(roles...))
		peerAddress1 := node1.PeersAddress()
		require.NotEmpty(t, peerAddress1)
		require.NotNil(t, sd1)

		// create and start system cluster
		node2, sd2 := testNATs(t, srv.Addr().String(), withMockRoles(roles...))
		peerAddress2 := node2.PeersAddress()
		require.NotEmpty(t, peerAddress2)
		require.NotNil(t, sd2)

		// create and start system cluster
		node3, sd3 := testNATs(t, srv.Addr().String(), withMockRoles(roles...))
		peerAddress3 := node3.PeersAddress()
		require.NotEmpty(t, peerAddress3)
		require.NotNil(t, sd3)

		pause.For(time.Second)

		// create an actor on node1
		actor := NewMockActor()
		actorName := "actorID"
		role := "api"
		err := node1.SpawnOn(ctx, actorName, actor, WithRole(role), WithPlacement(Local))
		require.NoError(t, err)

		pause.For(200 * time.Millisecond)
		actors := node1.Actors()
		assert.Len(t, actors, 1)
		got := actors[0]
		assert.Equal(t, actorName, got.Name())

		_, err = node2.Spawn(ctx, actorName, actor)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorAlreadyExists)

		err = node1.SpawnOn(ctx, actorName, actor, WithRole(role))
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorAlreadyExists)

		// free resources
		require.NoError(t, node2.Stop(ctx))
		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))

		require.NoError(t, sd3.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd1.Close())

		// shutdown the nats server gracefully
		srv.Shutdown()
	})
	t.Run("SpawnOn with round-robin placement", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start system cluster
		node1, sd1 := testNATs(t, srv.Addr().String())
		peerAddress1 := node1.PeersAddress()
		require.NotEmpty(t, peerAddress1)
		require.NotNil(t, sd1)

		// create and start system cluster
		node2, sd2 := testNATs(t, srv.Addr().String())
		peerAddress2 := node2.PeersAddress()
		require.NotEmpty(t, peerAddress2)
		require.NotNil(t, sd2)

		// create and start system cluster
		node3, sd3 := testNATs(t, srv.Addr().String())
		peerAddress3 := node3.PeersAddress()
		require.NotEmpty(t, peerAddress3)
		require.NotNil(t, sd3)

		pause.For(time.Second)

		// create an actor on node1
		actor := NewMockActor()
		actorName := "actorID"
		err := node1.SpawnOn(ctx, actorName, actor, WithPlacement(RoundRobin))
		require.NoError(t, err)

		pause.For(200 * time.Millisecond)

		// either we can locate the actor or try to recreate it
		_, err = node2.Spawn(ctx, actorName, actor)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorAlreadyExists)

		// free resources
		require.NoError(t, node2.Stop(ctx))
		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))

		require.NoError(t, sd3.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd1.Close())

		// shutdown the nats server gracefully
		srv.Shutdown()
	})
	t.Run("SpawnOn with round-robin when getting next value failed", func(t *testing.T) {
		ctx := t.Context()
		clmock := mockcluster.NewCluster(t)
		system := MockReplicationTestSystem(clmock)
		system.remoting = remote.NewRemoting()
		system.remotingEnabled.Store(true)
		system.clusterEnabled.Store(true)

		peers := []*cluster.Peer{
			{Host: "10.0.0.2"},
			{Host: "10.0.0.3"},
		}

		actorName := "actorID"
		clmock.EXPECT().ActorExists(ctx, actorName).Return(false, nil).Once()
		clmock.EXPECT().Members(ctx).Return(peers, nil).Once()
		clmock.EXPECT().NextRoundRobinValue(ctx, cluster.ActorsRoundRobinKey).Return(-1, assert.AnError).Once()

		actor := NewMockActor()
		err := system.SpawnOn(ctx, actorName, actor, WithPlacement(RoundRobin))
		require.Error(t, err)
		assert.ErrorIs(t, err, assert.AnError)
	})

	t.Run("SpawnOn with WithDataCenter delegates to spawnOnDatacenter", func(t *testing.T) {
		ctx := context.Background()
		remotingMock := mocksremote.NewRemoting(t)
		targetDC := datacenter.DataCenter{Name: "dc-west", Region: "r", Zone: "z"}
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{{
				ID:        targetDC.ID(),
				State:     datacenter.DataCenterActive,
				Endpoints: []string{"127.0.0.1:9999"},
			}}, nil
		}, remotingMock)

		remotingMock.EXPECT().
			RemoteSpawn(mock.Anything, "127.0.0.1", 9999, mock.MatchedBy(func(req *remote.SpawnRequest) bool {
				return req.Name == "actor-1" &&
					req.Kind != "" &&
					req.Relocatable == true &&
					req.EnableStashing == false
			})).
			Return(nil).
			Once()

		actor := NewMockActor()
		err := sys.SpawnOn(ctx, "actor-1", actor, WithDataCenter(&targetDC))
		require.NoError(t, err)
		remotingMock.AssertExpectations(t)
	})
}

func TestSpawnOnDatacenter(t *testing.T) {
	ctx := context.Background()

	t.Run("returns ErrDataCenterNotReady when controller is nil", func(t *testing.T) {
		dcConfig := datacenter.NewConfig()
		dcConfig.ControlPlane = &MockControlPlane{}
		dcConfig.DataCenter = datacenter.DataCenter{Name: "local"}
		dcConfig.Endpoints = []string{"127.0.0.1:8080"}

		sys := MockReplicationTestSystem(mockcluster.NewCluster(t))
		sys.remoting = mocksremote.NewRemoting(t)
		sys.remotingEnabled.Store(true)
		sys.clusterConfig = NewClusterConfig().WithDataCenter(dcConfig)
		sys.dataCenterController = nil

		config := newSpawnConfig(WithDataCenter(&datacenter.DataCenter{Name: "dc-west", Region: "r", Zone: "z"}))
		actor := NewMockActor()

		err := sys.spawnOnDatacenter(ctx, "actor-1", actor, config)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrDataCenterNotReady)
	})

	t.Run("returns ErrDataCenterStaleRecords when cache is stale", func(t *testing.T) {
		dcConfig := datacenter.NewConfig()
		dcConfig.ControlPlane = &MockControlPlane{
			listActive: func(context.Context) ([]datacenter.DataCenterRecord, error) {
				return []datacenter.DataCenterRecord{{
					ID:        "dc-west",
					State:     datacenter.DataCenterActive,
					Endpoints: []string{"127.0.0.1:9999"},
				}}, nil
			},
		}
		dcConfig.DataCenter = datacenter.DataCenter{Name: "local"}
		dcConfig.Endpoints = []string{"127.0.0.1:8080"}
		dcConfig.MaxCacheStaleness = 1 * time.Millisecond
		dcConfig.CacheRefreshInterval = 5 * time.Millisecond

		controller, err := datacentercontroller.NewController(dcConfig)
		require.NoError(t, err)
		startCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err = controller.Start(startCtx)
		cancel()
		require.NoError(t, err)
		t.Cleanup(func() {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
			_ = controller.Stop(stopCtx)
			stopCancel()
		})

		// Wait until cache is stale (past MaxCacheStaleness)
		pause.For(5 * time.Millisecond)

		sys := MockReplicationTestSystem(mockcluster.NewCluster(t))
		sys.remoting = mocksremote.NewRemoting(t)
		sys.remotingEnabled.Store(true)
		sys.clusterConfig = NewClusterConfig().WithDataCenter(dcConfig)
		sys.dataCenterController = controller

		config := newSpawnConfig(WithDataCenter(&datacenter.DataCenter{Name: "dc-west", Region: "r", Zone: "z"}))
		actor := NewMockActor()

		err = sys.spawnOnDatacenter(ctx, "actor-1", actor, config)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrDataCenterStaleRecords)
	})

	t.Run("returns ErrDataCenterRecordNotFound when target DC not in active records", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{{
				ID:        "dc-other",
				State:     datacenter.DataCenterActive,
				Endpoints: []string{"127.0.0.1:9999"},
			}}, nil
		}, remotingMock)

		config := newSpawnConfig(WithDataCenter(&datacenter.DataCenter{Name: "dc-west", Region: "r", Zone: "z"}))
		actor := NewMockActor()

		err := sys.spawnOnDatacenter(ctx, "actor-1", actor, config)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrDataCenterRecordNotFound)
	})

	t.Run("returns ErrDataCenterRecordNotFound when no active records", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return nil, nil
		}, remotingMock)

		config := newSpawnConfig(WithDataCenter(&datacenter.DataCenter{Name: "dc-west", Region: "r", Zone: "z"}))
		actor := NewMockActor()

		err := sys.spawnOnDatacenter(ctx, "actor-1", actor, config)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrDataCenterRecordNotFound)
	})

	t.Run("returns ErrDataCenterRecordNotFound when target DC record exists but state is not ACTIVE", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
		targetDC := datacenter.DataCenter{Name: "dc-west", Region: "r", Zone: "z"}
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{{
				ID:        targetDC.ID(),
				State:     datacenter.DataCenterDraining,
				Endpoints: []string{"127.0.0.1:9999"},
			}}, nil
		}, remotingMock)

		config := newSpawnConfig(WithDataCenter(&targetDC))
		actor := NewMockActor()

		err := sys.spawnOnDatacenter(ctx, "actor-1", actor, config)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrDataCenterRecordNotFound)
	})

	t.Run("returns error when endpoint has invalid host:port format", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
		targetDC := datacenter.DataCenter{Name: "dc-west", Region: "r", Zone: "z"}
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{{
				ID:        targetDC.ID(),
				State:     datacenter.DataCenterActive,
				Endpoints: []string{"no-colon-invalid"},
			}}, nil
		}, remotingMock)

		config := newSpawnConfig(WithDataCenter(&targetDC))
		actor := NewMockActor()

		err := sys.spawnOnDatacenter(ctx, "actor-1", actor, config)
		require.Error(t, err)
		assert.ErrorContains(t, err, "failed to split host and port from endpoint")
	})

	t.Run("returns error when endpoint port is not numeric", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
		targetDC := datacenter.DataCenter{Name: "dc-west", Region: "r", Zone: "z"}
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{{
				ID:        targetDC.ID(),
				State:     datacenter.DataCenterActive,
				Endpoints: []string{"127.0.0.1:notaport"},
			}}, nil
		}, remotingMock)

		config := newSpawnConfig(WithDataCenter(&targetDC))
		actor := NewMockActor()

		err := sys.spawnOnDatacenter(ctx, "actor-1", actor, config)
		require.Error(t, err)
		assert.ErrorContains(t, err, "failed to convert port to int")
	})

	t.Run("returns RemoteSpawn error when remoting fails", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
		targetDC := datacenter.DataCenter{Name: "dc-west", Region: "r", Zone: "z"}
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{{
				ID:        targetDC.ID(),
				State:     datacenter.DataCenterActive,
				Endpoints: []string{"127.0.0.1:9999"},
			}}, nil
		}, remotingMock)

		remotingMock.EXPECT().
			RemoteSpawn(mock.Anything, "127.0.0.1", 9999, mock.MatchedBy(func(req *remote.SpawnRequest) bool {
				return req.Name == "actor-1" && req.Kind != "" && !req.Relocatable
			})).
			Return(gerrors.ErrTypeNotRegistered).
			Once()

		config := newSpawnConfig(WithDataCenter(&targetDC), WithRelocationDisabled())
		actor := NewMockActor()

		err := sys.spawnOnDatacenter(ctx, "actor-1", actor, config)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrTypeNotRegistered)
	})

	t.Run("succeeds and calls RemoteSpawn with correct request", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
		targetDC := datacenter.DataCenter{Name: "dc-west", Region: "r", Zone: "z"}
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{{
				ID:        targetDC.ID(),
				State:     datacenter.DataCenterActive,
				Endpoints: []string{"127.0.0.1:9999"},
			}}, nil
		}, remotingMock)

		remotingMock.EXPECT().
			RemoteSpawn(mock.Anything, "127.0.0.1", 9999, mock.MatchedBy(func(req *remote.SpawnRequest) bool {
				return req.Name == "actor-1" &&
					req.Kind != "" &&
					req.Relocatable == true &&
					req.EnableStashing == false
			})).
			Return(nil).
			Once()

		config := newSpawnConfig(WithDataCenter(&targetDC))
		actor := NewMockActor()

		err := sys.spawnOnDatacenter(ctx, "actor-1", actor, config)
		require.NoError(t, err)
		remotingMock.AssertExpectations(t)
	})

	t.Run("passes relocatable and passivation from config to RemoteSpawn", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
		targetDC := datacenter.DataCenter{Name: "dc-west", Region: "r", Zone: "z"}
		passivationStrategy := passivation.NewTimeBasedStrategy(30 * time.Second)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{{
				ID:        targetDC.ID(),
				State:     datacenter.DataCenterActive,
				Endpoints: []string{"192.168.1.10:9000"},
			}}, nil
		}, remotingMock)

		remotingMock.EXPECT().
			RemoteSpawn(mock.Anything, "192.168.1.10", 9000, mock.MatchedBy(func(req *remote.SpawnRequest) bool {
				return req.Relocatable == false &&
					req.PassivationStrategy != nil &&
					req.EnableStashing == true
			})).
			Return(nil).
			Once()

		config := newSpawnConfig(
			WithDataCenter(&targetDC),
			WithRelocationDisabled(),
			WithPassivationStrategy(passivationStrategy),
			WithStashing(),
		)
		actor := NewMockActor()

		err := sys.spawnOnDatacenter(ctx, "actor-2", actor, config)
		require.NoError(t, err)
		remotingMock.AssertExpectations(t)
	})
}
