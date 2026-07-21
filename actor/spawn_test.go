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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/datacenter"
	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/internal/datacentercontroller"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	dynaport "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/remoteclient"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	mockcluster "github.com/tochemey/goakt/v4/mocks/cluster"
	mocks "github.com/tochemey/goakt/v4/mocks/discovery"
	mocksremote "github.com/tochemey/goakt/v4/mocks/remoteclient"
	"github.com/tochemey/goakt/v4/passivation"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
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

	t.Run("With Spawn on remote node", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start a system cluster
		node1, sd1 := testNATs(t, srv.Addr().String())
		require.NotNil(t, node1)
		require.NotNil(t, sd1)

		// create and start a system cluster
		node2, sd2 := testNATs(t, srv.Addr().String())
		require.NotNil(t, node2)
		require.NotNil(t, sd2)

		// create and start a system cluster
		node3, sd3 := testNATs(t, srv.Addr().String())
		require.NotNil(t, node3)
		require.NotNil(t, sd3)

		actorName := "actorName"
		actor := newExchanger()

		// define the spawn options
		opts := []SpawnOption{
			WithHostAndPort(node2.Host(), node2.Port()),
			WithRelocationDisabled(),
		}

		pid, err := node1.Spawn(ctx, actorName, actor, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid)
		require.True(t, pid.IsRemote())

		local, err := node1.Spawn(ctx, "localActor", actor)
		require.NoError(t, err)
		require.NotNil(t, local)
		require.True(t, local.IsLocal())

		message := new(testpb.TestReply)
		response, err := local.Ask(ctx, pid, message, time.Minute)
		require.NoError(t, err)
		require.NotNil(t, response)

		reply, ok := response.(*testpb.Reply)
		require.True(t, ok)
		expected := new(testpb.Reply)
		assert.True(t, proto.Equal(expected, reply))

		require.NoError(t, node2.Stop(ctx))
		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, sd3.Close())
		require.NoError(t, sd2.Close())
		srv.Shutdown()
	})

	t.Run("With Spawn on remote node with remoting disabled", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actorName := "actorName"
		actor := newExchanger()
		opts := []SpawnOption{
			WithHostAndPort(sys.Host(), sys.Port()),
		}
		pid, err := sys.Spawn(ctx, actorName, actor, opts...)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrRemotingDisabled)
		require.Nil(t, pid)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
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

		receiveFn := func(_ context.Context, message any) error {
			expected := &testpb.Reply{Content: "test spawn from func"}
			actual, ok := message.(*testpb.Reply)
			require.True(t, ok)
			assert.True(t, proto.Equal(expected, actual))
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

		receiveFn := func(_ context.Context, message any) error {
			expected := &testpb.Reply{Content: "test spawn from func"}
			actual, ok := message.(*testpb.Reply)
			require.True(t, ok)
			assert.True(t, proto.Equal(expected, actual))
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

		receiveFn := func(_ context.Context, message any) error {
			expected := &testpb.Reply{Content: "test spawn from func"}
			actual, ok := message.(*testpb.Reply)
			require.True(t, ok)
			assert.True(t, proto.Equal(expected, actual))
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

		receiveFn := func(_ context.Context, message any) error {
			expected := &testpb.Reply{Content: "test spawn from func"}
			actual, ok := message.(*testpb.Reply)
			require.True(t, ok)
			assert.True(t, proto.Equal(expected, actual))
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

		receiveFn := func(_ context.Context, message any) error {
			expected := &testpb.Reply{Content: "test spawn from func"}
			actual, ok := message.(*testpb.Reply)
			require.True(t, ok)
			assert.True(t, proto.Equal(expected, actual))
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

		receiveFn := func(ctx context.Context, message any) error {
			expected := &testpb.Reply{Content: "test spawn from func"}
			actual, ok := message.(*testpb.Reply)
			require.True(t, ok)
			assert.True(t, proto.Equal(expected, actual))
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
		receiveFn := func(ctx context.Context, message any) error { // nolint
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

		var items []*ActorSuspended
		for message := range consumer.Iterator() {
			payload := message.Payload()
			suspended, ok := payload.(*ActorSuspended)
			if ok {
				items = append(items, suspended)
			}
		}

		require.Len(t, items, 1)
		item := items[0]
		assert.True(t, pid.Path().Equals(item.ActorPath()))
		assert.Equal(t, mockErr.Error(), item.Reason())

		// unsubscribe the consumer
		err = actorSystem.Unsubscribe(consumer)
		require.NoError(t, err)

		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With SpawnFromFunc with actorSystem not started", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		receiveFn := func(_ context.Context, message any) error {
			expected := &testpb.Reply{Content: "test spawn from func"}
			actual, ok := message.(*testpb.Reply)
			require.True(t, ok)
			assert.True(t, proto.Equal(expected, actual))
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
		_, err := node.SpawnOn(ctx, actorName, actor)
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
		_, err := node1.SpawnOn(ctx, actorName, actor)
		require.NoError(t, err)

		pause.For(200 * time.Millisecond)

		// either we can locate the actor or try to recreate it
		_, err = node2.Spawn(ctx, actorName, actor)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorAlreadyExists)

		_, err = node1.SpawnOn(ctx, actorName, actor)
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
		_, err := actorSystem.SpawnOn(ctx, actorName, actor)
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
		_, err = actorSystem.SpawnOn(ctx, actorName, actor)
		require.Error(t, err)

		err = actorSystem.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("SpawnOn when cluster peers lookup fails", func(t *testing.T) {
		ctx := context.TODO()
		clusterMock := new(mockcluster.Cluster)

		system := MockReplicationTestSystem(clusterMock)
		system.remoting = remoteclient.NewClient()
		system.remotingEnabled.Store(true)

		actor := NewMockActor()
		actorName := "actorID"

		clusterMock.EXPECT().ActorExists(mock.Anything, actorName).Return(false, nil)
		clusterMock.EXPECT().Members(mock.Anything).Return(nil, assert.AnError)

		t.Cleanup(func() {
			system.remoting.Close()
			clusterMock.AssertExpectations(t)
		})

		_, err := system.SpawnOn(ctx, actorName, actor)
		require.Error(t, err)
		assert.ErrorIs(t, err, assert.AnError)
		assert.ErrorContains(t, err, "failed to fetch cluster nodes")
	})
	t.Run("SpawnOn remote includes reentrancy config", func(t *testing.T) {
		ctx := context.TODO()
		clusterMock := mockcluster.NewCluster(t)
		remotingMock := mocksremote.NewClient(t)

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
			Return(new("goakt://test@127.0.0.1:9000/actor"), nil).
			Once()

		_, err := system.SpawnOn(ctx, actorName, actor,
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
		clusterMock.EXPECT().Actors(mock.Anything, mock.Anything).Return(nil, nil).Once()
		clusterMock.EXPECT().PutActor(mock.Anything, mock.Anything).Return(nil).Once()

		_, err = actorSystem.SpawnOn(ctx, actorName, actor)
		require.NoError(t, err)

		pause.For(200 * time.Millisecond)

		actors, err := actorSystem.Actors(ctx, time.Second)
		require.NoError(t, err)
		require.Len(t, actors, 1)
		assert.Equal(t, actorName, actors[0].Name())
	})
	t.Run("SpawnOn with least load placement and no peers spawns locally", func(t *testing.T) {
		ctx := context.TODO()
		sys, err := NewActorSystem("spawn-least-load", WithLogger(log.DiscardLogger))
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

		// Return empty peers list, so LeastLoad should default to local spawn
		clusterMock.EXPECT().ActorExists(mock.Anything, actorName).Return(false, nil).Twice()
		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{}, nil)
		clusterMock.EXPECT().Actors(mock.Anything, mock.Anything).Return(nil, nil).Once()
		clusterMock.EXPECT().PutActor(mock.Anything, mock.Anything).Return(nil).Once()

		_, err = actorSystem.SpawnOn(ctx, actorName, actor, WithPlacement(LeastLoad))
		require.NoError(t, err)

		pause.For(200 * time.Millisecond)

		// Verify actor was spawned locally
		actors, err := actorSystem.Actors(ctx, time.Second)
		require.NoError(t, err)
		require.Len(t, actors, 1)
		assert.Equal(t, actorName, actors[0].Name())
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
		_, err := node1.SpawnOn(ctx, actorName, actor, WithPlacement(Random))
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
		_, err := node1.SpawnOn(ctx, actorName, actor, WithPlacement(Local))
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
		_, err := node1.SpawnOn(ctx, actorName, actor, WithPlacement(LeastLoad))
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
		_, err := node1.SpawnOn(ctx, actorName, actor, WithPlacement(LeastLoad))
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
		_, err := node1.SpawnOn(ctx, actorName, actor, WithPlacement(LeastLoad))
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
		_, err := node1.SpawnOn(ctx, actorName, actor, WithPlacement(LeastLoad))
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
		_, err := node1.SpawnOn(ctx, actorName, actor, WithRole(role))
		require.NoError(t, err)

		pause.For(200 * time.Millisecond)

		_, err = node2.Spawn(ctx, actorName, actor)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorAlreadyExists)

		_, err = node1.SpawnOn(ctx, actorName, actor, WithRole(role))
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
		_, err := node1.SpawnOn(ctx, actorName, actor, WithRole(role))
		require.Error(t, err)
		assert.ErrorContains(t, err, fmt.Sprintf("no nodes with role %s found in the cluster", role))

		_, err = node3.SpawnOn(ctx, actorName, actor, WithRole(role))
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
		_, err := node1.SpawnOn(ctx, actorName, actor, WithRole(role), WithPlacement(Local))
		require.NoError(t, err)

		pause.For(200 * time.Millisecond)
		actors, err := node1.Actors(ctx, time.Second)
		require.NoError(t, err)
		assert.Len(t, actors, 1)
		got := actors[0]
		assert.Equal(t, actorName, got.Name())

		_, err = node2.Spawn(ctx, actorName, actor)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorAlreadyExists)

		_, err = node1.SpawnOn(ctx, actorName, actor, WithRole(role))
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
		_, err := node1.SpawnOn(ctx, actorName, actor, WithPlacement(RoundRobin))
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
		system.remoting = remoteclient.NewClient()
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
		_, err := system.SpawnOn(ctx, actorName, actor, WithPlacement(RoundRobin))
		require.Error(t, err)
		assert.ErrorIs(t, err, assert.AnError)
	})

	t.Run("SpawnOn with WithDataCenter delegates to spawnOnDatacenter", func(t *testing.T) {
		ctx := context.Background()
		remotingMock := mocksremote.NewClient(t)
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
			Return(new("goakt://test@127.0.0.1:9000/actor"), nil).
			Once()

		actor := NewMockActor()
		_, err := sys.SpawnOn(ctx, "actor-1", actor, WithDataCenter(&targetDC))
		require.NoError(t, err)
		remotingMock.AssertExpectations(t)
	})
	t.Run("With Spawn when RemoteSpawn fails", func(t *testing.T) {
		ctx := context.TODO()
		remotingMock := mocksremote.NewClient(t)
		clusterMock := mockcluster.NewCluster(t)

		system := MockReplicationTestSystem(clusterMock)
		system.remoting = remotingMock
		system.remotingEnabled.Store(true)

		host := "127.0.0.1"
		port := 9000
		actorName := "actorID"
		actor := NewMockActor()

		remotingMock.EXPECT().
			RemoteSpawn(mock.Anything, host, port, mock.MatchedBy(func(req *remote.SpawnRequest) bool {
				return req.Name == actorName && req.Kind != ""
			})).
			Return(nil, assert.AnError).
			Once()

		pid, err := system.Spawn(ctx, actorName, actor, WithHostAndPort(host, port))
		require.Error(t, err)
		assert.ErrorIs(t, err, assert.AnError)
		assert.Nil(t, pid)
	})
}

func TestSpawnOnDatacenter(t *testing.T) {
	ctx := context.Background()

	t.Run("returns ErrDataCenterNotReady when controller is nil", func(t *testing.T) {
		dcConfig := datacenter.NewConfig()
		dcConfig.ControlPlane = &MockControlPlane{}
		dcConfig.DataCenter = datacenter.DataCenter{Name: "local", Region: "r", Zone: "z"}

		sys := MockReplicationTestSystem(mockcluster.NewCluster(t))
		sys.remoting = mocksremote.NewClient(t)
		sys.remotingEnabled.Store(true)
		sys.clusterConfig = NewClusterConfig().WithDataCenter(dcConfig)
		sys.dataCenterController = nil

		config := newSpawnConfig(WithDataCenter(&datacenter.DataCenter{Name: "dc-west", Region: "r", Zone: "z"}))
		actor := NewMockActor()

		_, err := sys.spawnOnDatacenter(ctx, "actor-1", actor, config)
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
		dcConfig.DataCenter = datacenter.DataCenter{Name: "local", Region: "r", Zone: "z"}
		dcConfig.MaxCacheStaleness = 1 * time.Millisecond
		dcConfig.CacheRefreshInterval = 5 * time.Millisecond

		controller, err := datacentercontroller.NewController(dcConfig, []string{"127.0.0.1:8080"})
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
		sys.remoting = mocksremote.NewClient(t)
		sys.remotingEnabled.Store(true)
		sys.clusterConfig = NewClusterConfig().WithDataCenter(dcConfig)
		sys.dataCenterController = controller

		config := newSpawnConfig(WithDataCenter(&datacenter.DataCenter{Name: "dc-west", Region: "r", Zone: "z"}))
		actor := NewMockActor()

		_, err = sys.spawnOnDatacenter(ctx, "actor-1", actor, config)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrDataCenterStaleRecords)
	})

	t.Run("returns ErrDataCenterRecordNotFound when target DC not in active records", func(t *testing.T) {
		remotingMock := mocksremote.NewClient(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{{
				ID:        "dc-other",
				State:     datacenter.DataCenterActive,
				Endpoints: []string{"127.0.0.1:9999"},
			}}, nil
		}, remotingMock)

		config := newSpawnConfig(WithDataCenter(&datacenter.DataCenter{Name: "dc-west", Region: "r", Zone: "z"}))
		actor := NewMockActor()

		_, err := sys.spawnOnDatacenter(ctx, "actor-1", actor, config)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrDataCenterRecordNotFound)
	})

	t.Run("returns ErrDataCenterRecordNotFound when no active records", func(t *testing.T) {
		remotingMock := mocksremote.NewClient(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return nil, nil
		}, remotingMock)

		config := newSpawnConfig(WithDataCenter(&datacenter.DataCenter{Name: "dc-west", Region: "r", Zone: "z"}))
		actor := NewMockActor()

		_, err := sys.spawnOnDatacenter(ctx, "actor-1", actor, config)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrDataCenterRecordNotFound)
	})

	t.Run("returns ErrDataCenterRecordNotFound when target DC record exists but state is not ACTIVE", func(t *testing.T) {
		remotingMock := mocksremote.NewClient(t)
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

		_, err := sys.spawnOnDatacenter(ctx, "actor-1", actor, config)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrDataCenterRecordNotFound)
	})

	t.Run("returns error when endpoint has invalid host:port format", func(t *testing.T) {
		remotingMock := mocksremote.NewClient(t)
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

		_, err := sys.spawnOnDatacenter(ctx, "actor-1", actor, config)
		require.Error(t, err)
		assert.ErrorContains(t, err, "failed to split host and port from endpoint")
	})

	t.Run("returns error when endpoint port is not numeric", func(t *testing.T) {
		remotingMock := mocksremote.NewClient(t)
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

		_, err := sys.spawnOnDatacenter(ctx, "actor-1", actor, config)
		require.Error(t, err)
		assert.ErrorContains(t, err, "failed to convert port to int")
	})

	t.Run("returns RemoteSpawn error when remoting fails", func(t *testing.T) {
		remotingMock := mocksremote.NewClient(t)
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
			Return(nil, gerrors.ErrTypeNotRegistered).
			Once()

		config := newSpawnConfig(WithDataCenter(&targetDC), WithRelocationDisabled())
		actor := NewMockActor()

		_, err := sys.spawnOnDatacenter(ctx, "actor-1", actor, config)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrTypeNotRegistered)
	})

	t.Run("succeeds and calls RemoteSpawn with correct request", func(t *testing.T) {
		remotingMock := mocksremote.NewClient(t)
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
			Return(new("goakt://test@127.0.0.1:9000/actor"), nil).
			Once()

		config := newSpawnConfig(WithDataCenter(&targetDC))
		actor := NewMockActor()

		_, err := sys.spawnOnDatacenter(ctx, "actor-1", actor, config)
		require.NoError(t, err)
		remotingMock.AssertExpectations(t)
	})

	t.Run("passes relocatable and passivation from config to RemoteSpawn", func(t *testing.T) {
		remotingMock := mocksremote.NewClient(t)
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
			Return(new("goakt://test@127.0.0.1:9000/actor"), nil).
			Once()

		config := newSpawnConfig(
			WithDataCenter(&targetDC),
			WithRelocationDisabled(),
			WithPassivationStrategy(passivationStrategy),
			WithStashing(),
		)
		actor := NewMockActor()

		_, err := sys.spawnOnDatacenter(ctx, "actor-2", actor, config)
		require.NoError(t, err)
		remotingMock.AssertExpectations(t)
	})
}

