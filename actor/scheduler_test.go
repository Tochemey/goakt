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
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"

	"github.com/tochemey/goakt/v4/address"
	"github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	testkit "github.com/tochemey/goakt/v4/mocks/discovery"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

func TestScheduler(t *testing.T) {
	t.Run("With ScheduleOnce", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// create the actor system
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		pause.For(time.Second)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		err = newActorSystem.ScheduleOnce(ctx, message, actorRef, 100*time.Millisecond)
		require.NoError(t, err)

		pause.For(time.Second)
		typedSystem := newActorSystem.(*actorSystem)
		keys, err := typedSystem.scheduler.quartzScheduler.GetJobKeys()
		require.NoError(t, err)
		assert.Empty(t, keys)
		assert.EqualValues(t, 1, actorRef.ProcessedCount()-1)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With ScheduleOnce repeated calls", func(t *testing.T) {
		// This test verifies that repeated ScheduleOnce calls work correctly
		// and don't stop triggering after some time (issue #1037)
		ctx := context.TODO()
		logger := log.DiscardLogger
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		require.NoError(t, err)

		err = newActorSystem.Start(ctx)
		require.NoError(t, err)

		pause.For(time.Second)

		actorName := "test"
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		require.NotNil(t, actorRef)

		pause.For(time.Second)

		// Schedule multiple messages with short delays
		numMessages := 20
		for i := 0; i < numMessages; i++ {
			message := new(testpb.TestSend)
			err = newActorSystem.ScheduleOnce(ctx, message, actorRef, 50*time.Millisecond)
			require.NoError(t, err)
			// Small pause between scheduling to simulate real-world usage
			pause.For(10 * time.Millisecond)
		}

		// Wait for all messages to be delivered
		require.Eventually(t, func() bool {
			// ProcessedCount includes the initial PostStart message, so we subtract 1
			return actorRef.ProcessedCount()-1 >= numMessages
		}, 5*time.Second, 100*time.Millisecond)

		processed := actorRef.ProcessedCount() - 1
		assert.GreaterOrEqual(t, processed, numMessages)

		err = newActorSystem.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("With ScheduleOnce when cluster is enabled", func(t *testing.T) {
		ctx := context.TODO()
		nodePorts := dynaport.Get(3)
		discoveryPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		logger := log.DiscardLogger
		host := "127.0.0.1"

		// define discovered addresses
		addrs := []string{
			net.JoinHostPort(host, strconv.Itoa(discoveryPort)),
		}

		// mock the discovery provider
		provider := new(testkit.Provider)
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
					WithDiscoveryPort(discoveryPort).
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

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		pause.For(time.Second)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		err = newActorSystem.ScheduleOnce(ctx, message, actorRef, 100*time.Millisecond)
		require.NoError(t, err)

		pause.For(time.Second)
		typedSystem := newActorSystem.(*actorSystem)
		keys, err := typedSystem.scheduler.quartzScheduler.GetJobKeys()
		require.NoError(t, err)
		assert.Empty(t, keys)
		assert.EqualValues(t, 1, actorRef.ProcessedCount()-1)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		require.NoError(t, err)
		provider.AssertExpectations(t)
	})
	t.Run("With ScheduleOnce when actor not started", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// create the actor system
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		pause.For(time.Second)
		require.NoError(t, newActorSystem.Kill(ctx, actorName))

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		err = newActorSystem.ScheduleOnce(ctx, message, actorRef, 100*time.Millisecond)
		require.NoError(t, err)

		pause.For(time.Second)
		typedSystem := newActorSystem.(*actorSystem)
		keys, err := typedSystem.scheduler.quartzScheduler.GetJobKeys()
		require.NoError(t, err)
		assert.Empty(t, keys)
		assert.EqualValues(t, 0, actorRef.ProcessedCount())

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With ScheduleOnce with scheduler not started", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// create the actor system
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		scheduler := newActorSystem.(*actorSystem).scheduler
		scheduler.Stop(ctx)

		// create the actor ref
		pid, err := newActorSystem.Spawn(ctx, "test", NewMockActor())
		require.NoError(t, err)
		assert.NotNil(t, pid)

		message := new(testpb.TestSend)
		err = scheduler.ScheduleOnce(message, pid, 100*time.Millisecond)
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrSchedulerNotStarted)

		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("With ScheduleOnce for remote actor", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewClient()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, newActorSystem.Host(), int(newActorSystem.Port()), actorName)
		require.NoError(t, err)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		err = newActorSystem.ScheduleOnce(ctx, message, newRemotePID(addr, remoting), 100*time.Millisecond)
		require.NoError(t, err)

		pause.For(time.Second)
		typedSystem := newActorSystem.(*actorSystem)
		// for test purpose only
		keys, err := typedSystem.scheduler.quartzScheduler.GetJobKeys()
		require.NoError(t, err)
		assert.Empty(t, keys)
		assert.EqualValues(t, 1, actorRef.ProcessedCount()-1)

		remoting.Close()
		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With ScheduleOnce for remote actor when cluster is enabled", func(t *testing.T) {
		ctx := context.TODO()
		nodePorts := dynaport.Get(3)
		discoveryPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		logger := log.DiscardLogger
		host := "127.0.0.1"

		// define discovered addresses
		addrs := []string{
			net.JoinHostPort(host, strconv.Itoa(discoveryPort)),
		}

		// mock the discovery provider
		provider := new(testkit.Provider)
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
					WithDiscoveryPort(discoveryPort).
					WithDiscovery(provider)),
		)
		require.NoError(t, err)

		provider.EXPECT().ID().Return("nats")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		// start the actor system
		err = newActorSystem.Start(ctx)
		require.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewClient()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, newActorSystem.Host(), int(newActorSystem.Port()), actorName)
		require.NoError(t, err)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		err = newActorSystem.ScheduleOnce(ctx, message, newRemotePID(addr, remoting), 100*time.Millisecond)
		require.NoError(t, err)

		pause.For(time.Second)
		typedSystem := newActorSystem.(*actorSystem)
		// for test purpose only
		keys, err := typedSystem.scheduler.quartzScheduler.GetJobKeys()
		require.NoError(t, err)
		assert.Empty(t, keys)
		assert.EqualValues(t, 1, actorRef.ProcessedCount()-1)

		remoting.Close()
		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
		provider.AssertExpectations(t)
	})
	t.Run("With ScheduleOnce for remote actor with scheduler not started", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// test purpose only
		typedSystem := newActorSystem.(*actorSystem)
		typedSystem.scheduler.Stop(ctx)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewClient()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, newActorSystem.Host(), int(newActorSystem.Port()), actorName)
		require.NoError(t, err)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		err = newActorSystem.ScheduleOnce(ctx, message, newRemotePID(addr, remoting), 100*time.Millisecond)
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrSchedulerNotStarted)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With ScheduleWithCron", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// create the actor system
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		pause.For(time.Second)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		// set cron expression to run every second
		const expr = "* * * ? * *"
		err = newActorSystem.ScheduleWithCron(ctx, message, actorRef, expr)
		require.NoError(t, err)

		// wait for two seconds
		pause.For(2 * time.Second)
		assert.EqualValues(t, 2, actorRef.ProcessedCount()-1)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With ScheduleWithCron when cluster is enabled", func(t *testing.T) {
		ctx := context.TODO()
		nodePorts := dynaport.Get(3)
		discoveryPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		logger := log.DiscardLogger
		host := "127.0.0.1"

		// define discovered addresses
		addrs := []string{
			net.JoinHostPort(host, strconv.Itoa(discoveryPort)),
		}

		// mock the discovery provider
		provider := new(testkit.Provider)
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
					WithDiscoveryPort(discoveryPort).
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

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		pause.For(time.Second)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		// set cron expression to run every second
		const expr = "* * * ? * *"
		err = newActorSystem.ScheduleWithCron(ctx, message, actorRef, expr)
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			return actorRef.ProcessedCount()-1 >= 2
		}, 5*time.Second, 100*time.Millisecond)
		processed := actorRef.ProcessedCount() - 1
		assert.GreaterOrEqual(t, processed, 2)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
		provider.AssertExpectations(t)
	})
	t.Run("With ScheduleWithCron with invalid cron length", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// create the actor system
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		pause.For(time.Second)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		// set cron expression to run every minute
		const expr = "* * * * *"
		err = newActorSystem.ScheduleWithCron(ctx, message, actorRef, expr)
		require.Error(t, err)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With ScheduleWithCron with scheduler not started", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// create the actor system
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// test purpose only
		typedSystem := newActorSystem.(*actorSystem)
		typedSystem.scheduler.Stop(ctx)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		pause.For(time.Second)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		// set cron expression to run every second
		const expr = "* * * ? * *"
		err = newActorSystem.ScheduleWithCron(ctx, message, actorRef, expr)
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrSchedulerNotStarted)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With ScheduleWithCron for remote actor", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewClient()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, newActorSystem.Host(), int(newActorSystem.Port()), actorName)
		require.NoError(t, err)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		// set cron expression to run every second
		const expr = "* * * ? * *"
		err = newActorSystem.ScheduleWithCron(ctx, message, newRemotePID(addr, remoting), expr)
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			return actorRef.ProcessedCount()-1 >= 2
		}, 5*time.Second, 100*time.Millisecond)
		processed := actorRef.ProcessedCount() - 1
		assert.GreaterOrEqual(t, processed, 2)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With ScheduleWithCron for remote actor when cluster is enabled", func(t *testing.T) {
		ctx := context.TODO()
		nodePorts := dynaport.Get(3)
		discoveryPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		logger := log.DiscardLogger
		host := "127.0.0.1"

		// define discovered addresses
		addrs := []string{
			net.JoinHostPort(host, strconv.Itoa(discoveryPort)),
		}

		// mock the discovery provider
		provider := new(testkit.Provider)
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
					WithDiscoveryPort(discoveryPort).
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

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewClient()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, newActorSystem.Host(), int(newActorSystem.Port()), actorName)
		require.NoError(t, err)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		// set cron expression to run every second
		const expr = "* * * ? * *"
		err = newActorSystem.ScheduleWithCron(ctx, message, newRemotePID(addr, remoting), expr)
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			return actorRef.ProcessedCount()-1 >= 2
		}, 5*time.Second, 100*time.Millisecond)
		processed := actorRef.ProcessedCount() - 1
		assert.GreaterOrEqual(t, processed, 2)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
		provider.AssertExpectations(t)
	})
	t.Run("With ScheduleWithCron for remote actor with invalid cron expression", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewClient()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, newActorSystem.Host(), int(newActorSystem.Port()), actorName)
		require.NoError(t, err)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		// set cron expression
		const expr = "* * * * *"
		err = newActorSystem.ScheduleWithCron(ctx, message, newRemotePID(addr, remoting), expr)
		require.Error(t, err)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With ScheduleWithCron for remote actor with scheduler not started", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)
		// test purpose only
		typedSystem := newActorSystem.(*actorSystem)
		typedSystem.scheduler.Stop(ctx)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewClient()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, newActorSystem.Host(), int(newActorSystem.Port()), actorName)
		require.NoError(t, err)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		// set cron expression to run every second
		const expr = "* * * ? * *"
		err = newActorSystem.ScheduleWithCron(ctx, message, newRemotePID(addr, remoting), expr)
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrSchedulerNotStarted)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Schedule", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// create the actor system
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		require.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		require.NotNil(t, actorRef)

		pause.For(time.Second)

		// send a message to the actor after one second
		message := new(testpb.TestSend)
		err = newActorSystem.Schedule(ctx, message, actorRef, time.Second)
		require.NoError(t, err)

		pause.For(time.Second)

		typedSystem := newActorSystem.(*actorSystem)
		keys, err := typedSystem.scheduler.quartzScheduler.GetJobKeys()
		require.NoError(t, err)
		require.NotEmpty(t, keys)
		require.Len(t, keys, 1)

		pause.For(500 * time.Millisecond)
		require.EqualValues(t, 1, actorRef.ProcessedCount()-1)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("With Schedule when cluster is enabled", func(t *testing.T) {
		ctx := context.TODO()
		nodePorts := dynaport.Get(3)
		discoveryPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		logger := log.DiscardLogger
		host := "127.0.0.1"

		// define discovered addresses
		addrs := []string{
			net.JoinHostPort(host, strconv.Itoa(discoveryPort)),
		}

		// mock the discovery provider
		provider := new(testkit.Provider)
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
					WithDiscoveryPort(discoveryPort).
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

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		pause.For(time.Second)

		// send a message to the actor after one second
		message := new(testpb.TestSend)
		err = newActorSystem.Schedule(ctx, message, actorRef, time.Second)
		require.NoError(t, err)

		pause.For(time.Second)

		typedSystem := newActorSystem.(*actorSystem)
		keys, err := typedSystem.scheduler.quartzScheduler.GetJobKeys()
		require.NoError(t, err)
		require.NotEmpty(t, keys)
		require.Len(t, keys, 1)

		pause.For(500 * time.Millisecond)
		require.EqualValues(t, 1, actorRef.ProcessedCount()-1)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
		provider.AssertExpectations(t)
	})
	t.Run("With Schedule when actor not started", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// create the actor system
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		pause.For(time.Second)
		require.NoError(t, newActorSystem.Kill(ctx, actorName))

		// send a message to the actor after one second
		message := new(testpb.TestSend)
		err = newActorSystem.Schedule(ctx, message, actorRef, time.Second)
		require.NoError(t, err)

		pause.For(time.Second)

		typedSystem := newActorSystem.(*actorSystem)
		keys, err := typedSystem.scheduler.quartzScheduler.GetJobKeys()
		require.NoError(t, err)
		require.NotEmpty(t, keys)
		require.Len(t, keys, 1)

		pause.For(500 * time.Millisecond)
		require.EqualValues(t, 0, actorRef.ProcessedCount())

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Schedule with scheduler not started", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// create the actor system
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)
		// test purpose only
		typedSystem := newActorSystem.(*actorSystem)
		typedSystem.scheduler.Stop(ctx)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		pause.For(time.Second)

		// send a message to the actor after one second
		message := new(testpb.TestSend)
		err = newActorSystem.Schedule(ctx, message, actorRef, time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrSchedulerNotStarted)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Schedule for remote actor", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewClient()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, newActorSystem.Host(), int(newActorSystem.Port()), actorName)
		require.NoError(t, err)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		err = newActorSystem.Schedule(ctx, message, newRemotePID(addr, remoting), time.Second)
		require.NoError(t, err)

		pause.For(time.Second)

		typedSystem := newActorSystem.(*actorSystem)
		keys, err := typedSystem.scheduler.quartzScheduler.GetJobKeys()
		require.NoError(t, err)
		require.NotEmpty(t, keys)
		require.Len(t, keys, 1)

		pause.For(500 * time.Millisecond)
		require.EqualValues(t, 1, actorRef.ProcessedCount()-1)
		pause.For(800 * time.Millisecond)
		require.EqualValues(t, 2, actorRef.ProcessedCount()-1)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Schedule for remote actor with scheduler not started", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// test purpose only
		typedSystem := newActorSystem.(*actorSystem)
		typedSystem.scheduler.Stop(ctx)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewClient()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, newActorSystem.Host(), int(newActorSystem.Port()), actorName)
		require.NoError(t, err)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		err = newActorSystem.Schedule(ctx, message, newRemotePID(addr, remoting), 100*time.Millisecond)
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrSchedulerNotStarted)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Schedule for remote actor when actor not started", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewClient()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, newActorSystem.Host(), int(newActorSystem.Port()), actorName)
		require.NoError(t, err)

		require.NoError(t, newActorSystem.Kill(ctx, actorName))
		pause.For(time.Second)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		err = newActorSystem.Schedule(ctx, message, newRemotePID(addr, remoting), time.Second)
		require.NoError(t, err)

		pause.For(time.Second)

		typedSystem := newActorSystem.(*actorSystem)
		keys, err := typedSystem.scheduler.quartzScheduler.GetJobKeys()
		require.NoError(t, err)
		require.NotEmpty(t, keys)
		require.Len(t, keys, 1)

		pause.For(500 * time.Millisecond)
		require.Zero(t, actorRef.ProcessedCount())

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With ScheduleWithCron for remote actor when remoting not enabled", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		scheduler := newScheduler(logger, time.Second, nil)
		scheduler.Start(ctx)

		addr := address.New("test", "test", host, remotingPort)
		remotePID := newRemotePID(addr, nil)

		message := new(testpb.TestSend)
		const expr = "* * * ? * *"
		err := scheduler.ScheduleWithCron(message, remotePID, expr)
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrRemotingDisabled)

		scheduler.Stop(ctx)
	})
	t.Run("With Schedule for remote actor when remoting not enabled", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		scheduler := newScheduler(logger, time.Second, nil)
		scheduler.Start(ctx)

		addr := address.New("test", "test", host, remotingPort)
		remotePID := newRemotePID(addr, nil)

		message := new(testpb.TestSend)
		err := scheduler.Schedule(message, remotePID, time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrRemotingDisabled)

		scheduler.Stop(ctx)
	})
	t.Run("With ScheduleOnce for remote actor when remoting not enabled", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		scheduler := newScheduler(logger, time.Second, nil)
		scheduler.Start(ctx)

		addr := address.New("test", "test", host, remotingPort)
		remotePID := newRemotePID(addr, nil)

		message := new(testpb.TestSend)
		err := scheduler.ScheduleOnce(message, remotePID, time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrRemotingDisabled)
		scheduler.Stop(ctx)
	})
	t.Run("With Schedule for remote actor when cluster is enabled", func(t *testing.T) {
		ctx := context.TODO()
		nodePorts := dynaport.Get(3)
		discoveryPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		logger := log.DiscardLogger
		host := "127.0.0.1"

		// define discovered addresses
		addrs := []string{
			net.JoinHostPort(host, strconv.Itoa(discoveryPort)),
		}

		// mock the discovery provider
		provider := new(testkit.Provider)
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
					WithDiscoveryPort(discoveryPort).
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

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewClient()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, newActorSystem.Host(), int(newActorSystem.Port()), actorName)
		require.NoError(t, err)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		err = newActorSystem.Schedule(ctx, message, newRemotePID(addr, remoting), time.Second)
		require.NoError(t, err)

		pause.For(time.Second)

		typedSystem := newActorSystem.(*actorSystem)
		keys, err := typedSystem.scheduler.quartzScheduler.GetJobKeys()
		require.NoError(t, err)
		require.NotEmpty(t, keys)
		require.Len(t, keys, 1)

		pause.For(500 * time.Millisecond)
		require.EqualValues(t, 1, actorRef.ProcessedCount()-1)
		pause.For(800 * time.Millisecond)
		require.EqualValues(t, 2, actorRef.ProcessedCount()-1)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
		provider.AssertExpectations(t)
	})
	t.Run("With Pause and Resume Schedule", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
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
		require.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := actorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		require.NotNil(t, actorRef)

		pause.For(time.Second)

		message := new(testpb.TestSend)
		err = actorSystem.Schedule(ctx, message, actorRef, 10*time.Millisecond, WithReference("reference"))
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			return actorRef.ProcessedCount()-1 >= 5
		}, 2*time.Second, 10*time.Millisecond)
		require.NoError(t, actorSystem.PauseSchedule("reference"))

		require.Eventually(t, func() bool {
			paused := actorRef.ProcessedCount() - 1
			pause.For(25 * time.Millisecond) // > interval; ensures no further ticks
			return actorRef.ProcessedCount()-1 == paused
		}, 750*time.Millisecond, 25*time.Millisecond)
		processedAtPause := actorRef.ProcessedCount() - 1

		require.NoError(t, actorSystem.ResumeSchedule("reference"))
		require.Eventually(t, func() bool {
			return actorRef.ProcessedCount()-1 >= processedAtPause+5
		}, 2*time.Second, 10*time.Millisecond)

		// stop the actor
		err = actorSystem.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("With PauseSchedule with scheduler not started", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// create the actor system
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)
		// test purpose only
		typedSystem := newActorSystem.(*actorSystem)
		typedSystem.scheduler.Stop(ctx)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		pause.For(time.Second)

		err = newActorSystem.PauseSchedule("reference")
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrSchedulerNotStarted)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With ResumeSchedule with scheduler not started", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// create the actor system
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)
		// test purpose only
		typedSystem := newActorSystem.(*actorSystem)
		typedSystem.scheduler.Stop(ctx)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		pause.For(time.Second)

		err = newActorSystem.ResumeSchedule("reference")
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrSchedulerNotStarted)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Pause, Cancel and Resume Schedule with no reference found", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
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
		require.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := actorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		require.NotNil(t, actorRef)

		pause.For(time.Second)

		require.ErrorIs(t, actorSystem.PauseSchedule("reference"), errors.ErrScheduledReferenceNotFound)
		require.ErrorIs(t, actorSystem.ResumeSchedule("reference"), errors.ErrScheduledReferenceNotFound)
		require.ErrorIs(t, actorSystem.CancelSchedule("reference"), errors.ErrScheduledReferenceNotFound)

		// stop the actor
		err = actorSystem.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("With CancelSchedule", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// create the actor system
		system, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = system.Start(ctx)
		require.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := system.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		require.NotNil(t, actorRef)

		pause.For(time.Second)

		message := new(testpb.TestSend)
		err = system.Schedule(ctx, message, actorRef, 10*time.Millisecond, WithReference("reference"))
		require.NoError(t, err)

		pause.For(55 * time.Millisecond)
		require.EqualValues(t, 5, actorRef.ProcessedCount()-1)
		require.NoError(t, system.CancelSchedule("reference"))

		pause.For(55 * time.Millisecond)
		require.EqualValues(t, 5, actorRef.ProcessedCount()-1)

		typedSystem := system.(*actorSystem)
		keys, err := typedSystem.scheduler.quartzScheduler.GetJobKeys()
		require.NoError(t, err)
		require.Empty(t, keys)
		require.Len(t, keys, 0)

		// stop the actor
		err = system.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("With CancelSchedule with scheduler not started", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// create the actor system
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)
		// test purpose only
		typedSystem := newActorSystem.(*actorSystem)
		typedSystem.scheduler.Stop(ctx)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		pause.For(time.Second)

		err = newActorSystem.CancelSchedule("reference")
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrSchedulerNotStarted)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
}
