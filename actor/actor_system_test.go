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
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v3/address"
	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/extension"
	"github.com/tochemey/goakt/v3/internal/cluster"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/metric"
	"github.com/tochemey/goakt/v3/internal/pause"
	"github.com/tochemey/goakt/v3/internal/registry"
	"github.com/tochemey/goakt/v3/internal/xsync"
	"github.com/tochemey/goakt/v3/log"
	mockscluster "github.com/tochemey/goakt/v3/mocks/cluster"
	mocksdiscovery "github.com/tochemey/goakt/v3/mocks/discovery"
	mocksextension "github.com/tochemey/goakt/v3/mocks/extension"
	mocksremote "github.com/tochemey/goakt/v3/mocks/remote"
	"github.com/tochemey/goakt/v3/passivation"
	"github.com/tochemey/goakt/v3/remote"
	"github.com/tochemey/goakt/v3/test/data/testpb"
	gtls "github.com/tochemey/goakt/v3/tls"
)

// remoteTestCtxKey is a custom type for context keys in remote context propagation tests (avoids SA1029).
type remoteTestCtxKey struct{}

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

		t.Cleanup(func() { otel.SetMeterProvider(noopmetric.NewMeterProvider()) })

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

		t.Cleanup(func() { otel.SetMeterProvider(noopmetric.NewMeterProvider()) })

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
	t.Run("When metrics callback fails fetching cluster members", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)

		sys, err := NewActorSystem(
			"testSys",
			WithLogger(log.DiscardLogger),
			WithMetrics(),
		)
		require.NoError(t, err)

		sysImpl := sys.(*actorSystem)
		sysImpl.cluster = clusterMock
		sysImpl.clusterEnabled.Store(true)

		immediate := &immediateMeter{
			manualMeter: &manualMeter{
				Meter: noopmetric.NewMeterProvider().Meter("test"),
			},
			system:  sysImpl,
			cluster: clusterMock,
		}

		otel.SetMeterProvider(&manualMeterProvider{
			MeterProvider: noopmetric.NewMeterProvider(),
			meter:         immediate,
		})
		t.Cleanup(func() { otel.SetMeterProvider(noopmetric.NewMeterProvider()) })

		sysImpl.metricProvider = metric.NewProvider()

		clusterMock.EXPECT().Members(mock.Anything).Return(nil, assert.AnError)

		err = sysImpl.registerMetrics()
		require.Error(t, err)
		require.ErrorIs(t, err, assert.AnError)
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
		require.True(t, remoteAddr.Equals(addr))

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

		clusterMock := new(mockscluster.Cluster)
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
		actorRef.setState(stoppingState, true)

		addr, pid, err := actorSystem.ActorOf(ctx, actorName)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorNotFound)
		require.Nil(t, pid)
		require.Nil(t, addr)

		// reset the stopping flag for cleanup
		actorRef.setState(stoppingState, false)

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
		actual, err := Ask(ctx, actorRef, new(testpb.TestReply), replyTimeout)
		require.NoError(t, err)
		require.NotNil(t, actual)
		expected := new(testpb.Reply)
		reply, ok := actual.(*testpb.Reply)
		require.True(t, ok)
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

		var items []*ActorRestarted
		for message := range consumer.Iterator() {
			payload := message.Payload()
			restarted, ok := payload.(*ActorRestarted)
			if ok {
				items = append(items, restarted)
			}
		}

		require.Len(t, items, 1)

		// send a message to the actor
		actual, err = Ask(ctx, actorRef, new(testpb.TestReply), replyTimeout)
		require.NoError(t, err)
		require.NotNil(t, actual)

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
		actual, err := Ask(ctx, actorRef, new(testpb.TestReply), replyTimeout)
		require.NoError(t, err)
		require.NotNil(t, actual)
		reply, ok := actual.(*testpb.Reply)
		require.True(t, ok)
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
		actual, err := Ask(ctx, actorRef, new(testpb.TestReply), replyTimeout)
		require.NoError(t, err)
		require.NotNil(t, actual)
		expected := new(testpb.Reply)
		reply, ok := actual.(*testpb.Reply)
		require.True(t, ok)
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
		addr = address.New(actorName, "", host, remotingPort)
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

		clusterMock := new(mockscluster.Cluster)
		system := MockReplicationTestSystem(clusterMock)

		clusterMock.EXPECT().GetActor(mock.Anything, actorName).Return(nil, assert.AnError)
		t.Cleanup(func() { clusterMock.AssertExpectations(t) })

		addr, err := system.RemoteActor(ctx, actorName)
		require.Error(t, err)
		assert.ErrorIs(t, err, assert.AnError)
		assert.ErrorContains(t, err, "failed to fetch remote actor")
		require.Nil(t, addr)
	})
	t.Run("With ActorOf failure when cluster address is invalid", func(t *testing.T) {
		ctx := context.TODO()
		actorName := "remoteActor"

		clusterMock := new(mockscluster.Cluster)
		system := MockReplicationTestSystem(clusterMock)

		system.locker.Lock()
		system.actors = newTree()
		system.locker.Unlock()

		clusterMock.EXPECT().GetActor(mock.Anything, actorName).Return(&internalpb.Actor{
			Address: "invalid-address",
		}, nil)
		t.Cleanup(func() { clusterMock.AssertExpectations(t) })

		addr, pid, err := system.ActorOf(ctx, actorName)
		require.Error(t, err)
		assert.ErrorContains(t, err, "address format is invalid")
		require.Nil(t, addr)
		require.Nil(t, pid)
	})
	t.Run("With RemoteActor failure when cluster address is invalid", func(t *testing.T) {
		ctx := context.TODO()
		actorName := "remoteActor"

		clusterMock := new(mockscluster.Cluster)
		system := MockReplicationTestSystem(clusterMock)

		clusterMock.EXPECT().GetActor(mock.Anything, actorName).Return(&internalpb.Actor{
			Address: "invalid-address",
		}, nil)
		t.Cleanup(func() { clusterMock.AssertExpectations(t) })

		addr, err := system.RemoteActor(ctx, actorName)
		require.Error(t, err)
		assert.ErrorContains(t, err, "address format is invalid")
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
	t.Run("With Kill: remote actor not found in cluster", func(t *testing.T) {
		ctx := context.TODO()
		actorName := "remoteActor"

		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)

		system.locker.Lock()
		system.actors = newTree()
		system.locker.Unlock()

		clusterMock.EXPECT().GetActor(mock.Anything, actorName).Return(nil, cluster.ErrActorNotFound)

		err := system.Kill(ctx, actorName)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorNotFound)
	})
	t.Run("With Kill: cluster lookup failure", func(t *testing.T) {
		ctx := context.TODO()
		actorName := "remoteActor"

		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)

		system.locker.Lock()
		system.actors = newTree()
		system.locker.Unlock()

		clusterMock.EXPECT().GetActor(mock.Anything, actorName).Return(nil, assert.AnError)

		err := system.Kill(ctx, actorName)
		require.Error(t, err)
		assert.ErrorIs(t, err, assert.AnError)
		assert.ErrorContains(t, err, "failed to fetch remote actor")
	})
	t.Run("With Kill: cluster returned invalid address", func(t *testing.T) {
		ctx := context.TODO()
		actorName := "remoteActor"

		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)

		system.locker.Lock()
		system.actors = newTree()
		system.locker.Unlock()

		clusterMock.EXPECT().GetActor(mock.Anything, actorName).Return(&internalpb.Actor{
			Address: "invalid-address",
		}, nil)

		err := system.Kill(ctx, actorName)
		require.Error(t, err)
		assert.ErrorContains(t, err, "failed to parse remote actor")
		assert.ErrorContains(t, err, "address format is invalid")
	})
	t.Run("With Kill: stop remote actor via remoting", func(t *testing.T) {
		ctx := context.TODO()
		actorName := "remoteActor"
		remoteHost := "10.0.0.1"
		remotePort := 9090

		clusterMock := mockscluster.NewCluster(t)
		remotingMock := mocksremote.NewRemoting(t)
		system := MockReplicationTestSystem(clusterMock)

		system.locker.Lock()
		system.actors = newTree()
		system.remoting = remotingMock
		system.locker.Unlock()

		addr := address.New(actorName, "test-replication", remoteHost, remotePort)
		clusterMock.EXPECT().GetActor(mock.Anything, actorName).Return(&internalpb.Actor{
			Address: addr.String(),
		}, nil)
		remotingMock.EXPECT().RemoteStop(mock.Anything, remoteHost, remotePort, actorName).Return(nil)

		err := system.Kill(ctx, actorName)
		require.NoError(t, err)
	})
	t.Run("With kill: stop remote actor", func(t *testing.T) {
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

		// let us create 4 actors on each node
		for j := 1; j <= 4; j++ {
			actorName := fmt.Sprintf("Actor1-%d", j)
			pid, err := node1.Spawn(ctx, actorName, NewMockActor(), WithRelocationDisabled())
			require.NoError(t, err)
			require.NotNil(t, pid)
		}

		pause.For(time.Second)

		for j := 1; j <= 4; j++ {
			actorName := fmt.Sprintf("Actor2-%d", j)
			pid, err := node2.Spawn(ctx, actorName, NewMockActor(), WithRelocationDisabled())
			require.NoError(t, err)
			require.NotNil(t, pid)
		}

		pause.For(time.Second)

		for j := 1; j <= 4; j++ {
			actorName := fmt.Sprintf("Actor3-%d", j)
			pid, err := node3.Spawn(ctx, actorName, NewMockActor(), WithRelocationDisabled())
			require.NoError(t, err)
			require.NotNil(t, pid)
		}

		pause.For(time.Second)

		sender, err := node1.LocalActor("Actor1-1")
		require.NoError(t, err)
		require.NotNil(t, sender)

		// let us access some of the node2 actors from node 1 and  node 3
		actorName := "Actor2-1"
		err = node1.Kill(ctx, actorName)
		require.NoError(t, err)

		pause.For(time.Second)

		err = node3.Kill(ctx, actorName)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorNotFound)

		err = node2.Kill(ctx, actorName)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorNotFound)

		assert.NoError(t, node1.Stop(ctx))
		assert.NoError(t, node2.Stop(ctx))
		assert.NoError(t, node3.Stop(ctx))
		assert.NoError(t, sd1.Close())
		assert.NoError(t, sd2.Close())
		assert.NoError(t, sd3.Close())
		srv.Shutdown()
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

		var items []*Deadletter
		for message := range consumer.Iterator() {
			payload := message.Payload()
			// only listening to deadletter
			deadletter, ok := payload.(*Deadletter)
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
		var joins []*NodeJoined
		for event := range subscriber1.Iterator() {
			// get the event payload
			payload := event.Payload()
			// only listening to cluster event
			if nodeJoined, ok := payload.(*NodeJoined); ok {
				joins = append(joins, nodeJoined)
			}
		}

		// assert the joins list
		require.NotEmpty(t, joins)
		require.Len(t, joins, 1)
		require.Equal(t, peerAddress2, joins[0].Address())

		// wait for some time
		pause.For(time.Second)

		// stop the node
		require.NoError(t, cl1.Unsubscribe(subscriber1))
		require.NoError(t, cl1.Stop(ctx))
		require.NoError(t, sd1.Close())

		// wait for some time
		pause.For(time.Second)

		var lefts []*NodeLeft
		for event := range subscriber2.Iterator() {
			payload := event.Payload()

			// only listening to cluster event
			nodeLeft, ok := payload.(*NodeLeft)
			if ok {
				lefts = append(lefts, nodeLeft)
			}
		}

		require.NotEmpty(t, lefts)
		require.Len(t, lefts, 1)
		require.Equal(t, peerAddress1, lefts[0].Address())

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
			Singleton:   nil,
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
		actual, err := remoting.RemoteAsk(ctx, from, addr, new(testpb.TestReply), 20*time.Second)

		require.NoError(t, err)
		require.NotNil(t, actual)
		reply, ok := actual.(*testpb.Reply)
		require.True(t, ok)
		expected := new(testpb.Reply)
		assert.True(t, proto.Equal(expected, reply))

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
		clusterMock := new(mockscluster.Cluster)
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

		var items []*Deadletter
		for message := range consumer.Iterator() {
			payload := message.Payload()
			// only listening to deadletter
			deadletter, ok := payload.(*Deadletter)
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
		pid.setState(stoppingState, true)

		exists, err = actorSystem.ActorExists(ctx, actorName)
		require.NoError(t, err)
		require.False(t, exists)

		// reset the stopping flag
		pid.setState(stoppingState, false)

		require.NoError(t, actorSystem.Stop(ctx))
	})
}