func TestRecreateActorFromWireRestoresRecordOnFailure(t *testing.T) {
	// releaseDepartedEntry removes the registry record before the respawn; when
	// the respawn then fails, the record must be restored so the actor stays
	// recoverable instead of being silently lost
	clusterMock := mockcluster.NewCluster(t)
	system := MockReplicationTestSystem(clusterMock)
	system.registry = types.NewRegistry()
	system.reflection = newReflection(system.registry)

	departedNode := "127.0.0.1:8080"
	record := &internalpb.Actor{
		Address:     address.New("phoenix", system.name, "127.0.0.1", 8080).String(),
		Type:        "unregistered.Type",
		Relocatable: true,
	}

	clusterMock.EXPECT().GetActor(mock.Anything, "phoenix").Return(record, nil).Once()
	clusterMock.EXPECT().RemoveActor(mock.Anything, "phoenix").Return(nil).Once()
	// the restore after the failed instantiation
	clusterMock.EXPECT().PutActor(mock.Anything, record).Return(nil).Once()

	err := system.recreateActorFromWire(context.Background(), record, departedNode)
	require.Error(t, err)
	clusterMock.AssertExpectations(t)
}

func TestRecreateActorFromWireRestoreFailureKeepsRespawnError(t *testing.T) {
	// a failed restore is logged, not propagated: the caller reports the
	// respawn error itself
	clusterMock := mockcluster.NewCluster(t)
	system := MockReplicationTestSystem(clusterMock)
	system.registry = types.NewRegistry()
	system.reflection = newReflection(system.registry)

	departedNode := "127.0.0.1:8080"
	record := &internalpb.Actor{
		Address:     address.New("phoenix", system.name, "127.0.0.1", 8080).String(),
		Type:        "unregistered.Type",
		Relocatable: true,
	}

	clusterMock.EXPECT().GetActor(mock.Anything, "phoenix").Return(record, nil).Once()
	clusterMock.EXPECT().RemoveActor(mock.Anything, "phoenix").Return(nil).Once()
	clusterMock.EXPECT().PutActor(mock.Anything, record).Return(assert.AnError).Once()

	err := system.recreateActorFromWire(context.Background(), record, departedNode)
	require.Error(t, err)
	require.NotErrorIs(t, err, assert.AnError)
	clusterMock.AssertExpectations(t)
}

