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
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/kapetan-io/tackle/autotls"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"go.opentelemetry.io/otel"
	noopmetric "go.opentelemetry.io/otel/metric/noop"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/tochemey/goakt/v3/address"
	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/extension"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/collection"
	internalpb "github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/metric"
	"github.com/tochemey/goakt/v3/internal/pause"
	"github.com/tochemey/goakt/v3/internal/registry"
	"github.com/tochemey/goakt/v3/log"
	mockcluster "github.com/tochemey/goakt/v3/mocks/cluster"
	mocksdiscovery "github.com/tochemey/goakt/v3/mocks/discovery"
	mocksextension "github.com/tochemey/goakt/v3/mocks/extension"
	"github.com/tochemey/goakt/v3/passivation"
	"github.com/tochemey/goakt/v3/remote"
	"github.com/tochemey/goakt/v3/test/data/testpb"
	gtls "github.com/tochemey/goakt/v3/tls"
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
	t.Run("New instance with Defaults and TLS", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// AutoGenerate TLS certs
		conf := autotls.Config{
			AutoTLS:            true,
			ClientAuth:         tls.RequireAndVerifyClientCert,
			InsecureSkipVerify: false,
		}
		require.NoError(t, autotls.Setup(&conf))

		serverConfig := conf.ServerTLS
		clientConfig := conf.ClientTLS
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithTLS(&gtls.Info{
				ClientConfig: clientConfig,
				ServerConfig: serverConfig,
			}),
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)
		require.NotNil(t, sys)
		require.NoError(t, sys.Start(ctx))
		pause.For(time.Second)
		require.NoError(t, sys.Stop(ctx))
	})
	t.Run("When already started", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)
		require.NotNil(t, sys)
		require.NoError(t, sys.Start(ctx))

		err = sys.Start(ctx)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorSystemAlreadyStarted)

		pause.For(500 * time.Millisecond)
		require.NoError(t, sys.Stop(ctx))
	})
	t.Run("When metrics instruments cannot be created", func(t *testing.T) {
		ctx := context.TODO()
		sys, err := NewActorSystem(
			"testSys",
			WithLogger(log.DiscardLogger),
		)
		require.NoError(t, err)

		prevProvider := otel.GetMeterProvider()
		t.Cleanup(func() { otel.SetMeterProvider(prevProvider) })

		errInstrument := assert.AnError
		baseProvider := noopmetric.NewMeterProvider()
		otel.SetMeterProvider(&MockMeterProvider{
			MeterProvider: baseProvider,
			meter: instrumentFailingMeter{
				Meter: baseProvider.Meter("test"),
				failures: map[string]error{
					"actorsystem.deadletters.count": errInstrument,
				},
			},
		})

		sysImpl := sys.(*actorSystem)
		sysImpl.metricProvider = metric.NewProvider()

		err = sys.Start(ctx)
		require.Error(t, err)
		require.ErrorIs(t, err, errInstrument)
	})
	t.Run("When metrics callback registration fails", func(t *testing.T) {
		ctx := context.TODO()
		sys, err := NewActorSystem(
			"testSys",
			WithLogger(log.DiscardLogger),
		)
		require.NoError(t, err)

		prevProvider := otel.GetMeterProvider()
		t.Cleanup(func() { otel.SetMeterProvider(prevProvider) })

		errRegister := assert.AnError
		baseProvider := noopmetric.NewMeterProvider()
		otel.SetMeterProvider(&MockMeterProvider{
			MeterProvider: baseProvider,
			meter: registerCallbackFailingMeter{
				Meter: baseProvider.Meter("test"),
				err:   errRegister,
			},
		})

		sysImpl := sys.(*actorSystem)
		sysImpl.metricProvider = metric.NewProvider()

		err = sys.Start(ctx)
		require.Error(t, err)
		require.False(t, sys.Running())
	})
	t.Run("When metrics enabled without cluster", func(t *testing.T) {
		ctx := context.TODO()

		sys, err := NewActorSystem(
			"testSys",
			WithLogger(log.DiscardLogger),
			WithMetrics(),
		)
		require.NoError(t, err)

		require.NoError(t, sys.Start(ctx))
		require.NoError(t, sys.Stop(ctx))
	})

	t.Run("New instance with Missing Name", func(t *testing.T) {
		sys, err := NewActorSystem("")
		assert.Error(t, err)
		assert.Nil(t, sys)
		require.ErrorIs(t, err, gerrors.ErrNameRequired)
	})
	t.Run("With invalid actor system Name", func(t *testing.T) {
		sys, err := NewActorSystem("$omeN@me")
		assert.Error(t, err)
		assert.Nil(t, sys)
		require.ErrorIs(t, err, gerrors.ErrInvalidActorSystemName)
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
		provider := new(mocksdiscovery.Provider)
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

		require.NotZero(t, newActorSystem.PeersPort())
		assert.EqualValues(t, clusterPort, newActorSystem.PeersPort())
		require.NotZero(t, newActorSystem.DiscoveryPort())
		assert.EqualValues(t, gossipPort, newActorSystem.DiscoveryPort())

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

		remoting := remote.NewRemoting()
		from := address.NoSender()
		reply, err := remoting.RemoteAsk(ctx, from, addr, new(testpb.TestReply), 20*time.Second)
		require.NoError(t, err)
		require.NotNil(t, reply)

		// assert actor not found
		actorName = "some-actor"
		exists, err = newActorSystem.ActorExists(ctx, actorName)
		require.NoError(t, err)
		require.False(t, exists)

		addr, pid, err := newActorSystem.ActorOf(ctx, actorName)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorNotFound)
		require.Nil(t, addr)
		require.Nil(t, pid)

		remoteAddr, err = newActorSystem.RemoteActor(ctx, actorName)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorNotFound)
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
		require.ErrorIs(t, err, gerrors.ErrMethodCallNotAllowed)
		require.Nil(t, addr)
		require.Nil(t, pid)

		err = newActorSystem.Stop(ctx)
		require.NoError(t, err)
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

		err = sys.Stop(ctx)
		assert.NoError(t, err)
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
		require.ErrorIs(t, err, gerrors.ErrActorNotFound)
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

	t.Run("ActorOf returns error when cluster lookup fails", func(t *testing.T) {
		ctx := context.TODO()
		actorName := uuid.NewString()

		clusterMock := new(mockcluster.Cluster)
		system := MockReplicationTestSystem(clusterMock)

		system.locker.Lock()
		system.actors = newTree()
		system.locker.Unlock()

		clusterMock.EXPECT().GetActor(mock.Anything, actorName).Return(nil, assert.AnError)
		t.Cleanup(func() { clusterMock.AssertExpectations(t) })

		addr, pid, err := system.ActorOf(ctx, actorName)
		require.Error(t, err)
		assert.ErrorIs(t, err, assert.AnError)
		assert.ErrorContains(t, err, "failed to fetch remote actor")
		require.Nil(t, addr)
		require.Nil(t, pid)
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
		require.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
		require.Nil(t, addr)
		require.Nil(t, pid)
	})
	t.Run("ActorOf returns error when actor is stopping", func(t *testing.T) {
		ctx := context.TODO()
		actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := actorSystem.Start(ctx)
		assert.NoError(t, err)

		actorName := "actorQA"
		actor := NewMockActor()
		actorRef, err := actorSystem.Spawn(ctx, actorName, actor)
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// initiate stopping of the actor
		actorRef.toggleFlag(stoppingFlag, true)

		addr, pid, err := actorSystem.ActorOf(ctx, actorName)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorNotFound)
		require.Nil(t, pid)
		require.Nil(t, addr)

		// reset the stopping flag for cleanup
		actorRef.toggleFlag(stoppingFlag, false)

		// stop the actor after some time
		pause.For(time.Second)

		err = actorSystem.Stop(ctx)
		assert.NoError(t, err)
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
		assert.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
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

		remoting := remote.NewRemoting()
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
		require.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
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
		require.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
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
		require.ErrorIs(t, err, gerrors.ErrClusterDisabled)
		require.Nil(t, remoteAddr)

		// stop the actor after some time
		pause.For(time.Second)

		err = newActorSystem.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("With RemoteActor failure when cluster lookup fails", func(t *testing.T) {
		ctx := context.TODO()
		actorName := uuid.NewString()

		clusterMock := new(mockcluster.Cluster)
		system := MockReplicationTestSystem(clusterMock)

		clusterMock.EXPECT().GetActor(mock.Anything, actorName).Return(nil, assert.AnError)
		t.Cleanup(func() { clusterMock.AssertExpectations(t) })

		addr, err := system.RemoteActor(ctx, actorName)
		require.Error(t, err)
		assert.ErrorIs(t, err, assert.AnError)
		assert.ErrorContains(t, err, "failed to fetch remote actor")
		require.Nil(t, addr)
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
		require.ErrorIs(t, err, gerrors.ErrActorNotFound)

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
		require.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
		require.Nil(t, local)
	})
	t.Run("With Kill an actor when not System started", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Kill(ctx, "Test")
		assert.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
	})
	t.Run("With Kill an actor when actor not found", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)
		err = sys.Kill(ctx, "Test")
		assert.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorNotFound)
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
		require.ErrorIs(t, err, gerrors.ErrActorNotFound)

		// stop the actor after some time
		pause.For(time.Second)

		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("With PeersPort and DiscoveryPort returning zero in non cluster env", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem(
			"test",
			WithLogger(log.DiscardLogger),
		)

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		// wait for the system to properly start
		pause.For(time.Second)

		port := sys.PeersPort()
		assert.Zero(t, port)
		port = sys.DiscoveryPort()
		assert.Zero(t, port)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With GetPartition returning zero in non cluster env", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem(
			"test",
			WithLogger(log.DiscardLogger),
		)

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		// wait for the system to properly start
		pause.For(time.Second)

		partition := sys.GetPartition("some-actor")
		assert.Zero(t, partition)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With GetPartition when cluster mode is enabled", func(t *testing.T) {
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

		pid, err := node1.Spawn(ctx, "actor11", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
		pause.For(time.Second)
		part1 := node1.GetPartition("actor11")

		pid, err = node2.Spawn(ctx, "actor21", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
		pause.For(time.Second)
		part2 := node2.GetPartition("actor21")

		pid, err = node3.Spawn(ctx, "actor31", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
		pause.For(time.Second)
		part3 := node3.GetPartition("actor31")

		// get the partition of the actor actor11
		partition := node2.GetPartition("actor11")
		require.Exactly(t, part1, partition)

		// get the partition of the actor21
		partition = node3.GetPartition("actor21")
		require.Exactly(t, part2, partition)

		// get the partition of the actor31
		partition = node1.GetPartition("actor31")
		require.Exactly(t, part3, partition)

		assert.NoError(t, node1.Stop(ctx))
		assert.NoError(t, node3.Stop(ctx))
		assert.NoError(t, sd1.Close())
		assert.NoError(t, sd3.Close())
		srv.Shutdown()
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
		for range 5 {
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
		provider := new(mocksdiscovery.Provider)
		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

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
		require.ErrorIs(t, err, gerrors.ErrActorNotFound)
		require.Nil(t, addr)
		require.Nil(t, pid)

		// use RemoteActor method and compare the results
		remoteAddr, err := newActorSystem.RemoteActor(ctx, actorName)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorNotFound)
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
		cl1, sd1 := testNATs(t, srv.Addr().String())
		peerAddress1 := cl1.PeersAddress()
		require.NotEmpty(t, peerAddress1)

		// create a subscriber to node 1
		subscriber1, err := cl1.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, subscriber1)

		// create and start system cluster
		cl2, sd2 := testNATs(t, srv.Addr().String())
		peerAddress2 := cl2.PeersAddress()
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
		require.Empty(t, sys.PeersAddress())

		require.NoError(t, sys.Stop(ctx))
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
		assert.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)

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
		assert.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)

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
		provider := new(mocksdiscovery.Provider)
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

		remoting := remote.NewRemoting()
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
		node1, sd1 := testNATs(t, srv.Addr().String())
		peerAddress1 := node1.PeersAddress()
		require.NotEmpty(t, peerAddress1)

		// create and start system cluster
		node2, sd2 := testNATs(t, srv.Addr().String())
		peerAddress2 := node2.PeersAddress()
		require.NotEmpty(t, peerAddress2)

		// create and start system cluster
		node3, sd3 := testNATs(t, srv.Addr().String())
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
		require.ErrorIs(t, err, gerrors.ErrActorAlreadyExists)

		actors, err := node3.ActorRefs(ctx, time.Second)
		require.NoError(t, err)
		require.Len(t, actors, 1)

		actors, err = node1.ActorRefs(ctx, time.Second)
		require.NoError(t, err)
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
	t.Run("ActorRefs returns error when cluster scan fails", func(t *testing.T) {
		ctx := context.TODO()
		clusterMock := new(mockcluster.Cluster)
		system := MockReplicationTestSystem(clusterMock)

		system.locker.Lock()
		system.actors = newTree()
		system.locker.Unlock()

		clusterMock.EXPECT().Actors(mock.Anything, mock.Anything).Return(nil, assert.AnError)
		t.Cleanup(func() { clusterMock.AssertExpectations(t) })

		actorRefs, err := system.ActorRefs(ctx, time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, assert.AnError)
		require.Nil(t, actorRefs)
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
		provider := new(mocksdiscovery.Provider)
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
			WithTLS(&gtls.Info{
				ClientConfig: &tls.Config{InsecureSkipVerify: true}, // nolint
				ServerConfig: nil,
			}),
		)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrInvalidTLSConfiguration)
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
		ext := mocksextension.NewExtension(t)
		ext.EXPECT().ID().Return(strings.Repeat("a", 300))
		actorSystem, err := NewActorSystem("testSys", WithExtensions(ext))
		require.Error(t, err)
		require.Nil(t, actorSystem)
	})
	t.Run("With invalid Extension ID", func(t *testing.T) {
		ext := mocksextension.NewExtension(t)
		ext.EXPECT().ID().Return("$omeN@me")
		actorSystem, err := NewActorSystem("testSys", WithExtensions(ext))
		require.Error(t, err)
		require.Nil(t, actorSystem)
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
		err = sys.Inject(mocksextension.NewDependency(t))
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)

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
		err = sys.Inject(mocksextension.NewDependency(t))
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
		require.ErrorIs(t, err, gerrors.ErrActorNotFound)
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
		require.ErrorIs(t, err, gerrors.ErrActorNotFound)

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
		require.ErrorIs(t, err, gerrors.ErrActorNotFound)
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
		require.ErrorIs(t, err, gerrors.ErrActorNotFound)
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
		require.ErrorIs(t, err, gerrors.ErrActorNotFound)
		require.Nil(t, addr)

		err = sys.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("With LRU eviction policy with threshold kept rather than percentage", func(t *testing.T) {
		ctx := context.TODO()
		strategy, _ := NewEvictionStrategy(7, LRU, 10)
		actorSystem, _ := NewActorSystem("testSys",
			WithLogger(log.DiscardLogger),
			WithEvictionStrategy(strategy, time.Second))

		require.NoError(t, actorSystem.Start(ctx))

		// this is to make sure the actor system is started properly in the test
		pause.For(time.Second)

		// let us create some actors
		for i := range 10 {
			actorName := fmt.Sprintf("actor-%d", i)
			_, err := actorSystem.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
			require.NoError(t, err)
		}

		// let us send a message to the last actor
		actorName := "actor-3"
		pid, err := actorSystem.LocalActor(actorName)
		require.NoError(t, err)
		require.NotNil(t, pid)
		err = Tell(ctx, pid, new(testpb.TestSend))
		require.NoError(t, err)

		pause.For(2 * time.Second)

		require.Exactly(t, uint64(7), actorSystem.NumActors())
		require.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With LRU eviction policy with percentage based eviction", func(t *testing.T) {
		ctx := context.TODO()
		strategy, _ := NewEvictionStrategy(7, LRU, 50)
		actorSystem, _ := NewActorSystem("testSys",
			WithLogger(log.DiscardLogger),
			WithEvictionStrategy(strategy, time.Second))

		require.NoError(t, actorSystem.Start(ctx))

		// this is to make sure the actor system is started properly in the test
		pause.For(time.Second)

		// let us create some actors
		for i := range 10 {
			actorName := fmt.Sprintf("actor-%d", i)
			_, err := actorSystem.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
			require.NoError(t, err)
		}

		// let us send a message to the last actor
		actorName := "actor-3"
		pid, err := actorSystem.LocalActor(actorName)
		require.NoError(t, err)
		require.NotNil(t, pid)
		err = Tell(ctx, pid, new(testpb.TestSend))
		require.NoError(t, err)

		pause.For(2 * time.Second)

		require.Exactly(t, uint64(5), actorSystem.NumActors())
		require.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With LFU eviction policy with threshold met", func(t *testing.T) {
		ctx := context.TODO()
		strategy, _ := NewEvictionStrategy(7, LFU, 10)
		actorSystem, _ := NewActorSystem("testSys",
			WithLogger(log.DiscardLogger),
			WithEvictionStrategy(strategy, time.Second))

		require.NoError(t, actorSystem.Start(ctx))

		// this is to make sure the actor system is started properly in the test
		pause.For(time.Second)

		// let us create some actors
		for i := range 10 {
			actorName := fmt.Sprintf("actor-%d", i)
			_, err := actorSystem.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
			require.NoError(t, err)
		}

		// let us send a message to the last actor
		actorName := "actor-3"
		pid, err := actorSystem.LocalActor(actorName)
		require.NoError(t, err)
		require.NotNil(t, pid)
		err = Tell(ctx, pid, new(testpb.TestSend))
		require.NoError(t, err)

		pause.For(2 * time.Second)

		require.Exactly(t, uint64(7), actorSystem.NumActors())

		require.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With LFU eviction policy with percentage-based eviction", func(t *testing.T) {
		ctx := context.TODO()
		strategy, _ := NewEvictionStrategy(7, LFU, 50)
		actorSystem, _ := NewActorSystem("testSys",
			WithLogger(log.DiscardLogger),
			WithEvictionStrategy(strategy, time.Second))

		require.NoError(t, actorSystem.Start(ctx))

		// this is to make sure the actor system is started properly in the test
		pause.For(time.Second)

		// let us create some actors
		for i := range 10 {
			actorName := fmt.Sprintf("actor-%d", i)
			_, err := actorSystem.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
			require.NoError(t, err)
		}

		// let us send a message to the last actor
		actorName := "actor-3"
		pid, err := actorSystem.LocalActor(actorName)
		require.NoError(t, err)
		require.NotNil(t, pid)
		err = Tell(ctx, pid, new(testpb.TestSend))
		require.NoError(t, err)

		pause.For(2 * time.Second)

		require.Exactly(t, uint64(5), actorSystem.NumActors())

		require.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With MRU eviction policy with percentage-based eviction", func(t *testing.T) {
		ctx := context.TODO()
		strategy, _ := NewEvictionStrategy(7, MRU, 50)
		actorSystem, _ := NewActorSystem("testSys",
			WithLogger(log.DiscardLogger),
			WithEvictionStrategy(strategy, time.Second))

		require.NoError(t, actorSystem.Start(ctx))

		// this is to make sure the actor system is started properly in the test
		pause.For(2 * time.Second)

		// let us create some actors
		for i := range 10 {
			actorName := fmt.Sprintf("actor-%d", i)
			_, err := actorSystem.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
			require.NoError(t, err)
		}

		// let us send a message to the last actor
		actorName := "actor-3"
		pid, err := actorSystem.LocalActor(actorName)
		require.NoError(t, err)
		require.NotNil(t, pid)
		err = Tell(ctx, pid, new(testpb.TestSend))
		require.NoError(t, err)

		pause.For(time.Second)

		require.Exactly(t, uint64(5), actorSystem.NumActors())

		require.NoError(t, actorSystem.Stop(ctx))
	})
	t.Run("With ActorExists when system not started", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		exists, err := sys.ActorExists(ctx, "")
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
		require.False(t, exists)
	})
	t.Run("With ActorExists when actor is stopping", func(t *testing.T) {
		ctx := context.TODO()
		actorSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		err = actorSystem.Start(ctx)
		require.NoError(t, err)

		// pause for some time. This is an abitrary wait to ensure the actor system is started
		pause.For(300 * time.Millisecond)
		actorName := "test"
		actor := NewMockActor()
		pid, err := actorSystem.Spawn(ctx, actorName, actor, WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)

		exists, err := actorSystem.ActorExists(ctx, actorName)
		require.NoError(t, err)
		require.True(t, exists)

		// let us fake the actor stopping
		pid.toggleFlag(stoppingFlag, true)

		exists, err = actorSystem.ActorExists(ctx, actorName)
		require.NoError(t, err)
		require.False(t, exists)

		// reset the stopping flag
		pid.toggleFlag(stoppingFlag, false)

		require.NoError(t, actorSystem.Stop(ctx))
	})
}