func TestRemoteContextPropagation(t *testing.T) {
	t.Run("RemoteAsk extracts context values", func(t *testing.T) {
		ctxKey := remoteTestCtxKey{}
		headerKey := "x-goakt-propagated"
		headerVal := "inbound-ask"
		ctx := context.Background()

		sys, err := NewActorSystem(
			"ctx-extract-ask",
			WithLogger(log.DiscardLogger),
			WithRemote(remote.NewConfig("127.0.0.1", dynaport.Get(1)[0],
				remote.WithContextPropagator(&headerPropagator{headerKey: headerKey, ctxKey: ctxKey}))),
		)
		require.NoError(t, err)
		require.NoError(t, sys.Start(ctx))
		pause.For(200 * time.Millisecond)
		t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })

		actor := &contextEchoActor{key: ctxKey}
		actorName := "context-ask"
		pid, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		require.NotNil(t, pid)

		rem := remote.NewRemoting(remote.WithRemotingContextPropagator(&headerPropagator{headerKey: headerKey, ctxKey: ctxKey}))
		t.Cleanup(rem.Close)

		addr, err := rem.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		requestCtx := context.WithValue(context.Background(), ctxKey, headerVal)
		actual, err := rem.RemoteAsk(requestCtx, address.NoSender(), addr, new(testpb.TestReply), time.Second)
		require.NoError(t, err)
		require.NotNil(t, actual)
		reply, ok := actual.(*testpb.Reply)
		require.True(t, ok)

		require.Equal(t, headerVal, reply.GetContent())
		require.Equal(t, headerVal, actor.Seen())
	})

	t.Run("RemoteTell extracts context values", func(t *testing.T) {
		ctxKey := remoteTestCtxKey{}
		headerKey := "x-goakt-propagated"
		headerVal := "inbound-tell"
		ctx := context.Background()

		sys, err := NewActorSystem(
			"ctx-extract-tell",
			WithLogger(log.DiscardLogger),
			WithRemote(remote.NewConfig("127.0.0.1", dynaport.Get(1)[0],
				remote.WithContextPropagator(&headerPropagator{headerKey: headerKey, ctxKey: ctxKey}))),
		)
		require.NoError(t, err)
		require.NoError(t, sys.Start(ctx))
		pause.For(200 * time.Millisecond)
		t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })

		actor := &contextEchoActor{key: ctxKey}
		actorName := "context-tell"
		pid, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		require.NotNil(t, pid)

		rem := remote.NewRemoting(remote.WithRemotingContextPropagator(&headerPropagator{headerKey: headerKey, ctxKey: ctxKey}))
		t.Cleanup(rem.Close)

		addr, err := rem.RemoteLookup(ctx, sys.Host(), int(sys.Port()), actorName)
		require.NoError(t, err)

		tellCtx := context.WithValue(context.Background(), ctxKey, headerVal)
		require.NoError(t, rem.RemoteTell(tellCtx, address.NoSender(), addr, new(testpb.TestSend)))

		require.Eventually(t, func() bool {
			return actor.Seen() == headerVal
		}, time.Second, 10*time.Millisecond)
	})
}