func TestRecreateActorFromWireRestoresRecordOnSpawnOptionsFailure(t *testing.T) {
	// the actor type is registered but its serialized dependencies cannot be
	// rebuilt: the record must be restored so the actor stays recoverable
	clusterMock := mockcluster.NewCluster(t)
	system := MockReplicationTestSystem(clusterMock)
	system.registry = types.NewRegistry()
	system.registry.Register(new(MockActor))
	system.reflection = newReflection(system.registry)

	departedNode := "127.0.0.1:8080"
	record := &internalpb.Actor{
		Address:     address.New("phoenix", system.name, "127.0.0.1", 8080).String(),
		Type:        types.Name(new(MockActor)),
		Relocatable: true,
		Dependencies: []*internalpb.Dependency{
			{TypeName: "unregistered.Dependency"},
		},
	}

	clusterMock.EXPECT().GetActor(mock.Anything, "phoenix").Return(record, nil).Once()
	clusterMock.EXPECT().RemoveActor(mock.Anything, "phoenix").Return(nil).Once()
	clusterMock.EXPECT().PutActor(mock.Anything, record).Return(nil).Once()

	err := system.recreateActorFromWire(context.Background(), record, departedNode)
	require.Error(t, err)
	clusterMock.AssertExpectations(t)
}