func TestRemoteTell(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewRemoting()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)
		// create a message to send to the test actor
		message := new(testpb.TestSend)
		from := address.NoSender()
		// send the message to the actor
		for i := 0; i < 10; i++ {
			err = remoting.RemoteTell(ctx, from, addr, message)
			// perform some assertions
			require.NoError(t, err)
		}

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("With RemoteTell invalid remote message", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)

		require.NoError(t, sys.Start(ctx))
		pause.For(time.Second)

		actorName := "test"
		actorRef, err := sys.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, actorRef)

		system := sys.(*actorSystem)
		remoting := remote.NewRemoting()
		t.Cleanup(func() {
			remoting.Close()
			assert.NoError(t, system.Stop(ctx))
		})

		remoteMsg := &internalpb.RemoteMessage{
			Sender:   address.NoSender().Address,
			Receiver: actorRef.Address().Address,
			Message:  &anypb.Any{TypeUrl: "invalid"},
		}

		req := connect.NewRequest(
			&internalpb.RemoteTellRequest{RemoteMessages: []*internalpb.RemoteMessage{remoteMsg}},
		)

		resp, err := system.RemoteTell(ctx, req)
		require.Error(t, err)
		require.Nil(t, resp)

		var connectErr *connect.Error
		require.True(t, errors.As(err, &connectErr))
		assert.Equal(t, connect.CodeInternal, connectErr.Code())
		assert.ErrorContains(t, err, gerrors.ErrInvalidRemoteMessage.Error())
	})

	t.Run("With invalid message", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewRemoting()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)
		from := address.NoSender()
		err = remoting.RemoteTell(ctx, from, addr, nil)
		// perform some assertions
		require.Error(t, err)

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With remote service failure", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// create a wrong address
		addr := &goaktpb.Address{
			Host: host,
			Port: 2222,
			Name: "",
			Id:   "",
		}

		remoting := remote.NewRemoting()
		// create a message to send to the test actor
		message := new(testpb.TestSend)
		// send the message to the actor
		from := address.NoSender()
		err = remoting.RemoteTell(ctx, from, address.From(addr), message)
		// perform some assertions
		require.Error(t, err)

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With remoting disabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewRemoting()
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		// let us disable remoting
		actorsSystem := sys.(*actorSystem)
		actorsSystem.remotingEnabled.Store(false)

		// create a message to send to the test actor
		message := new(testpb.TestSend)
		// send the message to the actor
		from := address.NoSender()
		err = remoting.RemoteTell(ctx, from, addr, message)
		// perform some assertions
		require.Error(t, err)

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Batch request", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewRemoting()

		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)
		// create a message to send to the test actor
		messages := make([]proto.Message, 10)
		// send the message to the actor
		for i := range 10 {
			messages[i] = new(testpb.TestSend)
		}

		from := address.NoSender()
		err = remoting.RemoteBatchTell(ctx, from, addr, messages)
		require.NoError(t, err)

		// wait for processing to complete on the actor side
		pause.For(500 * time.Millisecond)
		require.EqualValues(t, 10, actorRef.ProcessedCount()-1)

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("With Batch invalid message", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewRemoting()

		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)
		// create a message to send to the test actor
		messages := make([]proto.Message, 10)
		// send the message to the actor
		for i := range 10 {
			invalid := string([]byte{0xff, 0xfe, 0xfd})
			msg := &wrapperspb.StringValue{Value: invalid}
			messages[i] = msg
		}

		from := address.NoSender()
		err = remoting.RemoteBatchTell(ctx, from, addr, messages)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("With Batch service failure", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// create a wrong address
		addr := &goaktpb.Address{
			Host: host,
			Port: 2222,
			Name: "",
			Id:   "",
		}

		remoting := remote.NewRemoting()
		from := address.NoSender()
		// create a message to send to the test actor
		message := new(testpb.TestSend)
		// send the message to the actor
		err = remoting.RemoteBatchTell(ctx, from, address.From(addr), []proto.Message{message})
		// perform some assertions
		require.Error(t, err)

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Batch when remoting is disabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewRemoting()
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)
		from := address.NoSender()
		// let us disable remoting
		actorsSystem := sys.(*actorSystem)
		actorsSystem.remotingEnabled.Store(false)

		// create a message to send to the test actor
		message := new(testpb.TestSend)
		// send the message to the actor
		err = remoting.RemoteBatchTell(ctx, from, addr, []proto.Message{message})
		// perform some assertions
		require.Error(t, err)

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With actor not found", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewRemoting()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		// stop the actor when wait for cleanup to take place
		require.NoError(t, actorRef.Shutdown(ctx))
		pause.For(time.Second)

		from := address.NoSender()
		// create a message to send to the test actor
		message := new(testpb.TestSend)
		// send the message to the actor
		err = remoting.RemoteTell(ctx, from, addr, message)
		// perform some assertions
		require.Error(t, err)
		require.Contains(t, err.Error(), "not found")

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Batch actor not found", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewRemoting()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		// stop the actor when wait for cleanup to take place
		require.NoError(t, actorRef.Shutdown(ctx))
		pause.For(time.Second)

		from := address.NoSender()
		// create a message to send to the test actor
		message := new(testpb.TestSend)
		// send the message to the actor
		err = remoting.RemoteBatchTell(ctx, from, addr, []proto.Message{message})
		// perform some assertions
		require.Error(t, err)
		require.Contains(t, err.Error(), "not found")

		// stop the actor after some time
		pause.For(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With TLS enabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// AutoGenerate TLS certs
		serverConf := autotls.Config{
			CaFile:           "../test/data/certs/ca.cert",
			CertFile:         "../test/data/certs/auto.pem",
			KeyFile:          "../test/data/certs/auto.key",
			ClientAuthCaFile: "../test/data/certs/client-auth-ca.pem",
			ClientAuth:       tls.RequireAndVerifyClientCert,
		}
		require.NoError(t, autotls.Setup(&serverConf))

		clientConf := &autotls.Config{
			CertFile:           "../test/data/certs/client-auth.pem",
			KeyFile:            "../test/data/certs/client-auth.key",
			InsecureSkipVerify: true,
		}
		require.NoError(t, autotls.Setup(clientConf))

		serverConfig := serverConf.ServerTLS
		clientConfig := clientConf.ClientTLS
		serverConfig.NextProtos = []string{"h2", "http/1.1"}
		clientConfig.NextProtos = []string{"h2", "http/1.1"}

		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		remoteConfig := remote.NewConfig(host, remotingPort)

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remoteConfig),
			WithTLS(&gtls.Info{
				ClientConfig: clientConfig,
				ServerConfig: serverConfig,
			}),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewRemoting(remote.WithRemotingTLS(clientConfig))
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)
		// create a message to send to the test actor
		message := new(testpb.TestSend)
		from := address.NoSender()
		// send the message to the actor
		for range 10 {
			err = remoting.RemoteTell(ctx, from, addr, message)
			// perform some assertions
			require.NoError(t, err)
		}

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Brotli compression", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort,
				remote.WithCompression(remote.BrotliCompression))),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewRemoting(remote.WithRemotingCompression(remote.BrotliCompression))
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)
		// create a message to send to the test actor
		message := new(testpb.TestSend)
		from := address.NoSender()
		// send the message to the actor
		for range 10 {
			err = remoting.RemoteTell(ctx, from, addr, message)
			// perform some assertions
			require.NoError(t, err)
		}

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("With Zstandard compression", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort,
				remote.WithCompression(remote.ZstdCompression))),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewRemoting(remote.WithRemotingCompression(remote.ZstdCompression))
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)
		// create a message to send to the test actor
		message := new(testpb.TestSend)
		from := address.NoSender()
		// send the message to the actor
		for range 10 {
			err = remoting.RemoteTell(ctx, from, addr, message)
			// perform some assertions
			require.NoError(t, err)
		}

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("With Gzip compression", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort,
				remote.WithCompression(remote.GzipCompression))),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewRemoting(remote.WithRemotingCompression(remote.GzipCompression))
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)
		// create a message to send to the test actor
		message := new(testpb.TestSend)
		from := address.NoSender()
		// send the message to the actor
		for range 10 {
			err = remoting.RemoteTell(ctx, from, addr, message)
			// perform some assertions
			require.NoError(t, err)
		}

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestRemoteAsk(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewRemoting()
		from := address.NoSender()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		reply, err := remoting.RemoteAsk(ctx, from, addr, message, time.Minute)
		// perform some assertions
		require.NoError(t, err)
		require.NotNil(t, reply)
		require.True(t, reply.MessageIs(new(testpb.Reply)))

		actual := new(testpb.Reply)
		err = reply.UnmarshalTo(actual)
		require.NoError(t, err)

		expected := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expected, actual))

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With RemoteAsk invalid remote message", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)

		require.NoError(t, sys.Start(ctx))
		pause.For(time.Second)

		actorName := "test"
		actorRef, err := sys.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, actorRef)

		system := sys.(*actorSystem)
		remoting := remote.NewRemoting()
		t.Cleanup(func() {
			remoting.Close()
			assert.NoError(t, system.Stop(ctx))
		})

		remoteMsg := &internalpb.RemoteMessage{
			Sender:   address.NoSender().Address,
			Receiver: actorRef.Address().Address,
			Message:  &anypb.Any{TypeUrl: "invalid"},
		}

		req := connect.NewRequest(
			&internalpb.RemoteAskRequest{RemoteMessages: []*internalpb.RemoteMessage{remoteMsg}},
		)

		resp, err := system.RemoteAsk(ctx, req)
		require.Error(t, err)
		require.Nil(t, resp)

		var connectErr *connect.Error
		require.True(t, errors.As(err, &connectErr))
		assert.Equal(t, connect.CodeInternal, connectErr.Code())
		assert.ErrorContains(t, err, gerrors.ErrInvalidRemoteMessage.Error())
	})

	t.Run("With invalid message", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewRemoting()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		from := address.NoSender()
		// send the message to the actor
		reply, err := remoting.RemoteAsk(ctx, from, addr, nil, time.Minute)
		// perform some assertions
		require.Error(t, err)
		require.Nil(t, reply)

		// stop the actor after some time
		pause.For(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With remote service failure", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// get the address of the actor
		addr := &goaktpb.Address{
			Host: host,
			Port: 2222,
			Name: "",
			Id:   "",
		}

		remoting := remote.NewRemoting()
		from := address.NoSender()
		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		reply, err := remoting.RemoteAsk(ctx, from, address.From(addr), message, time.Minute)
		// perform some assertions
		require.Error(t, err)
		require.Nil(t, reply)

		// stop the actor after some time
		pause.For(time.Second)
		remoting.Close()
		t.Cleanup(
			func() {
				assert.NoError(t, sys.Stop(ctx))
			},
		)
	})
	t.Run("With remoting disabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewRemoting()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		// let us disable remoting
		actorsSystem := sys.(*actorSystem)
		actorsSystem.remotingEnabled.Store(false)

		from := address.NoSender()
		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		reply, err := remoting.RemoteAsk(ctx, from, addr, message, time.Minute)
		// perform some assertions
		require.Error(t, err)
		require.Nil(t, reply)

		// stop the actor after some time
		pause.For(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Batch request", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewRemoting()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		from := address.NoSender()
		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		replies, err := remoting.RemoteBatchAsk(ctx, from, addr, []proto.Message{message}, time.Minute)
		// perform some assertions
		require.NoError(t, err)
		require.Len(t, replies, 1)
		require.NotNil(t, replies[0])
		require.True(t, replies[0].MessageIs(new(testpb.Reply)))

		actual := new(testpb.Reply)
		err = replies[0].UnmarshalTo(actual)
		require.NoError(t, err)

		expected := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expected, actual))

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("With Batch invalid message", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewRemoting()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		from := address.NoSender()
		// create a message to send to the test actor
		invalid := string([]byte{0xff, 0xfe, 0xfd})
		message := &wrapperspb.StringValue{Value: invalid}
		// send the message to the actor
		replies, err := remoting.RemoteBatchAsk(ctx, from, addr, []proto.Message{message}, time.Minute)
		// perform some assertions
		require.Error(t, err)
		require.Nil(t, replies)
		assert.Empty(t, replies)
		assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("With Batch service failure", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// get the address of the actor
		addr := &goaktpb.Address{
			Host: host,
			Port: 2222,
			Name: "",
			Id:   "",
		}

		remoting := remote.NewRemoting()
		from := address.NoSender()
		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		reply, err := remoting.RemoteBatchAsk(ctx, from, address.From(addr), []proto.Message{message}, time.Minute)
		// perform some assertions
		require.Error(t, err)
		require.Nil(t, reply)

		remoting.Close()
		// stop the actor after some time
		pause.For(time.Second)
		t.Cleanup(
			func() {
				assert.NoError(t, sys.Stop(ctx))
			},
		)
	})
	t.Run("With Batch when remoting is disabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewRemoting()

		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		// let us disable remoting
		actorsSystem := sys.(*actorSystem)
		actorsSystem.remotingEnabled.Store(false)

		from := address.NoSender()
		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		reply, err := remoting.RemoteBatchAsk(ctx, from, addr, []proto.Message{message}, time.Minute)
		// perform some assertions
		require.Error(t, err)
		require.Nil(t, reply)

		// stop the actor after some time
		pause.For(time.Second)
		remoting.Close()
		t.Cleanup(
			func() {
				assert.NoError(t, sys.Stop(ctx))
			},
		)
	})
	t.Run("With actor not found", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewRemoting()
		from := address.NoSender()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		// stop the actor when wait for cleanup to take place
		require.NoError(t, actorRef.Shutdown(ctx))
		pause.For(time.Second)

		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		reply, err := remoting.RemoteAsk(ctx, from, addr, message, time.Minute)
		// perform some assertions
		require.Error(t, err)
		require.Contains(t, err.Error(), "not found")
		require.Nil(t, reply)

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With actor is dead", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		actorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = actorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := actorSystem.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewRemoting()
		from := address.NoSender()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, actorSystem.Host(), actorSystem.Port(), actorName)
		require.NoError(t, err)

		// suspend the actor to make it unresponsive
		actorRef.suspend("testing")
		pause.For(time.Second)

		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		reply, err := remoting.RemoteAsk(ctx, from, addr, message, time.Minute)
		// perform some assertions
		require.Error(t, err)
		require.Contains(t, err.Error(), "actor is not alive")
		require.Nil(t, reply)

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = actorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With timeout", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewRemoting()
		from := address.NoSender()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), sys.Port(), actorName)
		require.NoError(t, err)

		// create a message to send to the test actor
		message := new(testpb.TestTimeout)
		// send the message to the actor
		reply, err := remoting.RemoteAsk(ctx, from, addr, message, 100*time.Millisecond)
		// perform some assertions
		require.Error(t, err)
		require.Contains(t, err.Error(), gerrors.ErrRequestTimeout.Error())
		require.Nil(t, reply)

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With context deadline exceeded", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)

		require.NoError(t, sys.Start(ctx))
		pause.For(time.Second)

		actorName := "test"
		actorRef, err := sys.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, actorRef)

		system := sys.(*actorSystem)
		t.Cleanup(func() { assert.NoError(t, system.Stop(ctx)) })

		payload, err := anypb.New(new(testpb.TestTimeout))
		require.NoError(t, err)

		remoteMsg := &internalpb.RemoteMessage{
			Sender:   address.NoSender().Address,
			Receiver: actorRef.Address().Address,
			Message:  payload,
		}

		req := connect.NewRequest(
			&internalpb.RemoteAskRequest{RemoteMessages: []*internalpb.RemoteMessage{remoteMsg}},
		)

		reqCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		resp, err := system.RemoteAsk(reqCtx, req)
		require.Error(t, err)
		require.Nil(t, resp)

		var connectErr *connect.Error
		require.True(t, errors.As(err, &connectErr))
		assert.Equal(t, connect.CodeInternal, connectErr.Code())
		assert.ErrorContains(t, err, context.DeadlineExceeded.Error())
		assert.ErrorContains(t, err, gerrors.ErrRequestTimeout.Error())
		assert.ErrorContains(t, err, gerrors.ErrRemoteSendFailure.Error())

		pause.For(time.Second)
	})
	t.Run("With Batch actor not found", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewRemoting()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		// stop the actor when wait for cleanup to take place
		require.NoError(t, actorRef.Shutdown(ctx))
		pause.For(time.Second)

		from := address.NoSender()
		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		reply, err := remoting.RemoteBatchAsk(ctx, from, addr, []proto.Message{message}, time.Minute)
		// perform some assertions
		require.Error(t, err)
		require.Contains(t, err.Error(), "not found")
		require.Nil(t, reply)

		// stop the actor after some time
		pause.For(time.Second)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With TLS enabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// AutoGenerate TLS certs
		serverConf := autotls.Config{
			CaFile:           "../test/data/certs/ca.cert",
			CertFile:         "../test/data/certs/auto.pem",
			KeyFile:          "../test/data/certs/auto.key",
			ClientAuthCaFile: "../test/data/certs/client-auth-ca.pem",
			ClientAuth:       tls.RequireAndVerifyClientCert,
		}
		require.NoError(t, autotls.Setup(&serverConf))

		clientConf := &autotls.Config{
			CertFile:           "../test/data/certs/client-auth.pem",
			KeyFile:            "../test/data/certs/client-auth.key",
			InsecureSkipVerify: true,
		}
		require.NoError(t, autotls.Setup(clientConf))

		serverConfig := serverConf.ServerTLS
		clientConfig := clientConf.ClientTLS
		serverConfig.NextProtos = []string{"h2", "http/1.1"}
		clientConfig.NextProtos = []string{"h2", "http/1.1"}

		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithTLS(&gtls.Info{
				ClientConfig: clientConfig,
				ServerConfig: serverConfig,
			}),
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewRemoting(remote.WithRemotingTLS(clientConfig))
		from := address.NoSender()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		reply, err := remoting.RemoteAsk(ctx, from, addr, message, time.Minute)
		// perform some assertions
		require.NoError(t, err)
		require.NotNil(t, reply)
		require.True(t, reply.MessageIs(new(testpb.Reply)))

		actual := new(testpb.Reply)
		err = reply.UnmarshalTo(actual)
		require.NoError(t, err)

		expected := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expected, actual))

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Brotli Compression", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort,
				remote.WithCompression(remote.BrotliCompression))),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewRemoting(remote.WithRemotingCompression(remote.BrotliCompression))
		from := address.NoSender()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		reply, err := remoting.RemoteAsk(ctx, from, addr, message, time.Minute)
		// perform some assertions
		require.NoError(t, err)
		require.NotNil(t, reply)
		require.True(t, reply.MessageIs(new(testpb.Reply)))

		actual := new(testpb.Reply)
		err = reply.UnmarshalTo(actual)
		require.NoError(t, err)

		expected := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expected, actual))

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Zstandard Compression", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort,
				remote.WithCompression(remote.ZstdCompression))),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewRemoting(remote.WithRemotingCompression(remote.ZstdCompression))
		from := address.NoSender()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		reply, err := remoting.RemoteAsk(ctx, from, addr, message, time.Minute)
		// perform some assertions
		require.NoError(t, err)
		require.NotNil(t, reply)
		require.True(t, reply.MessageIs(new(testpb.Reply)))

		actual := new(testpb.Reply)
		err = reply.UnmarshalTo(actual)
		require.NoError(t, err)

		expected := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expected, actual))

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With Gzip Compression", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort,
				remote.WithCompression(remote.GzipCompression))),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		remoting := remote.NewRemoting(remote.WithRemotingCompression(remote.GzipCompression))
		from := address.NoSender()
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		// create a message to send to the test actor
		message := new(testpb.TestReply)
		// send the message to the actor
		reply, err := remoting.RemoteAsk(ctx, from, addr, message, time.Minute)
		// perform some assertions
		require.NoError(t, err)
		require.NotNil(t, reply)
		require.True(t, reply.MessageIs(new(testpb.Reply)))

		actual := new(testpb.Reply)
		err = reply.UnmarshalTo(actual)
		require.NoError(t, err)

		expected := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expected, actual))

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With invalid host", func(t *testing.T) {
		ctx := t.Context()
		port := dynaport.Get(1)[0]

		sys, err := NewActorSystem(
			"test",
			WithLogger(log.DiscardLogger),
			WithRemote(remote.NewConfig("127.0.0.1", port)),
		)
		require.NoError(t, err)
		require.NoError(t, sys.Start(ctx))
		t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })

		actorName := "test"
		pid, err := sys.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		t.Cleanup(func() { assert.NoError(t, pid.Shutdown(ctx)) })

		rem := remote.NewRemoting()
		addr, err := rem.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		// Mutate the server’s view so host/port no longer match the request.
		sys.(*actorSystem).remoteConfig = remote.NewConfig("127.0.0.1", port+1)

		reply, err := rem.RemoteAsk(ctx, address.NoSender(), addr, new(testpb.TestReply), time.Second)

		require.Error(t, err)
		require.Nil(t, reply)
		assert.ErrorContains(t, err, gerrors.ErrInvalidHost.Error())
	})
}