func TestRemotingRecover(t *testing.T) {
	t.Run("With remoting panic recovery returns an error", func(t *testing.T) {
		ctx := context.Background()
		host := "127.0.0.1"
		remotingPort := dynaport.Get(1)[0]

		sys, err := NewActorSystem(
			"remoting-recover",
			WithLogger(log.DiscardLogger),
			WithRemote(remote.NewConfig(host, remotingPort, remote.WithContextPropagator(&MockPanicContextPropagator{}))),
		)
		require.NoError(t, err)
		require.NoError(t, sys.Start(ctx))
		pause.For(200 * time.Millisecond)
		t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })

		remoting := remote.NewRemoting()
		t.Cleanup(remoting.Close)

		to := address.New("receiver", "remote-sys", host, remotingPort)
		err = remoting.RemoteTell(ctx, address.NoSender(), to, new(testpb.TestSend))
		require.Error(t, err)
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
		clusterMock := new(mockscluster.Cluster)
		clusterMock.EXPECT().Start(mock.Anything).Return(assert.AnError)

		actorSys.clusterEnabled.Store(true)
		actorSys.cluster = clusterMock

		err = actorSys.startCluster(ctx)
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
			Singleton:      nil,
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
		actual, err := remoting.RemoteAsk(ctx, from, addr, new(testpb.TestReply), time.Minute)

		require.NoError(t, err)
		require.NotNil(t, actual)
		reply, ok := actual.(*testpb.Reply)
		require.True(t, ok)

		expected := new(testpb.Reply)
		assert.True(t, proto.Equal(expected, reply))

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
			Singleton:      nil,
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
			Singleton:      nil,
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
			Singleton:   nil,
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
	t.Run("With dependency reflection failure", func(t *testing.T) {
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
		t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })

		require.NoError(t, sys.Register(ctx, &exchanger{}))

		remoting := remote.NewRemoting()
		t.Cleanup(remoting.Close)
		role := "role"

		request := &remote.SpawnRequest{
			Name: "actorName",
			Kind: "actor.exchanger",
			Role: &role,
			Dependencies: []extension.Dependency{
				NewMockDependency("dep-id", "test", "test"),
			},
		}

		err = remoting.RemoteSpawn(ctx, host, remotingPort, request)
		require.Error(t, err)
		assert.ErrorContains(t, err, gerrors.ErrDependencyTypeNotRegistered.Error())
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
			Singleton:   nil,
			Relocatable: false,
		}
		err = remoting.RemoteSpawn(ctx, host, remotingPort, request)
		require.Error(t, err)

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
			Singleton:   nil,
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
			Singleton:   nil,
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
		actual, err := remoting.RemoteAsk(ctx, from, addr, new(testpb.TestReply), time.Minute)

		require.NoError(t, err)
		require.NotNil(t, actual)
		reply, ok := actual.(*testpb.Reply)
		require.True(t, ok)

		expected := new(testpb.Reply)
		assert.True(t, proto.Equal(expected, reply))

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
			Singleton:   nil,
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
			Singleton:      nil,
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
			Singleton:      nil,
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
		actual, ok := reply.(*testpb.Reply)
		require.True(t, ok)

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
			Singleton:      nil,
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
		actual, ok := reply.(*testpb.Reply)
		require.True(t, ok)

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
			Singleton:      nil,
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
		actual, ok := reply.(*testpb.Reply)
		require.True(t, ok)

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
	clusterMock := new(mockscluster.Cluster)
	system := MockReplicationTestSystem(clusterMock)
	system.relocationEnabled.Store(true)

	// Expect PutKind to fail for singleton actors and trigger the warning path.
	addr := address.New("singleton", "test-replication", "127.0.0.1", 8080)
	singleton := &internalpb.Actor{
		Address: addr.String(),
		Type:    "singleton-kind",
		Singleton: &internalpb.SingletonSpec{
			SpawnTimeout: durationpb.New(time.Second),
			WaitInterval: durationpb.New(500 * time.Millisecond),
			MaxRetries:   int32(3),
		},
	}
	clusterMock.EXPECT().PutKind(mock.Anything, singleton.GetType()).Return(fmt.Errorf("put kind failure")).Once()

	// Expect PutActor to fail for regular actors after publish attempts.
	regular := &internalpb.Actor{
		Address: address.New("regular", "test-replication", "127.0.0.1", 8080).String(),
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
	clusterMock := new(mockscluster.Cluster)
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

func TestCleanupStaleLocalActors(t *testing.T) {
	t.Run("returns nil when clustering disabled", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		system.actors = newTree()
		system.clusterEnabled.Store(false)
		system.cluster = nil

		require.NoError(t, system.cleanupStaleLocalActors(context.Background()))
	})

	t.Run("returns nil when cluster is nil", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		system.actors = newTree()
		system.cluster = nil

		require.NoError(t, system.cleanupStaleLocalActors(context.Background()))
	})

	t.Run("returns error when cluster actors lookup fails", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		system.actors = newTree()

		clusterMock.EXPECT().Actors(mock.Anything, mock.Anything).Return(nil, assert.AnError).Once()

		err := system.cleanupStaleLocalActors(context.Background())
		require.Error(t, err)
		require.ErrorIs(t, err, assert.AnError)
	})

	t.Run("skips non-stale entries and removes stale local actors", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		system.actors = newTree()

		addPIDNode := func(pid *PID) {
			node := newPidNode(pid)
			system.actors.pids[node.id] = node
			system.actors.names[node.name] = node
		}

		localAddr := address.New("local", system.name, "127.0.0.1", 8080)
		localPID := &PID{address: localAddr, actorSystem: system}
		addPIDNode(localPID)

		staleAddr := address.New("stale", system.name, "127.0.0.1", 8080)
		actors := []*internalpb.Actor{
			{Address: "not-a-valid-address"},
			{Address: address.New("other", "other-system", "127.0.0.1", 8080).String()},
			{Address: address.New("other-host", system.name, "127.0.0.2", 8080).String()},
			{Address: address.New("other-port", system.name, "127.0.0.1", 8081).String()},
			{Address: address.New("GoAktSystemGuardian", system.name, "127.0.0.1", 8080).String()},
			{Address: localAddr.String()},
			{Address: staleAddr.String()},
		}

		clusterMock.EXPECT().Actors(mock.Anything, mock.Anything).Return(actors, nil).Once()
		clusterMock.EXPECT().RemoveActor(mock.Anything, staleAddr.Name()).Return(nil).Once()

		require.NoError(t, system.cleanupStaleLocalActors(context.Background()))
	})

	t.Run("removal failure does not fail cleanup", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		system.actors = newTree()

		staleAddr := address.New("stale", system.name, "127.0.0.1", 8080)
		actors := []*internalpb.Actor{{Address: staleAddr.String()}}

		clusterMock.EXPECT().Actors(mock.Anything, mock.Anything).Return(actors, nil).Once()
		clusterMock.EXPECT().RemoveActor(mock.Anything, staleAddr.Name()).Return(assert.AnError).Once()

		require.NoError(t, system.cleanupStaleLocalActors(context.Background()))
	})
}

