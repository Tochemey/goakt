/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/address"
	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/internal/lib"
	"github.com/tochemey/goakt/v2/internal/types"
	"github.com/tochemey/goakt/v2/log"
	clustermocks "github.com/tochemey/goakt/v2/mocks/cluster"
	testkit "github.com/tochemey/goakt/v2/mocks/discovery"
	"github.com/tochemey/goakt/v2/test/data/testpb"
)

// nolint
func TestActorSystem(t *testing.T) {
	t.Run(
		"New instance with Defaults", func(t *testing.T) {
			actorSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
			require.NoError(t, err)
			require.NotNil(t, actorSystem)
			var iface any = actorSystem
			_, ok := iface.(ActorSystem)
			assert.True(t, ok)
			assert.Equal(t, "testSys", actorSystem.Name())
			assert.Empty(t, actorSystem.Actors())
			assert.NotNil(t, actorSystem.Logger())
		},
	)
	t.Run(
		"New instance with Missing Name", func(t *testing.T) {
			sys, err := NewActorSystem("")
			assert.Error(t, err)
			assert.Nil(t, sys)
			assert.EqualError(t, err, ErrNameRequired.Error())
		},
	)
	t.Run(
		"With invalid actor system Name", func(t *testing.T) {
			sys, err := NewActorSystem("$omeN@me")
			assert.Error(t, err)
			assert.Nil(t, sys)
			assert.EqualError(t, err, ErrInvalidActorSystemName.Error())
		},
	)
	t.Run(
		"With Spawn an actor when not System started", func(t *testing.T) {
			ctx := context.TODO()
			sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
			actor := newTestActor()
			actorRef, err := sys.Spawn(ctx, "Test", actor)
			assert.Error(t, err)
			assert.EqualError(t, err, ErrActorSystemNotStarted.Error())
			assert.Nil(t, actorRef)
		},
	)
	t.Run(
		"With Spawn an actor when started", func(t *testing.T) {
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
			lib.Pause(time.Second)

			t.Cleanup(
				func() {
					err = sys.Stop(ctx)
					assert.NoError(t, err)
				},
			)
		},
	)
	t.Run(
		"With Spawn an actor with invalid actor name", func(t *testing.T) {
			ctx := context.TODO()
			sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

			// start the actor system
			err := sys.Start(ctx)
			require.NoError(t, err)

			actor := newTestActor()
			actorRef, err := sys.Spawn(ctx, "$omeN@me", actor)
			require.Error(t, err)
			assert.EqualError(t, err, "must contain only word characters (i.e. [a-zA-Z0-9] plus non-leading '-' or '_')")
			assert.Nil(t, actorRef)

			// stop the actor after some time
			lib.Pause(time.Second)

			t.Cleanup(
				func() {
					err = sys.Stop(ctx)
					assert.NoError(t, err)
				},
			)
		},
	)
	t.Run(
		"With Spawn an actor already exist", func(t *testing.T) {
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
			lib.Pause(time.Second)

			t.Cleanup(
				func() {
					err = sys.Stop(ctx)
					assert.NoError(t, err)
				},
			)
		},
	)
	t.Run(
		"With RemoteActor/ActorOf with clustering enabled", func(t *testing.T) {
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
				WithPassivationDisabled(),
				WithLogger(logger),
				WithRemoting(host, int32(remotingPort)),
				WithClustering(provider, 9, 1, gossipPort, clusterPort, new(testActor)),
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
			lib.Pause(time.Second)

			// create an actor
			actorName := uuid.NewString()
			actor := newTestActor()
			actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
			assert.NoError(t, err)
			assert.NotNil(t, actorRef)

			// wait for a while for replication to take effect
			// otherwise the subsequent test will return actor not found
			lib.Pause(time.Second)

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
			reply, err := remoting.RemoteAsk(ctx, addr, new(testpb.TestReply), 20*time.Second)
			require.NoError(t, err)
			require.NotNil(t, reply)

			// get the actor partition
			partition := newActorSystem.GetPartition(actorName)
			assert.GreaterOrEqual(t, partition, uint64(0))

			// assert actor not found
			actorName = "some-actor"
			addr, pid, err := newActorSystem.ActorOf(ctx, actorName)
			require.Error(t, err)
			require.EqualError(t, err, ErrActorNotFound(actorName).Error())
			require.Nil(t, addr)
			require.Nil(t, pid)

			remoteAddr, err = newActorSystem.RemoteActor(ctx, actorName)
			require.Error(t, err)
			require.EqualError(t, err, ErrActorNotFound(actorName).Error())
			require.Nil(t, remoteAddr)

			// stop the actor after some time
			lib.Pause(time.Second)

			remoting.Close()
			t.Cleanup(
				func() {
					err = newActorSystem.Stop(ctx)
					assert.NoError(t, err)
					provider.AssertExpectations(t)
				},
			)
		},
	)
	t.Run(
		"With remoting enabled", func(t *testing.T) {
			ctx := context.TODO()
			remotingPort := dynaport.Get(1)[0]

			logger := log.DiscardLogger
			host := "127.0.0.1"

			newActorSystem, err := NewActorSystem(
				"test",
				WithPassivationDisabled(),
				WithLogger(logger),
				WithRemoting(host, int32(remotingPort)),
			)
			require.NoError(t, err)

			// start the actor system
			err = newActorSystem.Start(ctx)
			require.NoError(t, err)

			// wait for the cluster to fully start
			lib.Pause(time.Second)

			// create an actor
			actorName := uuid.NewString()

			addr, pid, err := newActorSystem.ActorOf(ctx, actorName)
			require.Error(t, err)
			require.EqualError(t, err, ErrMethodCallNotAllowed.Error())
			require.Nil(t, addr)
			require.Nil(t, pid)

			t.Cleanup(
				func() {
					err = newActorSystem.Stop(ctx)
					assert.NoError(t, err)
				},
			)
		},
	)
	t.Run(
		"With ActorOf:remoting not enabled", func(t *testing.T) {
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
			require.NotNil(t, addr)

			// stop the actor after some time
			lib.Pause(time.Second)

			t.Cleanup(
				func() {
					err = sys.Stop(ctx)
					assert.NoError(t, err)
				},
			)
		},
	)
	t.Run(
		"With ActorOf: not found", func(t *testing.T) {
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
			lib.Pause(time.Second)

			t.Cleanup(
				func() {
					err = sys.Stop(ctx)
					assert.NoError(t, err)
				},
			)
		},
	)
	t.Run(
		"With ActorOf actor system started", func(t *testing.T) {
			ctx := context.TODO()
			remotingPort := dynaport.Get(1)[0]

			logger := log.DiscardLogger
			host := "127.0.0.1"

			newActorSystem, err := NewActorSystem(
				"test",
				WithPassivationDisabled(),
				WithLogger(logger),
				WithRemoting(host, int32(remotingPort)),
			)
			require.NoError(t, err)

			// create an actor
			actorName := uuid.NewString()

			addr, pid, err := newActorSystem.ActorOf(ctx, actorName)
			require.Error(t, err)
			require.EqualError(t, err, ErrActorSystemNotStarted.Error())
			require.Nil(t, addr)
			require.Nil(t, pid)
		},
	)
	t.Run(
		"With ReSpawn", func(t *testing.T) {
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
			reply, err := Ask(ctx, actorRef, new(testpb.TestReply), replyTimeout)
			require.NoError(t, err)
			require.NotNil(t, reply)
			expected := new(testpb.Reply)
			require.True(t, proto.Equal(expected, reply))
			require.True(t, actorRef.IsRunning())

			// wait for a while for the system to stop
			lib.Pause(time.Second)
			// restart the actor
			_, err = sys.ReSpawn(ctx, actorName)
			require.NoError(t, err)

			// wait for the actor to complete start
			// TODO we can add a callback for complete start
			lib.Pause(time.Second)
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
		},
	)
	t.Run(
		"With ReSpawn with PreStart failure", func(t *testing.T) {
			ctx := context.TODO()
			sys, _ := NewActorSystem(
				"testSys",
				WithLogger(log.DiscardLogger),
				WithExpireActorAfter(time.Minute),
			)

			// start the actor system
			err := sys.Start(ctx)
			assert.NoError(t, err)

			actorName := "actor"
			actorRef, err := sys.Spawn(ctx, actorName, newTestRestart())
			assert.NoError(t, err)
			assert.NotNil(t, actorRef)

			require.True(t, actorRef.IsRunning())

			// wait for a while for the system to stop
			lib.Pause(time.Second)
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
		},
	)
	t.Run(
		"With ReSpawn: actor not found", func(t *testing.T) {
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
			lib.Pause(time.Second)

			err = sys.Kill(ctx, actorName)
			require.NoError(t, err)

			// wait for a while for the system to stop
			lib.Pause(time.Second)
			// restart the actor
			_, err = sys.ReSpawn(ctx, actorName)
			require.Error(t, err)

			t.Cleanup(
				func() {
					err = sys.Stop(ctx)
					assert.NoError(t, err)
				},
			)
		},
	)
	t.Run(
		"With ReSpawn an actor when not System started", func(t *testing.T) {
			ctx := context.TODO()
			sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
			_, err := sys.ReSpawn(ctx, "some-actor")
			assert.Error(t, err)
			assert.EqualError(t, err, "actor system has not started yet")
		},
	)
	t.Run(
		"ReSpawn with remoting enabled", func(t *testing.T) {
			ctx := context.TODO()
			remotingPort := dynaport.Get(1)[0]

			logger := log.DiscardLogger
			host := "127.0.0.1"

			newActorSystem, err := NewActorSystem(
				"test",
				WithPassivationDisabled(),
				WithLogger(logger),
				WithRemoting(host, int32(remotingPort)),
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
			lib.Pause(time.Second)

			// restart the actor
			_, err = newActorSystem.ReSpawn(ctx, actorName)
			require.NoError(t, err)

			// wait for the actor to complete start
			// TODO we can add a callback for complete start
			lib.Pause(time.Second)
			require.True(t, actorRef.IsRunning())

			t.Cleanup(
				func() {
					err = newActorSystem.Stop(ctx)
					assert.NoError(t, err)
				},
			)
		},
	)
	t.Run(
		"With NumActors", func(t *testing.T) {
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
			lib.Pause(time.Second)

			assert.EqualValues(t, 1, sys.NumActors())

			t.Cleanup(
				func() {
					err = sys.Stop(ctx)
					assert.NoError(t, err)
				},
			)
		},
	)
	t.Run(
		"With remoting enabled: Actor not found", func(t *testing.T) {
			ctx := context.TODO()
			remotingPort := dynaport.Get(1)[0]

			logger := log.DiscardLogger
			host := "127.0.0.1"

			newActorSystem, err := NewActorSystem(
				"test",
				WithPassivationDisabled(),
				WithLogger(logger),
				WithRemoting(host, int32(remotingPort)),
			)
			require.NoError(t, err)

			// start the actor system
			err = newActorSystem.Start(ctx)
			require.NoError(t, err)

			lib.Pause(time.Second)

			remoting := NewRemoting()
			actorName := "some-actor"
			addr, err := remoting.RemoteLookup(ctx, host, remotingPort, actorName)
			require.NoError(t, err)
			require.Nil(t, addr)

			// attempt to send a message will fail
			addr = address.From(
				&goaktpb.Address{
					Host: host,
					Port: int32(remotingPort),
					Name: actorName,
					Id:   "",
				},
			)
			reply, err := remoting.RemoteAsk(ctx, addr, new(testpb.TestReply), 20*time.Second)
			require.Error(t, err)
			require.Nil(t, reply)

			// stop the actor after some time
			lib.Pause(time.Second)

			remoting.Close()
			t.Cleanup(
				func() {
					err = newActorSystem.Stop(ctx)
					assert.NoError(t, err)
				},
			)
		},
	)
	t.Run(
		"With RemoteActor failure when system not started", func(t *testing.T) {
			ctx := context.TODO()
			remotingPort := dynaport.Get(1)[0]

			logger := log.DiscardLogger
			host := "127.0.0.1"

			newActorSystem, err := NewActorSystem(
				"test",
				WithPassivationDisabled(),
				WithLogger(logger),
				WithRemoting(host, int32(remotingPort)),
			)
			require.NoError(t, err)

			actorName := "some-actor"
			remoteAddr, err := newActorSystem.RemoteActor(ctx, actorName)
			require.Error(t, err)
			require.EqualError(t, err, ErrActorSystemNotStarted.Error())
			require.Nil(t, remoteAddr)
		},
	)
	t.Run(
		"With RemoteActor failure when system not started", func(t *testing.T) {
			ctx := context.TODO()
			remotingPort := dynaport.Get(1)[0]

			logger := log.DiscardLogger
			host := "127.0.0.1"

			newActorSystem, err := NewActorSystem(
				"test",
				WithPassivationDisabled(),
				WithLogger(logger),
				WithRemoting(host, int32(remotingPort)),
			)
			require.NoError(t, err)

			err = newActorSystem.Stop(ctx)
			require.Error(t, err)
			require.EqualError(t, err, ErrActorSystemNotStarted.Error())
		},
	)
	t.Run(
		"With RemoteActor failure when cluster is not enabled", func(t *testing.T) {
			ctx := context.TODO()
			remotingPort := dynaport.Get(1)[0]

			logger := log.DiscardLogger
			host := "127.0.0.1"

			newActorSystem, err := NewActorSystem(
				"test",
				WithPassivationDisabled(),
				WithLogger(logger),
				WithRemoting(host, int32(remotingPort)),
			)
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
			lib.Pause(time.Second)

			t.Cleanup(
				func() {
					err = newActorSystem.Stop(ctx)
					assert.NoError(t, err)
				},
			)
		},
	)
	t.Run(
		"With LocalActor", func(t *testing.T) {
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
			lib.Pause(time.Second)

			t.Cleanup(
				func() {
					err = sys.Stop(ctx)
					assert.NoError(t, err)
				},
			)
		},
	)
	t.Run(
		"With LocalActor: Actor not found", func(t *testing.T) {
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
			lib.Pause(time.Second)

			t.Cleanup(
				func() {
					err = sys.Stop(ctx)
					assert.NoError(t, err)
				},
			)
		},
	)
	t.Run(
		"With LocalActor when system not started", func(t *testing.T) {
			sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

			// create an actor
			actorName := "exchanger"

			// locate the actor
			local, err := sys.LocalActor(actorName)
			require.Error(t, err)
			require.EqualError(t, err, ErrActorSystemNotStarted.Error())
			require.Nil(t, local)
		},
	)
	t.Run(
		"With Kill an actor when not System started", func(t *testing.T) {
			ctx := context.TODO()
			sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
			err := sys.Kill(ctx, "Test")
			assert.Error(t, err)
			assert.EqualError(t, err, "actor system has not started yet")
		},
	)
	t.Run(
		"With Kill an actor when actor not found", func(t *testing.T) {
			ctx := context.TODO()
			sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

			// start the actor system
			err := sys.Start(ctx)
			assert.NoError(t, err)
			err = sys.Kill(ctx, "Test")
			assert.Error(t, err)
			assert.EqualError(t, err, "actor=goakt://testSys@127.0.0.1:0/Test not found")
			t.Cleanup(
				func() {
					err = sys.Stop(ctx)
					assert.NoError(t, err)
				},
			)
		},
	)
	t.Run(
		"With housekeeping", func(t *testing.T) {
			ctx := context.TODO()
			sys, _ := NewActorSystem(
				"housekeeperSys",
				WithLogger(log.DiscardLogger),
				WithExpireActorAfter(passivateAfter),
			)

			// start the actor system
			err := sys.Start(ctx)
			assert.NoError(t, err)

			// wait for the system to properly start
			lib.Pause(time.Second)

			actorName := "HousekeeperActor"
			actorHandler := newTestActor()
			actorRef, err := sys.Spawn(ctx, actorName, actorHandler)
			assert.NoError(t, err)
			require.NotNil(t, actorRef)

			// wait for the actor to properly start
			lib.Pause(time.Second)

			// locate the actor
			ref, err := sys.LocalActor(actorName)
			require.Error(t, err)
			require.Nil(t, ref)
			require.EqualError(t, err, ErrActorNotFound(actorName).Error())

			// stop the actor after some time
			lib.Pause(time.Second)

			t.Cleanup(
				func() {
					err = sys.Stop(ctx)
					assert.NoError(t, err)
				},
			)
		},
	)
	t.Run(
		"With GetPartition returning zero in non cluster env", func(t *testing.T) {
			ctx := context.TODO()
			sys, _ := NewActorSystem(
				"housekeeperSys",
				WithLogger(log.DiscardLogger),
				WithExpireActorAfter(passivateAfter),
			)

			// start the actor system
			err := sys.Start(ctx)
			assert.NoError(t, err)

			// wait for the system to properly start
			lib.Pause(time.Second)

			partition := sys.GetPartition("some-actor")
			assert.Zero(t, partition)

			t.Cleanup(
				func() {
					err = sys.Stop(ctx)
					assert.NoError(t, err)
				},
			)
		},
	)
	t.Run(
		"With actor PostStop error", func(t *testing.T) {
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
			lib.Pause(time.Second)

			t.Cleanup(
				func() {
					assert.Error(t, sys.Stop(ctx))
				},
			)
		},
	)
	t.Run(
		"With deadletters subscription ", func(t *testing.T) {
			ctx := context.TODO()
			sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

			// start the actor system
			err := sys.Start(ctx)
			assert.NoError(t, err)

			// wait for complete start
			lib.Pause(time.Second)

			// create a deadletter subscriber
			consumer, err := sys.Subscribe()
			require.NoError(t, err)
			require.NotNil(t, consumer)

			// create the black hole actor
			actor := &discarder{}
			actorRef, err := sys.Spawn(ctx, "discarder", actor)
			assert.NoError(t, err)
			assert.NotNil(t, actorRef)

			// wait a while
			lib.Pause(time.Second)

			// every message sent to the actor will result in deadletters
			for i := 0; i < 5; i++ {
				require.NoError(t, Tell(ctx, actorRef, new(testpb.TestSend)))
			}

			lib.Pause(time.Second)

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

			t.Cleanup(
				func() {
					err = sys.Stop(ctx)
					assert.NoError(t, err)
				},
			)
		},
	)
	t.Run(
		"With deadletters subscription when not started", func(t *testing.T) {
			sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

			// create a deadletter subscriber
			consumer, err := sys.Subscribe()
			require.Error(t, err)
			require.Nil(t, consumer)
		},
	)
	t.Run(
		"With deadletters unsubscription when not started", func(t *testing.T) {
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

			lib.Pause(time.Second)

			// create a deadletter subscriber
			err = sys.Unsubscribe(consumer)
			require.Error(t, err)
		},
	)
	t.Run(
		"With Passivation with clustering enabled", func(t *testing.T) {
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
				WithExpireActorAfter(passivateAfter),
				WithLogger(logger),
				WithRemoting(host, int32(remotingPort)),
				WithClustering(provider, 9, 1, gossipPort, clusterPort, new(testActor)),
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
			lib.Pause(time.Second)

			// create an actor
			actorName := uuid.NewString()
			actor := newTestActor()
			actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
			assert.NoError(t, err)
			assert.NotNil(t, actorRef)

			// wait for a while for replication to take effect
			// otherwise the subsequent test will return actor not found
			lib.Pause(time.Second)

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
			lib.Pause(time.Second)

			t.Cleanup(
				func() {
					err = newActorSystem.Stop(ctx)
					assert.NoError(t, err)
					provider.AssertExpectations(t)
				},
			)
		},
	)
	t.Run(
		"With cluster events subscription", func(t *testing.T) {
			// create a context
			ctx := context.TODO()
			// start the NATS server
			srv := startNatsServer(t)

			// create and start system cluster
			cl1, sd1 := startClusterSystem(t, srv.Addr().String())
			peerAddress1 := cl1.PeerAddress()
			require.NotEmpty(t, peerAddress1)

			// create a subscriber to node 1
			subscriber1, err := cl1.Subscribe()
			require.NoError(t, err)
			require.NotNil(t, subscriber1)

			// create and start system cluster
			cl2, sd2 := startClusterSystem(t, srv.Addr().String())
			peerAddress2 := cl2.PeerAddress()
			require.NotEmpty(t, peerAddress2)

			// create a subscriber to node 2
			subscriber2, err := cl2.Subscribe()
			require.NoError(t, err)
			require.NotNil(t, subscriber2)

			// wait for some time
			lib.Pause(time.Second)

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
			lib.Pause(time.Second)

			// stop the node
			require.NoError(t, cl1.Unsubscribe(subscriber1))
			assert.NoError(t, cl1.Stop(ctx))
			assert.NoError(t, sd1.Close())

			// wait for some time
			lib.Pause(time.Second)

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

			t.Cleanup(
				func() {
					assert.NoError(t, cl2.Stop(ctx))
					// stop the discovery engines
					assert.NoError(t, sd2.Close())
					// shutdown the nats server gracefully
					srv.Shutdown()
				},
			)
		},
	)
	t.Run(
		"With PeerAddress empty when cluster not enabled", func(t *testing.T) {
			ctx := context.TODO()
			sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

			// start the actor system
			err := sys.Start(ctx)
			assert.NoError(t, err)
			require.Empty(t, sys.PeerAddress())

			require.NoError(t, sys.Stop(ctx))
		},
	)
	t.Run(
		"With SpawnFromFunc (cluster/remote enabled)", func(t *testing.T) {
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
				WithPassivationDisabled(),
				WithLogger(logger),
				WithRemoting(host, int32(remotingPort)),
				WithClustering(provider, 9, 1, gossipPort, clusterPort, new(testActor)),
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
			lib.Pause(time.Second)

			// send a message to the actor
			require.NoError(t, Tell(ctx, actorRef, &testpb.Reply{Content: "test spawn from func"}))

			t.Cleanup(
				func() {
					err = newActorSystem.Stop(ctx)
					assert.NoError(t, err)
					provider.AssertExpectations(t)
				},
			)
		},
	)
	t.Run(
		"With SpawnFromFunc with PreStart error", func(t *testing.T) {
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
		},
	)
	t.Run(
		"With SpawnFromFunc with PreStop error", func(t *testing.T) {
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
			lib.Pause(time.Second)

			// send a message to the actor
			require.NoError(t, Tell(ctx, actorRef, &testpb.Reply{Content: "test spawn from func"}))

			t.Cleanup(
				func() {
					assert.Error(t, sys.Stop(ctx))
				},
			)
		},
	)
	t.Run(
		"With SpawnFromFunc with actorSystem not started", func(t *testing.T) {
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
			assert.EqualError(t, err, ErrActorSystemNotStarted.Error())
			assert.Nil(t, actorRef)
		},
	)
	t.Run(
		"With happy path Register", func(t *testing.T) {
			ctx := context.TODO()
			logger := log.DiscardLogger

			// create the actor system
			sys, err := NewActorSystem(
				"test",
				WithLogger(logger),
				WithPassivationDisabled(),
			)
			// assert there are no error
			require.NoError(t, err)

			// start the actor system
			err = sys.Start(ctx)
			assert.NoError(t, err)

			// register the actor
			err = sys.Register(ctx, &exchanger{})
			require.NoError(t, err)

			t.Cleanup(
				func() {
					err = sys.Stop(ctx)
					assert.NoError(t, err)
				},
			)
		},
	)
	t.Run(
		"With Register when actor system not started", func(t *testing.T) {
			ctx := context.TODO()
			logger := log.DiscardLogger

			// create the actor system
			sys, err := NewActorSystem(
				"test",
				WithLogger(logger),
				WithPassivationDisabled(),
			)
			// assert there are no error
			require.NoError(t, err)

			// register the actor
			err = sys.Register(ctx, &exchanger{})
			require.Error(t, err)
			assert.EqualError(t, err, ErrActorSystemNotStarted.Error())

			t.Cleanup(
				func() {
					err = sys.Stop(ctx)
					assert.Error(t, err)
				},
			)
		},
	)
	t.Run(
		"With happy path Deregister", func(t *testing.T) {
			ctx := context.TODO()
			logger := log.DiscardLogger

			// create the actor system
			sys, err := NewActorSystem(
				"test",
				WithLogger(logger),
				WithPassivationDisabled(),
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
		},
	)
	t.Run(
		"With Deregister when actor system not started", func(t *testing.T) {
			ctx := context.TODO()
			logger := log.DiscardLogger

			// create the actor system
			sys, err := NewActorSystem(
				"test",
				WithLogger(logger),
				WithPassivationDisabled(),
			)
			// assert there are no error
			require.NoError(t, err)

			err = sys.Deregister(ctx, &exchanger{})
			require.Error(t, err)
			assert.EqualError(t, err, ErrActorSystemNotStarted.Error())

			t.Cleanup(
				func() {
					err = sys.Stop(ctx)
					assert.Error(t, err)
				},
			)
		},
	)
	t.Run(
		"With cluster start failure with remoting not enabled", func(t *testing.T) {
			ctx := context.TODO()
			logger := log.DiscardLogger
			mockedCluster := new(clustermocks.Interface)
			mockedErr := errors.New("failed to start")
			mockedCluster.EXPECT().Start(ctx).Return(mockedErr)

			// mock the discovery provider
			provider := new(testkit.Provider)
			provider.EXPECT().ID().Return("id")

			system := &actorSystem{
				name:          "testSystem",
				logger:        logger,
				cluster:       mockedCluster,
				locker:        sync.Mutex{},
				scheduler:     newScheduler(logger, time.Second, withSchedulerCluster(mockedCluster)),
				clusterConfig: NewClusterConfig(),
				registry:      types.NewRegistry(),
			}
			system.clusterEnabled.Store(true)

			err := system.Start(ctx)
			require.Error(t, err)
			assert.EqualError(t, err, "clustering needs remoting to be enabled")
		},
	)
	t.Run(
		"With RemoteSpawn with clustering enabled", func(t *testing.T) {
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
				WithPassivationDisabled(),
				WithLogger(logger),
				WithRemoting(host, int32(remotingPort)),
				WithClustering(provider, 9, 1, gossipPort, clusterPort, new(exchanger)),
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
			lib.Pause(time.Second)

			remoting := NewRemoting()
			// create an actor
			actorName := "actorID"
			// fetching the address of the that actor should return nil address
			addr, err := remoting.RemoteLookup(ctx, host, remotingPort, actorName)
			require.NoError(t, err)
			require.Nil(t, addr)

			// spawn the remote actor
			err = remoting.RemoteSpawn(ctx, host, remotingPort, actorName, "actors.exchanger")
			require.NoError(t, err)

			// re-fetching the address of the actor should return not nil address after start
			addr, err = remoting.RemoteLookup(ctx, host, remotingPort, actorName)
			require.NoError(t, err)
			require.NotNil(t, addr)

			// send the message to exchanger actor one using remote messaging
			reply, err := remoting.RemoteAsk(ctx, addr, new(testpb.TestReply), 20*time.Second)

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
		},
	)
	t.Run(
		"With Spawn with custom mailbox", func(t *testing.T) {
			ctx := context.TODO()
			actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

			// start the actor system
			err := actorSystem.Start(ctx)
			assert.NoError(t, err)

			// wait for complete start
			lib.Pause(time.Second)

			// create the black hole actor
			actor := newTestActor()
			pid, err := actorSystem.Spawn(ctx, "test", actor, WithMailbox(NewBoundedMailbox(10)))
			assert.NoError(t, err)
			assert.NotNil(t, pid)

			// wait a while
			lib.Pause(time.Second)
			assert.EqualValues(t, 1, pid.ProcessedCount())
			require.True(t, pid.IsRunning())

			// every message sent to the actor will result in deadletters
			counter := 0
			for i := 1; i <= 5; i++ {
				require.NoError(t, Tell(ctx, pid, new(testpb.TestSend)))
				counter = counter + 1
			}

			lib.Pause(time.Second)

			assert.EqualValues(t, counter, pid.ProcessedCount()-1)
			require.NoError(t, err)

			t.Cleanup(
				func() {
					err = actorSystem.Stop(ctx)
					assert.NoError(t, err)
				},
			)
		},
	)
}
