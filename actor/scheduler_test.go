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
	stderrors "errors"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/reugn/go-quartz/job"
	"github.com/reugn/go-quartz/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/cluster"
	dynaport "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/remoteclient"
	"github.com/tochemey/goakt/v4/log"
	mockscluster "github.com/tochemey/goakt/v4/mocks/cluster"
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
		for range numMessages {
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
		host := "127.0.0.1"

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

		remoting := remoteclient.NewClient()
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

		remoting := remoteclient.NewClient()
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
		host := "127.0.0.1"

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

		remoting := remoteclient.NewClient()
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

		// send a message to the actor after 100 ms; cluster-mode cron requires an explicit
		// reference since single fire is now intrinsic to cron+cluster.
		message := new(testpb.TestSend)
		// set cron expression to run every second
		const expr = "* * * ? * *"
		err = newActorSystem.ScheduleWithCron(ctx, message, actorRef, expr, WithReference("cron-cluster-test"))
		require.NoError(t, err)

		// as the sole node racing for every tick, this node always wins the claim, so
		// delivery must proceed exactly as it would without any other node in the race.
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
	t.Run("With ScheduleWithCron in cluster mode without explicit reference returns an error", func(t *testing.T) {
		ctx := context.TODO()
		nodePorts := dynaport.Get(3)
		discoveryPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		logger := log.DiscardLogger
		host := "127.0.0.1"

		addrs := []string{
			net.JoinHostPort(host, strconv.Itoa(discoveryPort)),
		}

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

		err = newActorSystem.Start(ctx)
		require.NoError(t, err)

		pause.For(time.Second)

		actorName := "test"
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		pause.For(time.Second)

		// no WithReference: an auto-generated per-node reference would let every node
		// deliver independently, defeating cluster-wide single fire, so this must be refused.
		message := new(testpb.TestSend)
		const expr = "* * * ? * *"
		err = newActorSystem.ScheduleWithCron(ctx, message, actorRef, expr)
		require.ErrorIs(t, err, errors.ErrScheduleReferenceRequired)

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
		host := "127.0.0.1"

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

		remoting := remoteclient.NewClient()
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

		remoting := remoteclient.NewClient()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, newActorSystem.Host(), int(newActorSystem.Port()), actorName)
		require.NoError(t, err)

		// send a message to the actor after 100 ms; cluster-mode cron requires an explicit
		// reference since single fire is now intrinsic to cron+cluster.
		message := new(testpb.TestSend)
		// set cron expression to run every second
		const expr = "* * * ? * *"
		err = newActorSystem.ScheduleWithCron(ctx, message, newRemotePID(addr, remoting), expr, WithReference("cron-remote-cluster-test"))
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
		host := "127.0.0.1"

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

		remoting := remoteclient.NewClient()
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
		host := "127.0.0.1"

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

		remoting := remoteclient.NewClient()
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
		host := "127.0.0.1"

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

		remoting := remoteclient.NewClient()
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
		host := "127.0.0.1"

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

		remoting := remoteclient.NewClient()
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
		host := "127.0.0.1"

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

		remoting := remoteclient.NewClient()
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
	t.Run("With ScheduleOnce for remote PID when remoting not enabled", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		newActorSystem, err := NewActorSystem("test", WithLogger(logger))
		require.NoError(t, err)
		require.NoError(t, newActorSystem.Start(ctx))
		pause.For(time.Second)

		addr := address.New("remote-actor", "other", "127.0.0.1", 9999)
		remotePID := &PID{address: addr, path: newPath(addr), remoting: nil}
		remotePID.setState(remoteState, true)

		message := new(testpb.TestSend)
		err = newActorSystem.ScheduleOnce(ctx, message, remotePID, 100*time.Millisecond)
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrRemotingDisabled)
		require.NoError(t, newActorSystem.Stop(ctx))
	})
	t.Run("With Schedule for remote PID when remoting not enabled", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		newActorSystem, err := NewActorSystem("test", WithLogger(logger))
		require.NoError(t, err)
		require.NoError(t, newActorSystem.Start(ctx))
		pause.For(time.Second)

		addr := address.New("remote-actor", "other", "127.0.0.1", 9999)
		remotePID := &PID{address: addr, path: newPath(addr), remoting: nil}
		remotePID.setState(remoteState, true)

		message := new(testpb.TestSend)
		err = newActorSystem.Schedule(ctx, message, remotePID, time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrRemotingDisabled)
		require.NoError(t, newActorSystem.Stop(ctx))
	})
	t.Run("With ScheduleWithCron for remote PID when remoting not enabled", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		newActorSystem, err := NewActorSystem("test", WithLogger(logger))
		require.NoError(t, err)
		require.NoError(t, newActorSystem.Start(ctx))
		pause.For(time.Second)

		addr := address.New("remote-actor", "other", "127.0.0.1", 9999)
		remotePID := &PID{address: addr, path: newPath(addr), remoting: nil}
		remotePID.setState(remoteState, true)

		message := new(testpb.TestSend)
		err = newActorSystem.ScheduleWithCron(ctx, message, remotePID, "* * * ? * *")
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrRemotingDisabled)
		require.NoError(t, newActorSystem.Stop(ctx))
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

		remoting := remoteclient.NewClient()
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

func TestCronClaimTTL(t *testing.T) {
	t.Run("sub-minute period clamps to the floor", func(t *testing.T) {
		trigger, err := quartz.NewCronTrigger("* * * ? * *") // every second
		require.NoError(t, err)
		require.Equal(t, minScheduleFireClaimTTL, cronClaimTTL(trigger))
	})
	t.Run("in-range period is used as-is", func(t *testing.T) {
		trigger, err := quartz.NewCronTrigger("0 */5 * * * *") // every 5 minutes
		require.NoError(t, err)
		require.Equal(t, 5*time.Minute, cronClaimTTL(trigger))
	})
	t.Run("multi-hour period is used as-is", func(t *testing.T) {
		trigger, err := quartz.NewCronTrigger("0 0 */3 * * *") // every 3 hours
		require.NoError(t, err)
		require.Equal(t, 3*time.Hour, cronClaimTTL(trigger))
	})
	t.Run("multi-day period clamps to the ceiling", func(t *testing.T) {
		trigger, err := quartz.NewCronTrigger("0 0 0 ? * 1") // weekly
		require.NoError(t, err)
		require.Equal(t, maxScheduleFireClaimTTL, cronClaimTTL(trigger))
	})
	t.Run("first fire time error falls back to the floor", func(t *testing.T) {
		trigger := quartz.NewRunOnceTrigger(time.Second)
		_, err := trigger.NextFireTime(quartz.NowNano()) // expire the trigger
		require.NoError(t, err)
		require.Equal(t, minScheduleFireClaimTTL, cronClaimTTL(trigger))
	})
	t.Run("second fire time error falls back to the floor", func(t *testing.T) {
		// a fresh run-once trigger yields one fire time then expires, so cronClaimTTL's
		// second NextFireTime call fails.
		trigger := quartz.NewRunOnceTrigger(time.Second)
		require.Equal(t, minScheduleFireClaimTTL, cronClaimTTL(trigger))
	})
}