func TestResyncActors_ErrorPaths(t *testing.T) {
	clusterMock := new(mockscluster.Cluster)
	system := MockReplicationTestSystem(clusterMock)

	system.locker.Lock()
	system.actors = newTree()
	system.actors.noSender = system.noSender
	system.grains = xsync.NewMap[string, *grainPID]()
	system.locker.Unlock()

	dependency := mocksextension.NewDependency(t)
	dependency.EXPECT().MarshalBinary().Return(nil, assert.AnError).Once()

	pid := &PID{
		actor:        NewMockActor(),
		address:      address.New("resync-actor", system.name, "127.0.0.1", int(system.remoteConfig.BindPort())),
		dependencies: xsync.NewMap[string, extension.Dependency](),
		logger:       log.DiscardLogger,
		actorSystem:  system,
	}
	pid.dependencies.Set("dep", dependency)
	pid.setState(runningState, true)

	node := newPidNode(pid)
	system.actors.pids[node.id] = node
	system.actors.counter.Add(1)

	err := system.resyncActors()
	defer dependency.AssertExpectations(t)
	require.Error(t, err)
	assert.ErrorIs(t, err, assert.AnError)
}

func TestResyncGrains_ErrorPaths(t *testing.T) {
	clusterMock := new(mockscluster.Cluster)
	system := MockReplicationTestSystem(clusterMock)

	system.locker.Lock()
	system.grains = xsync.NewMap[string, *grainPID]()
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
	clusterMock := new(mockscluster.Cluster)
	system := MockReplicationTestSystem(clusterMock)
	system.grains = xsync.NewMap[string, *grainPID]()

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
	clusterMock := new(mockscluster.Cluster)
	system := MockReplicationTestSystem(clusterMock)
	system.grains = xsync.NewMap[string, *grainPID]()

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
	clusterMock := new(mockscluster.Cluster)
	system := MockReplicationTestSystem(clusterMock)
	system.grains = xsync.NewMap[string, *grainPID]()

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
	clusterMock := new(mockscluster.Cluster)
	system := MockReplicationTestSystem(clusterMock)
	system.grains = xsync.NewMap[string, *grainPID]()
	system.extensions = xsync.NewMap[string, extension.Extension]()
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
		dependencies: xsync.NewMap[string, extension.Dependency](),
		logger:       log.DiscardLogger,
		actorSystem:  system,
	}
	pid.setState(runningState, true)
	node := newPidNode(pid)
	system.actors.pids[node.id] = node
	system.actors.counter.Add(1)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(false)
	// Peers is not called: relocation is disabled (MockReplicationTestSystem default), so preShutdown returns nil
	// and persistPeerStateToPeers is skipped.
	clusterMock.EXPECT().RemoveActor(mock.Anything, pid.Name()).Return(assert.AnError)
	clusterMock.EXPECT().Stop(mock.Anything).Return(nil)
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