func TestSpawnPublishFailureStopsActor(t *testing.T) {
	// a spawn whose synchronous cluster publication fails must return the
	// error and leave nothing behind: the actor is stopped and removed
	ctx := context.Background()
	sys, err := NewActorSystem("spawn-publish-failure", WithLogger(log.DiscardLogger))
	require.NoError(t, err)

	actorSystem := sys.(*actorSystem)
	require.NoError(t, actorSystem.Start(ctx))

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

	actorName := "publish-failure"
	removed := make(chan struct{}, 1)
	clusterMock.EXPECT().ActorExists(mock.Anything, actorName).Return(false, nil).Once()
	clusterMock.EXPECT().PutActor(mock.Anything, mock.Anything).Return(assert.AnError).Once()
	// the rollback stops the actor, whose death watch removes it from the cluster
	clusterMock.EXPECT().RemoveActor(mock.Anything, actorName).RunAndReturn(func(context.Context, string) error {
		removed <- struct{}{}
		return nil
	}).Once()

	pid, err := actorSystem.Spawn(ctx, actorName, NewMockActor())
	require.Error(t, err)
	assert.ErrorIs(t, err, assert.AnError)
	assert.Nil(t, pid)

	// the death watch removes the failed actor asynchronously
	select {
	case <-removed:
	case <-time.After(time.Second):
		t.Fatal("failed spawn must remove the actor from the cluster")
	}

	_, ok := actorSystem.actors.nodeByName(actorName)
	require.False(t, ok, "failed spawn must leave no actor behind")
}