func TestRemotingLookup(t *testing.T) {
	t.Run("When remoting is not enabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// let us disable remoting
		actorsSystem := sys.(*actorSystem)
		actorsSystem.remotingEnabled.Store(false)
		remoting := remote.NewRemoting()
		// create a test actor
		actorName := "test"
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.Error(t, err)
		require.Nil(t, addr)

		t.Cleanup(
			func() {
				assert.NoError(t, sys.Stop(ctx))
			},
		)
	})
	t.Run("With mismatched remote address", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)

		require.NoError(t, sys.Start(ctx))
		pause.For(time.Second)

		const actorName = "test"
		actualHost := sys.Host()
		actualPort := int(sys.Port())
		sys.(*actorSystem).remoteConfig = remote.NewConfig(actualHost, actualPort+1)

		remoting := remote.NewRemoting()
		t.Cleanup(remoting.Close)

		addr, err := remoting.RemoteLookup(ctx, actualHost, actualPort, actorName)
		require.Error(t, err)
		assert.ErrorContains(t, err, gerrors.ErrInvalidHost.Error())
		require.Nil(t, addr)

		t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })
	})
	t.Run("When TLS enabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// AutoGenerate TLS certs
		serverConf := autotls.Config{
			CaFile:           "../test/data/certs/ca.cert",
			CertFile:         "../test/data/certs/auto.pem",
			KeyFile:          "../test/data/certs/auto.key",
			ClientAuthCaFile: "../test/data/certs/client-auth-ca.pem",
			ClientAuth:       tls.RequireAndVerifyClientCert,
		}
		require.NoError(t, autotls.Setup(&serverConf))

		clientConf := &autotls.Config{
			CertFile:           "../test/data/certs/client-auth.pem",
			KeyFile:            "../test/data/certs/client-auth.key",
			InsecureSkipVerify: true,
		}
		require.NoError(t, autotls.Setup(clientConf))

		serverConfig := serverConf.ServerTLS
		clientConfig := clientConf.ClientTLS
		serverConfig.NextProtos = []string{"h2", "http/1.1"}
		clientConfig.NextProtos = []string{"h2", "http/1.1"}

		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
			WithTLS(&gtls.Info{
				ClientConfig: clientConfig,
				ServerConfig: serverConfig,
			}),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// let us disable remoting
		actorsSystem := sys.(*actorSystem)
		actorsSystem.remotingEnabled.Store(false)
		remoting := remote.NewRemoting(remote.WithRemotingTLS(clientConfig))
		// create a test actor
		actorName := "test"
		// get the address of the actor
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.Error(t, err)
		require.Nil(t, addr)

		t.Cleanup(
			func() {
				assert.NoError(t, sys.Stop(ctx))
			},
		)
	})
	t.Run("When cluster lookup fails", func(t *testing.T) {
		ctx := context.TODO()
		actorName := uuid.NewString()

		clusterMock := new(mockcluster.Cluster)
		system := MockReplicationTestSystem(clusterMock)
		system.remotingEnabled.Store(true)

		host := system.remoteConfig.BindAddr()
		port := system.remoteConfig.BindPort()

		clusterMock.EXPECT().GetActor(mock.Anything, actorName).Return(nil, assert.AnError)
		t.Cleanup(func() { clusterMock.AssertExpectations(t) })

		req := connect.NewRequest(&internalpb.RemoteLookupRequest{
			Host: host,
			Port: int32(port),
			Name: actorName,
		})

		resp, err := system.RemoteLookup(ctx, req)
		require.Error(t, err)
		require.Nil(t, resp)

		var connectErr *connect.Error
		require.True(t, errors.As(err, &connectErr))
		assert.Equal(t, connect.CodeInternal, connectErr.Code())
		assert.ErrorIs(t, err, assert.AnError)
	})
}