func TestClaimClusterFire(t *testing.T) {
	metadataCtx := func(runTime int64) context.Context {
		return context.WithValue(context.Background(), quartz.JobMetadataContextKey, quartz.JobMetadata{RunTime: runTime})
	}

	t.Run("fails open when job metadata is missing", func(t *testing.T) {
		sched := newScheduler(log.DiscardLogger, time.Second, nil)
		claim := &scheduleFireClaim{reference: "ref", ttl: time.Minute}

		won, err := sched.claimClusterFire(context.Background(), claim)
		require.NoError(t, err)
		require.True(t, won)
	})
	t.Run("skips a stale tick without claiming", func(t *testing.T) {
		// a tick older than the claim TTL must be skipped before any cluster call: the
		// winner's claim entry may already have expired, so claiming would deliver a
		// duplicate. The bare actor system proves no claim was attempted, since a claim
		// attempt against its nil cluster engine would surface ErrClusterDisabled.
		sched := newScheduler(log.DiscardLogger, time.Second, &actorSystem{})
		claim := &scheduleFireClaim{reference: "ref", ttl: time.Minute}

		won, err := sched.claimClusterFire(metadataCtx(time.Now().Add(-2*time.Minute).UnixNano()), claim)
		require.NoError(t, err)
		require.False(t, won)
	})
	t.Run("errors when the cluster engine is unavailable", func(t *testing.T) {
		sched := newScheduler(log.DiscardLogger, time.Second, &actorSystem{})
		claim := &scheduleFireClaim{reference: "ref", ttl: time.Minute}

		won, err := sched.claimClusterFire(metadataCtx(time.Now().UnixNano()), claim)
		require.ErrorIs(t, err, errors.ErrClusterDisabled)
		require.False(t, won)
	})
	t.Run("skips delivery when another node already claimed the tick", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		clusterMock.EXPECT().ClaimScheduleFire(mock.Anything, mock.Anything, time.Minute).Return(cluster.ErrScheduleFireClaimed)

		sched := newScheduler(log.DiscardLogger, time.Second, &actorSystem{cluster: clusterMock})
		claim := &scheduleFireClaim{reference: "ref", ttl: time.Minute}

		won, err := sched.claimClusterFire(metadataCtx(time.Now().UnixNano()), claim)
		require.NoError(t, err)
		require.False(t, won)
	})
	t.Run("propagates claim errors and skips delivery", func(t *testing.T) {
		expectedErr := stderrors.New("claim failure")
		clusterMock := mockscluster.NewCluster(t)
		clusterMock.EXPECT().ClaimScheduleFire(mock.Anything, mock.Anything, time.Minute).Return(expectedErr)

		sched := newScheduler(log.DiscardLogger, time.Second, &actorSystem{cluster: clusterMock})
		claim := &scheduleFireClaim{reference: "ref", ttl: time.Minute}

		won, err := sched.claimClusterFire(metadataCtx(time.Now().UnixNano()), claim)
		require.ErrorIs(t, err, expectedErr)
		require.False(t, won)
	})
}

// TestScheduleWithCronTimezone pins that ScheduleWithCron evaluates the cron expression in UTC
// when the actor system is in cluster mode, so every node computes the same tick instants and
// the per-tick fire claims line up cluster-wide, and in the process local timezone otherwise.
// The chosen location is asserted through the scheduled trigger's description (which embeds the
// location) rather than by mutating the global time.Local, which would race the actor-system
// goroutines under the race detector. "UTC" and the local location name differ as strings, so
// the assertions have teeth even when the test host itself runs in UTC.
func TestScheduleWithCronTimezone(t *testing.T) {
	// daily at noon: never fires during the test, so the job stays queued for inspection and
	// no delivery or claim happens.
	const expr = "0 0 12 * * *"

	scheduledTriggerDescription := func(t *testing.T, sys ActorSystem, reference string) string {
		t.Helper()
		sysImpl := sys.(*actorSystem)
		job, err := sysImpl.scheduler.quartzScheduler.GetScheduledJob(quartz.NewJobKey(reference))
		require.NoError(t, err)

		return job.Trigger().Description()
	}

	t.Run("non-cluster mode evaluates cron in the local timezone", func(t *testing.T) {
		ctx := context.TODO()
		newActorSystem, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, newActorSystem.Start(ctx))

		pause.For(time.Second)

		actorRef, err := newActorSystem.Spawn(ctx, "test", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, actorRef)

		const reference = "cron-local-tz"
		require.NoError(t, newActorSystem.ScheduleWithCron(ctx, new(testpb.TestSend), actorRef, expr, WithReference(reference)))

		localTrigger, err := quartz.NewCronTriggerWithLoc(expr, time.Now().Location())
		require.NoError(t, err)
		require.Equal(t, localTrigger.Description(), scheduledTriggerDescription(t, newActorSystem, reference))

		require.NoError(t, newActorSystem.Stop(ctx))
	})

	t.Run("cluster mode evaluates cron in UTC", func(t *testing.T) {
		ctx := context.TODO()
		nodePorts := dynaport.Get(3)
		discoveryPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]
		host := "127.0.0.1"

		addrs := []string{net.JoinHostPort(host, strconv.Itoa(discoveryPort))}

		provider := new(testkit.Provider)
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(log.DiscardLogger),
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

		require.NoError(t, newActorSystem.Start(ctx))

		pause.For(time.Second)

		actorRef, err := newActorSystem.Spawn(ctx, "test", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, actorRef)

		pause.For(time.Second)

		const reference = "cron-utc-tz"
		require.NoError(t, newActorSystem.ScheduleWithCron(ctx, new(testpb.TestSend), actorRef, expr, WithReference(reference)))

		utcTrigger, err := quartz.NewCronTriggerWithLoc(expr, time.UTC)
		require.NoError(t, err)
		require.Equal(t, utcTrigger.Description(), scheduledTriggerDescription(t, newActorSystem, reference))

		require.NoError(t, newActorSystem.Stop(ctx))
		provider.AssertExpectations(t)
	})
}