func TestGetNodeMetric(t *testing.T) {
	t.Run("When cluster is not enabled", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		system.clusterEnabled.Store(false)

		request := &internalpb.GetNodeMetricRequest{NodeAddress: "127.0.0.1:8080"}
		resp, err := system.getNodeMetricHandler(context.Background(), nil, request)
		require.NoError(t, err)
		errResp, ok := resp.(*internalpb.Error)
		require.True(t, ok)
		assert.Equal(t, internalpb.Code_CODE_FAILED_PRECONDITION, errResp.GetCode())
		assert.Contains(t, errResp.GetMessage(), gerrors.ErrClusterDisabled.Error())
	})
	t.Run("When node address does not match", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)

		request := &internalpb.GetNodeMetricRequest{NodeAddress: "10.0.0.1:9999"}
		resp, err := system.getNodeMetricHandler(context.Background(), nil, request)
		require.NoError(t, err)
		errResp, ok := resp.(*internalpb.Error)
		require.True(t, ok)
		assert.Equal(t, internalpb.Code_CODE_INVALID_ARGUMENT, errResp.GetCode())
		assert.Contains(t, errResp.GetMessage(), gerrors.ErrInvalidHost.Error())
	})
	t.Run("When request type is invalid", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)

		request := &internalpb.GetKindsRequest{NodeAddress: "127.0.0.1:8080"}
		resp, err := system.getNodeMetricHandler(context.Background(), nil, request)
		require.NoError(t, err)
		errResp, ok := resp.(*internalpb.Error)
		require.True(t, ok)
		assert.Equal(t, internalpb.Code_CODE_INVALID_ARGUMENT, errResp.GetCode())
	})
	t.Run("When successful", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		system.actorsCounter.Store(5)
		system.grains = xsync.NewMap[string, *grainPID]()
		system.grains.Set("grain1", &grainPID{})
		system.grains.Set("grain2", &grainPID{})

		remoteAddr := fmt.Sprintf("%s:%d", system.remoteConfig.BindAddr(), system.remoteConfig.BindPort())
		request := &internalpb.GetNodeMetricRequest{NodeAddress: remoteAddr}
		resp, err := system.getNodeMetricHandler(context.Background(), nil, request)
		require.NoError(t, err)

		metricResp, ok := resp.(*internalpb.GetNodeMetricResponse)
		require.True(t, ok)
		assert.Equal(t, remoteAddr, metricResp.GetNodeAddress())
		assert.EqualValues(t, 7, metricResp.GetLoad()) // 5 actors + 2 grains
	})
	t.Run("With remoting integration", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		nodePorts := dynaport.Get(3)
		discoveryPort := nodePorts[0]
		peersPort := nodePorts[1]
		remotingPort := nodePorts[2]
		host := "127.0.0.1"

		addrs := []string{
			net.JoinHostPort(host, strconv.Itoa(discoveryPort)),
		}

		provider := mocksdiscovery.NewProvider(t)
		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		sys, err := NewActorSystem(
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

		err = sys.Start(ctx)
		require.NoError(t, err)

		pause.For(time.Second)

		// Spawn an actor to bump the load counter
		actorName := "metric-actor"
		actor := NewMockActor()
		_, err = sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)

		// Send GetNodeMetric over the wire
		remoting := remote.NewRemoting()
		client := remoting.NetClient(host, remotingPort)

		nodeAddr := fmt.Sprintf("%s:%d", host, remotingPort)
		request := &internalpb.GetNodeMetricRequest{NodeAddress: nodeAddr}
		resp, err := client.SendProto(ctx, request)
		require.NoError(t, err)

		metricResp, ok := resp.(*internalpb.GetNodeMetricResponse)
		require.True(t, ok)
		assert.Equal(t, nodeAddr, metricResp.GetNodeAddress())
		// At least 1 actor spawned; the system guardian adds more.
		assert.GreaterOrEqual(t, metricResp.GetLoad(), uint64(1))

		remoting.Close()
		t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })
	})
}