func TestRemotingReSpawn(t *testing.T) {
	t.Run("When remoting is not enabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// let us disable remoting
		actorsSystem := sys.(*actorSystem)
		actorsSystem.remotingEnabled.Store(false)
		remoting := remote.NewRemoting()
		// create a test actor
		actorName := "test"
		// get the address of the actor
		err = remoting.RemoteReSpawn(ctx, sys.Host(), int(sys.Port()), actorName)
		require.Error(t, err)

		remoting.Close()
		t.Cleanup(
			func() {
				assert.NoError(t, sys.Stop(ctx))
			},
		)
	})
	t.Run("With mismatched remote address", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)

		require.NoError(t, sys.Start(ctx))
		pause.For(time.Second)

		actor := NewMockActor()
		const actorName = "test"
		_, err = sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)

		actualHost := sys.Host()
		actualPort := int(sys.Port())
		sys.(*actorSystem).remoteConfig = remote.NewConfig(actualHost, actualPort+1)

		remoting := remote.NewRemoting()
		t.Cleanup(remoting.Close)

		err = remoting.RemoteReSpawn(ctx, actualHost, actualPort, actorName)
		require.Error(t, err)
		assert.ErrorContains(t, err, gerrors.ErrInvalidHost.Error())

		t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })
	})
	t.Run("When remoting is enabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// assert the actor restart count
		pid := actorRef
		assert.Zero(t, pid.restartCount.Load())
		remoting := remote.NewRemoting()
		// get the address of the actor
		err = remoting.RemoteReSpawn(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		assert.EqualValues(t, 1, pid.restartCount.Load())

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("When restart fails", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		ports := dynaport.Get(1)
		remotingPort := ports[0]
		host := "127.0.0.1"

		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)

		require.NoError(t, sys.Start(ctx))
		pause.For(time.Second)

		actorName := uuid.NewString()
		actorRef, err := sys.Spawn(ctx, actorName, NewMockRestart())
		require.NoError(t, err)
		require.NotNil(t, actorRef)

		remoting := remote.NewRemoting()
		t.Cleanup(remoting.Close)

		err = remoting.RemoteReSpawn(ctx, sys.Host(), int(sys.Port()), actorName)
		require.Error(t, err)
		assert.ErrorContains(t, err, "failed to restart actor")
		assert.Zero(t, actorRef.RestartCount())

		pause.For(time.Second)

		t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })
	})
	t.Run("When TLS enabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// AutoGenerate TLS certs
		serverConf := autotls.Config{
			CaFile:           "../test/data/certs/ca.cert",
			CertFile:         "../test/data/certs/auto.pem",
			KeyFile:          "../test/data/certs/auto.key",
			ClientAuthCaFile: "../test/data/certs/client-auth-ca.pem",
			ClientAuth:       tls.RequireAndVerifyClientCert,
		}
		require.NoError(t, autotls.Setup(&serverConf))

		clientConf := &autotls.Config{
			CertFile:           "../test/data/certs/client-auth.pem",
			KeyFile:            "../test/data/certs/client-auth.key",
			InsecureSkipVerify: true,
		}
		require.NoError(t, autotls.Setup(clientConf))

		serverConfig := serverConf.ServerTLS
		clientConfig := clientConf.ClientTLS
		serverConfig.NextProtos = []string{"h2", "http/1.1"}
		clientConfig.NextProtos = []string{"h2", "http/1.1"}

		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
			WithTLS(&gtls.Info{
				ClientConfig: clientConfig,
				ServerConfig: serverConfig,
			}),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// let us disable remoting
		actorsSystem := sys.(*actorSystem)
		actorsSystem.remotingEnabled.Store(false)
		remoting := remote.NewRemoting(remote.WithRemotingTLS(clientConfig))
		// create a test actor
		actorName := "test"
		// get the address of the actor
		err = remoting.RemoteReSpawn(ctx, sys.Host(), int(sys.Port()), actorName)
		require.Error(t, err)

		remoting.Close()
		t.Cleanup(
			func() {
				assert.NoError(t, sys.Stop(ctx))
			},
		)
	})
	t.Run("When actor name is reserved", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "GoAktXYZ"
		remoting := remote.NewRemoting()
		// get the address of the actor
		err = remoting.RemoteReSpawn(ctx, sys.Host(), int(sys.Port()), actorName)
		require.Error(t, err)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("When actor is not found returns no error", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		actorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = actorSystem.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "actorName"
		remoting := remote.NewRemoting()
		// get the address of the actor
		err = remoting.RemoteReSpawn(ctx, actorSystem.Host(), int(actorSystem.Port()), actorName)
		require.NoError(t, err)

		remoting.Close()
		err = actorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("When Brotli compression", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort,
				remote.WithCompression(remote.BrotliCompression))),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// assert the actor restart count
		pid := actorRef
		assert.Zero(t, pid.restartCount.Load())
		remoting := remote.NewRemoting(remote.WithRemotingCompression(remote.BrotliCompression))
		// get the address of the actor
		err = remoting.RemoteReSpawn(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		assert.EqualValues(t, 1, pid.restartCount.Load())

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("When Zstandard compression", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort,
				remote.WithCompression(remote.ZstdCompression))),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// assert the actor restart count
		pid := actorRef
		assert.Zero(t, pid.restartCount.Load())
		remoting := remote.NewRemoting(remote.WithRemotingCompression(remote.ZstdCompression))
		// get the address of the actor
		err = remoting.RemoteReSpawn(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		assert.EqualValues(t, 1, pid.restartCount.Load())

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("When Gzip compression", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort,
				remote.WithCompression(remote.GzipCompression))),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// assert the actor restart count
		pid := actorRef
		assert.Zero(t, pid.restartCount.Load())
		remoting := remote.NewRemoting(remote.WithRemotingCompression(remote.GzipCompression))
		// get the address of the actor
		err = remoting.RemoteReSpawn(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		assert.EqualValues(t, 1, pid.restartCount.Load())

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestRemotingStop(t *testing.T) {
	t.Run("When remoting is not enabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// let us disable remoting
		actorsSystem := sys.(*actorSystem)
		actorsSystem.remotingEnabled.Store(false)

		remoting := remote.NewRemoting()
		// create a test actor
		actorName := "test"
		// get the address of the actor
		err = remoting.RemoteStop(ctx, sys.Host(), int(sys.Port()), actorName)
		require.Error(t, err)

		remoting.Close()
		t.Cleanup(
			func() {
				assert.NoError(t, sys.Stop(ctx))
			},
		)
	})
	t.Run("With mismatched remote address", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)

		require.NoError(t, sys.Start(ctx))
		pause.For(time.Second)

		actor := NewMockActor()
		const actorName = "test"
		_, err = sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)

		actualHost := sys.Host()
		actualPort := int(sys.Port())
		sys.(*actorSystem).remoteConfig = remote.NewConfig(actualHost, actualPort+1)

		remoting := remote.NewRemoting()
		t.Cleanup(remoting.Close)

		err = remoting.RemoteStop(ctx, actualHost, actualPort, actorName)
		require.Error(t, err)
		assert.ErrorContains(t, err, gerrors.ErrInvalidHost.Error())

		t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })
	})
	t.Run("When remoting is enabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// assert the actor restart count
		pid := actorRef
		assert.Zero(t, pid.restartCount.Load())

		remoting := remote.NewRemoting()

		// get the address of the actor
		err = remoting.RemoteStop(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		pause.For(time.Second)

		assert.Empty(t, sys.Actors())

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("When shutdown fails", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)

		require.NoError(t, sys.Start(ctx))
		pause.For(time.Second)

		actorName := uuid.NewString()
		actorRef, err := sys.Spawn(ctx, actorName, &MockPostStop{})
		require.NoError(t, err)
		require.NotNil(t, actorRef)

		remoting := remote.NewRemoting()
		t.Cleanup(remoting.Close)

		err = remoting.RemoteStop(ctx, sys.Host(), int(sys.Port()), actorName)
		require.Error(t, err)
		assert.ErrorContains(t, err, "failed to stop actor")
		assert.ErrorContains(t, err, "failed")

		pause.For(time.Second)

		t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })
	})
	t.Run("When TLS enabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()

		// AutoGenerate TLS certs
		serverConf := autotls.Config{
			CaFile:           "../test/data/certs/ca.cert",
			CertFile:         "../test/data/certs/auto.pem",
			KeyFile:          "../test/data/certs/auto.key",
			ClientAuthCaFile: "../test/data/certs/client-auth-ca.pem",
			ClientAuth:       tls.RequireAndVerifyClientCert,
		}
		require.NoError(t, autotls.Setup(&serverConf))

		clientConf := &autotls.Config{
			CertFile:           "../test/data/certs/client-auth.pem",
			KeyFile:            "../test/data/certs/client-auth.key",
			InsecureSkipVerify: true,
		}
		require.NoError(t, autotls.Setup(clientConf))

		serverConfig := serverConf.ServerTLS
		clientConfig := clientConf.ClientTLS
		serverConfig.NextProtos = []string{"h2", "http/1.1"}
		clientConfig.NextProtos = []string{"h2", "http/1.1"}

		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
			WithTLS(&gtls.Info{
				ClientConfig: clientConfig,
				ServerConfig: serverConfig,
			}),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// let us disable remoting
		actorsSystem := sys.(*actorSystem)
		actorsSystem.remotingEnabled.Store(false)

		remoting := remote.NewRemoting(remote.WithRemotingTLS(clientConfig))
		// create a test actor
		actorName := "test"
		// get the address of the actor
		err = remoting.RemoteStop(ctx, sys.Host(), int(sys.Port()), actorName)
		require.Error(t, err)

		remoting.Close()
		t.Cleanup(
			func() {
				assert.NoError(t, sys.Stop(ctx))
			},
		)
	})
	t.Run("When actor name is reserved", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "GoAktXYZ"
		remoting := remote.NewRemoting()

		// get the address of the actor
		err = remoting.RemoteStop(ctx, sys.Host(), int(sys.Port()), actorName)
		require.Error(t, err)

		pause.For(time.Second)
		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("When actor is not found returns no error", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "actorName"
		remoting := remote.NewRemoting()

		// get the address of the actor
		err = remoting.RemoteStop(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		pause.For(time.Second)
		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("When Brotli Compression", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort,
				remote.WithCompression(remote.BrotliCompression))),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// assert the actor restart count
		pid := actorRef
		assert.Zero(t, pid.restartCount.Load())

		remoting := remote.NewRemoting(remote.WithRemotingCompression(remote.BrotliCompression))

		// get the address of the actor
		err = remoting.RemoteStop(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		pause.For(time.Second)

		assert.Empty(t, sys.Actors())

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("When Zstandard Compression", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort,
				remote.WithCompression(remote.ZstdCompression))),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// assert the actor restart count
		pid := actorRef
		assert.Zero(t, pid.restartCount.Load())

		remoting := remote.NewRemoting(remote.WithRemotingCompression(remote.ZstdCompression))

		// get the address of the actor
		err = remoting.RemoteStop(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		pause.For(time.Second)

		assert.Empty(t, sys.Actors())

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("When Gzip Compression", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort,
				remote.WithCompression(remote.GzipCompression))),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// assert the actor restart count
		pid := actorRef
		assert.Zero(t, pid.restartCount.Load())

		remoting := remote.NewRemoting(remote.WithRemotingCompression(remote.GzipCompression))

		// get the address of the actor
		err = remoting.RemoteStop(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		pause.For(time.Second)

		assert.Empty(t, sys.Actors())

		// stop the actor after some time
		pause.For(time.Second)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestActorSystemStartClusterErrors(t *testing.T) {
	t.Run("startClustering returns error when cluster start fails", func(t *testing.T) {
		ctx := context.TODO()
		sys, err := NewActorSystem(
			"test",
			WithLogger(log.DiscardLogger),
			WithRemote(remote.NewConfig("127.0.0.1", 30003)),
		)
		require.NoError(t, err)

		actorSys := sys.(*actorSystem)
		clusterMock := new(mockcluster.Cluster)
		clusterMock.EXPECT().Start(mock.Anything).Return(assert.AnError)

		actorSys.clusterEnabled.Store(true)
		actorSys.cluster = clusterMock

		err = actorSys.startClustering(ctx)
		require.Error(t, err)
		assert.ErrorIs(t, err, assert.AnError)

		clusterMock.AssertExpectations(t)
	})

	t.Run("returns error when clustering enabled without remoting", func(t *testing.T) {
		ctx := context.TODO()

		provider := mocksdiscovery.NewProvider(t)

		clusterConfig := NewClusterConfig().
			WithDiscovery(provider).
			WithDiscoveryPort(30001).
			WithPeersPort(30002).
			WithKinds(NewMockActor())

		sys, err := NewActorSystem(
			"test",
			WithLogger(log.DiscardLogger),
			WithCluster(clusterConfig),
		)
		require.NoError(t, err)

		err = sys.Start(ctx)
		require.Error(t, err)
		assert.ErrorContains(t, err, "clustering needs remoting to be enabled")
		assert.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
	})
}

func TestRemotingSpawn(t *testing.T) {
	t.Run("When remoting is enabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		ports := dynaport.Get(1)
		remotingPort := ports[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// register dependencies
		dependency := NewMockDependency("test", "test", "test")
		err = sys.Inject(dependency)
		require.NoError(t, err)

		// create an actor implementation and register it
		actor := &exchanger{}
		actorName := uuid.NewString()

		remoting := remote.NewRemoting()
		// fetching the address of the that actor should return nil address
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)
		require.True(t, addr.Equals(address.NoSender()))

		// register the actor
		err = sys.Register(ctx, actor)
		require.NoError(t, err)

		// spawn the remote actor
		request := &remote.SpawnRequest{
			Name:           actorName,
			Kind:           "actor.exchanger",
			Singleton:      false,
			Relocatable:    false,
			EnableStashing: false,
			Dependencies:   []extension.Dependency{dependency},
		}
		err = remoting.RemoteSpawn(ctx, host, remotingPort, request)
		require.NoError(t, err)

		// re-fetching the address of the actor should return not nil address after start
		addr, err = remoting.RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)
		require.NotNil(t, addr)

		from := address.NoSender()
		// send the message to exchanger actor one using remote messaging
		reply, err := remoting.RemoteAsk(ctx, from, addr, new(testpb.TestReply), time.Minute)

		require.NoError(t, err)
		require.NotNil(t, reply)
		require.True(t, reply.MessageIs(new(testpb.Reply)))

		actual := new(testpb.Reply)
		err = reply.UnmarshalTo(actual)
		require.NoError(t, err)

		expected := new(testpb.Reply)
		assert.True(t, proto.Equal(expected, actual))

		remoting.Close()
		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("With mismatched remote address", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		ports := dynaport.Get(1)
		remotingPort := ports[0]
		host := "127.0.0.1"

		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)

		require.NoError(t, sys.Start(ctx))
		pause.For(time.Second)

		require.NoError(t, sys.Register(ctx, &exchanger{}))

		actualHost := sys.Host()
		actualPort := int(sys.Port())
		sys.(*actorSystem).remoteConfig = remote.NewConfig(actualHost, actualPort+1)

		remoting := remote.NewRemoting()
		t.Cleanup(remoting.Close)

		request := &remote.SpawnRequest{
			Name: uuid.NewString(),
			Kind: "actor.exchanger",
		}
		err = remoting.RemoteSpawn(ctx, actualHost, actualPort, request)
		require.Error(t, err)
		assert.ErrorContains(t, err, gerrors.ErrInvalidHost.Error())

		t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })
	})
	t.Run("When Spawn failed", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		ports := dynaport.Get(1)
		remotingPort := ports[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// register dependencies
		dependency := NewMockDependency("test", "test", "test")
		err = sys.Inject(dependency)
		require.NoError(t, err)

		// create an actor implementation and register it
		actor := &MockPreStart{}
		actorName := uuid.NewString()

		remoting := remote.NewRemoting()
		// fetching the address of the that actor should return nil address
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), sys.Port(), actorName)
		require.NoError(t, err)
		require.True(t, addr.Equals(address.NoSender()))

		// register the actor
		err = sys.Register(ctx, actor)
		require.NoError(t, err)

		// spawn the remote actor
		request := &remote.SpawnRequest{
			Name:           actorName,
			Kind:           registry.Name(actor),
			Singleton:      false,
			Relocatable:    false,
			EnableStashing: true,
			Dependencies:   []extension.Dependency{dependency},
		}
		err = remoting.RemoteSpawn(ctx, host, remotingPort, request)
		require.Error(t, err)

		remoting.Close()
		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("With unregistered dependency", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		ports := dynaport.Get(1)
		remotingPort := ports[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// register dependencies
		dependency := NewMockDependency("test", "test", "test")
		err = sys.Inject(dependency)
		require.NoError(t, err)

		// create an actor implementation and register it
		actor := &exchanger{}
		actorName := uuid.NewString()

		remoting := remote.NewRemoting()
		// fetching the address of the that actor should return nil address
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)
		require.True(t, addr.Equals(address.NoSender()))

		// register the actor
		err = sys.Register(ctx, actor)
		require.NoError(t, err)

		// spawn the remote actor
		request := &remote.SpawnRequest{
			Name:           actorName,
			Kind:           "actor.exchanger",
			Singleton:      false,
			Relocatable:    false,
			EnableStashing: false,
			Dependencies:   []extension.Dependency{new(MockDependency)},
		}
		err = remoting.RemoteSpawn(ctx, host, remotingPort, request)
		require.Error(t, err)

		remoting.Close()
		err = sys.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("When actor not registered", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		ports := dynaport.Get(1)
		remotingPort := ports[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create an actor implementation and register it
		actorName := uuid.NewString()

		remoting := remote.NewRemoting()
		// fetching the address of the that actor should return nil address
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)
		require.True(t, addr.Equals(address.NoSender()))

		// spawn the remote actor
		request := &remote.SpawnRequest{
			Name:        actorName,
			Kind:        "actor.exchanger",
			Singleton:   false,
			Relocatable: false,
		}
		err = remoting.RemoteSpawn(ctx, sys.Host(), int(sys.Port()), request)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrTypeNotRegistered)

		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("When registered type is not an actor", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		ports := dynaport.Get(1)
		remotingPort := ports[0]
		host := "127.0.0.1"

		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)

		require.NoError(t, sys.Start(ctx))

		actual := sys.(*actorSystem)
		actual.locker.Lock()
		actual.registry.Register(new(MockUnimplementedActor))
		actual.locker.Unlock()

		remoting := remote.NewRemoting()
		t.Cleanup(remoting.Close)

		request := &remote.SpawnRequest{
			Name: uuid.NewString(),
			Kind: registry.Name(new(MockUnimplementedActor)),
		}

		err = remoting.RemoteSpawn(ctx, host, remotingPort, request)
		require.Error(t, err)
		assert.ErrorContains(t, err, gerrors.ErrInstanceNotAnActor.Error())

		t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })
	})
	t.Run("With dependencies marshaling failure", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		ports := dynaport.Get(1)
		remotingPort := ports[0]
		host := "127.0.0.1"

		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)

		require.NoError(t, sys.Start(ctx))

		actual := sys.(*actorSystem)
		actual.locker.Lock()
		actual.registry.Register(new(MockActor))
		actual.locker.Unlock()

		remoting := remote.NewRemoting()
		t.Cleanup(remoting.Close)

		mockedErr := errors.New("failed to marshal dependency")
		dependency := &MockFailingDependency{
			err: mockedErr,
		}

		request := &remote.SpawnRequest{
			Name: uuid.NewString(),
			Kind: registry.Name(new(MockActor)),
			Dependencies: []extension.Dependency{
				dependency,
			},
		}

		err = remoting.RemoteSpawn(ctx, host, remotingPort, request)
		require.Error(t, err)
		assert.ErrorIs(t, err, mockedErr)

		t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })
	})
	t.Run("When remoting is not available", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		ports := dynaport.Get(1)
		remotingPort := ports[0]
		host := "127.0.0.1"

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

		// create an actor implementation and register it
		actorName := uuid.NewString()
		remoting := remote.NewRemoting()
		// spawn the remote actor
		request := &remote.SpawnRequest{
			Name:        actorName,
			Kind:        "actor.exchanger",
			Singleton:   false,
			Relocatable: false,
		}
		err = remoting.RemoteSpawn(ctx, host, remotingPort, request)
		require.Error(t, err)
		code := connect.CodeOf(err)
		assert.Equal(t, connect.CodeUnavailable, code)

		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("When remoting is not enabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		ports := dynaport.Get(1)
		remotingPort := ports[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// let us disable remoting
		actorsSystem := sys.(*actorSystem)
		actorsSystem.remotingEnabled.Store(false)

		// create an actor implementation and register it
		actorName := uuid.NewString()
		remoting := remote.NewRemoting()
		// spawn the remote actor
		request := &remote.SpawnRequest{
			Name:        actorName,
			Kind:        "actor.exchanger",
			Singleton:   false,
			Relocatable: false,
		}
		err = remoting.RemoteSpawn(ctx, host, remotingPort, request)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrRemotingDisabled)

		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("When TLS enabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()

		// AutoGenerate TLS certs
		serverConf := autotls.Config{
			CaFile:           "../test/data/certs/ca.cert",
			CertFile:         "../test/data/certs/auto.pem",
			KeyFile:          "../test/data/certs/auto.key",
			ClientAuthCaFile: "../test/data/certs/client-auth-ca.pem",
			ClientAuth:       tls.RequireAndVerifyClientCert,
		}
		require.NoError(t, autotls.Setup(&serverConf))

		clientConf := &autotls.Config{
			CertFile:           "../test/data/certs/client-auth.pem",
			KeyFile:            "../test/data/certs/client-auth.key",
			InsecureSkipVerify: true,
		}
		require.NoError(t, autotls.Setup(clientConf))

		serverConfig := serverConf.ServerTLS
		clientConfig := clientConf.ClientTLS
		serverConfig.NextProtos = []string{"h2", "http/1.1"}
		clientConfig.NextProtos = []string{"h2", "http/1.1"}

		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		ports := dynaport.Get(1)
		remotingPort := ports[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
			WithTLS(&gtls.Info{
				ClientConfig: clientConfig,
				ServerConfig: serverConfig,
			}),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create an actor implementation and register it
		actor := &exchanger{}
		actorName := uuid.NewString()

		remoting := remote.NewRemoting(remote.WithRemotingTLS(clientConfig))
		// fetching the address of the that actor should return nil address
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)
		require.NotNil(t, addr)
		require.True(t, addr.Equals(address.NoSender()))

		// register the actor
		err = sys.Register(ctx, actor)
		require.NoError(t, err)

		// spawn the remote actor
		request := &remote.SpawnRequest{
			Name:        actorName,
			Kind:        "actor.exchanger",
			Singleton:   false,
			Relocatable: false,
		}
		err = remoting.RemoteSpawn(ctx, host, remotingPort, request)
		require.NoError(t, err)

		// re-fetching the address of the actor should return not nil address after start
		addr, err = remoting.RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)
		require.NotNil(t, addr)

		from := address.NoSender()
		// send the message to exchanger actor one using remote messaging
		reply, err := remoting.RemoteAsk(ctx, from, addr, new(testpb.TestReply), time.Minute)

		require.NoError(t, err)
		require.NotNil(t, reply)
		require.True(t, reply.MessageIs(new(testpb.Reply)))

		actual := new(testpb.Reply)
		err = reply.UnmarshalTo(actual)
		require.NoError(t, err)

		expected := new(testpb.Reply)
		assert.True(t, proto.Equal(expected, actual))

		remoting.Close()
		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("When request is invalid", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		ports := dynaport.Get(1)
		remotingPort := ports[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// create an actor implementation and register it
		actor := &exchanger{}
		actorName := uuid.NewString()

		remoting := remote.NewRemoting()
		// fetching the address of the that actor should return nil address
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)
		require.True(t, addr.Equals(address.NoSender()))

		// register the actor
		err = sys.Register(ctx, actor)
		require.NoError(t, err)

		// spawn the remote actor
		request := &remote.SpawnRequest{
			Name:        "",
			Kind:        "actor.exchanger",
			Singleton:   false,
			Relocatable: false,
		}
		err = remoting.RemoteSpawn(ctx, host, remotingPort, request)
		require.Error(t, err)

		remoting.Close()
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("When actor name is reserved", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		ports := dynaport.Get(1)
		remotingPort := ports[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// register dependencies
		dependency := NewMockDependency("test", "test", "test")
		err = sys.Inject(dependency)
		require.NoError(t, err)

		actorName := "GoAktXYZ"

		remoting := remote.NewRemoting()
		// spawn the remote actor
		request := &remote.SpawnRequest{
			Name:           actorName,
			Kind:           "actor.exchanger",
			Singleton:      false,
			Relocatable:    false,
			EnableStashing: false,
			Dependencies:   []extension.Dependency{dependency},
		}
		err = remoting.RemoteSpawn(ctx, host, remotingPort, request)
		require.Error(t, err)

		remoting.Close()
		err = sys.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("With Brotli compression", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		ports := dynaport.Get(1)
		remotingPort := ports[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort,
				remote.WithCompression(remote.BrotliCompression))),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// register dependencies
		dependency := NewMockDependency("test", "test", "test")
		err = sys.Inject(dependency)
		require.NoError(t, err)

		// create an actor implementation and register it
		actor := &exchanger{}
		actorName := uuid.NewString()

		remoting := remote.NewRemoting(remote.WithRemotingCompression(remote.BrotliCompression))
		// fetching the address of the that actor should return nil address
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)
		require.True(t, addr.Equals(address.NoSender()))

		// register the actor
		err = sys.Register(ctx, actor)
		require.NoError(t, err)

		// spawn the remote actor
		request := &remote.SpawnRequest{
			Name:           actorName,
			Kind:           "actor.exchanger",
			Singleton:      false,
			Relocatable:    false,
			EnableStashing: false,
			Dependencies:   []extension.Dependency{dependency},
		}
		err = remoting.RemoteSpawn(ctx, host, remotingPort, request)
		require.NoError(t, err)

		// re-fetching the address of the actor should return not nil address after start
		addr, err = remoting.RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)
		require.NotNil(t, addr)

		from := address.NoSender()
		// send the message to exchanger actor one using remote messaging
		reply, err := remoting.RemoteAsk(ctx, from, addr, new(testpb.TestReply), time.Minute)

		require.NoError(t, err)
		require.NotNil(t, reply)
		require.True(t, reply.MessageIs(new(testpb.Reply)))

		actual := new(testpb.Reply)
		err = reply.UnmarshalTo(actual)
		require.NoError(t, err)

		expected := new(testpb.Reply)
		assert.True(t, proto.Equal(expected, actual))

		remoting.Close()
		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("With Zstandard compression", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		ports := dynaport.Get(1)
		remotingPort := ports[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort,
				remote.WithCompression(remote.ZstdCompression))),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// register dependencies
		dependency := NewMockDependency("test", "test", "test")
		err = sys.Inject(dependency)
		require.NoError(t, err)

		// create an actor implementation and register it
		actor := &exchanger{}
		actorName := uuid.NewString()

		remoting := remote.NewRemoting(remote.WithRemotingCompression(remote.ZstdCompression))
		// fetching the address of the that actor should return nil address
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)
		require.True(t, addr.Equals(address.NoSender()))

		// register the actor
		err = sys.Register(ctx, actor)
		require.NoError(t, err)

		// spawn the remote actor
		request := &remote.SpawnRequest{
			Name:           actorName,
			Kind:           "actor.exchanger",
			Singleton:      false,
			Relocatable:    false,
			EnableStashing: false,
			Dependencies:   []extension.Dependency{dependency},
		}
		err = remoting.RemoteSpawn(ctx, host, remotingPort, request)
		require.NoError(t, err)

		// re-fetching the address of the actor should return not nil address after start
		addr, err = remoting.RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)
		require.NotNil(t, addr)

		from := address.NoSender()
		// send the message to exchanger actor one using remote messaging
		reply, err := remoting.RemoteAsk(ctx, from, addr, new(testpb.TestReply), time.Minute)

		require.NoError(t, err)
		require.NotNil(t, reply)
		require.True(t, reply.MessageIs(new(testpb.Reply)))

		actual := new(testpb.Reply)
		err = reply.UnmarshalTo(actual)
		require.NoError(t, err)

		expected := new(testpb.Reply)
		assert.True(t, proto.Equal(expected, actual))

		remoting.Close()
		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
	t.Run("With Gzip compression", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		ports := dynaport.Get(1)
		remotingPort := ports[0]
		host := "127.0.0.1"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort,
				remote.WithCompression(remote.GzipCompression))),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		// register dependencies
		dependency := NewMockDependency("test", "test", "test")
		err = sys.Inject(dependency)
		require.NoError(t, err)

		// create an actor implementation and register it
		actor := &exchanger{}
		actorName := uuid.NewString()

		remoting := remote.NewRemoting(remote.WithRemotingCompression(remote.GzipCompression))
		// fetching the address of the that actor should return nil address
		addr, err := remoting.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)
		require.True(t, addr.Equals(address.NoSender()))

		// register the actor
		err = sys.Register(ctx, actor)
		require.NoError(t, err)

		// spawn the remote actor
		request := &remote.SpawnRequest{
			Name:           actorName,
			Kind:           "actor.exchanger",
			Singleton:      false,
			Relocatable:    false,
			EnableStashing: false,
			Dependencies:   []extension.Dependency{dependency},
		}
		err = remoting.RemoteSpawn(ctx, host, remotingPort, request)
		require.NoError(t, err)

		// re-fetching the address of the actor should return not nil address after start
		addr, err = remoting.RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)
		require.NotNil(t, addr)

		from := address.NoSender()
		// send the message to exchanger actor one using remote messaging
		reply, err := remoting.RemoteAsk(ctx, from, addr, new(testpb.TestReply), time.Minute)

		require.NoError(t, err)
		require.NotNil(t, reply)
		require.True(t, reply.MessageIs(new(testpb.Reply)))

		actual := new(testpb.Reply)
		err = reply.UnmarshalTo(actual)
		require.NoError(t, err)

		expected := new(testpb.Reply)
		assert.True(t, proto.Equal(expected, actual))

		remoting.Close()
		t.Cleanup(
			func() {
				err = sys.Stop(ctx)
				assert.NoError(t, err)
			},
		)
	})
}