// TestSchedulerJobMetadataPresent pins that go-quartz attaches JobMetadata to a scheduled
// job's execution context: claimClusterFire fails open (skips arbitration) without it, so a
// regression here would silently defeat cluster-wide single fire instead of failing loudly.
func TestSchedulerJobMetadataPresent(t *testing.T) {
	sched := newScheduler(log.DiscardLogger, time.Second, nil)
	ctx := context.Background()
	sched.Start(ctx)
	defer sched.Stop(ctx)

	metadataCh := make(chan quartz.JobMetadata, 1)
	probe := func(ctx context.Context) (bool, error) {
		if md, ok := ctx.Value(quartz.JobMetadataContextKey).(quartz.JobMetadata); ok {
			metadataCh <- md
		}
		return true, nil
	}

	detail := quartz.NewJobDetail(job.NewFunctionJob(probe), quartz.NewJobKey("probe"))
	require.NoError(t, sched.quartzScheduler.ScheduleJob(detail, quartz.NewRunOnceTrigger(10*time.Millisecond)))

	select {
	case md := <-metadataCh:
		require.NotZero(t, md.RunTime)
	case <-time.After(2 * time.Second):
		t.Fatal("job metadata was not present in execution context")
	}
}

func TestSchedulerListSchedules(t *testing.T) {
	t.Run("With no schedules", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		system, err := NewActorSystem("test", WithLogger(logger))
		require.NoError(t, err)

		require.NoError(t, system.Start(ctx))
		pause.For(time.Second)

		assert.Empty(t, system.ListSchedules())

		require.NoError(t, system.Stop(ctx))
	})
	t.Run("With scheduler not started", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		system, err := NewActorSystem("test", WithLogger(logger))
		require.NoError(t, err)

		require.NoError(t, system.Start(ctx))
		pause.For(time.Second)

		// test purpose only
		typedSystem := system.(*actorSystem)
		typedSystem.scheduler.Stop(ctx)

		assert.Empty(t, system.ListSchedules())

		require.NoError(t, system.Stop(ctx))
	})
	t.Run("With an interval schedule", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		system, err := NewActorSystem("test", WithLogger(logger))
		require.NoError(t, err)

		require.NoError(t, system.Start(ctx))
		pause.For(time.Second)

		actorRef, err := system.Spawn(ctx, "test", NewMockActor())
		require.NoError(t, err)
		pause.For(time.Second)

		message := new(testpb.TestSend)
		err = system.Schedule(ctx, message, actorRef, 100*time.Millisecond, WithReference("interval-ref"))
		require.NoError(t, err)

		schedules := system.ListSchedules()
		require.Len(t, schedules, 1)
		info := schedules[0]
		assert.Equal(t, "interval-ref", info.Reference)
		assert.Equal(t, actorRef.Path().String(), info.Path.String())

		require.NoError(t, system.CancelSchedule("interval-ref"))
		assert.Empty(t, system.ListSchedules())

		require.NoError(t, system.Stop(ctx))
	})
	t.Run("With a ScheduleOnce schedule that disappears once delivered", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		system, err := NewActorSystem("test", WithLogger(logger))
		require.NoError(t, err)

		require.NoError(t, system.Start(ctx))
		pause.For(time.Second)

		actorRef, err := system.Spawn(ctx, "test", NewMockActor())
		require.NoError(t, err)
		pause.For(time.Second)

		message := new(testpb.TestSend)
		err = system.ScheduleOnce(ctx, message, actorRef, 50*time.Millisecond, WithReference("run-once"))
		require.NoError(t, err)

		schedules := system.ListSchedules()
		require.Len(t, schedules, 1)
		info := schedules[0]
		assert.Equal(t, "run-once", info.Reference)
		assert.Equal(t, actorRef.Path().String(), info.Path.String())

		require.Eventually(t, func() bool {
			return actorRef.ProcessedCount()-1 >= 1
		}, 2*time.Second, 20*time.Millisecond)

		// the one-shot has fired: it must no longer be listed even though it was
		// never explicitly canceled.
		require.Eventually(t, func() bool {
			return len(system.ListSchedules()) == 0
		}, 2*time.Second, 20*time.Millisecond)

		require.NoError(t, system.Stop(ctx))
	})
	t.Run("With a cron schedule", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		system, err := NewActorSystem("test", WithLogger(logger))
		require.NoError(t, err)

		require.NoError(t, system.Start(ctx))
		pause.For(time.Second)

		actorRef, err := system.Spawn(ctx, "test", NewMockActor())
		require.NoError(t, err)
		pause.For(time.Second)

		message := new(testpb.TestSend)
		const expr = "* * * ? * *"
		err = system.ScheduleWithCron(ctx, message, actorRef, expr, WithReference("cron-ref"))
		require.NoError(t, err)

		schedules := system.ListSchedules()
		require.Len(t, schedules, 1)
		info := schedules[0]
		assert.Equal(t, "cron-ref", info.Reference)
		assert.Equal(t, actorRef.Path().String(), info.Path.String())

		require.NoError(t, system.CancelSchedule("cron-ref"))
		assert.Empty(t, system.ListSchedules())

		require.NoError(t, system.Stop(ctx))
	})
	t.Run("With multiple schedules", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		system, err := NewActorSystem("test", WithLogger(logger))
		require.NoError(t, err)

		require.NoError(t, system.Start(ctx))
		pause.For(time.Second)

		actorRef, err := system.Spawn(ctx, "test", NewMockActor())
		require.NoError(t, err)
		pause.For(time.Second)

		message := new(testpb.TestSend)
		require.NoError(t, system.Schedule(ctx, message, actorRef, 5*time.Second, WithReference("interval-ref")))
		require.NoError(t, system.ScheduleOnce(ctx, message, actorRef, 5*time.Second, WithReference("once-ref")))
		require.NoError(t, system.ScheduleWithCron(ctx, message, actorRef, "0 0 0 1 1 ?", WithReference("cron-ref")))

		schedules := system.ListSchedules()
		require.Len(t, schedules, 3)

		byReference := make(map[string]ScheduleInfo, len(schedules))
		for _, info := range schedules {
			byReference[info.Reference] = info
		}

		require.Contains(t, byReference, "interval-ref")
		require.Contains(t, byReference, "once-ref")
		require.Contains(t, byReference, "cron-ref")

		require.NoError(t, system.Stop(ctx))
	})
}