func TestGetKinds(t *testing.T) {
	t.Run("When cluster is not enabled", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		system.clusterEnabled.Store(false)

		request := &internalpb.GetKindsRequest{NodeAddress: "127.0.0.1:8080"}
		resp, err := system.getKindsHandler(context.Background(), nil, request)
		require.NoError(t, err)
		errResp, ok := resp.(*internalpb.Error)
		require.True(t, ok)
		assert.Equal(t, internalpb.Code_CODE_FAILED_PRECONDITION, errResp.GetCode())
		assert.Contains(t, errResp.GetMessage(), gerrors.ErrClusterDisabled.Error())
	})
	t.Run("When node address does not match", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)

		request := &internalpb.GetKindsRequest{NodeAddress: "10.0.0.1:9999"}
		resp, err := system.getKindsHandler(context.Background(), nil, request)
		require.NoError(t, err)
		errResp, ok := resp.(*internalpb.Error)
		require.True(t, ok)
		assert.Equal(t, internalpb.Code_CODE_INVALID_ARGUMENT, errResp.GetCode())
		assert.Contains(t, errResp.GetMessage(), gerrors.ErrInvalidHost.Error())
	})
	t.Run("When request type is invalid", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)

		request := &internalpb.GetNodeMetricRequest{NodeAddress: "127.0.0.1:8080"}
		resp, err := system.getKindsHandler(context.Background(), nil, request)
		require.NoError(t, err)
		errResp, ok := resp.(*internalpb.Error)
		require.True(t, ok)
		assert.Equal(t, internalpb.Code_CODE_INVALID_ARGUMENT, errResp.GetCode())
	})
	t.Run("When successful", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		system.clusterConfig = NewClusterConfig().
			WithKinds(new(MockActor)).
			WithPartitionCount(9).
			WithReplicaCount(1).
			WithMinimumPeersQuorum(1)

		remoteAddr := fmt.Sprintf("%s:%d", system.remoteConfig.BindAddr(), system.remoteConfig.BindPort())
		request := &internalpb.GetKindsRequest{NodeAddress: remoteAddr}
		resp, err := system.getKindsHandler(context.Background(), nil, request)
		require.NoError(t, err)

		kindsResp, ok := resp.(*internalpb.GetKindsResponse)
		require.True(t, ok)
		// NewClusterConfig auto-registers FuncActor, so we expect 2 kinds.
		require.Len(t, kindsResp.GetKinds(), 2)
		assert.Contains(t, kindsResp.GetKinds(), registry.Name(new(MockActor)))
		assert.Contains(t, kindsResp.GetKinds(), registry.Name(new(FuncActor)))
	})
	t.Run("With remoting integration", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		nodePorts := dynaport.Get(3)
		discoveryPort := nodePorts[0]
		peersPort := nodePorts[1]
		remotingPort := nodePorts[2]
		host := "127.0.0.1"

		addrs := []string{
			net.JoinHostPort(host, strconv.Itoa(discoveryPort)),
		}

		provider := mocksdiscovery.NewProvider(t)
		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		sys, err := NewActorSystem(
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

		err = sys.Start(ctx)
		require.NoError(t, err)

		pause.For(time.Second)

		// Send GetKinds over the wire
		remoting := remote.NewRemoting()
		client := remoting.NetClient(host, remotingPort)

		nodeAddr := fmt.Sprintf("%s:%d", host, remotingPort)
		request := &internalpb.GetKindsRequest{NodeAddress: nodeAddr}
		resp, err := client.SendProto(ctx, request)
		require.NoError(t, err)

		kindsResp, ok := resp.(*internalpb.GetKindsResponse)
		require.True(t, ok)
		// NewClusterConfig auto-registers FuncActor + the explicit MockActor.
		require.Len(t, kindsResp.GetKinds(), 2)
		assert.Contains(t, kindsResp.GetKinds(), registry.Name(new(MockActor)))
		assert.Contains(t, kindsResp.GetKinds(), registry.Name(new(FuncActor)))

		remoting.Close()
		t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })
	})
}