func TestRemotingReinstate(t *testing.T) {
	t.Run("When remoting is not enabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// let us disable remoting
		actorsSystem := sys.(*actorSystem)
		actorsSystem.remotingEnabled.Store(false)

		remoting := remote.NewRemoting()
		// create a test actor
		actorName := "test"

		err = remoting.RemoteReinstate(ctx, sys.Host(), int(sys.Port()), actorName)
		require.Error(t, err)

		remoting.Close()
		require.NoError(t, sys.Stop(ctx))
	})
	t.Run("With mismatched remote address", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "127.0.0.1"

		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)

		require.NoError(t, sys.Start(ctx))
		pause.For(time.Second)

		actor := NewMockActor()
		const actorName = "test"
		_, err = sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)

		actualHost := sys.Host()
		actualPort := int(sys.Port())
		sys.(*actorSystem).remoteConfig = remote.NewConfig(actualHost, actualPort+1)

		remoting := remote.NewRemoting()
		t.Cleanup(remoting.Close)

		err = remoting.RemoteReinstate(ctx, actualHost, actualPort, actorName)
		require.Error(t, err)
		assert.ErrorContains(t, err, gerrors.ErrInvalidHost.Error())

		t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })
	})
	t.Run("When remoting is enabled", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		require.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewMockActor()
		pid, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		require.NotNil(t, pid)

		pause.For(time.Second)

		// suspend the actor
		pid.suspend("test")

		require.False(t, pid.IsRunning())
		require.True(t, pid.IsSuspended())

		remoting := remote.NewRemoting()

		err = remoting.RemoteReinstate(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		pause.For(time.Second)

		require.True(t, pid.IsRunning())
		require.False(t, pid.IsSuspended())

		remoting.Close()
		err = sys.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("When actor name is reserved", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		require.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "GoAktXYZ"
		remoting := remote.NewRemoting()

		err = remoting.RemoteReinstate(ctx, sys.Host(), int(sys.Port()), actorName)
		require.Error(t, err)

		remoting.Close()
		err = sys.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("When actor is not found returns no error", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		require.NoError(t, err)

		pause.For(time.Second)

		// create a test actor
		actorName := "actorName"
		remoting := remote.NewRemoting()

		err = remoting.RemoteReinstate(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		remoting.Close()
		err = sys.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("With Brotli compression", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort,
				remote.WithCompression(remote.BrotliCompression))),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// let us disable remoting
		actorsSystem := sys.(*actorSystem)
		actorsSystem.remotingEnabled.Store(false)

		remoting := remote.NewRemoting(remote.WithRemotingCompression(remote.BrotliCompression))
		// create a test actor
		actorName := "test"

		err = remoting.RemoteReinstate(ctx, sys.Host(), int(sys.Port()), actorName)
		require.Error(t, err)

		remoting.Close()
		require.NoError(t, sys.Stop(ctx))
	})
	t.Run("With Zstandard compression", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.DiscardLogger
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort,
				remote.WithCompression(remote.ZstdCompression))),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		pause.For(time.Second)

		// let us disable remoting
		actorsSystem := sys.(*actorSystem)
		actorsSystem.remotingEnabled.Store(false)

		remoting := remote.NewRemoting(remote.WithRemotingCompression(remote.ZstdCompression))
		// create a test actor
		actorName := "test"

		err = remoting.RemoteReinstate(ctx, sys.Host(), int(sys.Port()), actorName)
		require.Error(t, err)

		remoting.Close()
		require.NoError(t, sys.Stop(ctx))
	})
}