// TestSchedulerCounters asserts the scheduler totals every successfully
// accepted schedule and every honored cancellation, and leaves both counters
// untouched on the error paths.
func TestSchedulerCounters(t *testing.T) {
	t.Run("With successful schedules and cancellations", func(t *testing.T) {
		ctx := context.TODO()
		newActorSystem, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, newActorSystem.Start(ctx))

		pause.For(time.Second)

		actorRef, err := newActorSystem.Spawn(ctx, "test", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, actorRef)

		typedSystem := newActorSystem.(*actorSystem)
		require.Zero(t, typedSystem.scheduler.scheduledCount.Load())
		require.Zero(t, typedSystem.scheduler.cancelledCount.Load())

		message := new(testpb.TestSend)
		require.NoError(t, newActorSystem.ScheduleOnce(ctx, message, actorRef, time.Hour, WithReference("counter-once")))
		require.NoError(t, newActorSystem.Schedule(ctx, message, actorRef, time.Hour, WithReference("counter-interval")))
		require.NoError(t, newActorSystem.ScheduleWithCron(ctx, message, actorRef, "* * * ? * *", WithReference("counter-cron")))
		require.EqualValues(t, 3, typedSystem.scheduler.scheduledCount.Load())

		require.NoError(t, newActorSystem.CancelSchedule("counter-interval"))
		require.EqualValues(t, 1, typedSystem.scheduler.cancelledCount.Load())

		// an unknown reference is not a cancellation
		require.Error(t, newActorSystem.CancelSchedule("counter-unknown"))
		require.EqualValues(t, 1, typedSystem.scheduler.cancelledCount.Load())

		require.EqualValues(t, 3, typedSystem.scheduler.scheduledCount.Load())
		require.NoError(t, newActorSystem.Stop(ctx))
	})
	t.Run("With scheduler not started", func(t *testing.T) {
		scheduler := newScheduler(log.DiscardLogger, time.Second, nil)

		err := scheduler.ScheduleOnce(new(testpb.TestSend), nil, time.Hour)
		require.ErrorIs(t, err, errors.ErrSchedulerNotStarted)
		require.Zero(t, scheduler.scheduledCount.Load())

		err = scheduler.CancelSchedule("counter-none")
		require.ErrorIs(t, err, errors.ErrSchedulerNotStarted)
		require.Zero(t, scheduler.cancelledCount.Load())
	})
}

