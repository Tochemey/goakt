/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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

	"github.com/reugn/go-quartz/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"

	"github.com/tochemey/goakt/v3/address"
	"github.com/tochemey/goakt/v3/internal/util"
	"github.com/tochemey/goakt/v3/log"
	clustermocks "github.com/tochemey/goakt/v3/mocks/cluster"
	testkit "github.com/tochemey/goakt/v3/mocks/discovery"
	"github.com/tochemey/goakt/v3/remote"
	"github.com/tochemey/goakt/v3/test/data/testpb"
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
			WithPassivationDisabled(),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		util.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		util.Pause(time.Second)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		err = newActorSystem.ScheduleOnce(ctx, message, actorRef, 100*time.Millisecond)
		require.NoError(t, err)

		util.Pause(time.Second)
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
			WithPassivationDisabled(),
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
			WithCluster(
				NewClusterConfig().
					WithKinds(new(mockActor)).
					WithPartitionCount(9).
					WithReplicaCount(1).
					WithPeersPort(clusterPort).
					WithMinimumPeersQuorum(1).
					WithDiscoveryPort(discoveryPort).
					WithWAL(t.TempDir()).
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

		util.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		util.Pause(time.Second)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		err = newActorSystem.ScheduleOnce(ctx, message, actorRef, 100*time.Millisecond)
		require.NoError(t, err)

		util.Pause(time.Second)
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
			WithJanitorInterval(time.Minute),
			WithPassivationDisabled(),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		util.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		util.Pause(time.Second)
		require.NoError(t, newActorSystem.Kill(ctx, actorName))

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		err = newActorSystem.ScheduleOnce(ctx, message, actorRef, 100*time.Millisecond)
		require.NoError(t, err)

		util.Pause(time.Second)
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
			WithJanitorInterval(time.Minute),
			WithPassivationDisabled(),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		util.Pause(time.Second)

		scheduler := newActorSystem.(*actorSystem).scheduler
		scheduler.Stop(ctx)

		// create the actor ref
		pid, err := newActorSystem.Spawn(ctx, "test", newMockActor())
		require.NoError(t, err)
		assert.NotNil(t, pid)

		message := new(testpb.TestSend)
		err = scheduler.ScheduleOnce(ctx, message, pid, 100*time.Millisecond)
		require.Error(t, err)
		assert.EqualError(t, err, ErrSchedulerNotStarted.Error())

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
			WithPassivationDisabled(),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		util.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newMockActor()
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

		util.Pause(time.Second)
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
			WithPassivationDisabled(),
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
			WithCluster(
				NewClusterConfig().
					WithKinds(new(mockActor)).
					WithPartitionCount(9).
					WithReplicaCount(1).
					WithPeersPort(clusterPort).
					WithMinimumPeersQuorum(1).
					WithDiscoveryPort(discoveryPort).
					WithWAL(t.TempDir()).
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

		util.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newMockActor()
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

		util.Pause(time.Second)
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
			WithPassivationDisabled(),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		util.Pause(time.Second)

		// test purpose only
		typedSystem := newActorSystem.(*actorSystem)
		typedSystem.scheduler.Stop(ctx)

		// create a test actor
		actorName := "test"
		actor := newMockActor()
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
		assert.EqualError(t, err, ErrSchedulerNotStarted.Error())

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
			WithPassivationDisabled(),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		util.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		util.Pause(time.Second)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		// set cron expression to run every second
		const expr = "* * * ? * *"
		err = newActorSystem.ScheduleWithCron(ctx, message, actorRef, expr)
		require.NoError(t, err)

		// wait for two seconds
		util.Pause(2 * time.Second)
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
			WithPassivationDisabled(),
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
			WithCluster(
				NewClusterConfig().
					WithKinds(new(mockActor)).
					WithPartitionCount(9).
					WithReplicaCount(1).
					WithPeersPort(clusterPort).
					WithMinimumPeersQuorum(1).
					WithDiscoveryPort(discoveryPort).
					WithWAL(t.TempDir()).
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

		util.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		util.Pause(time.Second)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		// set cron expression to run every second
		const expr = "* * * ? * *"
		err = newActorSystem.ScheduleWithCron(ctx, message, actorRef, expr)
		require.NoError(t, err)

		// wait for two seconds
		util.Pause(2 * time.Second)
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
			WithPassivationDisabled(),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		util.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		util.Pause(time.Second)

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
			WithPassivationDisabled(),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		util.Pause(time.Second)

		// test purpose only
		typedSystem := newActorSystem.(*actorSystem)
		typedSystem.scheduler.Stop(ctx)

		// create a test actor
		actorName := "test"
		actor := newMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		util.Pause(time.Second)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		// set cron expression to run every second
		const expr = "* * * ? * *"
		err = newActorSystem.ScheduleWithCron(ctx, message, actorRef, expr)
		require.Error(t, err)
		assert.EqualError(t, err, ErrSchedulerNotStarted.Error())

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
			WithPassivationDisabled(),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		util.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newMockActor()
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
		util.Pause(2 * time.Second)
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
			WithPassivationDisabled(),
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
			WithCluster(
				NewClusterConfig().
					WithKinds(new(mockActor)).
					WithPartitionCount(9).
					WithReplicaCount(1).
					WithPeersPort(clusterPort).
					WithMinimumPeersQuorum(1).
					WithDiscoveryPort(discoveryPort).
					WithWAL(t.TempDir()).
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

		util.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newMockActor()
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
		util.Pause(2 * time.Second)
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
			WithPassivationDisabled(),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		util.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newMockActor()
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
			WithPassivationDisabled(),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		util.Pause(time.Second)
		// test purpose only
		typedSystem := newActorSystem.(*actorSystem)
		typedSystem.scheduler.Stop(ctx)

		// create a test actor
		actorName := "test"
		actor := newMockActor()
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
		assert.EqualError(t, err, ErrSchedulerNotStarted.Error())

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
			WithPassivationDisabled(),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		util.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		util.Pause(time.Second)

		// send a message to the actor after one second
		message := new(testpb.TestSend)
		err = newActorSystem.Schedule(ctx, message, actorRef, time.Second)
		require.NoError(t, err)

		util.Pause(time.Second)

		typedSystem := newActorSystem.(*actorSystem)
		keys, err := typedSystem.scheduler.quartzScheduler.GetJobKeys()
		require.NoError(t, err)
		require.NotEmpty(t, keys)
		require.Len(t, keys, 1)

		util.Pause(500 * time.Millisecond)
		require.EqualValues(t, 1, actorRef.ProcessedCount()-1)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
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
			WithPassivationDisabled(),
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
			WithCluster(
				NewClusterConfig().
					WithKinds(new(mockActor)).
					WithPartitionCount(9).
					WithReplicaCount(1).
					WithPeersPort(clusterPort).
					WithMinimumPeersQuorum(1).
					WithDiscoveryPort(discoveryPort).
					WithWAL(t.TempDir()).
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

		util.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		util.Pause(time.Second)

		// send a message to the actor after one second
		message := new(testpb.TestSend)
		err = newActorSystem.Schedule(ctx, message, actorRef, time.Second)
		require.NoError(t, err)

		util.Pause(time.Second)

		typedSystem := newActorSystem.(*actorSystem)
		keys, err := typedSystem.scheduler.quartzScheduler.GetJobKeys()
		require.NoError(t, err)
		require.NotEmpty(t, keys)
		require.Len(t, keys, 1)

		util.Pause(500 * time.Millisecond)
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
			WithJanitorInterval(time.Minute),
			WithPassivationDisabled(),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		util.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		util.Pause(time.Second)
		require.NoError(t, newActorSystem.Kill(ctx, actorName))

		// send a message to the actor after one second
		message := new(testpb.TestSend)
		err = newActorSystem.Schedule(ctx, message, actorRef, time.Second)
		require.NoError(t, err)

		util.Pause(time.Second)

		typedSystem := newActorSystem.(*actorSystem)
		keys, err := typedSystem.scheduler.quartzScheduler.GetJobKeys()
		require.NoError(t, err)
		require.NotEmpty(t, keys)
		require.Len(t, keys, 1)

		util.Pause(500 * time.Millisecond)
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
			WithPassivationDisabled(),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		util.Pause(time.Second)
		// test purpose only
		typedSystem := newActorSystem.(*actorSystem)
		typedSystem.scheduler.Stop(ctx)

		// create a test actor
		actorName := "test"
		actor := newMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		util.Pause(time.Second)

		// send a message to the actor after one second
		message := new(testpb.TestSend)
		err = newActorSystem.Schedule(ctx, message, actorRef, time.Second)
		require.Error(t, err)
		assert.EqualError(t, err, ErrSchedulerNotStarted.Error())

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
			WithPassivationDisabled(),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		util.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newMockActor()
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

		util.Pause(time.Second)

		typedSystem := newActorSystem.(*actorSystem)
		keys, err := typedSystem.scheduler.quartzScheduler.GetJobKeys()
		require.NoError(t, err)
		require.NotEmpty(t, keys)
		require.Len(t, keys, 1)

		util.Pause(500 * time.Millisecond)
		require.EqualValues(t, 1, actorRef.ProcessedCount()-1)
		util.Pause(800 * time.Millisecond)
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
			WithPassivationDisabled(),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		util.Pause(time.Second)

		// test purpose only
		typedSystem := newActorSystem.(*actorSystem)
		typedSystem.scheduler.Stop(ctx)

		// create a test actor
		actorName := "test"
		actor := newMockActor()
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
		assert.EqualError(t, err, ErrSchedulerNotStarted.Error())

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
			WithPassivationDisabled(),
			WithJanitorInterval(time.Minute),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		assert.NoError(t, err)

		util.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := NewRemoting()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, newActorSystem.Host(), int(newActorSystem.Port()), actorName)
		require.NoError(t, err)

		require.NoError(t, newActorSystem.Kill(ctx, actorName))
		util.Pause(time.Second)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		err = newActorSystem.RemoteSchedule(ctx, message, addr, time.Second)
		require.NoError(t, err)

		util.Pause(time.Second)

		typedSystem := newActorSystem.(*actorSystem)
		keys, err := typedSystem.scheduler.quartzScheduler.GetJobKeys()
		require.NoError(t, err)
		require.NotEmpty(t, keys)
		require.Len(t, keys, 1)

		util.Pause(500 * time.Millisecond)
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
		err := scheduler.RemoteScheduleWithCron(ctx, message, addr, expr)
		require.Error(t, err)
		assert.EqualError(t, err, ErrRemotingDisabled.Error())

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
		err := scheduler.RemoteSchedule(ctx, message, addr, time.Second)
		require.Error(t, err)
		assert.EqualError(t, err, ErrRemotingDisabled.Error())

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
		err := scheduler.RemoteScheduleOnce(ctx, message, addr, time.Second)
		require.Error(t, err)
		assert.EqualError(t, err, ErrRemotingDisabled.Error())
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
			WithPassivationDisabled(),
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
			WithCluster(
				NewClusterConfig().
					WithKinds(new(mockActor)).
					WithPartitionCount(9).
					WithReplicaCount(1).
					WithPeersPort(clusterPort).
					WithMinimumPeersQuorum(1).
					WithDiscoveryPort(discoveryPort).
					WithWAL(t.TempDir()).
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

		util.Pause(time.Second)

		// create a test actor
		actorName := "test"
		actor := newMockActor()
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

		util.Pause(time.Second)

		typedSystem := newActorSystem.(*actorSystem)
		keys, err := typedSystem.scheduler.quartzScheduler.GetJobKeys()
		require.NoError(t, err)
		require.NotEmpty(t, keys)
		require.Len(t, keys, 1)

		util.Pause(500 * time.Millisecond)
		require.EqualValues(t, 1, actorRef.ProcessedCount()-1)
		util.Pause(800 * time.Millisecond)
		require.EqualValues(t, 2, actorRef.ProcessedCount()-1)

		// stop the actor
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
		provider.AssertExpectations(t)
	})
	t.Run("With distributeJobKeyOrNot returns error when SchedulerJobKeyExists returns error", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		jobDetails := quartz.NewJobDetail(nil, quartz.NewJobKey("test"))
		clusterMock := new(clustermocks.Interface)
		clusterMock.EXPECT().SchedulerJobKeyExists(ctx, jobDetails.JobKey().String()).Return(false, assert.AnError)

		scheduler := newScheduler(logger, time.Second, withSchedulerCluster(clusterMock))
		err := scheduler.distributeJobKeyOrNot(ctx, jobDetails)
		require.Error(t, err)
		clusterMock.AssertExpectations(t)
	})
	t.Run("With distributeJobKeyOrNot job skipping", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		jobDetails := quartz.NewJobDetail(nil, quartz.NewJobKey("test"))
		clusterMock := new(clustermocks.Interface)
		clusterMock.EXPECT().SchedulerJobKeyExists(ctx, jobDetails.JobKey().String()).Return(true, nil)

		scheduler := newScheduler(logger, time.Second, withSchedulerCluster(clusterMock))
		err := scheduler.distributeJobKeyOrNot(ctx, jobDetails)
		require.Error(t, err)
		require.ErrorIs(t, err, errSkipJobScheduling)
		clusterMock.AssertExpectations(t)
	})
	t.Run("With distributeJobKeyOrNot failure when replicating job failed", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		jobDetails := quartz.NewJobDetail(nil, quartz.NewJobKey("test"))
		jobKey := jobDetails.JobKey().String()
		clusterMock := new(clustermocks.Interface)
		clusterMock.EXPECT().SchedulerJobKeyExists(ctx, jobKey).Return(false, nil)
		clusterMock.EXPECT().SetSchedulerJobKey(ctx, jobKey).Return(assert.AnError)

		scheduler := newScheduler(logger, time.Second, withSchedulerCluster(clusterMock))
		err := scheduler.distributeJobKeyOrNot(ctx, jobDetails)
		require.Error(t, err)
		require.ErrorIs(t, err, assert.AnError)
		clusterMock.AssertExpectations(t)
	})
}