func TestReplicateActors_ErrorPaths(t *testing.T) {
	clusterMock := new(mockcluster.Cluster)
	system := MockReplicationTestSystem(clusterMock)
	system.relocationEnabled.Store(true)

	// Expect PutKind to fail for singleton actors and trigger the warning path.
	singleton := &internalpb.Actor{
		Address:     &goaktpb.Address{Host: "127.0.0.1", Port: 8080, Name: "singleton"},
		Type:        "singleton-kind",
		IsSingleton: true,
	}
	clusterMock.EXPECT().PutKind(mock.Anything, singleton.GetType()).Return(fmt.Errorf("put kind failure")).Once()

	// Expect PutActor to fail for regular actors after publish attempts.
	regular := &internalpb.Actor{
		Address: &goaktpb.Address{Host: "127.0.0.1", Port: 8080, Name: "regular"},
		Type:    "regular-kind",
	}
	clusterMock.EXPECT().PutActor(mock.Anything, regular).Return(fmt.Errorf("put actor failure")).Once()

	t.Cleanup(func() { clusterMock.AssertExpectations(t) })

	done := make(chan struct{})
	go func() {
		system.replicateActors()
		close(done)
	}()

	system.actorsQueue <- singleton
	system.actorsQueue <- regular
	close(system.actorsQueue)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("replicateActors did not drain the queue")
	}
}