// TestGrainScheduler covers the Grain counterparts of ScheduleOnce, Schedule and
// ScheduleWithCron: delivery through one-way TellGrain, reactivation of a passivated Grain,
// validation, cluster gating and the reference-based management shared with actor schedules.
func TestGrainScheduler(t *testing.T) {
	t.Run("With ScheduleGrainOnce", func(t *testing.T) {
		ctx := context.TODO()
		newActorSystem, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, newActorSystem.Start(ctx))
		pause.For(time.Second)

		identity, err := GrainOf[*MockGrain](ctx, newActorSystem, "scheduled-once")
		require.NoError(t, err)

		err = newActorSystem.ScheduleGrainOnce(ctx, new(testpb.TestSend), identity, 100*time.Millisecond)
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			return grainProcessedCount(newActorSystem, identity) == 1
		}, 5*time.Second, 50*time.Millisecond)

		// a fired one-shot leaves the quartz scheduler
		require.Eventually(t, func() bool {
			keys, err := newActorSystem.(*actorSystem).scheduler.quartzScheduler.GetJobKeys()
			return err == nil && len(keys) == 0
		}, 5*time.Second, 50*time.Millisecond)

		require.NoError(t, newActorSystem.Stop(ctx))
	})
	t.Run("With ScheduleGrain", func(t *testing.T) {
		ctx := context.TODO()
		newActorSystem, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, newActorSystem.Start(ctx))
		pause.For(time.Second)

		identity, err := GrainOf[*MockGrain](ctx, newActorSystem, "scheduled-interval")
		require.NoError(t, err)

		err = newActorSystem.ScheduleGrain(ctx, new(testpb.TestSend), identity, 100*time.Millisecond, WithReference("grain-interval"))
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			return grainProcessedCount(newActorSystem, identity) >= 2
		}, 5*time.Second, 50*time.Millisecond)

		require.NoError(t, newActorSystem.CancelSchedule("grain-interval"))
		require.NoError(t, newActorSystem.Stop(ctx))
	})
	t.Run("With ScheduleGrainWithCron", func(t *testing.T) {
		ctx := context.TODO()
		newActorSystem, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, newActorSystem.Start(ctx))
		pause.For(time.Second)

		identity, err := GrainOf[*MockGrain](ctx, newActorSystem, "scheduled-cron")
		require.NoError(t, err)

		// every second
		const expr = "* * * ? * *"
		err = newActorSystem.ScheduleGrainWithCron(ctx, new(testpb.TestSend), identity, expr, WithReference("grain-cron"))
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			return grainProcessedCount(newActorSystem, identity) >= 2
		}, 5*time.Second, 100*time.Millisecond)

		require.NoError(t, newActorSystem.CancelSchedule("grain-cron"))
		require.NoError(t, newActorSystem.Stop(ctx))
	})
	t.Run("With ScheduleGrainOnce reactivates a passivated Grain", func(t *testing.T) {
		ctx := context.TODO()
		newActorSystem, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, newActorSystem.Start(ctx))
		pause.For(time.Second)

		sys := newActorSystem.(*actorSystem)
		identity, err := GrainOf[*MockGrain](ctx, newActorSystem, "scheduled-passivated", WithGrainDeactivateAfter(200*time.Millisecond))
		require.NoError(t, err)

		// the idle Grain passivates and leaves the local grains map
		require.Eventually(t, func() bool {
			_, exists := sys.grains.Get(identity.String())
			return !exists
		}, 3*time.Second, 20*time.Millisecond)

		require.NoError(t, newActorSystem.ScheduleGrainOnce(ctx, new(testpb.TestSend), identity, 50*time.Millisecond))

		// the delivery activates the Grain again and the new activation processes the message
		require.Eventually(t, func() bool {
			process, ok := sys.grains.Get(identity.String())
			return ok && process.isActive() && process.processedCount.Load() == 1
		}, 5*time.Second, 50*time.Millisecond)

		require.NoError(t, newActorSystem.Stop(ctx))
	})
	t.Run("With scheduler not started", func(t *testing.T) {
		ctx := context.TODO()
		newActorSystem, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, newActorSystem.Start(ctx))
		pause.For(time.Second)

		scheduler := newActorSystem.(*actorSystem).scheduler
		scheduler.Stop(ctx)

		identity := newGrainIdentity(NewMockGrain(), "not-started")
		message := new(testpb.TestSend)

		err = scheduler.ScheduleGrainOnce(message, identity, 100*time.Millisecond)
		require.ErrorIs(t, err, errors.ErrSchedulerNotStarted)

		err = scheduler.ScheduleGrain(message, identity, 100*time.Millisecond)
		require.ErrorIs(t, err, errors.ErrSchedulerNotStarted)

		err = scheduler.ScheduleGrainWithCron(message, identity, "* * * ? * *")
		require.ErrorIs(t, err, errors.ErrSchedulerNotStarted)

		require.NoError(t, newActorSystem.Stop(ctx))
	})
	t.Run("With an invalid identity", func(t *testing.T) {
		ctx := context.TODO()
		newActorSystem, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, newActorSystem.Start(ctx))
		pause.For(time.Second)

		invalid := &GrainIdentity{kind: "", name: ""}
		message := new(testpb.TestSend)

		err = newActorSystem.ScheduleGrainOnce(ctx, message, invalid, 100*time.Millisecond)
		require.ErrorIs(t, err, errors.ErrInvalidGrainIdentity)

		err = newActorSystem.ScheduleGrain(ctx, message, invalid, 100*time.Millisecond)
		require.ErrorIs(t, err, errors.ErrInvalidGrainIdentity)

		err = newActorSystem.ScheduleGrainWithCron(ctx, message, invalid, "* * * ? * *")
		require.ErrorIs(t, err, errors.ErrInvalidGrainIdentity)

		// nothing was registered
		assert.Empty(t, newActorSystem.ListSchedules())
		require.NoError(t, newActorSystem.Stop(ctx))
	})
	t.Run("With ScheduleGrainWithCron with an invalid cron expression", func(t *testing.T) {
		ctx := context.TODO()
		newActorSystem, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, newActorSystem.Start(ctx))
		pause.For(time.Second)

		identity, err := GrainOf[*MockGrain](ctx, newActorSystem, "scheduled-bad-cron")
		require.NoError(t, err)

		err = newActorSystem.ScheduleGrainWithCron(ctx, new(testpb.TestSend), identity, "not a cron expression")
		require.Error(t, err)
		assert.Empty(t, newActorSystem.ListSchedules())

		require.NoError(t, newActorSystem.Stop(ctx))
	})
	t.Run("With Pause, Resume and Cancel on a Grain schedule", func(t *testing.T) {
		ctx := context.TODO()
		newActorSystem, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, newActorSystem.Start(ctx))
		pause.For(time.Second)

		identity, err := GrainOf[*MockGrain](ctx, newActorSystem, "scheduled-managed")
		require.NoError(t, err)

		const reference = "grain-managed"
		err = newActorSystem.ScheduleGrain(ctx, new(testpb.TestSend), identity, 50*time.Millisecond, WithReference(reference))
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			return grainProcessedCount(newActorSystem, identity) >= 1
		}, 5*time.Second, 20*time.Millisecond)

		// a paused schedule stops delivering once the in-flight tick has drained
		require.NoError(t, newActorSystem.PauseSchedule(reference))
		pause.For(200 * time.Millisecond)
		paused := grainProcessedCount(newActorSystem, identity)
		pause.For(300 * time.Millisecond)
		require.Equal(t, paused, grainProcessedCount(newActorSystem, identity))

		// a resumed schedule delivers again
		require.NoError(t, newActorSystem.ResumeSchedule(reference))
		require.Eventually(t, func() bool {
			return grainProcessedCount(newActorSystem, identity) > paused
		}, 5*time.Second, 20*time.Millisecond)

		require.NoError(t, newActorSystem.CancelSchedule(reference))
		assert.Empty(t, newActorSystem.ListSchedules())
		require.ErrorIs(t, newActorSystem.CancelSchedule(reference), errors.ErrScheduledReferenceNotFound)

		require.NoError(t, newActorSystem.Stop(ctx))
	})
	t.Run("With an unregistered Grain kind", func(t *testing.T) {
		ctx := context.TODO()
		newActorSystem, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, newActorSystem.Start(ctx))
		pause.For(time.Second)

		sys := newActorSystem.(*actorSystem)
		// the kind is never registered on this node, so the delivery cannot activate the Grain
		identity := newGrainIdentity(NewMockGrain(), "scheduled-unregistered")

		require.NoError(t, newActorSystem.ScheduleGrainOnce(ctx, new(testpb.TestSend), identity, 50*time.Millisecond))
		pause.For(time.Second)

		// the tick failed without activating anything and the one-shot is gone
		_, exists := sys.grains.Get(identity.String())
		require.False(t, exists)
		keys, err := sys.scheduler.quartzScheduler.GetJobKeys()
		require.NoError(t, err)
		require.Empty(t, keys)

		// the job function reports the delivery failure to quartz
		done, err := sys.scheduler.makeGrainJobFn(identity, new(testpb.TestSend), nil)(ctx)
		require.ErrorIs(t, err, errors.ErrGrainNotRegistered)
		require.False(t, done)

		// an interval schedule outlives its failing ticks: quartz reschedules a job before
		// running it, so a delivery failure never removes the schedule
		const reference = "grain-unregistered-interval"
		require.NoError(t, newActorSystem.ScheduleGrain(ctx, new(testpb.TestSend), identity, 50*time.Millisecond, WithReference(reference)))
		pause.For(500 * time.Millisecond)

		_, err = sys.scheduler.quartzScheduler.GetScheduledJob(quartz.NewJobKey(reference))
		require.NoError(t, err)
		_, exists = sys.grains.Get(identity.String())
		require.False(t, exists)
		require.NoError(t, newActorSystem.CancelSchedule(reference))

		require.NoError(t, newActorSystem.Stop(ctx))
	})
	t.Run("With a handler failure recorded as a deadletter", func(t *testing.T) {
		ctx := context.TODO()
		newActorSystem, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, newActorSystem.Start(ctx))
		pause.For(time.Second)

		consumer, err := newActorSystem.Subscribe()
		require.NoError(t, err)

		require.NoError(t, newActorSystem.RegisterGrainKind(ctx, &MockReceiveFailingGrain{}))
		failing := newGrainIdentity(NewMockReceiveFailingGrain(), "scheduled-failing")

		// the handler reports Err on TestSend: with one-way delivery that is a deadletter
		// naming the Grain, not an error the scheduler could return to anyone.
		require.NoError(t, newActorSystem.ScheduleGrainOnce(ctx, new(testpb.TestSend), failing, 50*time.Millisecond))
		pause.For(time.Second)

		var reason string
		for message := range consumer.Iterator() {
			deadletter, ok := message.Payload().(*Deadletter)
			if !ok || deadletter.Receiver().Name() != failing.String() {
				continue
			}
			reason = deadletter.Reason()
		}

		require.Contains(t, reason, "failed to process message")
		require.NoError(t, newActorSystem.Stop(ctx))
	})
	t.Run("With ScheduleGrainOnce when cluster is enabled", func(t *testing.T) {
		ctx := context.TODO()
		newActorSystem, provider := startGrainSchedulerClusterNode(t)

		identity, err := GrainOf[*MockGrain](ctx, newActorSystem, "cluster-once")
		require.NoError(t, err)

		err = newActorSystem.ScheduleGrainOnce(ctx, new(testpb.TestSend), identity, 100*time.Millisecond)
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			return grainProcessedCount(newActorSystem, identity) == 1
		}, 5*time.Second, 50*time.Millisecond)

		require.NoError(t, newActorSystem.Stop(ctx))
		provider.AssertExpectations(t)
	})
	t.Run("With ScheduleGrain when cluster is enabled", func(t *testing.T) {
		ctx := context.TODO()
		newActorSystem, provider := startGrainSchedulerClusterNode(t)

		identity, err := GrainOf[*MockGrain](ctx, newActorSystem, "cluster-interval")
		require.NoError(t, err)

		err = newActorSystem.ScheduleGrain(ctx, new(testpb.TestSend), identity, 100*time.Millisecond, WithReference("grain-interval-cluster"))
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			return grainProcessedCount(newActorSystem, identity) >= 2
		}, 5*time.Second, 50*time.Millisecond)

		require.NoError(t, newActorSystem.CancelSchedule("grain-interval-cluster"))
		require.NoError(t, newActorSystem.Stop(ctx))
		provider.AssertExpectations(t)
	})
	t.Run("With ScheduleGrainWithCron when cluster is enabled", func(t *testing.T) {
		ctx := context.TODO()
		newActorSystem, provider := startGrainSchedulerClusterNode(t)

		identity, err := GrainOf[*MockGrain](ctx, newActorSystem, "cluster-cron")
		require.NoError(t, err)

		// every second; cluster-mode cron requires an explicit reference
		const expr = "* * * ? * *"
		err = newActorSystem.ScheduleGrainWithCron(ctx, new(testpb.TestSend), identity, expr, WithReference("grain-cron-cluster"))
		require.NoError(t, err)

		// as the sole node racing for every tick, this node always wins the claim
		require.Eventually(t, func() bool {
			return grainProcessedCount(newActorSystem, identity) >= 2
		}, 5*time.Second, 100*time.Millisecond)

		require.NoError(t, newActorSystem.CancelSchedule("grain-cron-cluster"))
		require.NoError(t, newActorSystem.Stop(ctx))
		provider.AssertExpectations(t)
	})
	t.Run("With ScheduleGrainWithCron in cluster mode without explicit reference returns an error", func(t *testing.T) {
		ctx := context.TODO()
		newActorSystem, provider := startGrainSchedulerClusterNode(t)

		identity, err := GrainOf[*MockGrain](ctx, newActorSystem, "cluster-cron-no-reference")
		require.NoError(t, err)

		err = newActorSystem.ScheduleGrainWithCron(ctx, new(testpb.TestSend), identity, "* * * ? * *")
		require.ErrorIs(t, err, errors.ErrScheduleReferenceRequired)
		assert.Empty(t, newActorSystem.ListSchedules())

		require.NoError(t, newActorSystem.Stop(ctx))
		provider.AssertExpectations(t)
	})
	t.Run("With a duplicate reference", func(t *testing.T) {
		ctx := context.TODO()
		newActorSystem, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, newActorSystem.Start(ctx))
		pause.For(time.Second)

		identity, err := GrainOf[*MockGrain](ctx, newActorSystem, "scheduled-duplicate")
		require.NoError(t, err)

		// a reference names one quartz job: registering it again is rejected by the scheduler
		const reference = "grain-duplicate"
		message := new(testpb.TestSend)
		require.NoError(t, newActorSystem.ScheduleGrainOnce(ctx, message, identity, time.Minute, WithReference(reference)))

		err = newActorSystem.ScheduleGrainOnce(ctx, message, identity, time.Minute, WithReference(reference))
		require.ErrorIs(t, err, quartz.ErrJobAlreadyExists)

		err = newActorSystem.ScheduleGrain(ctx, message, identity, time.Minute, WithReference(reference))
		require.ErrorIs(t, err, quartz.ErrJobAlreadyExists)

		err = newActorSystem.ScheduleGrainWithCron(ctx, message, identity, "0 0 12 * * *", WithReference(reference))
		require.ErrorIs(t, err, quartz.ErrJobAlreadyExists)

		require.NoError(t, newActorSystem.CancelSchedule(reference))
		require.NoError(t, newActorSystem.Stop(ctx))
	})
}