func TestSpawnSingletonPublishFailureLeavesNothingBehind(t *testing.T) {
	// a singleton spawn whose synchronous cluster publication fails must
	// leave no actor behind so the singleton can be created elsewhere
	ctx := context.Background()
	sys, err := NewActorSystem("singleton-publish-failure", WithLogger(log.DiscardLogger))
	require.NoError(t, err)

	actorSystem := sys.(*actorSystem)
	require.NoError(t, actorSystem.Start(ctx))

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

	actorName := "singleton-publish-failure"
	clusterMock.EXPECT().ActorExists(mock.Anything, actorName).Return(false, nil).Once()
	clusterMock.EXPECT().PutActor(mock.Anything, mock.Anything).Return(assert.AnError).Once()
	// the death watch removes the failed actor from the cluster best-effort
	clusterMock.EXPECT().RemoveActor(mock.Anything, actorName).Return(nil).Maybe()

	pid, err := actorSystem.spawnSingletonOnLocal(ctx, actorName, NewMockActor(), nil, time.Second, 100*time.Millisecond, 1, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, assert.AnError)
	assert.Nil(t, pid)

	require.Eventually(t, func() bool {
		_, ok := actorSystem.actors.nodeByName(actorName)
		return !ok
	}, time.Second, 10*time.Millisecond, "failed singleton spawn must leave no actor behind")
}