func TestReplicateGrains_ErrorPaths(t *testing.T) {
	clusterMock := new(mockcluster.Cluster)
	system := MockReplicationTestSystem(clusterMock)
	system.relocationEnabled.Store(true)

	grain := &internalpb.Grain{
		GrainId: &internalpb.GrainId{Kind: "grain-kind", Name: "grain", Value: "grain"},
		Host:    "127.0.0.1",
		Port:    7000,
	}

	clusterMock.EXPECT().PutGrain(mock.Anything, grain).Return(fmt.Errorf("put grain failure")).Once()
	clusterMock.EXPECT().PutKind(mock.Anything, grain.GetGrainId().GetKind()).Return(fmt.Errorf("put grain kind failure")).Once()

	t.Cleanup(func() { clusterMock.AssertExpectations(t) })

	done := make(chan struct{})
	go func() {
		system.replicateGrains()
		close(done)
	}()

	system.grainsQueue <- grain
	close(system.grainsQueue)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("replicateGrains did not drain the queue")
	}
}

func TestResyncActors_ErrorPaths(t *testing.T) {
	clusterMock := new(mockcluster.Cluster)
	system := MockReplicationTestSystem(clusterMock)

	system.locker.Lock()
	system.actors = newTree()
	system.actors.noSender = system.noSender
	system.grains = collection.NewMap[string, *grainPID]()
	system.locker.Unlock()

	dependency := mocksextension.NewDependency(t)
	dependency.EXPECT().MarshalBinary().Return(nil, assert.AnError).Once()

	pid := &PID{
		actor:        NewMockActor(),
		address:      address.New("resync-actor", system.name, "127.0.0.1", int(system.remoteConfig.BindPort())),
		dependencies: collection.NewMap[string, extension.Dependency](),
		logger:       log.DiscardLogger,
		actorSystem:  system,
	}
	pid.dependencies.Set("dep", dependency)
	pid.toggleFlag(runningFlag, true)

	node := &pidNode{
		watchers:    collection.NewMap[string, *PID](),
		watchees:    collection.NewMap[string, *PID](),
		descendants: collection.NewMap[string, *pidNode](),
	}
	node.pid.Store(pid)
	system.actors.pids.Set(pid.ID(), node)
	system.actors.counter.Inc()

	err := system.resyncActors()
	defer dependency.AssertExpectations(t)
	require.Error(t, err)
	assert.ErrorIs(t, err, assert.AnError)
}

func TestResyncGrains_ErrorPaths(t *testing.T) {
	clusterMock := new(mockcluster.Cluster)
	system := MockReplicationTestSystem(clusterMock)

	system.locker.Lock()
	system.grains = collection.NewMap[string, *grainPID]()
	system.locker.Unlock()

	dependency := mocksextension.NewDependency(t)
	dependency.EXPECT().MarshalBinary().Return(nil, assert.AnError).Once()

	config := newGrainConfig()
	config.dependencies.Set("dep", dependency)

	identity := &GrainIdentity{kind: "grain.kind", name: "grain"}
	grain := &grainPID{
		identity:     identity,
		actorSystem:  system,
		logger:       log.DiscardLogger,
		dependencies: config.dependencies,
		config:       config,
	}

	system.grains.Set(identity.String(), grain)

	err := system.resyncGrains()
	defer dependency.AssertExpectations(t)
	require.Error(t, err)
	assert.ErrorIs(t, err, assert.AnError)
}