// TestScheduleGrainWithCronTimezone pins that ScheduleGrainWithCron evaluates the cron
// expression in UTC in cluster mode and in the process local timezone otherwise, the same
// rule as ScheduleWithCron and for the same reason: the per-tick fire claims must line up
// cluster-wide. See TestScheduleWithCronTimezone for why the trigger description is asserted.
func TestScheduleGrainWithCronTimezone(t *testing.T) {
	// daily at noon: never fires during the test, so the job stays queued for inspection
	const expr = "0 0 12 * * *"

	scheduledTriggerDescription := func(t *testing.T, sys ActorSystem, reference string) string {
		t.Helper()
		job, err := sys.(*actorSystem).scheduler.quartzScheduler.GetScheduledJob(quartz.NewJobKey(reference))
		require.NoError(t, err)
		return job.Trigger().Description()
	}

	t.Run("non-cluster mode evaluates cron in the local timezone", func(t *testing.T) {
		ctx := context.TODO()
		newActorSystem, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, newActorSystem.Start(ctx))
		pause.For(time.Second)

		identity, err := GrainOf[*MockGrain](ctx, newActorSystem, "cron-local-tz")
		require.NoError(t, err)

		const reference = "grain-cron-local-tz"
		require.NoError(t, newActorSystem.ScheduleGrainWithCron(ctx, new(testpb.TestSend), identity, expr, WithReference(reference)))

		localTrigger, err := quartz.NewCronTriggerWithLoc(expr, time.Now().Location())
		require.NoError(t, err)
		require.Equal(t, localTrigger.Description(), scheduledTriggerDescription(t, newActorSystem, reference))

		require.NoError(t, newActorSystem.Stop(ctx))
	})
	t.Run("cluster mode evaluates cron in UTC", func(t *testing.T) {
		ctx := context.TODO()
		newActorSystem, provider := startGrainSchedulerClusterNode(t)

		identity, err := GrainOf[*MockGrain](ctx, newActorSystem, "cron-utc-tz")
		require.NoError(t, err)

		const reference = "grain-cron-utc-tz"
		require.NoError(t, newActorSystem.ScheduleGrainWithCron(ctx, new(testpb.TestSend), identity, expr, WithReference(reference)))

		utcTrigger, err := quartz.NewCronTriggerWithLoc(expr, time.UTC)
		require.NoError(t, err)
		require.Equal(t, utcTrigger.Description(), scheduledTriggerDescription(t, newActorSystem, reference))

		require.NoError(t, newActorSystem.Stop(ctx))
		provider.AssertExpectations(t)
	})
}