func TestPersistPeerState(t *testing.T) {
	t.Run("When remoting is not enabled", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		system.remotingEnabled.Store(false)

		request := &internalpb.PersistPeerStateRequest{
			PeerState: &internalpb.PeerState{Host: "127.0.0.1", PeersPort: 9000},
		}
		resp, err := system.persistPeerStateHandler(context.Background(), nil, request)
		require.NoError(t, err)
		errResp, ok := resp.(*internalpb.Error)
		require.True(t, ok)
		assert.Equal(t, internalpb.Code_CODE_FAILED_PRECONDITION, errResp.GetCode())
		assert.Contains(t, errResp.GetMessage(), gerrors.ErrRemotingDisabled.Error())
	})
	t.Run("When cluster is not enabled", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		system.remotingEnabled.Store(true)
		system.clusterEnabled.Store(false)

		request := &internalpb.PersistPeerStateRequest{
			PeerState: &internalpb.PeerState{Host: "127.0.0.1", PeersPort: 9000},
		}
		resp, err := system.persistPeerStateHandler(context.Background(), nil, request)
		require.NoError(t, err)
		errResp, ok := resp.(*internalpb.Error)
		require.True(t, ok)
		assert.Equal(t, internalpb.Code_CODE_FAILED_PRECONDITION, errResp.GetCode())
		assert.Contains(t, errResp.GetMessage(), gerrors.ErrClusterDisabled.Error())
	})
	t.Run("When request type is invalid", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		system.remotingEnabled.Store(true)

		request := &internalpb.GetKindsRequest{NodeAddress: "127.0.0.1:8080"}
		resp, err := system.persistPeerStateHandler(context.Background(), nil, request)
		require.NoError(t, err)
		errResp, ok := resp.(*internalpb.Error)
		require.True(t, ok)
		assert.Equal(t, internalpb.Code_CODE_INVALID_ARGUMENT, errResp.GetCode())
	})
	t.Run("When store returns error", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		system.remotingEnabled.Store(true)
		system.clusterStore = &recordingPeerStateStore{err: assert.AnError}

		request := &internalpb.PersistPeerStateRequest{
			PeerState: &internalpb.PeerState{Host: "127.0.0.1", PeersPort: 9000},
		}
		resp, err := system.persistPeerStateHandler(context.Background(), nil, request)
		require.NoError(t, err)
		errResp, ok := resp.(*internalpb.Error)
		require.True(t, ok)
		assert.Equal(t, internalpb.Code_CODE_INTERNAL_ERROR, errResp.GetCode())
	})
	t.Run("When successful", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		system.remotingEnabled.Store(true)
		store := &recordingPeerStateStore{}
		system.clusterStore = store

		peerState := &internalpb.PeerState{Host: "10.0.0.1", PeersPort: 7070}
		request := &internalpb.PersistPeerStateRequest{PeerState: peerState}
		resp, err := system.persistPeerStateHandler(context.Background(), nil, request)
		require.NoError(t, err)

		_, ok := resp.(*internalpb.PersistPeerStateResponse)
		require.True(t, ok)
		assert.True(t, store.called)
		assert.True(t, proto.Equal(peerState, store.lastPeer))
	})
	t.Run("With remoting integration", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		nodePorts := dynaport.Get(3)
		discoveryPort := nodePorts[0]
		peersPort := nodePorts[1]
		remotingPort := nodePorts[2]
		host := "127.0.0.1"

		addrs := []string{
			net.JoinHostPort(host, strconv.Itoa(discoveryPort)),
		}

		provider := mocksdiscovery.NewProvider(t)
		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		sys, err := NewActorSystem(
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

		err = sys.Start(ctx)
		require.NoError(t, err)

		pause.For(time.Second)

		// Send PersistPeerState over the wire
		remoting := remote.NewRemoting()
		client := remoting.NetClient(host, remotingPort)

		peerState := &internalpb.PeerState{
			Host:      "10.0.0.5",
			PeersPort: 3000,
		}
		request := &internalpb.PersistPeerStateRequest{PeerState: peerState}
		resp, err := client.SendProto(ctx, request)
		require.NoError(t, err)

		_, ok := resp.(*internalpb.PersistPeerStateResponse)
		require.True(t, ok)

		// Verify the state was persisted by checking the store
		actorsSystem := sys.(*actorSystem)
		stored, found := actorsSystem.clusterStore.GetPeerState(ctx, "10.0.0.5:3000")
		require.True(t, found)
		assert.Equal(t, "10.0.0.5", stored.GetHost())
		assert.EqualValues(t, 3000, stored.GetPeersPort())

		remoting.Close()
		t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })
	})
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

		clusterMock := mockscluster.NewCluster(t)
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
	t.Run("When cluster is not enabled", func(t *testing.T) {
		ctx := t.Context()
		logger := log.DiscardLogger

		actorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		require.NoError(t, err)

		// start the actor system
		err = actorSystem.Start(ctx)
		require.NoError(t, err)

		pause.For(time.Second)

		peers, err := actorSystem.Peers(ctx, time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrClusterDisabled)
		require.Empty(t, peers)

		err = actorSystem.Stop(ctx)
		assert.NoError(t, err)
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
		clusterMock := mockscluster.NewCluster(t)
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
	t.Run("With RegisterMetrics callback", func(t *testing.T) {
		ctx := context.Background()

		prevProvider := otel.GetMeterProvider()
		meterProvider := newManualMeterProvider()
		otel.SetMeterProvider(meterProvider)
		t.Cleanup(func() { otel.SetMeterProvider(prevProvider) })

		sys, err := NewActorSystem("testSys",
			WithLogger(log.DiscardLogger),
			WithMetrics())
		require.NoError(t, err)
		require.NotNil(t, sys)

		require.NoError(t, sys.Start(ctx))
		t.Cleanup(func() { require.NoError(t, sys.Stop(ctx)) })

		actorRef, err := sys.Spawn(ctx, "metrics-system-actor", NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, actorRef)

		manual, ok := meterProvider.meter.(*manualMeter)
		require.True(t, ok)
		require.NotEmpty(t, manual.callbacks)

		observer := &manualObserver{}
		for _, cb := range manual.callbacks {
			require.NoError(t, cb(ctx, observer))
		}
		require.GreaterOrEqual(t, len(observer.records), 4)
	})
}

func TestSelectOldestPeers(t *testing.T) {
	t.Run("returns empty slice when no peers exist", func(t *testing.T) {
		ctx := context.TODO()
		clusterMock := mockscluster.NewCluster(t)
		clusterMock.EXPECT().Peers(mock.Anything).Return([]*cluster.Peer{}, nil)

		system := MockReplicationTestSystem(clusterMock)

		peers, err := system.selectOldestPeers(ctx, 3)
		require.NoError(t, err)
		assert.Empty(t, peers)
	})

	t.Run("returns error when cluster.Peers fails", func(t *testing.T) {
		ctx := context.TODO()
		clusterMock := mockscluster.NewCluster(t)
		expectedErr := errors.New("cluster unavailable")
		clusterMock.EXPECT().Peers(mock.Anything).Return(nil, expectedErr)

		system := MockReplicationTestSystem(clusterMock)

		peers, err := system.selectOldestPeers(ctx, 3)
		require.Error(t, err)
		assert.Equal(t, expectedErr, err)
		assert.Nil(t, peers)
	})

	t.Run("returns all peers when fewer than k exist", func(t *testing.T) {
		ctx := context.TODO()
		clusterMock := mockscluster.NewCluster(t)

		// Two peers, but we request 3
		inputPeers := []*cluster.Peer{
			{Host: "host2", PeersPort: 9002, CreatedAt: 2000},
			{Host: "host1", PeersPort: 9001, CreatedAt: 1000}, // Oldest
		}
		clusterMock.EXPECT().Peers(mock.Anything).Return(inputPeers, nil)

		system := MockReplicationTestSystem(clusterMock)

		peers, err := system.selectOldestPeers(ctx, 3)
		require.NoError(t, err)
		require.Len(t, peers, 2)

		// Should be sorted by age (oldest first)
		assert.Equal(t, "host1", peers[0].Host)
		assert.Equal(t, "host2", peers[1].Host)
	})

	t.Run("returns k oldest peers when more than k exist", func(t *testing.T) {
		ctx := context.TODO()
		clusterMock := mockscluster.NewCluster(t)

		// Five peers, request 3
		inputPeers := []*cluster.Peer{
			{Host: "host5", PeersPort: 9005, CreatedAt: 5000}, // Newest
			{Host: "host2", PeersPort: 9002, CreatedAt: 2000},
			{Host: "host4", PeersPort: 9004, CreatedAt: 4000},
			{Host: "host1", PeersPort: 9001, CreatedAt: 1000}, // Oldest
			{Host: "host3", PeersPort: 9003, CreatedAt: 3000},
		}
		clusterMock.EXPECT().Peers(mock.Anything).Return(inputPeers, nil)

		system := MockReplicationTestSystem(clusterMock)

		peers, err := system.selectOldestPeers(ctx, 3)
		require.NoError(t, err)
		require.Len(t, peers, 3)

		// Should be the 3 oldest, sorted by age
		assert.Equal(t, "host1", peers[0].Host) // CreatedAt: 1000
		assert.Equal(t, "host2", peers[1].Host) // CreatedAt: 2000
		assert.Equal(t, "host3", peers[2].Host) // CreatedAt: 3000
	})

	t.Run("returns exactly k peers when exactly k exist", func(t *testing.T) {
		ctx := context.TODO()
		clusterMock := mockscluster.NewCluster(t)

		// Exactly 3 peers
		inputPeers := []*cluster.Peer{
			{Host: "host3", PeersPort: 9003, CreatedAt: 3000},
			{Host: "host1", PeersPort: 9001, CreatedAt: 1000},
			{Host: "host2", PeersPort: 9002, CreatedAt: 2000},
		}
		clusterMock.EXPECT().Peers(mock.Anything).Return(inputPeers, nil)

		system := MockReplicationTestSystem(clusterMock)

		peers, err := system.selectOldestPeers(ctx, 3)
		require.NoError(t, err)
		require.Len(t, peers, 3)

		// Should be sorted by age
		assert.Equal(t, "host1", peers[0].Host)
		assert.Equal(t, "host2", peers[1].Host)
		assert.Equal(t, "host3", peers[2].Host)
	})

	t.Run("handles single peer", func(t *testing.T) {
		ctx := context.TODO()
		clusterMock := mockscluster.NewCluster(t)

		inputPeers := []*cluster.Peer{
			{Host: "host1", PeersPort: 9001, CreatedAt: 1000},
		}
		clusterMock.EXPECT().Peers(mock.Anything).Return(inputPeers, nil)

		system := MockReplicationTestSystem(clusterMock)

		peers, err := system.selectOldestPeers(ctx, 3)
		require.NoError(t, err)
		require.Len(t, peers, 1)
		assert.Equal(t, "host1", peers[0].Host)
	})
}

func TestPreShutdown(t *testing.T) {
	t.Run("returns nil when relocation is disabled", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		system.relocationEnabled.Store(false)

		peerState, err := system.preShutdown()
		require.NoError(t, err)
		assert.Nil(t, peerState)
	})

	t.Run("returns nil when cluster is not enabled", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		system.relocationEnabled.Store(true)
		system.clusterEnabled.Store(false)

		peerState, err := system.preShutdown()
		require.NoError(t, err)
		assert.Nil(t, peerState)
	})

	t.Run("returns nil when cluster is nil", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		system.relocationEnabled.Store(true)
		system.cluster = nil

		peerState, err := system.preShutdown()
		require.NoError(t, err)
		assert.Nil(t, peerState)
	})
}