func TestCleanupCluster_RemoveKindFailure(t *testing.T) {
	clusterMock := new(mockcluster.Cluster)
	system := MockReplicationTestSystem(clusterMock)
	system.grains = collection.NewMap[string, *grainPID]()

	actorRef := ActorRef{
		name:        "singleton-actor",
		kind:        "singleton-kind",
		address:     address.New("singleton-actor", system.name, "127.0.0.1", 8080),
		isSingleton: true,
	}

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(true)
	clusterMock.EXPECT().RemoveKind(mock.Anything, actorRef.Kind()).Return(assert.AnError)
	clusterMock.EXPECT().RemoveActor(mock.Anything, actorRef.Name()).Return(nil)
	t.Cleanup(func() { clusterMock.AssertExpectations(t) })

	err := system.cleanupCluster(context.Background(), []ActorRef{actorRef})
	require.Error(t, err)
	assert.ErrorIs(t, err, assert.AnError)
}

func TestCleanupCluster_RemoveActorFailure(t *testing.T) {
	clusterMock := new(mockcluster.Cluster)
	system := MockReplicationTestSystem(clusterMock)
	system.grains = collection.NewMap[string, *grainPID]()

	actorRef := ActorRef{
		name:    "actor",
		kind:    "actor.kind",
		address: address.New("actor", system.name, "127.0.0.1", 8080),
	}

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(false)
	clusterMock.EXPECT().RemoveActor(mock.Anything, actorRef.Name()).Return(assert.AnError)
	t.Cleanup(func() { clusterMock.AssertExpectations(t) })

	err := system.cleanupCluster(context.Background(), []ActorRef{actorRef})
	require.Error(t, err)
	assert.ErrorIs(t, err, assert.AnError)
}

func TestCleanupCluster_RemoveGrainFailure(t *testing.T) {
	clusterMock := new(mockcluster.Cluster)
	system := MockReplicationTestSystem(clusterMock)
	system.grains = collection.NewMap[string, *grainPID]()

	actorRef := ActorRef{
		name:    "actor",
		kind:    "actor.kind",
		address: address.New("actor", system.name, "127.0.0.1", 8080),
	}
	grainID := &GrainIdentity{kind: "grain.kind", name: "grain"}
	grain := &grainPID{identity: grainID, actorSystem: system, logger: log.DiscardLogger}
	system.grains.Set(grainID.String(), grain)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(false)
	clusterMock.EXPECT().RemoveActor(mock.Anything, actorRef.Name()).Return(nil)
	clusterMock.EXPECT().RemoveGrain(mock.Anything, grainID.String()).Return(assert.AnError)
	t.Cleanup(func() { clusterMock.AssertExpectations(t) })

	err := system.cleanupCluster(context.Background(), []ActorRef{actorRef})
	require.Error(t, err)
	assert.ErrorIs(t, err, assert.AnError)
}

func TestStopReturnsCleanupClusterError(t *testing.T) {
	clusterMock := new(mockcluster.Cluster)
	system := MockReplicationTestSystem(clusterMock)
	system.grains = collection.NewMap[string, *grainPID]()
	system.extensions = collection.NewMap[string, extension.Extension]()
	system.relocating.Store(false)
	system.actors = newTree()
	system.actors.noSender = nil
	system.noSender = nil
	system.topicActor = nil
	system.rootGuardian = nil
	system.systemGuardian = nil
	system.userGuardian = nil
	system.singletonManager = nil
	system.relocator = nil
	system.deadletter = nil
	system.deathWatch = nil
	system.peerStatesWriter = nil
	system.remotingEnabled.Store(false)

	pid := &PID{
		actor:        NewMockActor(),
		address:      address.New("actor", system.name, "127.0.0.1", 8080),
		dependencies: collection.NewMap[string, extension.Dependency](),
		logger:       log.DiscardLogger,
		actorSystem:  system,
	}
	pid.toggleFlag(runningFlag, true)
	node := &pidNode{
		watchers:    collection.NewMap[string, *PID](),
		watchees:    collection.NewMap[string, *PID](),
		descendants: collection.NewMap[string, *pidNode](),
	}
	node.pid.Store(pid)
	system.actors.pids.Set(pid.ID(), node)
	system.actors.counter.Inc()

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(false)
	clusterMock.EXPECT().RemoveActor(mock.Anything, pid.Name()).Return(assert.AnError)
	t.Cleanup(func() { clusterMock.AssertExpectations(t) })

	err := system.Stop(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, assert.AnError)
}

// nolint:revive
func TestActorSystemRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal semantics differ on windows")
	}

	ctx := context.Background()

	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGTERM)
	defer func() {
		signal.Stop(sigterm)
		for len(sigterm) > 0 {
			<-sigterm
		}
	}()

	sys, err := NewActorSystem("testrun", WithLogger(log.DiscardLogger))
	require.NoError(t, err)

	startHookCalled := atomic.NewBool(false)
	stopHookCalled := atomic.NewBool(false)

	done := make(chan struct{})
	go func() {
		sys.Run(ctx, func(ctx context.Context) error {
			startHookCalled.Store(true)
			return nil
		}, func(ctx context.Context) error {
			stopHookCalled.Store(true)
			return nil
		})
		close(done)
	}()

	require.Eventually(t, func() bool {
		return startHookCalled.Load()
	}, 5*time.Second, 10*time.Millisecond)

	require.Eventually(t, func() bool {
		return sys.Running()
	}, 5*time.Second, 10*time.Millisecond)

	// Ensure the signal handling goroutine is ready.
	pause.For(50 * time.Millisecond)

	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGINT))

	require.Eventually(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, 5*time.Second, 10*time.Millisecond)

	require.Eventually(t, func() bool {
		return !sys.Running()
	}, 5*time.Second, 10*time.Millisecond)

	require.Eventually(t, func() bool {
		return stopHookCalled.Load()
	}, 5*time.Second, 10*time.Millisecond)

	select {
	case <-sigterm:
	case <-time.After(time.Second):
		t.Fatal("expected SIGTERM to be forwarded")
	}
}

func TestPeers(t *testing.T) {
	t.Run("Happy path", func(t *testing.T) {
		ctx := t.Context()
		nodePorts := dynaport.Get(3)
		discoveryPort := nodePorts[0]
		peersPort := nodePorts[1]
		remotingPort := nodePorts[2]

		logger := log.DiscardLogger
		host := "127.0.0.1"

		// define discovered addresses
		addrs := []string{
			net.JoinHostPort(host, strconv.Itoa(discoveryPort)),
		}

		// mock the discovery provider
		provider := mocksdiscovery.NewProvider(t)
		provider.EXPECT().ID().Return("test")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		actorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
			WithCluster(
				NewClusterConfig().
					WithKinds(new(MockActor)).
					WithPartitionCount(9).
					WithReplicaCount(1).
					WithPeersPort(peersPort).
					WithMinimumPeersQuorum(1).
					WithDiscoveryPort(discoveryPort).
					WithDiscovery(provider)),
		)
		require.NoError(t, err)

		// start the actor system
		err = actorSystem.Start(ctx)
		require.NoError(t, err)

		pause.For(time.Second)

		peers, err := actorSystem.Peers(t.Context(), time.Second)
		require.NoError(t, err)
		require.Empty(t, peers)

		err = actorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("When system has not started", func(t *testing.T) {
		nodePorts := dynaport.Get(3)
		discoveryPort := nodePorts[0]
		peersPort := nodePorts[1]
		remotingPort := nodePorts[2]

		logger := log.DiscardLogger
		host := "127.0.0.1"

		// mock the discovery provider
		provider := mocksdiscovery.NewProvider(t)
		actorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
			WithRemote(remote.NewConfig(host, remotingPort)),
			WithCluster(
				NewClusterConfig().
					WithKinds(new(MockActor)).
					WithPartitionCount(9).
					WithReplicaCount(1).
					WithPeersPort(peersPort).
					WithMinimumPeersQuorum(1).
					WithDiscoveryPort(discoveryPort).
					WithDiscovery(provider)),
		)
		require.NoError(t, err)

		peers, err := actorSystem.Peers(t.Context(), time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
		require.Empty(t, peers)
	})
	t.Run("When cluster is enabled", func(t *testing.T) {
		ctx := context.TODO()

		clusterMock := mockcluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)

		system.locker.Lock()
		system.actors = newTree()
		system.locker.Unlock()

		clusterMock.EXPECT().Peers(mock.Anything).Return(nil, assert.AnError)

		peers, err := system.Peers(ctx, time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, assert.AnError)
		require.Empty(t, peers)
	})
}

func TestIsLeader(t *testing.T) {
	t.Run("When actor system is not started", func(t *testing.T) {
		logger := log.DiscardLogger

		actorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		require.NoError(t, err)

		isLeader, err := actorSystem.IsLeader(t.Context())
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
		assert.False(t, isLeader)
	})
	t.Run("When cluster is enabled", func(t *testing.T) {
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

		pause.For(time.Second)
		isLeader, err := node1.IsLeader(ctx)
		require.NoError(t, err)
		assert.True(t, isLeader)

		isLeader, err = node2.IsLeader(ctx)
		require.NoError(t, err)
		assert.False(t, isLeader)

		isLeader, err = node3.IsLeader(ctx)
		require.NoError(t, err)
		assert.False(t, isLeader)

		assert.NoError(t, node1.Stop(ctx))
		assert.NoError(t, node3.Stop(ctx))
		assert.NoError(t, sd1.Close())
		assert.NoError(t, sd3.Close())
		srv.Shutdown()
	})
	t.Run("When cluster is disabled", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		logger := log.DiscardLogger

		actorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		require.NoError(t, err)

		// start the actor system
		err = actorSystem.Start(ctx)
		require.NoError(t, err)

		isLeader, err := actorSystem.IsLeader(ctx)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrClusterDisabled)
		assert.False(t, isLeader)

		err = actorSystem.Stop(ctx)
		require.NoError(t, err)
	})
}

func TestLeader(t *testing.T) {
	t.Run("When cluster is enabled", func(t *testing.T) {
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

		pause.For(time.Second)
		leader, err := node1.Leader(ctx)
		require.NoError(t, err)
		require.NotNil(t, leader)
		assert.Equal(t, node1.PeersAddress(), leader.PeersAddress())

		leader, err = node2.Leader(ctx)
		require.NoError(t, err)
		require.NotNil(t, leader)
		assert.Equal(t, node1.PeersAddress(), leader.PeersAddress())

		leader, err = node3.Leader(ctx)
		require.NoError(t, err)
		require.NotNil(t, leader)
		assert.Equal(t, node1.PeersAddress(), leader.PeersAddress())

		assert.NoError(t, node1.Stop(ctx))
		assert.NoError(t, node3.Stop(ctx))
		assert.NoError(t, sd1.Close())
		assert.NoError(t, sd3.Close())
		srv.Shutdown()
	})
	t.Run("When cluster members lookup fails", func(t *testing.T) {
		ctx := t.Context()
		clusterMock := mockcluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)

		clusterMock.EXPECT().Members(mock.Anything).Return(nil, assert.AnError)

		leader, err := system.Leader(ctx)
		require.Error(t, err)
		assert.ErrorIs(t, err, assert.AnError)
		assert.Nil(t, leader)
	})
	t.Run("When cluster is disabled", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		logger := log.DiscardLogger

		actorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		require.NoError(t, err)

		// start the actor system
		err = actorSystem.Start(ctx)
		require.NoError(t, err)

		leader, err := actorSystem.Leader(ctx)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrClusterDisabled)
		require.Nil(t, leader)

		err = actorSystem.Stop(ctx)
		require.NoError(t, err)
	})
	t.Run("When actor system not started", func(t *testing.T) {
		logger := log.DiscardLogger
		ctx := t.Context()

		actorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		require.NoError(t, err)
		leader, err := actorSystem.Leader(ctx)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
		assert.Nil(t, leader)
	})
}