// TestSpawnOnImmediateCrossNodeVisibility is the regression test for
// https://github.com/Tochemey/goakt/issues/1263: SpawnOn only returns once the
// actor's registry record is written to the cluster store, so an immediate
// name-based lookup and send from another node must succeed without retries.
func TestSpawnOnImmediateCrossNodeVisibility(t *testing.T) {
	ctx := context.Background()

	// start the NATS server
	srv := startNatsServer(t)
	t.Cleanup(srv.Shutdown)

	systems, providers := testNATsConcurrent(t, srv.Addr().String(), 2)
	node1, node2 := systems[0], systems[1]
	sd1, sd2 := providers[0], providers[1]

	t.Cleanup(func() {
		assert.NoError(t, node1.Stop(ctx))
		assert.NoError(t, node2.Stop(ctx))
		assert.NoError(t, sd1.Close())
		assert.NoError(t, sd2.Close())
	})

	// each actor spawned on node1 must be resolvable and reachable from node2
	// immediately, with no retry and no settling pause
	for i := range 10 {
		name := fmt.Sprintf("cross-node-%d", i)

		pid, err := node1.SpawnOn(ctx, name, NewMockActor(), WithPlacement(Local))
		require.NoError(t, err)
		require.NotNil(t, pid)

		remotePID, err := node2.ActorOf(ctx, name)
		require.NoError(t, err, "actor=%s must be visible from another node as soon as SpawnOn returns", name)
		require.NotNil(t, remotePID)
		require.True(t, remotePID.IsRemote())

		// a message sent right away must reach the actor
		reply, err := Ask(ctx, remotePID, new(testpb.TestReply), time.Second)
		require.NoError(t, err, "actor=%s must be reachable from another node as soon as SpawnOn returns", name)
		require.NotNil(t, reply)
	}
}

