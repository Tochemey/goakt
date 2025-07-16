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
	"crypto/tls"
	"errors"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/address"
	"github.com/tochemey/goakt/v4/goaktpb"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	testkit "github.com/tochemey/goakt/v4/mocks/discovery"
	extmocks "github.com/tochemey/goakt/v4/mocks/extension"
	"github.com/tochemey/goakt/v4/passivation"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// nolint
func TestActorSystem(t *testing.T) {
	t.Run("New instance with Defaults", func(t *testing.T) {
		actorSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSystem)
		var iface any = actorSystem
		_, ok := iface.(ActorSystem)
		assert.True(t, ok)
		assert.Equal(t, "testSys", actorSystem.Name())
		assert.Empty(t, actorSystem.Actors())
		assert.NotNil(t, actorSystem.Logger())
	})
	t.Run("New instance with Missing Name", func(t *testing.T) {
		sys, err := NewActorSystem("")
		assert.Error(t, err)
		assert.Nil(t, sys)
		require.ErrorIs(t, err, ErrNameRequired)
	})
	t.Run("With invalid actor system Name", func(t *testing.T) {
		sys, err := NewActorSystem("$omeN@me")
		assert.Error(t, err)
		assert.Nil(t, sys)
		require.ErrorIs(t, err, ErrInvalidActorSystemName)
	})
	t.Run("With Spawn an actor when not System started", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, "Test", actor)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrActorSystemNotStarted)
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
	t.Run("With RemoteActor/ActorOf with clustering enabled", func(t *testing.T) {
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

		// wait for the cluster to start
		pause.For(time.Second)

		// create an actor
		actorName := uuid.NewString()
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// wait for a while for replication to take effect
		// otherwise the subsequent test will return actor not found
		pause.For(time.Second)

		// the actor should now exist
		exists, err := newActorSystem.ActorExists(ctx, actorName)
		require.NoError(t, err)
		require.True(t, exists)

		// get the actor
		addr, _, err := newActorSystem.ActorOf(ctx, actorName)
		require.NoError(t, err)
		require.NotNil(t, addr)

		// use RemoteActor method and compare the results
		remoteAddr, err := newActorSystem.RemoteActor(ctx, actorName)
		require.NoError(t, err)
		require.NotNil(t, remoteAddr)
		require.True(t, proto.Equal(remoteAddr, addr))

		remoting := NewRemoting()
		from := address.NoSender()
		reply, err := remoting.RemoteAsk(ctx, from, addr, new(testpb.TestReply), 20*time.Second)
		require.NoError(t, err)
		require.NotNil(t, reply)

		// get the actor partition
		partition := newActorSystem.GetPartition(actorName)
		assert.GreaterOrEqual(t, partition, 0)

		// assert actor not found
		actorName = "some-actor"
		exists, err = newActorSystem.ActorExists(ctx, actorName)
		require.NoError(t, err)
		require.False(t, exists)

		addr, pid, err := newActorSystem.ActorOf(ctx, actorName)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrActorNotFound)
		require.Nil(t, addr)
		require.Nil(t, pid)

		remoteAddr, err = newActorSystem.RemoteActor(ctx, actorName)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrActorNotFound)
		require.Nil(t, remoteAddr)

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
		provider.AssertExpectations(t)
	})
	t.Run("With remoting enabled", func(t *testing.T) {
		ctx := context.TODO()
		remotingPort := dynaport.Get(1)[0]

		logger := log.DiscardLogger
		host := "127.0.0.1"

		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		require.NoError(t, err)

		// wait for the cluster to fully start
		pause.For(time.Second)

		// create an actor
		actorName := uuid.NewString()

		addr, pid, err := newActorSystem.ActorOf(ctx, actorName)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrMethodCallNotAllowed)
		require.Nil(t, addr)
		require.Nil(t, pid)

		t.Cleanup(
			func() {
				err = newActorSystem.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("With ActorOf:remoting not enabled", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actorName := "actorQA"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		addr, pid, err := sys.ActorOf(ctx, actorName)
		require.NoError(t, err)
		require.NotNil(t, pid)
		require.NotNil(t, addr)

		// stop the actor after some time
		pause.For(time.Second)

		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("With ActorOf: not found", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actorName := "notFound"

		exists, err := sys.ActorExists(ctx, actorName)
		require.NoError(t, err)
		require.False(t, exists)

		addr, pid, err := sys.ActorOf(ctx, actorName)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrActorNotFound)
		require.Nil(t, pid)
		require.Nil(t, addr)

		// stop the actor after some time
		pause.For(time.Second)

		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("With ActorOf actor system started", func(t *testing.T) {
		ctx := context.TODO()
		remotingPort := dynaport.Get(1)[0]

		logger := log.DiscardLogger
		host := "127.0.0.1"

		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)

		// create an actor
		actorName := uuid.NewString()

		addr, pid, err := newActorSystem.ActorOf(ctx, actorName)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrActorSystemNotStarted)
		require.Nil(t, addr)
		require.Nil(t, pid)
	})
	t.Run("With ReSpawn", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		consumer, err := sys.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		actorName := "exchanger"
		actorRef, err := sys.Spawn(ctx, actorName, &exchanger{})
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		pause.For(500 * time.Millisecond)

		// send a message to the actor
		reply, err := Ask(ctx, actorRef, new(testpb.TestReply), replyTimeout)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expected := new(testpb.Reply)
		require.True(t, proto.Equal(expected, reply))
		require.True(t, actorRef.IsRunning())

		pause.For(500 * time.Millisecond)

		// restart the actor
		_, err = sys.ReSpawn(ctx, actorName)
		require.NoError(t, err)

		// wait for the actor to complete start
		// TODO we can add a callback for complete start
		pause.For(time.Second)
		require.True(t, actorRef.IsRunning())

		var items []*goaktpb.ActorRestarted
		for message := range consumer.Iterator() {
			payload := message.Payload()
			restarted, ok := payload.(*goaktpb.ActorRestarted)
			if ok {
				items = append(items, restarted)
			}
		}

		require.Len(t, items, 1)

		// send a message to the actor
		reply, err = Ask(ctx, actorRef, new(testpb.TestReply), replyTimeout)
		require.NoError(t, err)
		require.NotNil(t, reply)

		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("With ReSpawn with PreStart failure", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem(
			"testSys",
			WithLogger(log.DiscardLogger),
		)

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actorName := "actor"
		actorRef, err := sys.Spawn(ctx, actorName, NewMockRestart(),
			WithPassivationStrategy(passivation.NewTimeBasedStrategy(time.Minute)))
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		require.True(t, actorRef.IsRunning())

		// wait for a while for the system to stop
		pause.For(time.Second)
		// restart the actor
		pid, err := sys.ReSpawn(ctx, actorName)
		require.Error(t, err)
		require.Nil(t, pid)

		require.False(t, actorRef.IsRunning())

		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("With ReSpawn: actor not found", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actorName := "exchanger"
		actorRef, err := sys.Spawn(ctx, actorName, &exchanger{})
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// send a message to the actor
		reply, err := Ask(ctx, actorRef, new(testpb.TestReply), replyTimeout)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expected := new(testpb.Reply)
		require.True(t, proto.Equal(expected, reply))
		require.True(t, actorRef.IsRunning())
		// stop the actor after some time
		pause.For(time.Second)

		err = sys.Kill(ctx, actorName)
		require.NoError(t, err)

		// wait for a while for the system to stop
		pause.For(time.Second)
		// restart the actor
		_, err = sys.ReSpawn(ctx, actorName)
		require.Error(t, err)

		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("With ReSpawn an actor when not System started", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		_, err := sys.ReSpawn(ctx, "some-actor")
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrActorSystemNotStarted)
	})
	t.Run("ReSpawn with remoting enabled", func(t *testing.T) {
		ctx := context.TODO()
		remotingPort := dynaport.Get(1)[0]

		logger := log.DiscardLogger
		host := "127.0.0.1"

		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		require.NoError(t, err)

		actorName := "exchanger"
		actorRef, err := newActorSystem.Spawn(ctx, actorName, &exchanger{})
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// send a message to the actor
		reply, err := Ask(ctx, actorRef, new(testpb.TestReply), replyTimeout)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expected := new(testpb.Reply)
		require.True(t, proto.Equal(expected, reply))
		require.True(t, actorRef.IsRunning())
		// stop the actor after some time
		pause.For(time.Second)

		// restart the actor
		_, err = newActorSystem.ReSpawn(ctx, actorName)
		require.NoError(t, err)

		// wait for the actor to complete start
		// TODO we can add a callback for complete start
		pause.For(time.Second)
		require.True(t, actorRef.IsRunning())

		t.Cleanup(
			func() {
				err = newActorSystem.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("With NumActors", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actorName := "exchanger"
		actorRef, err := sys.Spawn(ctx, actorName, &exchanger{})
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// wait for the start of the actor to be complete
		pause.For(time.Second)

		assert.EqualValues(t, 1, sys.NumActors())

		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("With remoting enabled: Actor not found", func(t *testing.T) {
		ctx := context.TODO()
		remotingPort := dynaport.Get(1)[0]

		logger := log.DiscardLogger
		host := "127.0.0.1"

		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		require.NoError(t, err)

		pause.For(time.Second)

		remoting := NewRemoting()
		actorName := "some-actor"
		addr, err := remoting.RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)
		require.True(t, addr.Equals(address.NoSender()))

		// attempt to send a message will fail
		addr = address.From(
			&goaktpb.Address{
				Host: host,
				Port: int32(remotingPort),
				Name: actorName,
				Id:   "",
			},
		)
		from := address.NoSender()
		reply, err := remoting.RemoteAsk(ctx, from, addr, new(testpb.TestReply), 20*time.Second)
		require.Error(t, err)
		require.Nil(t, reply)

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		t.Cleanup(
			func() {
				err = newActorSystem.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("With RemoteActor failure when system not started", func(t *testing.T) {
		ctx := context.TODO()
		remotingPort := dynaport.Get(1)[0]

		logger := log.DiscardLogger
		host := "127.0.0.1"

		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)

		actorName := "some-actor"
		remoteAddr, err := newActorSystem.RemoteActor(ctx, actorName)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrActorSystemNotStarted)
		require.Nil(t, remoteAddr)
	})
	t.Run("With RemoteActor failure when system not started", func(t *testing.T) {
		ctx := context.TODO()
		remotingPort := dynaport.Get(1)[0]

		logger := log.DiscardLogger
		host := "127.0.0.1"

		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)

		err = newActorSystem.Stop(ctx)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrActorSystemNotStarted)
	})
	t.Run("With RemoteActor failure when cluster is not enabled", func(t *testing.T) {
		ctx := context.TODO()
		remotingPort := dynaport.Get(1)[0]

		logger := log.DiscardLogger
		host := "127.0.0.1"

		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		require.NoError(t, err)

		// wait for the system to properly start
		actorName := "some-actor"
		remoteAddr, err := newActorSystem.RemoteActor(ctx, actorName)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrClusterDisabled)
		require.Nil(t, remoteAddr)

		// stop the actor after some time
		pause.For(time.Second)

		err = newActorSystem.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("With LocalActor", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		// create an actor
		actorName := "exchanger"
		ref, err := sys.Spawn(ctx, actorName, &exchanger{})
		assert.NoError(t, err)
		require.NotNil(t, ref)

		// locate the actor
		local, err := sys.LocalActor(actorName)
		require.NoError(t, err)
		require.NotNil(t, local)

		require.Equal(t, ref.Address().String(), local.Address().String())

		// stop the actor after some time
		pause.For(time.Second)

		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("With LocalActor: Actor not found", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		// locate the actor
		ref, err := sys.LocalActor("some-name")
		require.Error(t, err)
		require.Nil(t, ref)
		require.ErrorIs(t, err, ErrActorNotFound)

		// stop the actor after some time
		pause.For(time.Second)

		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("With LocalActor when system not started", func(t *testing.T) {
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// create an actor
		actorName := "exchanger"

		// locate the actor
		local, err := sys.LocalActor(actorName)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrActorSystemNotStarted)
		require.Nil(t, local)
	})
	t.Run("With Kill an actor when not System started", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Kill(ctx, "Test")
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrActorSystemNotStarted)
	})
	t.Run("With Kill an actor when actor not found", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)
		err = sys.Kill(ctx, "Test")
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrActorNotFound)
		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("With housekeeping", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem(
			"housekeeperSys",
			WithLogger(log.DiscardLogger),
		)

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		// wait for the system to properly start
		pause.For(time.Second)

		actorName := "HousekeeperActor"
		actorHandler := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actorHandler,
			WithPassivationStrategy(passivation.NewTimeBasedStrategy(passivateAfter)),
		)
		require.NoError(t, err)
		require.NotNil(t, actorRef)

		// wait for the actor to properly start
		pause.For(time.Second)

		// locate the actor
		ref, err := sys.LocalActor(actorName)
		require.Error(t, err)
		require.Nil(t, ref)
		require.ErrorIs(t, err, ErrActorNotFound)

		// stop the actor after some time
		pause.For(time.Second)

		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("With GetPartition returning zero in non cluster env", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem(
			"housekeeperSys",
			WithLogger(log.DiscardLogger),
		)

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		// wait for the system to properly start
		pause.For(time.Second)

		partition := sys.GetPartition("some-actor")
		assert.Zero(t, partition)

		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("With actor PostStop error", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actor := &MockPostStop{}
		actorRef, err := sys.Spawn(ctx, "Test", actor)
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// stop the actor after some time
		pause.For(time.Second)

		t.Cleanup(
			func() {
				assert.Error(t, sys.Stop(ctx))
			},
		)
	})
	t.Run("With deadletter subscription ", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		// wait for complete start
		pause.For(time.Second)

		// create a deadletter subscriber
		consumer, err := sys.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		// create the black hole actor
		actor := &MockUnhandled{}
		actorRef, err := sys.Spawn(ctx, "unhandledQA", actor)
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// wait a while
		pause.For(time.Second)

		// every message sent to the actor will result in deadletter
		for i := 0; i < 5; i++ {
			require.NoError(t, Tell(ctx, actorRef, new(testpb.TestSend)))
		}

		pause.For(time.Second)

		var items []*goaktpb.Deadletter
		for message := range consumer.Iterator() {
			payload := message.Payload()
			// only listening to deadletter
			deadletter, ok := payload.(*goaktpb.Deadletter)
			if ok {
				items = append(items, deadletter)
			}
		}

		require.Len(t, items, 5)

		// unsubscribe the consumer
		err = sys.Unsubscribe(consumer)
		require.NoError(t, err)

		metric := sys.Metric(ctx)
		require.NotNil(t, metric)
		require.EqualValues(t, 1, metric.ActorsCount())
		require.EqualValues(t, 5, metric.DeadlettersCount())
		require.NotZero(t, metric.Uptime())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With deadletter subscription when not started", func(t *testing.T) {
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// create a deadletter subscriber
		consumer, err := sys.Subscribe()
		require.Error(t, err)
		require.Nil(t, consumer)
	})
	t.Run("With deadletter unsubscription when not started", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		consumer, err := sys.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		// stop the actor system
		assert.NoError(t, sys.Stop(ctx))

		pause.For(time.Second)

		// create a deadletter subscriber
		err = sys.Unsubscribe(consumer)
		require.Error(t, err)
	})
	t.Run("With Passivation with clustering enabled", func(t *testing.T) {
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

		// wait for the cluster to start
		pause.For(time.Second)

		require.True(t, newActorSystem.Running())

		// create an actor
		actorName := uuid.NewString()
		actor := NewMockActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor,
			WithPassivationStrategy(passivation.NewTimeBasedStrategy(passivateAfter)))
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// wait for a while for replication to take effect
		// otherwise the subsequent test will return actor not found
		pause.For(time.Second)

		// get the actor
		addr, pid, err := newActorSystem.ActorOf(ctx, actorName)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrActorNotFound)
		require.Nil(t, addr)
		require.Nil(t, pid)

		// use RemoteActor method and compare the results
		remoteAddr, err := newActorSystem.RemoteActor(ctx, actorName)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrActorNotFound)
		require.Nil(t, remoteAddr)

		// stop the actor after some time
		pause.For(time.Second)
		err = newActorSystem.Stop(ctx)
		assert.NoError(t, err)
		provider.AssertExpectations(t)
	})
	t.Run("With cluster events subscription", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start system cluster
		cl1, sd1 := testCluster(t, srv.Addr().String())
		peerAddress1 := cl1.PeerAddress()
		require.NotEmpty(t, peerAddress1)

		// create a subscriber to node 1
		subscriber1, err := cl1.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, subscriber1)

		// create and start system cluster
		cl2, sd2 := testCluster(t, srv.Addr().String())
		peerAddress2 := cl2.PeerAddress()
		require.NotEmpty(t, peerAddress2)

		// create a subscriber to node 2
		subscriber2, err := cl2.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, subscriber2)

		// wait for some time
		pause.For(time.Second)

		// capture the joins
		var joins []*goaktpb.NodeJoined
		for event := range subscriber1.Iterator() {
			// get the event payload
			payload := event.Payload()
			// only listening to cluster event
			if nodeJoined, ok := payload.(*goaktpb.NodeJoined); ok {
				joins = append(joins, nodeJoined)
			}
		}

		// assert the joins list
		require.NotEmpty(t, joins)
		require.Len(t, joins, 1)
		require.Equal(t, peerAddress2, joins[0].GetAddress())

		// wait for some time
		pause.For(time.Second)

		// stop the node
		require.NoError(t, cl1.Unsubscribe(subscriber1))
		require.NoError(t, cl1.Stop(ctx))
		require.NoError(t, sd1.Close())

		// wait for some time
		pause.For(time.Second)

		var lefts []*goaktpb.NodeLeft
		for event := range subscriber2.Iterator() {
			payload := event.Payload()

			// only listening to cluster event
			nodeLeft, ok := payload.(*goaktpb.NodeLeft)
			if ok {
				lefts = append(lefts, nodeLeft)
			}
		}

		require.NotEmpty(t, lefts)
		require.Len(t, lefts, 1)
		require.Equal(t, peerAddress1, lefts[0].GetAddress())

		require.NoError(t, cl2.Unsubscribe(subscriber2))

		assert.NoError(t, cl2.Stop(ctx))
		// stop the discovery engines
		assert.NoError(t, sd2.Close())
		// shutdown the nats server gracefully
		srv.Shutdown()
	})
	t.Run("With PeerAddress empty when cluster not enabled", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)
		require.Empty(t, sys.PeerAddress())

		require.NoError(t, sys.Stop(ctx))
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
	t.Run("With SpawnFromFunc with actorSystem not started", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		receiveFn := func(ctx context.Context, message proto.Message) error {
			expected := &testpb.Reply{Content: "test spawn from func"}
			assert.True(t, proto.Equal(expected, message))
			return nil
		}

		preStart := func(ctx context.Context) error {
			return errors.New("failed")
		}

		actorRef, err := sys.SpawnFromFunc(ctx, receiveFn, WithPreStart(preStart))
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrActorSystemNotStarted)
		assert.Nil(t, actorRef)
	})
	t.Run("With happy path Register", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// register the actor
		err = sys.Register(ctx, &exchanger{})
		require.NoError(t, err)

		err = sys.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("With Register when actor system not started", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		// register the actor
		err = sys.Register(ctx, &exchanger{})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrActorSystemNotStarted)

		err = sys.Stop(ctx)
		require.Error(t, err)
	})
	t.Run("With happy path Deregister", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// register the actor
		err = sys.Register(ctx, &exchanger{})
		require.NoError(t, err)

		err = sys.Deregister(ctx, &exchanger{})
		require.NoError(t, err)

		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("With Deregister when actor system not started", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		err = sys.Deregister(ctx, &exchanger{})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrActorSystemNotStarted)

		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.Error(t, err)
			},
		)
	})
	t.Run("With RemoteSpawn with clustering enabled", func(t *testing.T) {
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
		provider := new(testkit.Provider)
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
			WithCluster(
				NewClusterConfig().
					WithKinds(new(exchanger)).
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

		// wait for the cluster to start
		pause.For(time.Second)

		remoting := NewRemoting()
		// create an actor
		actorName := "actorID"
		// fetching the address of the that actor should return nil address
		addr, err := remoting.RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)
		require.True(t, addr.Equals(address.NoSender()))

		// spawn the remote actor
		request := &remote.SpawnRequest{
			Name:        actorName,
			Kind:        "actor.exchanger",
			Singleton:   false,
			Relocatable: true,
		}
		err = remoting.RemoteSpawn(ctx, host, remotingPort, request)
		require.NoError(t, err)

		// re-fetching the address of the actor should return not nil address after start
		addr, err = remoting.RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)
		require.NotNil(t, addr)

		// send the message to exchanger actor one using remote messaging
		from := address.NoSender()
		reply, err := remoting.RemoteAsk(ctx, from, addr, new(testpb.TestReply), 20*time.Second)

		require.NoError(t, err)
		require.NotNil(t, reply)
		require.True(t, reply.MessageIs(new(testpb.Reply)))

		actual := new(testpb.Reply)
		err = reply.UnmarshalTo(actual)
		require.NoError(t, err)

		expected := new(testpb.Reply)
		assert.True(t, proto.Equal(expected, actual))

		t.Cleanup(
			func() {
				err = newActorSystem.Stop(ctx)
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
		node1, sd1 := testCluster(t, srv.Addr().String())
		peerAddress1 := node1.PeerAddress()
		require.NotEmpty(t, peerAddress1)

		// create and start system cluster
		node2, sd2 := testCluster(t, srv.Addr().String())
		peerAddress2 := node2.PeerAddress()
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
		require.ErrorIs(t, err, ErrActorAlreadyExists)

		// free resource
		require.NoError(t, node2.Stop(ctx))
		assert.NoError(t, node1.Stop(ctx))
		assert.NoError(t, sd2.Close())
		assert.NoError(t, sd1.Close())
		// shutdown the nats server gracefully
		srv.Shutdown()
	})
	t.Run("With CoordinatedShutdown with ShouldFail strategy", func(t *testing.T) {
		ctx := context.TODO()
		executionCount := atomic.NewInt32(0)
		// don't do this in production code, this is just for testing
		strategy := -1

		sys, _ := NewActorSystem("testSys",
			WithCoordinatedShutdown(
				&MockShutdownHook{executionCount: executionCount, strategy: ShouldFail},
				&MockShutdownHook{executionCount: executionCount, strategy: RecoveryStrategy(strategy)},
			),
			WithLogger(log.DiscardLogger))

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
		require.Error(t, err)
		require.EqualValues(t, 1, executionCount.Load())
	})
	t.Run("With CoordinatedShutdown with ShouldSkip strategy", func(t *testing.T) {
		ctx := context.TODO()
		executionCount := atomic.NewInt32(0)
		// don't do this in production code, this is just for testing
		strategy := -1

		sys, _ := NewActorSystem("testSys",
			WithCoordinatedShutdown(
				&MockShutdownHook{executionCount: executionCount, strategy: ShouldSkip},
				&MockShutdownHook{executionCount: executionCount, strategy: RecoveryStrategy(strategy)},
			),
			WithLogger(log.DiscardLogger))

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
		require.Error(t, err)
		require.EqualValues(t, 2, executionCount.Load())
	})
	t.Run("With CoordinatedShutdown with ShouldRetryAndFail strategy", func(t *testing.T) {
		ctx := context.TODO()
		executionCount := atomic.NewInt32(0)
		// don't do this in production code, this is just for testing
		strategy := -1

		sys, _ := NewActorSystem("testSys",
			WithCoordinatedShutdown(
				&MockShutdownHook{executionCount: executionCount, strategy: ShouldRetryAndFail, maxRetries: 2},
				&MockShutdownHook{executionCount: executionCount, strategy: RecoveryStrategy(strategy)},
			),
			WithLogger(log.DiscardLogger))

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
		require.Error(t, err)
		require.EqualValues(t, 3, executionCount.Load())
	})
	t.Run("With CoordinatedShutdown with ShouldRetryAndSkip strategy", func(t *testing.T) {
		ctx := context.TODO()
		executionCount := atomic.NewInt32(0)
		// don't do this in production code, this is just for testing
		strategy := -1

		sys, _ := NewActorSystem("testSys",
			WithCoordinatedShutdown(
				&MockShutdownHook{executionCount: executionCount, strategy: ShouldRetryAndSkip, maxRetries: 2},
				&MockShutdownHook{executionCount: executionCount, strategy: RecoveryStrategy(strategy)},
			),
			WithLogger(log.DiscardLogger))

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
		require.Error(t, err)
		require.EqualValues(t, 4, executionCount.Load())
	})
	t.Run("With CoordinatedShutdown with panic handling", func(t *testing.T) {
		ctx := context.TODO()
		executionCount := atomic.NewInt32(0)
		// don't do this in production code, this is just for testing
		strategy := -1

		sys, _ := NewActorSystem("testSys",
			WithCoordinatedShutdown(
				&MockPanickingShutdownHook{executionCount: executionCount},
				&MockShutdownHook{executionCount: executionCount, strategy: RecoveryStrategy(strategy)},
			),
			WithLogger(log.DiscardLogger))

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
		require.Error(t, err)
		require.EqualValues(t, 1, executionCount.Load())
	})
	t.Run("With CoordinatedShutdown with panic handling: case 1", func(t *testing.T) {
		ctx := context.TODO()
		executionCount := atomic.NewInt32(0)
		// don't do this in production code, this is just for testing
		strategy := -1

		sys, _ := NewActorSystem("testSys",
			WithCoordinatedShutdown(
				&MockPanickingShutdownHook{executionCount: executionCount, testCase: "case1"},
				&MockShutdownHook{executionCount: executionCount, strategy: RecoveryStrategy(strategy)},
			),
			WithLogger(log.DiscardLogger))

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
		require.Error(t, err)
		require.ErrorContains(t, err, "case1 panic error")
		require.EqualValues(t, 1, executionCount.Load())
	})
	t.Run("With CoordinatedShutdown with panic handling: case 2", func(t *testing.T) {
		ctx := context.TODO()
		executionCount := atomic.NewInt32(0)
		// don't do this in production code, this is just for testing
		strategy := -1

		sys, _ := NewActorSystem("testSys",
			WithCoordinatedShutdown(
				&MockPanickingShutdownHook{executionCount: executionCount, testCase: "case2"},
				&MockShutdownHook{executionCount: executionCount, strategy: RecoveryStrategy(strategy)},
			),
			WithLogger(log.DiscardLogger))

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
		require.Error(t, err)
		require.ErrorContains(t, err, "case2 panic error")
		require.EqualValues(t, 1, executionCount.Load())
	})
	t.Run("With CoordinatedShutdown failure without recovery", func(t *testing.T) {
		ctx := context.TODO()
		executionCount := atomic.NewInt32(0)
		// don't do this in production code, this is just for testing
		strategy := -1

		sys, _ := NewActorSystem("testSys",
			WithCoordinatedShutdown(
				&MockShutdownHookWithoutRecovery{executionCount: executionCount},
				&MockShutdownHook{executionCount: executionCount, strategy: RecoveryStrategy(strategy)},
			),
			WithLogger(log.DiscardLogger))

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
		require.Error(t, err)
		require.ErrorContains(t, err, "mock shutdown hook without recovery")
		require.EqualValues(t, 1, executionCount.Load())
	})
	t.Run("With CoordinatedShutdown", func(t *testing.T) {
		ctx := context.TODO()
		counter := atomic.NewInt32(0)

		// don't do this in production code, this is just for testing
		strategy := -1
		sys, _ := NewActorSystem("testSys",
			WithCoordinatedShutdown(
				&MockShutdownHook{executionCount: counter, strategy: RecoveryStrategy(strategy)},
				&MockShutdownHook{executionCount: counter, strategy: RecoveryStrategy(strategy)}),
			WithLogger(log.DiscardLogger))

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
		require.NoError(t, err)

		pause.For(time.Second)

		assert.Zero(t, sys.Uptime())
		require.EqualValues(t, 2, counter.Load())
	})
	t.Run("With ActorRefs", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start system cluster
		node1, sd1 := testCluster(t, srv.Addr().String())
		peerAddress1 := node1.PeerAddress()
		require.NotEmpty(t, peerAddress1)

		// create and start system cluster
		node2, sd2 := testCluster(t, srv.Addr().String())
		peerAddress2 := node2.PeerAddress()
		require.NotEmpty(t, peerAddress2)

		// create and start system cluster
		node3, sd3 := testCluster(t, srv.Addr().String())
		require.NotNil(t, node3)
		require.NotNil(t, sd3)

		pause.For(time.Second)

		// create an actor on node1
		actor := NewMockActor()
		actorName := "actorID"
		_, err := node1.Spawn(ctx, actorName, actor)
		require.NoError(t, err)

		pause.For(200 * time.Millisecond)

		_, err = node2.Spawn(ctx, actorName, actor)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrActorAlreadyExists)

		actors := node3.ActorRefs(ctx, time.Second)
		require.Len(t, actors, 1)

		// free resource
		require.NoError(t, node2.Stop(ctx))
		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd3.Close())
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
		wg.Add(1)
		go func() {
			pause.For(receivingDelay)
			wg.Done()
		}()
		// block until timer is up
		wg.Wait()
		// let us send a message to the actor
		err = Tell(ctx, pid, new(testpb.TestSend))
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrDead)
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
		wg.Add(1)
		go func() {
			pause.For(receivingDelay)
			wg.Done()
		}()
		// block until timer is up
		wg.Wait()
		// let us send a message to the actor
		err = Tell(ctx, pid, new(testpb.TestSend))
		assert.NoError(t, err)
		assert.True(t, pid.IsRunning())
		assert.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With invalid remote config address", func(t *testing.T) {
		remotingPort := dynaport.Get(1)[0]

		logger := log.DiscardLogger
		host := "256.256.256.256"

		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort, remote.WithWriteTimeout(-1))),
		)
		require.Error(t, err)
		require.Nil(t, newActorSystem)
	})
	t.Run("With invalid cluster config", func(t *testing.T) {
		logger := log.DiscardLogger
		host := "127.0.0.1"

		// mock the discovery provider
		provider := new(testkit.Provider)
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, 2222)),
			WithCluster(
				NewClusterConfig().
					WithKinds(new(MockActor)).
					WithPartitionCount(0).
					WithReplicaCount(1).
					WithPeersPort(-1).
					WithMinimumPeersQuorum(1).
					WithDiscoveryPort(-1).
					WithDiscovery(provider)),
		)
		require.Error(t, err)
		require.Nil(t, newActorSystem)
	})
	t.Run("With invalid TLS config", func(t *testing.T) {
		logger := log.DiscardLogger
		host := "127.0.0.1"

		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, 2222)),
			WithTLS(&TLSInfo{
				ClientTLS: &tls.Config{InsecureSkipVerify: true}, // nolint
				ServerTLS: nil,
			}),
		)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidTLSConfiguration)
		require.Nil(t, newActorSystem)
	})
	t.Run("With Metric", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		// wait for complete start
		pause.For(time.Second)

		// create a deadletter subscriber
		consumer, err := sys.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		// create the black hole actor
		actor := &MockUnhandled{}
		actorRef, err := sys.Spawn(ctx, "unhandledQA", actor)
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// wait a while
		pause.For(time.Second)

		// every message sent to the actor will result in deadletter
		for i := 0; i < 5; i++ {
			require.NoError(t, Tell(ctx, actorRef, new(testpb.TestSend)))
		}

		pause.For(time.Second)

		var items []*goaktpb.Deadletter
		for message := range consumer.Iterator() {
			payload := message.Payload()
			// only listening to deadletter
			deadletter, ok := payload.(*goaktpb.Deadletter)
			if ok {
				items = append(items, deadletter)
			}
		}

		require.Len(t, items, 5)

		// unsubscribe the consumer
		err = sys.Unsubscribe(consumer)
		require.NoError(t, err)

		metric := sys.Metric(ctx)
		require.NotNil(t, metric)
		require.EqualValues(t, 1, metric.ActorsCount())
		require.EqualValues(t, 5, metric.DeadlettersCount())
		require.NotZero(t, metric.Uptime())
		require.NotZero(t, metric.MemorySize())
		require.NotZero(t, metric.MemoryUsed())
		require.NotZero(t, metric.MemoryAvailable())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With cluster enabled and invalid WAL dir", func(t *testing.T) {
		ctx := context.TODO()
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		logger := log.DiscardLogger
		host := "127.0.0.1"

		remoteConfig := remote.NewConfig(host, remotingPort)
		// mock the discovery provider
		provider := new(testkit.Provider)
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remoteConfig),
			WithCluster(
				NewClusterConfig().
					WithKinds(new(MockActor)).
					WithPartitionCount(9).
					WithReplicaCount(1).
					WithPeersPort(clusterPort).
					WithMinimumPeersQuorum(1).
					WithDiscoveryPort(gossipPort).
					WithWAL("/").
					WithDiscovery(provider)),
		)
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		require.Error(t, err)
	})
	t.Run("With Extension", func(t *testing.T) {
		ext := new(MockExtension)
		actorSystem, err := NewActorSystem("testSys", WithExtensions(ext))
		require.NoError(t, err)
		require.NotNil(t, actorSystem)
		extensions := actorSystem.Extensions()
		require.NotNil(t, extensions)
		require.Len(t, extensions, 1)
		extension := actorSystem.Extension(ext.ID())
		require.NotNil(t, extension)
		require.True(t, reflect.DeepEqual(extension, ext))
	})
	t.Run("With invalid Extension ID length", func(t *testing.T) {
		ext := extmocks.NewExtension(t)
		ext.EXPECT().ID().Return(strings.Repeat("a", 300))
		actorSystem, err := NewActorSystem("testSys", WithExtensions(ext))
		require.Error(t, err)
		require.Nil(t, actorSystem)
		ext.AssertExpectations(t)
	})
	t.Run("With invalid Extension ID", func(t *testing.T) {
		ext := extmocks.NewExtension(t)
		ext.EXPECT().ID().Return("$omeN@me")
		actorSystem, err := NewActorSystem("testSys", WithExtensions(ext))
		require.Error(t, err)
		require.Nil(t, actorSystem)
		ext.AssertExpectations(t)
	})
	t.Run("With Inject when actor system not started", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		// register the actor
		err = sys.Inject(extmocks.NewDependency(t))
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrActorSystemNotStarted)

		err = sys.Stop(ctx)
		require.Error(t, err)
	})
	t.Run("With happy path Inject", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// register the actor
		err = sys.Inject(extmocks.NewDependency(t))
		require.NoError(t, err)

		err = sys.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("With ActorOf failure when it is a reserved name", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		name := "GoAktXYZ"
		addr, pid, err := sys.ActorOf(ctx, name)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrActorNotFound)
		require.Nil(t, addr)
		require.Nil(t, pid)

		err = sys.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("With Kill failure when it is a reserved name", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		name := "GoAktXYZ"
		err = sys.Kill(ctx, name)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrActorNotFound)

		err = sys.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("With ReSpawn failure when it is a reserved name", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		name := "GoAktXYZ"
		pid, err := sys.ReSpawn(ctx, name)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrActorNotFound)
		require.Nil(t, pid)

		err = sys.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("With LocalActor failure when it is a reserved name", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		name := "GoAktXYZ"
		pid, err := sys.LocalActor(name)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrActorNotFound)
		require.Nil(t, pid)

		err = sys.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("With RemoteActor failure when it is a reserved name", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		name := "GoAktXYZ"
		addr, err := sys.RemoteActor(ctx, name)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrActorNotFound)
		require.Nil(t, addr)

		err = sys.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("SpawnOn happy path", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start system cluster
		node1, sd1 := testCluster(t, srv.Addr().String())
		peerAddress1 := node1.PeerAddress()
		require.NotEmpty(t, peerAddress1)
		require.NotNil(t, sd1)

		// create and start system cluster
		node2, sd2 := testCluster(t, srv.Addr().String())
		peerAddress2 := node2.PeerAddress()
		require.NotEmpty(t, peerAddress2)
		require.NotNil(t, sd2)

		// create and start system cluster
		node3, sd3 := testCluster(t, srv.Addr().String())
		peerAddress3 := node3.PeerAddress()
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
		require.ErrorIs(t, err, ErrActorAlreadyExists)

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
	t.Run("SpawnOn with random placement", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start system cluster
		node1, sd1 := testCluster(t, srv.Addr().String())
		peerAddress1 := node1.PeerAddress()
		require.NotEmpty(t, peerAddress1)
		require.NotNil(t, sd1)

		// create and start system cluster
		node2, sd2 := testCluster(t, srv.Addr().String())
		peerAddress2 := node2.PeerAddress()
		require.NotEmpty(t, peerAddress2)
		require.NotNil(t, sd2)

		// create and start system cluster
		node3, sd3 := testCluster(t, srv.Addr().String())
		peerAddress3 := node3.PeerAddress()
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
		require.ErrorIs(t, err, ErrActorAlreadyExists)

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
		node1, sd1 := testCluster(t, srv.Addr().String())
		peerAddress1 := node1.PeerAddress()
		require.NotEmpty(t, peerAddress1)
		require.NotNil(t, sd1)

		// create and start system cluster
		node2, sd2 := testCluster(t, srv.Addr().String())
		peerAddress2 := node2.PeerAddress()
		require.NotEmpty(t, peerAddress2)
		require.NotNil(t, sd2)

		// create and start system cluster
		node3, sd3 := testCluster(t, srv.Addr().String())
		peerAddress3 := node3.PeerAddress()
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
		require.ErrorIs(t, err, ErrActorAlreadyExists)

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
	t.Run("SpawnOn with single node cluster", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start system cluster
		node, sd := testCluster(t, srv.Addr().String())
		peerAddress1 := node.PeerAddress()
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
}
