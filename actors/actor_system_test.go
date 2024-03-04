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

package actors

import (
	"context"
	"net"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/discovery"
	"github.com/tochemey/goakt/goaktpb"
	"github.com/tochemey/goakt/log"
	testkit "github.com/tochemey/goakt/mocks/discovery"
	"github.com/tochemey/goakt/telemetry"
	testpb "github.com/tochemey/goakt/test/data/testpb"
	"github.com/travisjeffery/go-dynaport"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/protobuf/proto"
)

func TestActorSystem(t *testing.T) {
	t.Run("New instance with Defaults", func(t *testing.T) {
		actorSys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSys)
		var iface any = actorSys
		_, ok := iface.(ActorSystem)
		assert.True(t, ok)
		assert.Equal(t, "testSys", actorSys.Name())
		assert.Empty(t, actorSys.Actors())
	})
	t.Run("New instance with Missing Name", func(t *testing.T) {
		sys, err := NewActorSystem("")
		assert.Error(t, err)
		assert.Nil(t, sys)
		assert.EqualError(t, err, ErrNameRequired.Error())
	})
	t.Run("With invalid actor system Name", func(t *testing.T) {
		sys, err := NewActorSystem("$omeN@me")
		assert.Error(t, err)
		assert.Nil(t, sys)
		assert.EqualError(t, err, ErrInvalidActorSystemName.Error())
	})
	t.Run("With Spawn an actor when not System started", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		actor := newTestActor()
		actorRef, err := sys.Spawn(ctx, "Test", actor)
		assert.Error(t, err)
		assert.EqualError(t, err, ErrActorSystemNotStarted.Error())
		assert.Nil(t, actorRef)
	})
	t.Run("With Spawn an actor when started", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actor := newTestActor()
		actorRef, err := sys.Spawn(ctx, "Test", actor)
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// stop the actor after some time
		time.Sleep(time.Second)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With Spawn an actor when started with tracing", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger), WithTracing())

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actor := newTestActor()
		actorRef, err := sys.Spawn(ctx, "Test", actor)
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// stop the actor after some time
		time.Sleep(time.Second)
		actorSystem := sys.(*actorSystem)
		assert.True(t, actorSystem.traceEnabled.Load())
		pid := actorRef.(*pid)
		assert.True(t, pid.traceEnabled.Load())

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With Spawn an actor already exist", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("test", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actor := newTestActor()
		ref1, err := sys.Spawn(ctx, "Test", actor)
		assert.NoError(t, err)
		assert.NotNil(t, ref1)

		ref2, err := sys.Spawn(ctx, "Test", actor)
		assert.NotNil(t, ref2)
		assert.NoError(t, err)

		// point to the same memory address
		assert.True(t, ref1 == ref2)

		// stop the actor after some time
		time.Sleep(time.Second)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With RemoteActor/ActorOf with clustering enabled", func(t *testing.T) {
		ctx := context.TODO()
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		logger := log.New(log.DebugLevel, os.Stdout)

		podName := "pod"
		host := "localhost"

		// set the environments
		t.Setenv("GOSSIP_PORT", strconv.Itoa(gossipPort))
		t.Setenv("CLUSTER_PORT", strconv.Itoa(clusterPort))
		t.Setenv("REMOTING_PORT", strconv.Itoa(remotingPort))
		t.Setenv("NODE_NAME", podName)
		t.Setenv("NODE_IP", host)
		t.Setenv("GRPC_GO_LOG_VERBOSITY_LEVEL", "99")
		t.Setenv("GRPC_GO_LOG_SEVERITY_LEVEL", "info")

		// define discovered addresses
		addrs := []string{
			net.JoinHostPort(host, strconv.Itoa(gossipPort)),
		}

		// mock the discovery provider
		provider := new(testkit.Provider)
		config := discovery.NewConfig()
		sd := discovery.NewServiceDiscovery(provider, config)
		newActorSystem, err := NewActorSystem(
			"test",
			WithPassivationDisabled(),
			WithLogger(logger),
			WithReplyTimeout(time.Minute),
			WithClustering(sd, 9))
		require.NoError(t, err)

		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().SetConfig(config).Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		// start the actor system
		err = newActorSystem.Start(ctx)
		require.NoError(t, err)

		// wait for the cluster to start
		time.Sleep(time.Second)

		// create an actor
		actorName := uuid.NewString()
		actor := newTestActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// wait for a while for replication to take effect
		// otherwise the subsequent test will return actor not found
		time.Sleep(time.Second)

		// get the actor
		addr, pid, err := newActorSystem.ActorOf(ctx, actorName)
		require.NoError(t, err)
		require.NotNil(t, addr)
		require.Nil(t, pid)

		// use RemoteActor method and compare the results
		remoteAddr, err := newActorSystem.RemoteActor(ctx, actorName)
		require.NoError(t, err)
		require.NotNil(t, remoteAddr)
		require.True(t, proto.Equal(remoteAddr, addr))

		reply, err := RemoteAsk(ctx, addr, new(testpb.TestReply))
		require.NoError(t, err)
		require.NotNil(t, reply)

		// get the actor partition
		partition := newActorSystem.GetPartition(actorName)
		assert.GreaterOrEqual(t, partition, uint64(0))

		// assert actor not found
		actorName = "some-actor"
		addr, pid, err = newActorSystem.ActorOf(ctx, actorName)
		require.Error(t, err)
		require.EqualError(t, err, ErrActorNotFound(actorName).Error())
		require.Nil(t, addr)

		remoteAddr, err = newActorSystem.RemoteActor(ctx, actorName)
		require.Error(t, err)
		require.EqualError(t, err, ErrActorNotFound(actorName).Error())
		require.Nil(t, remoteAddr)

		// stop the actor after some time
		time.Sleep(time.Second)

		t.Cleanup(func() {
			err = newActorSystem.Stop(ctx)
			assert.NoError(t, err)
			provider.AssertExpectations(t)
		})
	})
	t.Run("With remoting enabled", func(t *testing.T) {
		ctx := context.TODO()
		remotingPort := dynaport.Get(1)[0]

		logger := log.New(log.DebugLevel, os.Stdout)
		host := "localhost"

		newActorSystem, err := NewActorSystem(
			"test",
			WithPassivationDisabled(),
			WithLogger(logger),
			WithReplyTimeout(time.Minute),
			WithRemoting(host, int32(remotingPort)))
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		require.NoError(t, err)

		// wait for the cluster to fully start
		time.Sleep(time.Second)

		// create an actor
		actorName := uuid.NewString()
		actor := newTestActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		path := NewPath(actorName, &Address{
			host:     host,
			port:     remotingPort,
			system:   newActorSystem.Name(),
			protocol: protocol,
		})
		addr := path.RemoteAddress()

		reply, err := RemoteAsk(ctx, addr, new(testpb.TestReply))
		require.NoError(t, err)
		require.NotNil(t, reply)

		actual := new(testpb.Reply)
		require.NoError(t, reply.UnmarshalTo(actual))

		expected := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expected, actual))

		addr, pid, err := newActorSystem.ActorOf(ctx, actorName)
		require.Error(t, err)
		require.EqualError(t, err, ErrMethodCallNotAllowed.Error())
		require.Nil(t, addr)
		require.Nil(t, pid)

		// stop the actor after some time
		time.Sleep(time.Second)
		err = newActorSystem.Kill(ctx, actorName)
		require.NoError(t, err)

		t.Cleanup(func() {
			err = newActorSystem.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With ActorOf:remoting not enabled", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actorName := "testActor"
		actor := newTestActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		addr, pid, err := sys.ActorOf(ctx, actorName)
		require.NoError(t, err)
		require.NotNil(t, pid)
		require.Nil(t, addr)

		// stop the actor after some time
		time.Sleep(time.Second)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With ActorOf: not found", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actorName := "notFound"
		addr, pid, err := sys.ActorOf(ctx, actorName)
		require.Error(t, err)
		require.EqualError(t, err, ErrActorNotFound(actorName).Error())
		require.Nil(t, pid)
		require.Nil(t, addr)

		// stop the actor after some time
		time.Sleep(time.Second)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With ActorOf actor system started", func(t *testing.T) {
		ctx := context.TODO()
		remotingPort := dynaport.Get(1)[0]

		logger := log.New(log.DebugLevel, os.Stdout)
		host := "localhost"

		newActorSystem, err := NewActorSystem(
			"test",
			WithPassivationDisabled(),
			WithLogger(logger),
			WithReplyTimeout(time.Minute),
			WithRemoting(host, int32(remotingPort)))
		require.NoError(t, err)

		// create an actor
		actorName := uuid.NewString()

		addr, pid, err := newActorSystem.ActorOf(ctx, actorName)
		require.Error(t, err)
		require.EqualError(t, err, ErrActorSystemNotStarted.Error())
		require.Nil(t, addr)
		require.Nil(t, pid)
	})
	t.Run("With ReSpawn", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		// create a deadletter subscriber
		consumer, err := sys.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		actorName := "exchanger"
		actorRef, err := sys.Spawn(ctx, actorName, &exchanger{})
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// send a message to the actor
		reply, err := Ask(ctx, actorRef, new(testpb.TestReply), receivingTimeout)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expected := new(testpb.Reply)
		require.True(t, proto.Equal(expected, reply))
		require.True(t, actorRef.IsRunning())

		// wait for a while for the system to stop
		time.Sleep(time.Second)
		// restart the actor
		_, err = sys.ReSpawn(ctx, actorName)
		require.NoError(t, err)

		// wait for the actor to complete start
		// TODO we can add a callback for complete start
		time.Sleep(time.Second)
		require.True(t, actorRef.IsRunning())

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

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With ReSpawn with PreStart failure", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys",
			WithLogger(log.DiscardLogger),
			WithExpireActorAfter(time.Minute))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actorName := "actor"
		actorRef, err := sys.Spawn(ctx, actorName, newTestRestart())
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		require.True(t, actorRef.IsRunning())

		// wait for a while for the system to stop
		time.Sleep(time.Second)
		// restart the actor
		pid, err := sys.ReSpawn(ctx, actorName)
		require.Error(t, err)
		require.Nil(t, pid)

		require.False(t, actorRef.IsRunning())

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
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
		reply, err := Ask(ctx, actorRef, new(testpb.TestReply), receivingTimeout)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expected := new(testpb.Reply)
		require.True(t, proto.Equal(expected, reply))
		require.True(t, actorRef.IsRunning())
		// stop the actor after some time
		time.Sleep(time.Second)

		err = sys.Kill(ctx, actorName)
		require.NoError(t, err)

		// wait for a while for the system to stop
		time.Sleep(time.Second)
		// restart the actor
		_, err = sys.ReSpawn(ctx, actorName)
		require.Error(t, err)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With ReSpawn an actor when not System started", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		_, err := sys.ReSpawn(ctx, "some-actor")
		assert.Error(t, err)
		assert.EqualError(t, err, "actor system has not started yet")
	})
	t.Run("ReSpawn with remoting enabled", func(t *testing.T) {
		ctx := context.TODO()
		remotingPort := dynaport.Get(1)[0]

		logger := log.New(log.DebugLevel, os.Stdout)
		host := "localhost"

		newActorSystem, err := NewActorSystem(
			"test",
			WithPassivationDisabled(),
			WithLogger(logger),
			WithReplyTimeout(time.Minute),
			WithRemoting(host, int32(remotingPort)))
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		require.NoError(t, err)

		actorName := "exchanger"
		actorRef, err := newActorSystem.Spawn(ctx, actorName, &exchanger{})
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// send a message to the actor
		reply, err := Ask(ctx, actorRef, new(testpb.TestReply), receivingTimeout)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expected := new(testpb.Reply)
		require.True(t, proto.Equal(expected, reply))
		require.True(t, actorRef.IsRunning())
		// stop the actor after some time
		time.Sleep(time.Second)

		// restart the actor
		_, err = newActorSystem.ReSpawn(ctx, actorName)
		require.NoError(t, err)

		// wait for the actor to complete start
		// TODO we can add a callback for complete start
		time.Sleep(time.Second)
		require.True(t, actorRef.IsRunning())

		t.Cleanup(func() {
			err = newActorSystem.Stop(ctx)
			assert.NoError(t, err)
		})
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
		time.Sleep(time.Second)

		assert.EqualValues(t, 1, sys.NumActors())

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With remoting enabled: Actor not found", func(t *testing.T) {
		ctx := context.TODO()
		remotingPort := dynaport.Get(1)[0]

		logger := log.New(log.DebugLevel, os.Stdout)
		host := "localhost"

		newActorSystem, err := NewActorSystem(
			"test",
			WithPassivationDisabled(),
			WithLogger(logger),
			WithReplyTimeout(time.Minute),
			WithRemoting(host, int32(remotingPort)))
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		require.NoError(t, err)

		// wait for the cluster to fully start
		time.Sleep(time.Second)

		actorName := "some-actor"
		addr, err := RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)
		require.Nil(t, addr)

		// attempt to send a message will fail
		reply, err := RemoteAsk(ctx, &goaktpb.Address{
			Host: host,
			Port: int32(remotingPort),
			Name: actorName,
			Id:   "",
		}, new(testpb.TestReply))
		require.Error(t, err)
		require.Nil(t, reply)

		// stop the actor after some time
		time.Sleep(time.Second)

		t.Cleanup(func() {
			err = newActorSystem.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With RemoteActor failure when system not started", func(t *testing.T) {
		ctx := context.TODO()
		remotingPort := dynaport.Get(1)[0]

		logger := log.New(log.DebugLevel, os.Stdout)
		host := "localhost"

		newActorSystem, err := NewActorSystem(
			"test",
			WithPassivationDisabled(),
			WithLogger(logger),
			WithReplyTimeout(time.Minute),
			WithRemoting(host, int32(remotingPort)))
		require.NoError(t, err)

		actorName := "some-actor"
		remoteAddr, err := newActorSystem.RemoteActor(ctx, actorName)
		require.Error(t, err)
		require.EqualError(t, err, ErrActorSystemNotStarted.Error())
		require.Nil(t, remoteAddr)
	})
	t.Run("With RemoteActor failure when system not started", func(t *testing.T) {
		ctx := context.TODO()
		remotingPort := dynaport.Get(1)[0]

		logger := log.New(log.DebugLevel, os.Stdout)
		host := "localhost"

		newActorSystem, err := NewActorSystem(
			"test",
			WithPassivationDisabled(),
			WithLogger(logger),
			WithReplyTimeout(time.Minute),
			WithRemoting(host, int32(remotingPort)))
		require.NoError(t, err)

		err = newActorSystem.Stop(ctx)
		require.Error(t, err)
		require.EqualError(t, err, ErrActorSystemNotStarted.Error())
	})
	t.Run("With RemoteActor failure when cluster is not enabled", func(t *testing.T) {
		ctx := context.TODO()
		remotingPort := dynaport.Get(1)[0]

		logger := log.New(log.DebugLevel, os.Stdout)
		host := "localhost"

		newActorSystem, err := NewActorSystem(
			"test",
			WithPassivationDisabled(),
			WithLogger(logger),
			WithReplyTimeout(time.Minute),
			WithRemoting(host, int32(remotingPort)))
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		require.NoError(t, err)

		// wait for the system to properly start
		actorName := "some-actor"
		remoteAddr, err := newActorSystem.RemoteActor(ctx, actorName)
		require.Error(t, err)
		require.EqualError(t, err, ErrClusterDisabled.Error())
		require.Nil(t, remoteAddr)

		// stop the actor after some time
		time.Sleep(time.Second)

		t.Cleanup(func() {
			err = newActorSystem.Stop(ctx)
			assert.NoError(t, err)
		})
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

		require.Equal(t, ref.ActorPath().String(), local.ActorPath().String())

		// stop the actor after some time
		time.Sleep(time.Second)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
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
		require.EqualError(t, err, ErrActorNotFound("some-name").Error())

		// stop the actor after some time
		time.Sleep(time.Second)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With LocalActor when system not started", func(t *testing.T) {
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// create an actor
		actorName := "exchanger"

		// locate the actor
		local, err := sys.LocalActor(actorName)
		require.Error(t, err)
		require.EqualError(t, err, ErrActorSystemNotStarted.Error())
		require.Nil(t, local)
	})
	t.Run("With Kill an actor when not System started", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Kill(ctx, "Test")
		assert.Error(t, err)
		assert.EqualError(t, err, "actor system has not started yet")
	})
	t.Run("With Kill an actor when actor not found", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)
		err = sys.Kill(ctx, "Test")
		assert.Error(t, err)
		assert.EqualError(t, err, "actor=goakt://testSys@/Test not found")
		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With housekeeping", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("housekeeperSys",
			WithLogger(log.DefaultLogger),
			WithExpireActorAfter(passivateAfter))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		// wait for the system to properly start
		time.Sleep(time.Second)

		actorName := "HousekeeperActor"
		actorHandler := newTestActor()
		actorRef, err := sys.Spawn(ctx, actorName, actorHandler)
		assert.NoError(t, err)
		require.NotNil(t, actorRef)

		// wait for the actor to properly start
		time.Sleep(time.Second)

		// locate the actor
		ref, err := sys.LocalActor(actorName)
		require.Error(t, err)
		require.Nil(t, ref)
		require.EqualError(t, err, ErrActorNotFound(actorName).Error())

		// stop the actor after some time
		time.Sleep(time.Second)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With GetPartition returning zero in non cluster env", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("housekeeperSys",
			WithLogger(log.DefaultLogger),
			WithExpireActorAfter(passivateAfter))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		// wait for the system to properly start
		time.Sleep(time.Second)

		partition := sys.GetPartition("some-actor")
		assert.Zero(t, partition)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With error Stop", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actor := &testPostStop{}
		actorRef, err := sys.Spawn(ctx, "Test", actor)
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// stop the actor after some time
		time.Sleep(time.Second)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.Error(t, err)
		})
	})
	t.Run("With deadletters subscription ", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		// wait for complete start
		time.Sleep(time.Second)

		// create a deadletter subscriber
		consumer, err := sys.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		// create the black hole actor
		actor := &discarder{}
		actorRef, err := sys.Spawn(ctx, "discarder ", actor)
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// wait a while
		time.Sleep(time.Second)

		// every message sent to the actor will result in deadletters
		for i := 0; i < 5; i++ {
			require.NoError(t, Tell(ctx, actorRef, new(testpb.TestSend)))
		}

		time.Sleep(time.Second)

		var items []*goaktpb.Deadletter
		for message := range consumer.Iterator() {
			payload := message.Payload()
			// only listening to deadletters
			deadletter, ok := payload.(*goaktpb.Deadletter)
			if ok {
				items = append(items, deadletter)
			}
		}

		require.Len(t, items, 5)

		// unsubscribe the consumer
		err = sys.Unsubscribe(consumer)
		require.NoError(t, err)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With deadletters subscription when not started", func(t *testing.T) {
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// create a deadletter subscriber
		consumer, err := sys.Subscribe()
		require.Error(t, err)
		require.Nil(t, consumer)
	})
	t.Run("With deadletters unsubscription when not started", func(t *testing.T) {
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

		time.Sleep(time.Second)

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

		logger := log.New(log.DebugLevel, os.Stdout)

		podName := "pod"
		host := "localhost"

		// set the environments
		t.Setenv("GOSSIP_PORT", strconv.Itoa(gossipPort))
		t.Setenv("CLUSTER_PORT", strconv.Itoa(clusterPort))
		t.Setenv("REMOTING_PORT", strconv.Itoa(remotingPort))
		t.Setenv("NODE_NAME", podName)
		t.Setenv("NODE_IP", host)
		t.Setenv("GRPC_GO_LOG_VERBOSITY_LEVEL", "99")
		t.Setenv("GRPC_GO_LOG_SEVERITY_LEVEL", "info")

		// define discovered addresses
		addrs := []string{
			net.JoinHostPort(host, strconv.Itoa(gossipPort)),
		}

		// mock the discovery provider
		provider := new(testkit.Provider)
		config := discovery.NewConfig()
		sd := discovery.NewServiceDiscovery(provider, config)
		newActorSystem, err := NewActorSystem(
			"test",
			WithExpireActorAfter(passivateAfter),
			WithLogger(logger),
			WithReplyTimeout(time.Minute),
			WithClustering(sd, 9))
		require.NoError(t, err)

		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().SetConfig(config).Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		// start the actor system
		err = newActorSystem.Start(ctx)
		require.NoError(t, err)

		// wait for the cluster to start
		time.Sleep(time.Second)

		// create an actor
		actorName := uuid.NewString()
		actor := newTestActor()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// wait for a while for replication to take effect
		// otherwise the subsequent test will return actor not found
		time.Sleep(time.Second)

		// get the actor
		addr, pid, err := newActorSystem.ActorOf(ctx, actorName)
		require.Error(t, err)
		require.EqualError(t, err, ErrActorNotFound(actorName).Error())
		require.Nil(t, addr)
		require.Nil(t, pid)

		// use RemoteActor method and compare the results
		remoteAddr, err := newActorSystem.RemoteActor(ctx, actorName)
		require.Error(t, err)
		require.EqualError(t, err, ErrActorNotFound(actorName).Error())
		require.Nil(t, remoteAddr)

		// stop the actor after some time
		time.Sleep(time.Second)

		t.Cleanup(func() {
			err = newActorSystem.Stop(ctx)
			assert.NoError(t, err)
			provider.AssertExpectations(t)
		})
	})
	t.Run("With Metric enabled", func(t *testing.T) {
		r := sdkmetric.NewManualReader()
		mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(r))
		// create an instance of telemetry
		tel := telemetry.New(telemetry.WithMeterProvider(mp))

		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys",
			WithMetric(),
			WithTelemetry(tel),
			WithStash(5),
			WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actor := newTestActor()
		actorRef, err := sys.Spawn(ctx, "Test", actor)
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// create a message to send to the test actor
		message := new(testpb.TestSend)
		// send the message to the actor
		err = Tell(ctx, actorRef, message)
		// perform some assertions
		require.NoError(t, err)

		// Should collect 4 metrics, 3 for the actor and 1 for the actor system
		got := &metricdata.ResourceMetrics{}
		err = r.Collect(ctx, got)
		require.NoError(t, err)
		assert.Len(t, got.ScopeMetrics, 1)
		assert.Len(t, got.ScopeMetrics[0].Metrics, 4)

		expected := []string{
			"actor_child_count",
			"actor_stash_count",
			"actor_restart_count",
			"actors_count",
		}
		// sort the array
		sort.Strings(expected)
		// get the metric names
		actual := make([]string, len(got.ScopeMetrics[0].Metrics))
		for i, metric := range got.ScopeMetrics[0].Metrics {
			actual[i] = metric.Name
		}
		sort.Strings(actual)

		assert.ElementsMatch(t, expected, actual)

		// stop the actor after some time
		time.Sleep(time.Second)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With cluster events subscription", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start system cluster
		cl1, sd1 := startClusterSystem(t, "Node1", srv.Addr().String())
		peerAddress1 := cl1.PeerAddress()
		require.NotEmpty(t, peerAddress1)

		// create a subscriber to node 1
		subscriber1, err := cl1.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, subscriber1)

		// create and start system cluster
		cl2, sd2 := startClusterSystem(t, "Node2", srv.Addr().String())
		peerAddress2 := cl2.PeerAddress()
		require.NotEmpty(t, peerAddress2)

		// create a subscriber to node 2
		subscriber2, err := cl2.Subscribe()
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
		require.NoError(t, cl1.Unsubscribe(subscriber1))
		assert.NoError(t, cl1.Stop(ctx))
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

		require.NoError(t, cl2.Unsubscribe(subscriber2))

		t.Cleanup(func() {
			assert.NoError(t, cl2.Stop(ctx))
			// stop the discovery engines
			assert.NoError(t, sd2.Close())
			// shutdown the nats server gracefully
			srv.Shutdown()
		})
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
	t.Run("With Actor encoding failure with clustering enabled", func(t *testing.T) {
		ctx := context.TODO()
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		logger := log.DiscardLogger

		podName := "pod"
		host := "localhost"

		// set the environments
		t.Setenv("GOSSIP_PORT", strconv.Itoa(gossipPort))
		t.Setenv("CLUSTER_PORT", strconv.Itoa(clusterPort))
		t.Setenv("REMOTING_PORT", strconv.Itoa(remotingPort))
		t.Setenv("NODE_NAME", podName)
		t.Setenv("NODE_IP", host)
		t.Setenv("GRPC_GO_LOG_VERBOSITY_LEVEL", "99")
		t.Setenv("GRPC_GO_LOG_SEVERITY_LEVEL", "info")

		// define discovered addresses
		addrs := []string{
			net.JoinHostPort(host, strconv.Itoa(gossipPort)),
		}

		// mock the discovery provider
		provider := new(testkit.Provider)
		config := discovery.NewConfig()
		sd := discovery.NewServiceDiscovery(provider, config)
		newActorSystem, err := NewActorSystem(
			"test",
			WithExpireActorAfter(passivateAfter),
			WithLogger(logger),
			WithReplyTimeout(time.Minute),
			WithClustering(sd, 9))
		require.NoError(t, err)

		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().SetConfig(config).Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		// start the actor system
		err = newActorSystem.Start(ctx)
		require.NoError(t, err)

		// wait for the cluster to start
		time.Sleep(time.Second)

		// create an actor
		actorName := uuid.NewString()
		actor := new(faultyActor)
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		require.Error(t, err)
		assert.Nil(t, actorRef)

		t.Cleanup(func() {
			err = newActorSystem.Stop(ctx)
			assert.NoError(t, err)
			provider.AssertExpectations(t)
		})
	})
}