// TestConcurrentSpawnSameNameCreatesSingleActor verifies that many goroutines
// racing to Spawn the same name converge on a single live actor: only one
// instance starts, every caller gets the same PID, and the tree holds exactly
// one node for that name.
func TestConcurrentSpawnSameNameCreatesSingleActor(t *testing.T) {
	ctx := context.Background()
	sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	t.Cleanup(func() { _ = sys.Stop(ctx) })

	starts := &atomic.Int64{}
	pids, errs := spawnConcurrently(t, 64, func() (*PID, error) {
		return sys.Spawn(ctx, "concurrent", &countingActor{starts: starts})
	})

	requireSamePID(t, pids, errs)
	require.EqualValues(t, 1, starts.Load(), "more than one actor instance was created")

	node, ok := sys.tree().nodeByName("concurrent")
	require.True(t, ok)
	require.Equal(t, pids[0], node.value())
	require.EqualValues(t, 1, sys.NumActors())
}

// TestConcurrentSpawnNamedFromFuncSingleActor exercises the same race on the
// function-based spawn path.
func TestConcurrentSpawnNamedFromFuncSingleActor(t *testing.T) {
	ctx := context.Background()
	sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	t.Cleanup(func() { _ = sys.Stop(ctx) })

	receiveFn := func(context.Context, any) error { return nil }
	pids, errs := spawnConcurrently(t, 64, func() (*PID, error) {
		return sys.SpawnNamedFromFunc(ctx, "func-actor", receiveFn)
	})

	requireSamePID(t, pids, errs)
	node, ok := sys.tree().nodeByName("func-actor")
	require.True(t, ok)
	require.Equal(t, pids[0], node.value())
	require.EqualValues(t, 1, sys.NumActors())
}

// TestConcurrentSpawnChildCreatesSingleChild exercises the race on the child
// spawn path (PID.SpawnChild).
func TestConcurrentSpawnChildCreatesSingleChild(t *testing.T) {
	ctx := context.Background()
	sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	t.Cleanup(func() { _ = sys.Stop(ctx) })

	parent, err := sys.Spawn(ctx, "parent", NewMockActor())
	require.NoError(t, err)

	starts := &atomic.Int64{}
	pids, errs := spawnConcurrently(t, 64, func() (*PID, error) {
		return parent.SpawnChild(ctx, "child", &countingActor{starts: starts})
	})

	requireSamePID(t, pids, errs)
	require.EqualValues(t, 1, starts.Load(), "more than one child instance was created")

	children := parent.Children()
	require.Len(t, children, 1)
	require.Equal(t, pids[0], children[0])
}

// TestCompleteSpawnDuplicateReturnsCanonical exercises the completeSpawn safety
// net directly (bypassing the singleflight serialization): completing the spawn
// of a second, distinct PID instance for an already-registered identity must
// return the canonical instance, leave the canonical tree node intact and
// running, and must not inflate the actors counter.
func TestCompleteSpawnDuplicateReturnsCanonical(t *testing.T) {
	ctx := context.Background()
	sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	t.Cleanup(func() { _ = sys.Stop(ctx) })

	actorSys := sys.(*actorSystem)
	canonical, err := sys.Spawn(ctx, "dup", NewMockActor())
	require.NoError(t, err)
	before := sys.NumActors()

	// A distinct instance sharing the same identity (name/address).
	duplicate, err := actorSys.configPID(ctx, "dup", NewMockActor())
	require.NoError(t, err)
	require.NotSame(t, canonical, duplicate)

	got, err := actorSys.completeSpawn(ctx, actorSys.getUserGuardian(), duplicate)
	require.NoError(t, err)
	require.Same(t, canonical, got, "completeSpawn must return the canonical instance")
	require.Equal(t, before, sys.NumActors(), "duplicate handling must undo the counter bump")

	// The canonical node survives duplicate handling: still in the tree, still
	// the same instance, still running.
	node, ok := actorSys.tree().nodeByName("dup")
	require.True(t, ok)
	require.Same(t, canonical, node.value())
	require.True(t, canonical.IsRunning())

	// NOTE: the duplicate is intentionally not shut down here (and completeSpawn
	// intentionally does not stop it): its shutdown would resolve tree state by
	// the shared pid.ID() and detach the canonical node.
}

// TestTreeAddNodeDuplicateReturnsSentinel asserts the tree reports a duplicate
// insertion with the typed sentinel completeSpawn relies on.
func TestTreeAddNodeDuplicateReturnsSentinel(t *testing.T) {
	ctx := context.Background()
	sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	t.Cleanup(func() { _ = sys.Stop(ctx) })

	actorSys := sys.(*actorSystem)
	pid, err := sys.Spawn(ctx, "sentinel", NewMockActor())
	require.NoError(t, err)

	err = actorSys.tree().addNode(actorSys.getUserGuardian(), pid)
	require.ErrorIs(t, err, errNodeAlreadyExists)
}

