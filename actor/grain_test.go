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
	"errors"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/util"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/remote"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

// nolint
func TestGrain(t *testing.T) {
	t.Run("With without cluster mode", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		util.Pause(time.Second)

		// create a grain instance
		grain := NewMockGrain()
		identity, err := testSystem.GetGrain(ctx, "testGrain", func(ctx context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)
		require.NotNil(t, identity)

		// prepare a message to send to the grain
		message := new(testpb.TestSend)
		response, err := testSystem.AskGrain(ctx, identity, message)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.IsType(t, &testpb.Reply{}, response)

		// check if the grain is activated
		gp, ok := testSystem.(*actorSystem).grains.Get(*identity)
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isRunning())

		response, err = testSystem.AskGrain(ctx, identity, message)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.IsType(t, &testpb.Reply{}, response)

		// let us shutdown the grain by sending PoisonPill
		response, err = testSystem.AskGrain(ctx, identity, new(goaktpb.PoisonPill))
		require.NoError(t, err)
		require.NotNil(t, response)

		// check if the grain is activated
		gp, ok = testSystem.(*actorSystem).grains.Get(*identity)
		require.True(t, ok)
		require.NotNil(t, gp)
		require.False(t, gp.isRunning())

		// send a message to the grain to reactivate it
		response, err = testSystem.AskGrain(ctx, identity, message)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.IsType(t, &testpb.Reply{}, response)

		// check if the grain is activated
		gp, ok = testSystem.(*actorSystem).grains.Get(*identity)
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isRunning())

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("With cluster mode", func(t *testing.T) {
		ctx := t.Context()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start a system cluster
		node1, sd1 := testCluster(t, srv.Addr().String())
		require.NotNil(t, node1)
		require.NotNil(t, sd1)

		// create and start a system cluster
		node2, sd2 := testCluster(t, srv.Addr().String())
		require.NotNil(t, node2)
		require.NotNil(t, sd2)

		// create and start a system cluster
		node3, sd3 := testCluster(t, srv.Addr().String())
		require.NotNil(t, node3)
		require.NotNil(t, sd3)

		// create a grain instance
		grain := NewMockGrain()
		identity, err := node1.GetGrain(ctx, "testGrain", func(ctx context.Context) (Grain, error) {
			return grain, nil
		})
		require.NotNil(t, identity)
		require.NoError(t, err)

		// prepare a message to send to the grain
		message := new(testpb.TestSend)
		response, err := node1.AskGrain(ctx, identity, message)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.IsType(t, &testpb.Reply{}, response)

		util.Pause(time.Second)

		// check if the grain is activated
		gp, ok := node1.(*actorSystem).grains.Get(*identity)
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isRunning())

		response, err = node2.AskGrain(ctx, identity, message)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.IsType(t, &testpb.Reply{}, response)

		util.Pause(time.Second)

		// let us shutdown the grain by sending PoisonPill
		err = node3.TellGrain(ctx, identity, new(goaktpb.PoisonPill))
		require.NoError(t, err)

		util.Pause(time.Second)

		// check if the grain is activated
		gp, ok = node1.(*actorSystem).grains.Get(*identity)
		require.True(t, ok)
		require.NotNil(t, gp)
		require.False(t, gp.isRunning())

		// send a message to the grain to reactivate it
		response, err = node3.AskGrain(ctx, identity, message)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.IsType(t, &testpb.Reply{}, response)

		util.Pause(time.Second)

		// check if the grain is activated
		gp, ok = node1.(*actorSystem).grains.Get(*identity)
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isRunning())

		// let us shutdown the grain by sending PoisonPill
		err = node3.TellGrain(ctx, identity, new(goaktpb.PoisonPill))
		require.NoError(t, err)

		// check if the grain is activated
		gp, ok = node1.(*actorSystem).grains.Get(*identity)
		require.True(t, ok)
		require.NotNil(t, gp)
		require.False(t, gp.isRunning())

		assert.NoError(t, node1.Stop(ctx))
		assert.NoError(t, node3.Stop(ctx))
		assert.NoError(t, sd1.Close())
		assert.NoError(t, sd3.Close())
		srv.Shutdown()
	})
	t.Run("With activation error", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		util.Pause(time.Second)

		// create a grain instance
		grain := NewMockGrainActivation()
		identity, err := testSystem.GetGrain(ctx, "testGrain", func(ctx context.Context) (Grain, error) {
			return grain, nil
		})
		require.Error(t, err)
		require.ErrorIs(t, err, ErrGrainActivationFailure)
		require.Nil(t, identity)

		// prepare a message to send to the grain
		//message := new(testpb.TestSend)
		//response, err := testSystem.SendGrainSync(ctx, identity, message)
		//require.Error(t, err)
		//require.ErrorIs(t, err, ErrGrainActivationFailure)
		//require.Nil(t, response)

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("With deactivation error", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		util.Pause(time.Second)

		// create a grain instance
		grain := NewMockGrainDeactivation()
		identity, err := testSystem.GetGrain(ctx, "testGrain", func(ctx context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)
		require.NotNil(t, identity)

		// prepare a message to send to the grain
		message := new(testpb.TestSend)
		err = testSystem.TellGrain(ctx, identity, message)
		require.NoError(t, err)

		util.Pause(500 * time.Millisecond)

		// check if the grain is activated
		gp, ok := testSystem.(*actorSystem).grains.Get(*identity)
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isRunning())

		// let us shutdown the grain by sending PoisonPill
		err = testSystem.TellGrain(ctx, identity, new(goaktpb.PoisonPill))
		require.Error(t, err)
		require.ErrorIs(t, err, ErrGrainDeactivationFailure)

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("With message handling errors", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		util.Pause(time.Second)

		// create a grain instance
		grain := NewMockGrainError()
		identity, err := testSystem.GetGrain(ctx, "testGrain", func(ctx context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)
		require.NotNil(t, identity)

		// prepare a message to send to the grain
		message := new(testpb.TestSend)
		response, err := testSystem.AskGrain(ctx, identity, message)
		require.Error(t, err)
		require.Nil(t, response)

		// check if the grain is activated
		gp, ok := testSystem.(*actorSystem).grains.Get(*identity)
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isRunning())

		err = testSystem.TellGrain(ctx, identity, message)
		require.Error(t, err)

		gp, ok = testSystem.(*actorSystem).grains.Get(*identity)
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isRunning())

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("With reserved sender name", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		util.Pause(time.Second)

		// create a grain instance
		grain := NewMockGrain()
		identity, err := testSystem.GetGrain(ctx, "testGrain", func(ctx context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)
		require.NotNil(t, identity)

		sender := newIdentity(grain, "GoAktSender")
		message := new(testpb.TestSend)
		response, err := testSystem.AskGrain(ctx, identity, message, WithRequestSender(sender))
		require.Error(t, err)
		require.ErrorIs(t, err, ErrReservedName)
		require.Nil(t, response)

		err = testSystem.TellGrain(ctx, identity, message, WithRequestSender(sender))
		require.Error(t, err)
		require.ErrorIs(t, err, ErrReservedName)

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("With invalid grain identity", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		util.Pause(time.Second)

		identity := &Identity{}
		message := new(testpb.TestSend)
		response, err := testSystem.AskGrain(ctx, identity, message)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidGrainIdentity)
		require.Nil(t, response)

		err = testSystem.TellGrain(ctx, identity, message)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidGrainIdentity)

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("With reserved name as grain identity", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		util.Pause(time.Second)

		// create a grain instance
		grain := NewMockGrain()
		identity, err := testSystem.GetGrain(ctx, "GoAktGrain", func(ctx context.Context) (Grain, error) {
			return grain, nil
		})
		require.Nil(t, identity)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrReservedName)

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("When actor system not started", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// prepare a message to send to the grain
		message := new(testpb.TestSend)
		response, err := testSystem.AskGrain(ctx, &Identity{}, message)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrActorSystemNotStarted)
		require.Nil(t, response)

		err = testSystem.TellGrain(ctx, &Identity{}, message)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrActorSystemNotStarted)
	})
	t.Run("With PersistentGrain", func(t *testing.T) {
		ctx := t.Context()
		// create the state store extension
		stateStoreExtension := NewMockExtension()

		testSystem, err := NewActorSystem("testSys",
			WithExtensions(stateStoreExtension),
			WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		util.Pause(time.Second)

		// create a grain instance
		grain := NewPersistentGrain()
		identity, err := testSystem.GetGrain(ctx, "testGrain", func(ctx context.Context) (Grain, error) {
			return grain, nil
		})
		require.NotNil(t, identity)
		require.NoError(t, err)

		var message proto.Message
		// prepare a message to send to the grain
		message = &testpb.CreateAccount{
			AccountBalance: 500.00,
		}
		err = testSystem.TellGrain(ctx, identity, message)
		require.NoError(t, err)

		// check if the grain is activated
		gp, ok := testSystem.(*actorSystem).grains.Get(*identity)
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isRunning())

		message = &testpb.CreditAccount{
			Balance: 500.00,
		}

		response, err := testSystem.AskGrain(ctx, identity, message)
		require.NoError(t, err)
		require.NotNil(t, response)
		actual := response.(*testpb.Account)
		require.EqualValues(t, 1000.00, actual.GetAccountBalance())

		// let us shutdown the grain by sending PoisonPill
		response, err = testSystem.AskGrain(ctx, identity, new(goaktpb.PoisonPill))
		require.NoError(t, err)
		require.NotNil(t, response)

		// check if the grain is activated
		gp, ok = testSystem.(*actorSystem).grains.Get(*identity)
		require.True(t, ok)
		require.NotNil(t, gp)
		require.False(t, gp.isRunning())

		// send a message to the grain to reactivate it
		response, err = testSystem.AskGrain(ctx, identity, message)
		require.NoError(t, err)
		require.NotNil(t, response)
		actual = response.(*testpb.Account)
		require.EqualValues(t, 1500.00, actual.GetAccountBalance())

		// check if the grain is activated
		gp, ok = testSystem.(*actorSystem).grains.Get(*identity)
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isRunning())

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("GetGrain when not started returns error", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)
		// create a grain instance
		grain := NewMockGrain()
		identity, err := testSystem.GetGrain(ctx, "testGrain", func(ctx context.Context) (Grain, error) {
			return grain, nil
		})
		require.Error(t, err)
		require.ErrorIs(t, err, ErrActorSystemNotStarted)
		require.Nil(t, identity)
	})
	t.Run("GetGrain when not started returns error", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		util.Pause(time.Second)

		// create a grain instance
		identity, err := testSystem.GetGrain(ctx, "testGrain", func(ctx context.Context) (Grain, error) {
			return nil, assert.AnError
		})
		require.Error(t, err)
		require.ErrorIs(t, err, assert.AnError)
		require.Nil(t, identity)

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("GetGrain with invalid indentity name", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		util.Pause(time.Second)

		// create a grain instance
		name := strings.Repeat("a", 300)
		identity, err := testSystem.GetGrain(ctx, name, func(ctx context.Context) (Grain, error) {
			return NewMockGrain(), nil
		})
		require.Error(t, err)
		require.Nil(t, identity)

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("With invalid sender name", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		util.Pause(time.Second)

		// create a grain instance
		grain := NewMockGrain()
		identity, err := testSystem.GetGrain(ctx, "testGrain", func(ctx context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)
		require.NotNil(t, identity)

		sender := &Identity{}
		message := new(testpb.TestSend)
		response, err := testSystem.AskGrain(ctx, identity, message, WithRequestSender(sender))
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidGrainIdentity)
		require.Nil(t, response)

		err = testSystem.TellGrain(ctx, identity, message, WithRequestSender(sender))
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidGrainIdentity)

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("Remoting failed when remoting not enabled", func(t *testing.T) {
		ctx := t.Context()
		ports := dynaport.Get(1)
		remotingPort := ports[0]
		host := "127.0.0.1"
		timeout := 5 * time.Second

		testSystem, err := NewActorSystem(
			"testSys",
			WithLogger(log.DiscardLogger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		util.Pause(time.Second)

		// disable remoting for the sake of the test
		testSystem.(*actorSystem).remotingEnabled.Store(false)

		// create a wire
		grain := &internalpb.Grain{
			GrainId: &internalpb.GrainId{
				Kind:  "some-kind",
				Name:  "some-name",
				Value: "some-value",
			},
			Host:         host,
			Port:         int32(remotingPort),
			Dependencies: nil,
		}

		serialized, _ := anypb.New(grain)
		remoteClient := testSystem.getRemoting().remotingServiceClient(grain.GetHost(), int(grain.GetPort()))

		_, err = remoteClient.RemoteTellGrain(ctx, connect.NewRequest(&internalpb.RemoteTellGrainRequest{
			Grain:          grain,
			RequestTimeout: durationpb.New(timeout),
			Dependencies:   grain.GetDependencies(),
			Message:        serialized,
		}))
		require.Error(t, err)
		var connectErr *connect.Error
		require.True(t, errors.As(err, &connectErr))
		e := connectErr.Unwrap()
		require.ErrorContains(t, e, ErrRemotingDisabled.Error())

		_, err = remoteClient.RemoteAskGrain(ctx, connect.NewRequest(&internalpb.RemoteAskGrainRequest{
			Grain:          grain,
			RequestTimeout: durationpb.New(timeout),
			Dependencies:   grain.GetDependencies(),
			Message:        serialized,
		}))
		require.Error(t, err)
		require.True(t, errors.As(err, &connectErr))
		e = connectErr.Unwrap()
		require.ErrorContains(t, e, ErrRemotingDisabled.Error())

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("Remoting failed with invalid grain identity", func(t *testing.T) {
		ctx := t.Context()
		ports := dynaport.Get(1)
		remotingPort := ports[0]
		host := "127.0.0.1"
		timeout := 5 * time.Second

		testSystem, err := NewActorSystem(
			"testSys",
			WithLogger(log.DiscardLogger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		util.Pause(time.Second)

		// create a wire
		grain := &internalpb.Grain{
			GrainId:      &internalpb.GrainId{},
			Host:         host,
			Port:         int32(remotingPort),
			Dependencies: nil,
		}

		serialized, _ := anypb.New(grain)
		remoteClient := testSystem.getRemoting().remotingServiceClient(grain.GetHost(), int(grain.GetPort()))

		_, err = remoteClient.RemoteTellGrain(ctx, connect.NewRequest(&internalpb.RemoteTellGrainRequest{
			Grain:          grain,
			RequestTimeout: durationpb.New(timeout),
			Dependencies:   grain.GetDependencies(),
			Message:        serialized,
		}))
		require.Error(t, err)
		var connectErr *connect.Error
		require.True(t, errors.As(err, &connectErr))
		e := connectErr.Unwrap()
		require.ErrorContains(t, e, ErrInvalidGrainIdentity.Error())

		_, err = remoteClient.RemoteAskGrain(ctx, connect.NewRequest(&internalpb.RemoteAskGrainRequest{
			Grain:          grain,
			RequestTimeout: durationpb.New(timeout),
			Dependencies:   grain.GetDependencies(),
			Message:        serialized,
		}))
		require.Error(t, err)
		require.True(t, errors.As(err, &connectErr))
		e = connectErr.Unwrap()
		require.ErrorContains(t, e, ErrInvalidGrainIdentity.Error())

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("Remoting failed with reserved name grain identity", func(t *testing.T) {
		ctx := t.Context()
		ports := dynaport.Get(1)
		remotingPort := ports[0]
		host := "127.0.0.1"
		timeout := 5 * time.Second

		testSystem, err := NewActorSystem(
			"testSys",
			WithLogger(log.DiscardLogger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		util.Pause(time.Second)

		// create a wire
		grain := &internalpb.Grain{
			GrainId: &internalpb.GrainId{
				Kind:  "actor.MockGrain",
				Name:  "GoAktGrain",
				Value: "actor.MockGrain/GoAktGrain",
			},
			Host:         host,
			Port:         int32(remotingPort),
			Dependencies: nil,
		}

		serialized, _ := anypb.New(grain)
		remoteClient := testSystem.getRemoting().remotingServiceClient(grain.GetHost(), int(grain.GetPort()))

		_, err = remoteClient.RemoteTellGrain(ctx, connect.NewRequest(&internalpb.RemoteTellGrainRequest{
			Grain:          grain,
			RequestTimeout: durationpb.New(timeout),
			Dependencies:   grain.GetDependencies(),
			Message:        serialized,
		}))
		require.Error(t, err)
		var connectErr *connect.Error
		require.True(t, errors.As(err, &connectErr))
		e := connectErr.Unwrap()
		require.ErrorContains(t, e, ErrReservedName.Error())

		_, err = remoteClient.RemoteAskGrain(ctx, connect.NewRequest(&internalpb.RemoteAskGrainRequest{
			Grain:          grain,
			RequestTimeout: durationpb.New(timeout),
			Dependencies:   grain.GetDependencies(),
			Message:        serialized,
		}))
		require.Error(t, err)
		require.True(t, errors.As(err, &connectErr))
		e = connectErr.Unwrap()
		require.ErrorContains(t, e, ErrReservedName.Error())

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("Remoting failed with invalid grain identity as sender", func(t *testing.T) {
		ctx := t.Context()
		ports := dynaport.Get(1)
		remotingPort := ports[0]
		host := "127.0.0.1"
		timeout := 5 * time.Second

		testSystem, err := NewActorSystem(
			"testSys",
			WithLogger(log.DiscardLogger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		util.Pause(time.Second)

		// create a wire
		grain := &internalpb.Grain{
			GrainId: &internalpb.GrainId{
				Kind:  "actor.MockGrain",
				Name:  "me",
				Value: "actor.MockGrain/me",
			},
			Host:         host,
			Port:         int32(remotingPort),
			Dependencies: nil,
		}

		serialized, _ := anypb.New(grain)
		remoteClient := testSystem.getRemoting().remotingServiceClient(grain.GetHost(), int(grain.GetPort()))

		_, err = remoteClient.RemoteTellGrain(ctx, connect.NewRequest(&internalpb.RemoteTellGrainRequest{
			Grain:          grain,
			RequestTimeout: durationpb.New(timeout),
			Dependencies:   grain.GetDependencies(),
			Message:        serialized,
			Sender:         &internalpb.GrainId{},
		}))
		require.Error(t, err)
		var connectErr *connect.Error
		require.True(t, errors.As(err, &connectErr))
		e := connectErr.Unwrap()
		require.ErrorContains(t, e, ErrInvalidGrainIdentity.Error())

		_, err = remoteClient.RemoteAskGrain(ctx, connect.NewRequest(&internalpb.RemoteAskGrainRequest{
			Grain:          grain,
			RequestTimeout: durationpb.New(timeout),
			Dependencies:   grain.GetDependencies(),
			Message:        serialized,
			Sender:         &internalpb.GrainId{},
		}))
		require.Error(t, err)
		require.True(t, errors.As(err, &connectErr))
		e = connectErr.Unwrap()
		require.ErrorContains(t, e, ErrInvalidGrainIdentity.Error())

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("Remoting failed with reserved name grain identity as sender", func(t *testing.T) {
		ctx := t.Context()
		ports := dynaport.Get(1)
		remotingPort := ports[0]
		host := "127.0.0.1"
		timeout := 5 * time.Second

		testSystem, err := NewActorSystem(
			"testSys",
			WithLogger(log.DiscardLogger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		util.Pause(time.Second)

		// create a wire
		grain := &internalpb.Grain{
			GrainId: &internalpb.GrainId{
				Kind:  "actor.MockGrain",
				Name:  "me",
				Value: "actor.MockGrain/me",
			},
			Host:         host,
			Port:         int32(remotingPort),
			Dependencies: nil,
		}

		serialized, _ := anypb.New(grain)
		remoteClient := testSystem.getRemoting().remotingServiceClient(grain.GetHost(), int(grain.GetPort()))

		_, err = remoteClient.RemoteTellGrain(ctx, connect.NewRequest(&internalpb.RemoteTellGrainRequest{
			Grain:          grain,
			RequestTimeout: durationpb.New(timeout),
			Dependencies:   grain.GetDependencies(),
			Message:        serialized,
			Sender: &internalpb.GrainId{
				Kind:  "actor.MockGrain",
				Name:  "GoAktGrain",
				Value: "actor.MockGrain/GoAktGrain",
			},
		}))
		require.Error(t, err)
		var connectErr *connect.Error
		require.True(t, errors.As(err, &connectErr))
		e := connectErr.Unwrap()
		require.ErrorContains(t, e, ErrReservedName.Error())

		_, err = remoteClient.RemoteAskGrain(ctx, connect.NewRequest(&internalpb.RemoteAskGrainRequest{
			Grain:          grain,
			RequestTimeout: durationpb.New(timeout),
			Dependencies:   grain.GetDependencies(),
			Message:        serialized,
			Sender: &internalpb.GrainId{
				Kind:  "actor.MockGrain",
				Name:  "GoAktGrain",
				Value: "actor.MockGrain/GoAktGrain",
			},
		}))
		require.Error(t, err)
		require.True(t, errors.As(err, &connectErr))
		e = connectErr.Unwrap()
		require.ErrorContains(t, e, ErrReservedName.Error())

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("Remoting failed with wrong address", func(t *testing.T) {
		ctx := t.Context()
		ports := dynaport.Get(2)
		remotingPort := ports[0]
		grainPort := ports[1]
		host := "127.0.0.1"
		timeout := 5 * time.Second

		testSystem, err := NewActorSystem(
			"testSys",
			WithLogger(log.DiscardLogger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		util.Pause(time.Second)

		// for the sake of the test, we will use a different port for the remoting
		// this will never happen in production, but we want to test the error handling
		testSystem.(*actorSystem).remoteConfig = remote.NewConfig(host, grainPort)

		// create a wire
		grain := &internalpb.Grain{
			GrainId: &internalpb.GrainId{
				Kind:  "actor.MockGrain",
				Name:  "me",
				Value: "actor.MockGrain/me",
			},
			Host:         "127.0.0.1",
			Port:         int32(remotingPort),
			Dependencies: nil,
		}

		serialized, _ := anypb.New(grain)
		remoteClient := testSystem.getRemoting().remotingServiceClient(grain.GetHost(), int(grain.GetPort()))

		_, err = remoteClient.RemoteTellGrain(ctx, connect.NewRequest(&internalpb.RemoteTellGrainRequest{
			Grain:          grain,
			RequestTimeout: durationpb.New(timeout),
			Dependencies:   grain.GetDependencies(),
			Message:        serialized,
		}))
		require.Error(t, err)
		var connectErr *connect.Error
		require.True(t, errors.As(err, &connectErr))
		e := connectErr.Unwrap()
		require.ErrorContains(t, e, ErrInvalidHost.Error())

		_, err = remoteClient.RemoteAskGrain(ctx, connect.NewRequest(&internalpb.RemoteAskGrainRequest{
			Grain:          grain,
			RequestTimeout: durationpb.New(timeout),
			Dependencies:   grain.GetDependencies(),
			Message:        serialized,
		}))
		require.Error(t, err)
		require.True(t, errors.As(err, &connectErr))
		e = connectErr.Unwrap()
		require.ErrorContains(t, e, ErrInvalidHost.Error())

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("With Tell timeout", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		util.Pause(time.Second)

		// create a grain instance
		grain := NewMockGrain()
		identity, err := testSystem.GetGrain(ctx, "testGrain", func(ctx context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)
		require.NotNil(t, identity)

		// prepare a message to send to the grain
		message := new(testpb.TestTimeout)
		err = testSystem.TellGrain(ctx, identity, message, WithRequestTimeout(100*time.Millisecond))
		require.Error(t, err)
		require.ErrorIs(t, err, ErrRequestTimeout)

		// check if the grain is activated
		gp, ok := testSystem.(*actorSystem).grains.Get(*identity)
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isRunning())

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("With AskGrain timeout", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		util.Pause(time.Second)

		// create a grain instance
		grain := NewMockGrain()
		identity, err := testSystem.GetGrain(ctx, "testGrain", func(ctx context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)
		require.NotNil(t, identity)

		// prepare a message to send to the grain
		message := new(testpb.TestTimeout)
		response, err := testSystem.AskGrain(ctx, identity, message, WithRequestTimeout(100*time.Millisecond))
		require.Error(t, err)
		require.ErrorIs(t, err, ErrRequestTimeout)
		require.Nil(t, response)

		// check if the grain is activated
		gp, ok := testSystem.(*actorSystem).grains.Get(*identity)
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isRunning())

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("With AskGrain and context canceled", func(t *testing.T) {
		ctx := context.TODO()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		util.Pause(time.Second)

		// create a grain instance
		grain := NewMockGrain()
		identity, err := testSystem.GetGrain(ctx, "testGrain", func(ctx context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)
		require.NotNil(t, identity)

		cancelCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()
		// prepare a message to send to the grain
		message := new(testpb.TestTimeout)
		response, err := testSystem.AskGrain(cancelCtx, identity, message)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrRequestTimeout)
		require.Nil(t, response)

		// check if the grain is activated
		gp, ok := testSystem.(*actorSystem).grains.Get(*identity)
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isRunning())

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("With Tell and context canceled", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		util.Pause(time.Second)

		// create a grain instance
		grain := NewMockGrain()
		identity, err := testSystem.GetGrain(ctx, "testGrain", func(ctx context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)
		require.NotNil(t, identity)

		cancelCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()

		// prepare a message to send to the grain
		message := new(testpb.TestTimeout)
		err = testSystem.TellGrain(cancelCtx, identity, message, WithRequestTimeout(5*time.Minute))
		require.Error(t, err)
		require.ErrorIs(t, err, ErrRequestTimeout)

		// check if the grain is activated
		gp, ok := testSystem.(*actorSystem).grains.Get(*identity)
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isRunning())

		require.NoError(t, testSystem.Stop(ctx))
	})
}