// TestMakeGrainJobFn drives the Grain job function directly against a mocked cluster engine
// to pin its claim gate: a lost claim skips delivery silently, a claim error is propagated,
// and a won claim, or no claim outside cluster mode, hands the message to TellGrain. The bare
// actor system is not started, so ErrActorSystemNotStarted from TellGrain is the proof that
// delivery was attempted.
func TestMakeGrainJobFn(t *testing.T) {
	metadataCtx := func(runTime int64) context.Context {
		return context.WithValue(context.Background(), quartz.JobMetadataContextKey, quartz.JobMetadata{RunTime: runTime})
	}

	identity := newGrainIdentity(NewMockGrain(), "job")
	claim := &scheduleFireClaim{reference: "ref", ttl: time.Minute}

	t.Run("skips delivery when another node already claimed the tick", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		clusterMock.EXPECT().ClaimScheduleFire(mock.Anything, mock.Anything, time.Minute).Return(cluster.ErrScheduleFireClaimed)

		sched := newScheduler(log.DiscardLogger, time.Second, &actorSystem{cluster: clusterMock})
		done, err := sched.makeGrainJobFn(identity, new(testpb.TestSend), claim)(metadataCtx(time.Now().UnixNano()))
		require.NoError(t, err)
		require.True(t, done)
	})
	t.Run("propagates claim errors and skips delivery", func(t *testing.T) {
		expectedErr := stderrors.New("claim failure")
		clusterMock := mockscluster.NewCluster(t)
		clusterMock.EXPECT().ClaimScheduleFire(mock.Anything, mock.Anything, time.Minute).Return(expectedErr)

		sched := newScheduler(log.DiscardLogger, time.Second, &actorSystem{cluster: clusterMock})
		done, err := sched.makeGrainJobFn(identity, new(testpb.TestSend), claim)(metadataCtx(time.Now().UnixNano()))
		require.ErrorIs(t, err, expectedErr)
		require.False(t, done)
	})
	t.Run("delivers once the claim is won", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		clusterMock.EXPECT().ClaimScheduleFire(mock.Anything, mock.Anything, time.Minute).Return(nil)

		sched := newScheduler(log.DiscardLogger, time.Second, &actorSystem{cluster: clusterMock})
		done, err := sched.makeGrainJobFn(identity, new(testpb.TestSend), claim)(metadataCtx(time.Now().UnixNano()))
		require.ErrorIs(t, err, errors.ErrActorSystemNotStarted)
		require.False(t, done)
	})
	t.Run("delivers without a claim outside cluster mode", func(t *testing.T) {
		sched := newScheduler(log.DiscardLogger, time.Second, &actorSystem{})
		done, err := sched.makeGrainJobFn(identity, new(testpb.TestSend), nil)(context.Background())
		require.ErrorIs(t, err, errors.ErrActorSystemNotStarted)
		require.False(t, done)
	})
}

// TestGrainSchedulerListSchedules pins how ListSchedules reports Grain schedules: the target
// is the Grain identity and Path is nil, the mirror image of an actor schedule.
func TestGrainSchedulerListSchedules(t *testing.T) {
	t.Run("With a Grain schedule", func(t *testing.T) {
		ctx := context.TODO()
		newActorSystem, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, newActorSystem.Start(ctx))
		pause.For(time.Second)

		identity, err := GrainOf[*MockGrain](ctx, newActorSystem, "listed")
		require.NoError(t, err)

		err = newActorSystem.ScheduleGrain(ctx, new(testpb.TestSend), identity, 100*time.Millisecond, WithReference("grain-ref"))
		require.NoError(t, err)

		schedules := newActorSystem.ListSchedules()
		require.Len(t, schedules, 1)
		info := schedules[0]
		assert.Equal(t, "grain-ref", info.Reference)
		assert.Nil(t, info.Path)
		require.NotNil(t, info.Grain)
		assert.True(t, identity.Equal(info.Grain))

		require.NoError(t, newActorSystem.CancelSchedule("grain-ref"))
		assert.Empty(t, newActorSystem.ListSchedules())

		require.NoError(t, newActorSystem.Stop(ctx))
	})
	t.Run("With actor and Grain schedules", func(t *testing.T) {
		ctx := context.TODO()
		newActorSystem, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, newActorSystem.Start(ctx))
		pause.For(time.Second)

		actorRef, err := newActorSystem.Spawn(ctx, "test", NewMockActor())
		require.NoError(t, err)
		pause.For(time.Second)

		identity, err := GrainOf[*MockGrain](ctx, newActorSystem, "listed-alongside")
		require.NoError(t, err)

		message := new(testpb.TestSend)
		require.NoError(t, newActorSystem.Schedule(ctx, message, actorRef, 100*time.Millisecond, WithReference("actor-ref")))
		require.NoError(t, newActorSystem.ScheduleGrain(ctx, message, identity, 100*time.Millisecond, WithReference("grain-ref")))

		schedules := newActorSystem.ListSchedules()
		require.Len(t, schedules, 2)

		byReference := make(map[string]ScheduleInfo, len(schedules))
		for _, info := range schedules {
			byReference[info.Reference] = info
		}

		actorInfo, ok := byReference["actor-ref"]
		require.True(t, ok)
		require.NotNil(t, actorInfo.Path)
		assert.Equal(t, actorRef.Path().String(), actorInfo.Path.String())
		assert.Nil(t, actorInfo.Grain)

		grainInfo, ok := byReference["grain-ref"]
		require.True(t, ok)
		assert.Nil(t, grainInfo.Path)
		require.NotNil(t, grainInfo.Grain)
		assert.True(t, identity.Equal(grainInfo.Grain))

		require.NoError(t, newActorSystem.CancelSchedule("actor-ref"))
		require.NoError(t, newActorSystem.CancelSchedule("grain-ref"))
		assert.Empty(t, newActorSystem.ListSchedules())

		require.NoError(t, newActorSystem.Stop(ctx))
	})
	t.Run("With a ScheduleGrainOnce schedule that disappears once delivered", func(t *testing.T) {
		ctx := context.TODO()
		newActorSystem, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, newActorSystem.Start(ctx))
		pause.For(time.Second)

		identity, err := GrainOf[*MockGrain](ctx, newActorSystem, "listed-once")
		require.NoError(t, err)

		err = newActorSystem.ScheduleGrainOnce(ctx, new(testpb.TestSend), identity, 500*time.Millisecond, WithReference("grain-once-ref"))
		require.NoError(t, err)

		schedules := newActorSystem.ListSchedules()
		require.Len(t, schedules, 1)
		assert.Equal(t, "grain-once-ref", schedules[0].Reference)
		assert.True(t, identity.Equal(schedules[0].Grain))

		require.Eventually(t, func() bool {
			return len(newActorSystem.ListSchedules()) == 0
		}, 5*time.Second, 50*time.Millisecond)

		assert.EqualValues(t, 1, grainProcessedCount(newActorSystem, identity))

		require.NoError(t, newActorSystem.Stop(ctx))
	})
}