// TestRunSpawnActivationHonorsCallerContext verifies the wait on an in-flight
// spawn is context-aware: a waiter whose context is canceled stops waiting with
// ctx.Err() instead of blocking until the winner finishes, an already-done
// context is rejected without starting or joining a flight, and in both cases
// the caller's own spawn function never runs.
func TestRunSpawnActivationHonorsCallerContext(t *testing.T) {
	x := new(actorSystem)

	started := make(chan struct{})
	release := make(chan struct{})
	winnerDone := make(chan struct{})
	go func() {
		defer close(winnerDone)
		_, _ = x.runSpawnActivation(context.Background(), "key", func() (*PID, error) {
			close(started)
			<-release
			return nil, nil
		})
	}()
	<-started

	// a waiter abandons the in-flight spawn when its own context is canceled
	ctx, cancel := context.WithCancel(context.Background())
	ran := atomic.NewBool(false)
	waiterErr := make(chan error, 1)
	go func() {
		_, err := x.runSpawnActivation(ctx, "key", func() (*PID, error) {
			ran.Store(true)
			return nil, nil
		})
		waiterErr <- err
	}()
	pause.For(50 * time.Millisecond) // let the waiter join the flight
	cancel()
	require.ErrorIs(t, <-waiterErr, context.Canceled)
	require.False(t, ran.Load(), "the abandoned caller's spawn function must not run")

	// an already-done context is rejected upfront, while the flight is still in progress
	_, err := x.runSpawnActivation(ctx, "key", func() (*PID, error) {
		ran.Store(true)
		return nil, nil
	})
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, ran.Load(), "the rejected caller's spawn function must not run")

	close(release)
	<-winnerDone
}

// TestRunSpawnActivationRetriesInheritedCancellation verifies a waiter does not
// fail with a cancellation that was never its own: when the winning caller's
// context aborts the shared spawn, a waiter whose context is still live re-runs
// the spawn once instead of surfacing the winner's context error.
func TestRunSpawnActivationRetriesInheritedCancellation(t *testing.T) {
	x := new(actorSystem)

	winnerCtx, winnerCancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	winnerDone := make(chan error, 1)
	go func() {
		_, err := x.runSpawnActivation(winnerCtx, "key", func() (*PID, error) {
			close(entered)
			// the winner's spawn is aborted by its own context
			<-winnerCtx.Done()
			return nil, winnerCtx.Err()
		})
		winnerDone <- err
	}()
	<-entered

	// join the in-flight spawn as a waiter with a healthy context
	reran := atomic.NewBool(false)
	waiterDone := make(chan error, 1)
	go func() {
		_, err := x.runSpawnActivation(context.Background(), "key", func() (*PID, error) {
			reran.Store(true)
			return nil, nil
		})
		waiterDone <- err
	}()

	// let the waiter coalesce onto the winner's flight, then abort the winner.
	// If the waiter joins late it simply starts its own flight, which passes too.
	pause.For(50 * time.Millisecond)
	winnerCancel()

	require.ErrorIs(t, <-winnerDone, context.Canceled)
	require.NoError(t, <-waiterDone)
	require.True(t, reran.Load(), "the waiter must re-run the spawn after inheriting the winner's cancellation")
}

// spawnConcurrently runs spawn from n goroutines released simultaneously and
// returns their results.
func spawnConcurrently(t *testing.T, n int, spawn func() (*PID, error)) ([]*PID, []error) {
	t.Helper()
	var wg sync.WaitGroup
	gate := make(chan struct{})
	pids := make([]*PID, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-gate
			pids[i], errs[i] = spawn()
		}(i)
	}
	close(gate)
	wg.Wait()
	return pids, errs
}

// requireSamePID asserts every spawn succeeded, is running, and returned the
// same PID.
func requireSamePID(t *testing.T, pids []*PID, errs []error) {
	t.Helper()
	for i, pid := range pids {
		require.NoErrorf(t, errs[i], "call %d failed", i)
		require.NotNilf(t, pid, "call %d returned a nil pid", i)
		require.Truef(t, pid.IsRunning(), "call %d returned a non-running pid", i)
		require.Truef(t, pids[0].Equals(pid), "call %d returned a different pid", i)
	}
}
