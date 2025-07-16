/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"

	"github.com/tochemey/goakt/v4/address"
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
		assert.ErrorIs(t, err, ErrSchedulerNotStarted)

		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("With RemoteScheduleOnce", func(t *testing.T) {
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

		remoting := NewRemoting()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, newActorSystem.Host(), int(newActorSystem.Port()), actorName)
		require.NoError(t, err)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		err = newActorSystem.RemoteScheduleOnce(ctx, message, addr, 100*time.Millisecond)
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
	t.Run("With RemoteScheduleOnce when cluster is enabled", func(t *testing.T) {
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

		remoting := NewRemoting()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, newActorSystem.Host(), int(newActorSystem.Port()), actorName)
		require.NoError(t, err)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		err = newActorSystem.RemoteScheduleOnce(ctx, message, addr, 100*time.Millisecond)
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
	t.Run("With RemoteScheduleOnce with scheduler not started", func(t *testing.T) {
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

		remoting := NewRemoting()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, newActorSystem.Host(), int(newActorSystem.Port()), actorName)
		require.NoError(t, err)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		err = newActorSystem.RemoteScheduleOnce(ctx, message, addr, 100*time.Millisecond)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrSchedulerNotStarted)

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

		// wait for two seconds
		pause.For(2 * time.Second)
		assert.EqualValues(t, 2, actorRef.ProcessedCount()-1)

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
		assert.ErrorIs(t, err, ErrSchedulerNotStarted)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With RemoteScheduleWithCron", func(t *testing.T) {
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

		remoting := NewRemoting()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, newActorSystem.Host(), int(newActorSystem.Port()), actorName)
		require.NoError(t, err)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		// set cron expression to run every second
		const expr = "* * * ? * *"
		err = newActorSystem.RemoteScheduleWithCron(ctx, message, addr, expr)
		require.NoError(t, err)

		// wait for two seconds
		pause.For(2 * time.Second)
		assert.EqualValues(t, 2, actorRef.ProcessedCount()-1)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With RemoteScheduleWithCron when cluster is enabled", func(t *testing.T) {
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

		remoting := NewRemoting()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, newActorSystem.Host(), int(newActorSystem.Port()), actorName)
		require.NoError(t, err)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		// set cron expression to run every second
		const expr = "* * * ? * *"
		err = newActorSystem.RemoteScheduleWithCron(ctx, message, addr, expr)
		require.NoError(t, err)

		// wait for two seconds
		pause.For(2 * time.Second)
		assert.EqualValues(t, 2, actorRef.ProcessedCount()-1)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
		provider.AssertExpectations(t)
	})
	t.Run("With RemoteScheduleWithCron with invalid cron expression", func(t *testing.T) {
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

		remoting := NewRemoting()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, newActorSystem.Host(), int(newActorSystem.Port()), actorName)
		require.NoError(t, err)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		// set cron expression
		const expr = "* * * * *"
		err = newActorSystem.RemoteScheduleWithCron(ctx, message, addr, expr)
		require.Error(t, err)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With RemoteScheduleWithCron with scheduler not started", func(t *testing.T) {
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

		remoting := NewRemoting()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, newActorSystem.Host(), int(newActorSystem.Port()), actorName)
		require.NoError(t, err)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		// set cron expression to run every second
		const expr = "* * * ? * *"
		err = newActorSystem.RemoteScheduleWithCron(ctx, message, addr, expr)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrSchedulerNotStarted)

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
		assert.ErrorIs(t, err, ErrSchedulerNotStarted)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With RemoteSchedule", func(t *testing.T) {
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

		remoting := NewRemoting()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, newActorSystem.Host(), int(newActorSystem.Port()), actorName)
		require.NoError(t, err)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		err = newActorSystem.RemoteSchedule(ctx, message, addr, time.Second)
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
	t.Run("With RemoteSchedule with scheduler not started", func(t *testing.T) {
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

		remoting := NewRemoting()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, newActorSystem.Host(), int(newActorSystem.Port()), actorName)
		require.NoError(t, err)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		err = newActorSystem.RemoteSchedule(ctx, message, addr, 100*time.Millisecond)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrSchedulerNotStarted)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With RemoteSchedule when actor not started", func(t *testing.T) {
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

		remoting := NewRemoting()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, newActorSystem.Host(), int(newActorSystem.Port()), actorName)
		require.NoError(t, err)

		require.NoError(t, newActorSystem.Kill(ctx, actorName))
		pause.For(time.Second)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		err = newActorSystem.RemoteSchedule(ctx, message, addr, time.Second)
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
	t.Run("With RemoteScheduleWithCron with cron expression when remoting not enabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		scheduler := newScheduler(logger, time.Second)
		scheduler.Start(ctx)

		addr := address.New("test", "test", host, remotingPort)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		// set cron expression to run every second
		const expr = "* * * ? * *"
		err := scheduler.RemoteScheduleWithCron(message, addr, expr)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrRemotingDisabled)

		scheduler.Stop(ctx)
	})
	t.Run("With RemoteSchedule when remoting not enabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		scheduler := newScheduler(logger, time.Second)
		scheduler.Start(ctx)

		addr := address.New("test", "test", host, remotingPort)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		err := scheduler.RemoteSchedule(message, addr, time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrRemotingDisabled)

		scheduler.Stop(ctx)
	})
	t.Run("With RemoteScheduleOnce when remoting not enabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		scheduler := newScheduler(logger, time.Second)
		scheduler.Start(ctx)

		addr := address.New("test", "test", host, remotingPort)
		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		err := scheduler.RemoteScheduleOnce(message, addr, time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrRemotingDisabled)
		scheduler.Stop(ctx)
	})
	t.Run("With RemoteSchedule when cluster is enabled", func(t *testing.T) {
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

		remoting := NewRemoting()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, newActorSystem.Host(), int(newActorSystem.Port()), actorName)
		require.NoError(t, err)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		err = newActorSystem.RemoteSchedule(ctx, message, addr, time.Second)
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

		pause.For(55 * time.Millisecond)
		require.EqualValues(t, 5, actorRef.ProcessedCount()-1)
		require.NoError(t, actorSystem.PauseSchedule("reference"))

		pause.For(55 * time.Millisecond)
		require.EqualValues(t, 5, actorRef.ProcessedCount()-1)

		require.NoError(t, actorSystem.ResumeSchedule("reference"))
		pause.For(55 * time.Millisecond)
		require.EqualValues(t, 10, actorRef.ProcessedCount()-1)

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
		assert.ErrorIs(t, err, ErrSchedulerNotStarted)

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
		assert.ErrorIs(t, err, ErrSchedulerNotStarted)

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

		require.ErrorIs(t, actorSystem.PauseSchedule("reference"), ErrScheduledReferenceNotFound)
		require.ErrorIs(t, actorSystem.ResumeSchedule("reference"), ErrScheduledReferenceNotFound)
		require.ErrorIs(t, actorSystem.CancelSchedule("reference"), ErrScheduledReferenceNotFound)

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
		assert.ErrorIs(t, err, ErrSchedulerNotStarted)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
}