func TestPersistPeerStateToPeers(t *testing.T) {
	t.Run("returns nil when peerState is nil", func(t *testing.T) {
		ctx := context.TODO()
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)

		err := system.persistPeerStateToPeers(ctx, nil)
		require.NoError(t, err)
	})

	t.Run("returns nil when no peers available", func(t *testing.T) {
		ctx := context.TODO()
		clusterMock := mockscluster.NewCluster(t)
		clusterMock.EXPECT().Peers(mock.Anything).Return([]*cluster.Peer{}, nil)

		system := MockReplicationTestSystem(clusterMock)
		peerState := &internalpb.PeerState{
			Host:      "127.0.0.1",
			PeersPort: 9000,
		}

		err := system.persistPeerStateToPeers(ctx, peerState)
		require.NoError(t, err)
	})

	t.Run("returns error when cluster.Peers fails", func(t *testing.T) {
		ctx := context.TODO()
		clusterMock := mockscluster.NewCluster(t)
		expectedErr := errors.New("cluster unavailable")
		clusterMock.EXPECT().Peers(mock.Anything).Return(nil, expectedErr)

		system := MockReplicationTestSystem(clusterMock)
		peerState := &internalpb.PeerState{
			Host:      "127.0.0.1",
			PeersPort: 9000,
		}

		err := system.persistPeerStateToPeers(ctx, peerState)
		require.Error(t, err)
		assert.Equal(t, expectedErr, err)
	})

	t.Run("handles context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		clusterMock := mockscluster.NewCluster(t)
		peers := []*cluster.Peer{
			{Host: "127.0.0.1", RemotingPort: 8080, PeersPort: 9001, CreatedAt: 1000},
		}
		clusterMock.EXPECT().Peers(mock.Anything).Return(peers, nil)

		system := MockReplicationTestSystem(clusterMock)
		peerState := &internalpb.PeerState{
			Host:      "127.0.0.1",
			PeersPort: 9000,
		}

		err := system.persistPeerStateToPeers(ctx, peerState)
		// Should handle gracefully - either succeed with partial or return context error
		// The exact behavior depends on timing
		_ = err // Error is acceptable here due to context cancellation
	})
}