// TestGrainSchedulerCounters pins that Grain schedules feed the same scheduled and cancelled
// counters as actor schedules, so the scheduler metrics count them.
func TestGrainSchedulerCounters(t *testing.T) {
	ctx := context.TODO()
	newActorSystem, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, newActorSystem.Start(ctx))
	pause.For(time.Second)

	identity, err := GrainOf[*MockGrain](ctx, newActorSystem, "counted")
	require.NoError(t, err)

	sched := newActorSystem.(*actorSystem).scheduler
	scheduled := sched.scheduledCount.Load()
	cancelled := sched.cancelledCount.Load()

	message := new(testpb.TestSend)
	require.NoError(t, newActorSystem.ScheduleGrainOnce(ctx, message, identity, time.Minute, WithReference("counted-once")))
	require.NoError(t, newActorSystem.ScheduleGrain(ctx, message, identity, time.Minute, WithReference("counted-interval")))
	require.NoError(t, newActorSystem.ScheduleGrainWithCron(ctx, message, identity, "0 0 12 * * *", WithReference("counted-cron")))
	assert.Equal(t, scheduled+3, sched.scheduledCount.Load())

	require.NoError(t, newActorSystem.CancelSchedule("counted-once"))
	require.NoError(t, newActorSystem.CancelSchedule("counted-interval"))
	require.NoError(t, newActorSystem.CancelSchedule("counted-cron"))
	assert.Equal(t, cancelled+3, sched.cancelledCount.Load())

	require.NoError(t, newActorSystem.Stop(ctx))
}

// TestGrainSchedulerMultiNode runs the cluster rules of Grain schedules on a real three-node
// NATS-backed cluster: cron single fire across nodes, node-local interval schedules, and a
// one-shot registered on a node that does not own the Grain. The Grain is activated on node1
// only, so every delivery from another node travels through TellGrain's remote routing.
func TestGrainSchedulerMultiNode(t *testing.T) {
	ctx := context.TODO()
	srv := startNatsServer(t)
	systems, providers := startNATsSystems(t, srv.Addr().String(), 3, withTestExtraGrains(new(MockCountingGrain)))
	node1, node2, node3 := systems[0], systems[1], systems[2]

	// let the membership settle before registering anything
	pause.For(3 * time.Second)

	// activate activates a counting Grain on node1 under name and waits until the other nodes
	// can resolve its registry record, so their deliveries route to node1 instead of activating
	// a copy of their own.
	activate := func(t *testing.T, name string) (*MockCountingGrain, *GrainIdentity) {
		t.Helper()
		grain := NewMockCountingGrain()
		identity, err := node1.GrainIdentity(ctx, name, func(context.Context) (Grain, error) { return grain, nil })
		require.NoError(t, err)

		for _, node := range []ActorSystem{node2, node3} {
			require.Eventually(t, func() bool {
				record, err := node.(*actorSystem).getCluster().GetGrain(ctx, identity.String())
				return err == nil && node1.(*actorSystem).isLocalGrainOwner(record)
			}, 10*time.Second, 100*time.Millisecond)
		}

		return grain, identity
	}

	t.Run("three nodes racing one cron tick deliver once per tick", func(t *testing.T) {
		grain, identity := activate(t, "cron-race")

		// every second, registered identically on every node
		const expr = "* * * ? * *"
		const reference = "grain-cron-race"
		for _, node := range systems {
			require.NoError(t, node.ScheduleGrainWithCron(ctx, new(testpb.TestSend), identity, expr, WithReference(reference)))
		}

		// a five-second window holds four to six ticks. Three triggers fire for each of them, so
		// duplicate delivery by even two nodes would push the count to eight or more; single fire
		// keeps it at one per tick, plus at most one tick in flight at cancel time.
		pause.For(5 * time.Second)
		for _, node := range systems {
			require.NoError(t, node.CancelSchedule(reference))
		}

		pause.For(500 * time.Millisecond)
		delivered := grain.sends.Load()
		require.GreaterOrEqual(t, delivered, int64(3))
		require.LessOrEqual(t, delivered, int64(7), "a cron tick was delivered by more than one node")

		// the Grain stayed on node1: the other nodes routed to it instead of activating a copy
		for _, node := range []ActorSystem{node2, node3} {
			_, exists := node.(*actorSystem).grains.Get(identity.String())
			require.False(t, exists)
		}
	})
	t.Run("two nodes each deliver their own interval schedule", func(t *testing.T) {
		grain, identity := activate(t, "interval-local")

		// the same reference on two nodes names two independent, node-local schedules; each node
		// sends a different message type so the deliveries can be attributed
		const reference = "grain-interval-local"
		require.NoError(t, node1.ScheduleGrain(ctx, new(testpb.TestSend), identity, 200*time.Millisecond, WithReference(reference)))
		require.NoError(t, node2.ScheduleGrain(ctx, new(testpb.TestReply), identity, 200*time.Millisecond, WithReference(reference)))

		require.Eventually(t, func() bool {
			return grain.sends.Load() >= 3 && grain.replies.Load() >= 3
		}, 10*time.Second, 100*time.Millisecond)

		require.NoError(t, node1.CancelSchedule(reference))
		require.NoError(t, node2.CancelSchedule(reference))
		require.ErrorIs(t, node3.CancelSchedule(reference), errors.ErrScheduledReferenceNotFound)
	})
	t.Run("a one-shot registered on a non-owner node reaches the owner", func(t *testing.T) {
		grain, identity := activate(t, "one-shot-remote")

		require.NoError(t, node3.ScheduleGrainOnce(ctx, new(testpb.TestSend), identity, 100*time.Millisecond))

		require.Eventually(t, func() bool {
			return grain.sends.Load() == 1
		}, 10*time.Second, 50*time.Millisecond)

		// delivered to node1's activation, not to a copy on node3
		_, exists := node3.(*actorSystem).grains.Get(identity.String())
		require.False(t, exists)
		process, ok := node1.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		require.EqualValues(t, 1, process.processedCount.Load())
	})

	for i, node := range systems {
		assert.NoError(t, node.Stop(ctx))
		assert.NoError(t, providers[i].Close())
	}

	srv.Shutdown()
}
