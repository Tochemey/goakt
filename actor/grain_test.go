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
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"

	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/pause"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/remote"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

// nolint
func TestGrain(t *testing.T) {
	t.Run("With single node", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		// create a grain instance
		grain := NewMockGrain()
		identity, err := testSystem.GrainIdentity(ctx, "testGrain", func(_ context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)
		require.NotNil(t, identity)

		// send a message to the grain
		message := new(testpb.TestReply)
		response, err := testSystem.AskGrain(ctx, identity, message, time.Second)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.IsType(t, &testpb.Reply{}, response)

		// check if the grain is activated
		gp, ok := testSystem.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isActive())

		response, err = testSystem.AskGrain(ctx, identity, message, time.Second)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.IsType(t, &testpb.Reply{}, response)

		// check if the grain is activated
		gp, ok = testSystem.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isActive())

		grains := testSystem.Grains(ctx, time.Second)
		require.NotEmpty(t, grains)
		require.Len(t, grains, 1)
		actual := grains[0]
		require.True(t, identity.Equal(actual))

		// deactivate the grain
		err = testSystem.TellGrain(ctx, identity, new(goaktpb.PoisonPill))
		require.NoError(t, err)

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("With multiple nodes", func(t *testing.T) {
		ctx := t.Context()
		// start the NATS server
		srv := startNatsServer(t)

		// create and start a system cluster
		node1, sd1 := natsPeer(t, srv.Addr().String())
		require.NotNil(t, node1)
		require.NotNil(t, sd1)

		// create and start a system cluster
		node2, sd2 := natsPeer(t, srv.Addr().String())
		require.NotNil(t, node2)
		require.NotNil(t, sd2)

		// create and start a system cluster
		node3, sd3 := natsPeer(t, srv.Addr().String())
		require.NotNil(t, node3)
		require.NotNil(t, sd3)

		grain := NewMockGrain()
		identity, err := node1.GrainIdentity(ctx, "testGrain", func(_ context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)
		require.NotNil(t, identity)

		// send a message to the grain
		message := new(testpb.TestReply)
		response, err := node1.AskGrain(ctx, identity, message, time.Second)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.IsType(t, &testpb.Reply{}, response)

		pause.For(time.Second)

		// check if the grain is activated
		gp, ok := node1.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isActive())

		response, err = node2.AskGrain(ctx, identity, message, time.Second)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.IsType(t, &testpb.Reply{}, response)

		pause.For(time.Second)

		// let us shutdown the grain by sending PoisonPill
		err = node3.TellGrain(ctx, identity, new(goaktpb.PoisonPill))
		require.NoError(t, err)

		pause.For(time.Second)

		identity, err = node1.GrainIdentity(ctx, "testGrain", func(_ context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)
		require.NotNil(t, identity)

		// check if the grain is activated
		gp, ok = node1.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isActive())

		pause.For(time.Second)

		// send a message to the grain to reactivate it
		response, err = node3.AskGrain(ctx, identity, message, time.Second)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.IsType(t, &testpb.Reply{}, response)

		grains := node3.Grains(ctx, time.Second)
		require.NotEmpty(t, grains)
		require.Len(t, grains, 1)

		pause.For(time.Second)

		// check if the grain is activated
		gp, ok = node1.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isActive())

		// let us shutdown the grain by sending PoisonPill
		err = node3.TellGrain(ctx, identity, new(goaktpb.PoisonPill))
		require.NoError(t, err)

		// check if the grain is activated
		gp, ok = node1.(*actorSystem).grains.Get(identity.String())
		require.False(t, ok)
		require.Nil(t, gp)

		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, sd3.Close())
		srv.Shutdown()
	})
	t.Run("With unhandled message", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		// create a grain instance
		grain := NewMockGrain()
		identity, err := testSystem.GrainIdentity(ctx, "testGrain", func(_ context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)
		require.NotNil(t, identity)

		// send a message to the grain
		message := new(testpb.TestClusterForward) // we know this message is not handled by the grain
		response, err := testSystem.AskGrain(ctx, identity, message, time.Second)
		require.Error(t, err)
		require.Nil(t, response)
		require.ErrorIs(t, err, gerrors.ErrUnhanledMessage)

		// check if the grain is activated
		gp, ok := testSystem.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isActive())

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("With panic handling", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		// create a grain instance
		grain := NewMockPanickingGrain()
		identity, err := testSystem.GrainIdentity(ctx, "testGrain", func(_ context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)
		require.NotNil(t, identity)

		var message proto.Message

		// send a message to the grain
		message = new(testpb.TestSend)
		response, err := testSystem.AskGrain(ctx, identity, message, time.Second)
		require.Error(t, err)
		require.Nil(t, response)
		require.ErrorContains(t, err, "test panic") // we expect the panic to be caught and returned as an error

		// check if the grain is activated
		gp, ok := testSystem.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isActive())

		message = new(testpb.TestReply)
		response, err = testSystem.AskGrain(ctx, identity, message, time.Second)
		require.Error(t, err)
		require.Nil(t, response)
		require.ErrorContains(t, err, "test panic") // we expect the panic to be caught and returned as an error

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("With activation error", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		// create a grain instance
		grain := NewMockGrainActivationFailure()
		identity, err := testSystem.GrainIdentity(ctx, "testGrain", func(_ context.Context) (Grain, error) {
			return grain, nil
		})
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrGrainActivationFailure)
		require.Nil(t, identity)

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
		pause.For(time.Second)

		// create a grain instance
		grain := NewMockGrainDeactivationFailure()
		identity, err := testSystem.GrainIdentity(ctx, "testGrain", func(_ context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)
		require.NotNil(t, identity)

		// prepare a message to send to the grain
		message := new(testpb.TestSend)
		err = testSystem.TellGrain(ctx, identity, message)
		require.NoError(t, err)

		pause.For(500 * time.Millisecond)

		// check if the grain is activated
		gp, ok := testSystem.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isActive())

		// let us shutdown the grain by sending PoisonPill
		err = testSystem.TellGrain(ctx, identity, new(goaktpb.PoisonPill))
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrGrainDeactivationFailure)

		pause.For(500 * time.Millisecond)

		// check if the grain is activated
		gp, ok = testSystem.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		require.NotNil(t, gp)
		require.False(t, gp.isActive())

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
		pause.For(time.Second)

		// create a grain instance
		grain := NewMockGrainReceiveFailure()
		identity, err := testSystem.GrainIdentity(ctx, "testGrain", func(ctx context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)
		require.NotNil(t, identity)

		// prepare a message to send to the grain
		message := new(testpb.TestSend)
		response, err := testSystem.AskGrain(ctx, identity, message, time.Second)
		require.Error(t, err)
		require.Nil(t, response)

		// check if the grain is activated
		gp, ok := testSystem.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isActive())

		err = testSystem.TellGrain(ctx, identity, message)
		require.Error(t, err)

		gp, ok = testSystem.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isActive())

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
		pause.For(time.Second)

		identity := &GrainIdentity{}
		message := new(testpb.TestSend)
		response, err := testSystem.AskGrain(ctx, identity, message, time.Second)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrInvalidGrainIdentity)
		require.Nil(t, response)

		err = testSystem.TellGrain(ctx, identity, message)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrInvalidGrainIdentity)

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
		pause.For(time.Second)

		// create a grain instance
		grain := NewMockGrain()
		identity, err := testSystem.GrainIdentity(ctx, "GoAktGrain", func(ctx context.Context) (Grain, error) {
			return grain, nil
		})
		require.Nil(t, identity)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrReservedName)

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("When actor system not started", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// prepare a message to send to the grain
		message := new(testpb.TestSend)
		response, err := testSystem.AskGrain(ctx, &GrainIdentity{}, message, time.Second)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
		require.Nil(t, response)

		err = testSystem.TellGrain(ctx, &GrainIdentity{}, message)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
	})
	t.Run("GrainIdentity when not started returns error", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)
		// create a grain instance
		grain := NewMockGrain()
		identity, err := testSystem.GrainIdentity(ctx, "testGrain", func(ctx context.Context) (Grain, error) {
			return grain, nil
		})
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
		require.Nil(t, identity)
	})
	t.Run("When GrainIdentity returns error", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		// create a grain instance
		identity, err := testSystem.GrainIdentity(ctx, "testGrain", func(ctx context.Context) (Grain, error) {
			return nil, assert.AnError
		})
		require.Error(t, err)
		require.ErrorIs(t, err, assert.AnError)
		require.Nil(t, identity)

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("GrainIdentity with invalid indentity name", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		// create a grain instance
		name := strings.Repeat("a", 300)
		identity, err := testSystem.GrainIdentity(ctx, name, func(ctx context.Context) (Grain, error) {
			return NewMockGrain(), nil
		})
		require.Error(t, err)
		require.Nil(t, identity)

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
		pause.For(time.Second)

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
		remoteClient := testSystem.getRemoting().RemotingServiceClient(grain.GetHost(), int(grain.GetPort()))

		_, err = remoteClient.RemoteTellGrain(ctx, connect.NewRequest(&internalpb.RemoteTellGrainRequest{
			Grain:   grain,
			Message: serialized,
		}))
		require.Error(t, err)
		var connectErr *connect.Error
		require.True(t, errors.As(err, &connectErr))
		e := connectErr.Unwrap()
		require.ErrorContains(t, e, gerrors.ErrRemotingDisabled.Error())

		_, err = remoteClient.RemoteAskGrain(ctx, connect.NewRequest(&internalpb.RemoteAskGrainRequest{
			Grain:          grain,
			RequestTimeout: durationpb.New(timeout),
			Message:        serialized,
		}))
		require.Error(t, err)
		require.True(t, errors.As(err, &connectErr))
		e = connectErr.Unwrap()
		require.ErrorContains(t, e, gerrors.ErrRemotingDisabled.Error())

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
		pause.For(time.Second)

		// create a wire
		grain := &internalpb.Grain{
			GrainId:      &internalpb.GrainId{},
			Host:         host,
			Port:         int32(remotingPort),
			Dependencies: nil,
		}

		serialized, _ := anypb.New(grain)
		remoteClient := testSystem.getRemoting().RemotingServiceClient(grain.GetHost(), int(grain.GetPort()))

		_, err = remoteClient.RemoteTellGrain(ctx, connect.NewRequest(&internalpb.RemoteTellGrainRequest{
			Grain:   grain,
			Message: serialized,
		}))
		require.Error(t, err)
		var connectErr *connect.Error
		require.True(t, errors.As(err, &connectErr))
		e := connectErr.Unwrap()
		require.ErrorContains(t, e, gerrors.ErrInvalidGrainIdentity.Error())

		_, err = remoteClient.RemoteAskGrain(ctx, connect.NewRequest(&internalpb.RemoteAskGrainRequest{
			Grain:          grain,
			RequestTimeout: durationpb.New(timeout),
			Message:        serialized,
		}))
		require.Error(t, err)
		require.True(t, errors.As(err, &connectErr))
		e = connectErr.Unwrap()
		require.ErrorContains(t, e, gerrors.ErrInvalidGrainIdentity.Error())

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
		pause.For(time.Second)

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
		remoteClient := testSystem.getRemoting().RemotingServiceClient(grain.GetHost(), int(grain.GetPort()))

		_, err = remoteClient.RemoteTellGrain(ctx, connect.NewRequest(&internalpb.RemoteTellGrainRequest{
			Grain:   grain,
			Message: serialized,
		}))
		require.Error(t, err)
		var connectErr *connect.Error
		require.True(t, errors.As(err, &connectErr))
		e := connectErr.Unwrap()
		require.ErrorContains(t, e, gerrors.ErrReservedName.Error())

		_, err = remoteClient.RemoteAskGrain(ctx, connect.NewRequest(&internalpb.RemoteAskGrainRequest{
			Grain:          grain,
			RequestTimeout: durationpb.New(timeout),
			Message:        serialized,
		}))
		require.Error(t, err)
		require.True(t, errors.As(err, &connectErr))
		e = connectErr.Unwrap()
		require.ErrorContains(t, e, gerrors.ErrReservedName.Error())

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
		pause.For(time.Second)

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
		remoteClient := testSystem.getRemoting().RemotingServiceClient(grain.GetHost(), int(grain.GetPort()))

		_, err = remoteClient.RemoteTellGrain(ctx, connect.NewRequest(&internalpb.RemoteTellGrainRequest{
			Grain:   grain,
			Message: serialized,
		}))
		require.Error(t, err)
		var connectErr *connect.Error
		require.True(t, errors.As(err, &connectErr))
		e := connectErr.Unwrap()
		require.ErrorContains(t, e, gerrors.ErrInvalidHost.Error())

		_, err = remoteClient.RemoteAskGrain(ctx, connect.NewRequest(&internalpb.RemoteAskGrainRequest{
			Grain:          grain,
			RequestTimeout: durationpb.New(timeout),
			Message:        serialized,
		}))
		require.Error(t, err)
		require.True(t, errors.As(err, &connectErr))
		e = connectErr.Unwrap()
		require.ErrorContains(t, e, gerrors.ErrInvalidHost.Error())

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("With TellGrain timeout", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		// create a grain instance
		grain := NewMockGrain()
		identity, err := testSystem.GrainIdentity(ctx, "testGrain", func(ctx context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)
		require.NotNil(t, identity)

		// prepare a message to send to the grain
		message := new(testpb.TestTimeout)
		err = testSystem.TellGrain(ctx, identity, message)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrRequestTimeout)

		// check if the grain is activated
		gp, ok := testSystem.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isActive())

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
		pause.For(time.Second)

		// create a grain instance
		grain := NewMockGrain()
		identity, err := testSystem.GrainIdentity(ctx, "testGrain", func(ctx context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)
		require.NotNil(t, identity)

		// prepare a message to send to the grain
		message := new(testpb.TestTimeout)
		response, err := testSystem.AskGrain(ctx, identity, message, time.Second)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrRequestTimeout)
		require.Nil(t, response)

		// check if the grain is activated
		gp, ok := testSystem.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isActive())

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
		pause.For(time.Second)

		// create a grain instance
		grain := NewMockGrain()
		identity, err := testSystem.GrainIdentity(ctx, "testGrain", func(ctx context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)
		require.NotNil(t, identity)

		cancelCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()
		// prepare a message to send to the grain
		message := new(testpb.TestTimeout)
		response, err := testSystem.AskGrain(cancelCtx, identity, message, time.Second)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrRequestTimeout)
		require.Nil(t, response)

		// check if the grain is activated
		gp, ok := testSystem.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isActive())

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("With TellGrain and context canceled", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		// create a grain instance
		grain := NewMockGrain()
		identity, err := testSystem.GrainIdentity(ctx, "testGrain", func(ctx context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)
		require.NotNil(t, identity)

		cancelCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()

		// prepare a message to send to the grain
		message := new(testpb.TestTimeout)
		err = testSystem.TellGrain(cancelCtx, identity, message)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrRequestTimeout)

		// check if the grain is activated
		gp, ok := testSystem.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isActive())

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("With PersistenceGrain", func(t *testing.T) {
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
		pause.For(time.Second)

		// create a grain instance
		grain := NewMockPersistenceGrain()
		identity, err := testSystem.GrainIdentity(ctx, "testGrain", func(ctx context.Context) (Grain, error) {
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
		gp, ok := testSystem.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isActive())

		message = &testpb.CreditAccount{
			Balance: 500.00,
		}

		response, err := testSystem.AskGrain(ctx, identity, message, time.Second)
		require.NoError(t, err)
		require.NotNil(t, response)
		actual := response.(*testpb.Account)
		require.EqualValues(t, 1000.00, actual.GetAccountBalance())

		// let us shutdown the grain by sending PoisonPill
		err = testSystem.TellGrain(ctx, identity, new(goaktpb.PoisonPill))
		require.NoError(t, err)

		// check if the grain is activated
		gp, ok = testSystem.(*actorSystem).grains.Get(identity.String())
		require.False(t, ok)
		require.Nil(t, gp)

		// reactivate the grain
		identity, err = testSystem.GrainIdentity(ctx, "testGrain", func(ctx context.Context) (Grain, error) {
			return grain, nil
		})

		// check if the grain is activated
		gp, ok = testSystem.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isActive())

		// send a message to the grain to reactivate it
		response, err = testSystem.AskGrain(ctx, identity, message, time.Second)
		require.NoError(t, err)
		require.NotNil(t, response)
		actual = response.(*testpb.Account)
		require.EqualValues(t, 1500.00, actual.GetAccountBalance())

		// check if the grain is activated
		gp, ok = testSystem.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isActive())

		require.NoError(t, testSystem.Stop(ctx))
	})
	t.Run("With Passivation", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		// create a grain instance
		grain := NewMockGrain()
		identity, err := testSystem.GrainIdentity(ctx, "testGrain", func(_ context.Context) (Grain, error) {
			return grain, nil
		}, WithGrainDeactivateAfter(200*time.Millisecond))
		require.NoError(t, err)
		require.NotNil(t, identity)

		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			pause.For(500 * time.Millisecond)
			wg.Done()
		}()
		// block until timer is up
		wg.Wait()

		// check if the grain is activated
		gp, ok := testSystem.(*actorSystem).grains.Get(identity.String())
		require.False(t, ok)
		require.Nil(t, gp)

		identity, err = testSystem.GrainIdentity(ctx, "testGrain", func(_ context.Context) (Grain, error) {
			return grain, nil
		}, WithLongLivedGrain())
		require.NoError(t, err)
		require.NotNil(t, identity)

		message := new(testpb.TestReply)
		response, err := testSystem.AskGrain(ctx, identity, message, time.Second)
		require.NoError(t, err)
		require.NotNil(t, response)
		require.IsType(t, &testpb.Reply{}, response)

		// check if the grain is activated
		gp, ok = testSystem.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isActive())

		// deactivate the grain
		err = testSystem.TellGrain(ctx, identity, new(goaktpb.PoisonPill))
		require.NoError(t, err)

		// check if the grain is activated
		gp, ok = testSystem.(*actorSystem).grains.Get(identity.String())
		require.False(t, ok)
		require.Nil(t, gp)

		require.NoError(t, testSystem.Stop(ctx))
	})

	t.Run("With GrainIdentity when dependencies are invalid", func(t *testing.T) {
		ctx := t.Context()
		testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, testSystem)

		// start the actor system
		err = testSystem.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		dependency := NewMockDependency("$omeN@me", "user", "email")

		grain := NewMockGrain()
		identity, err := testSystem.GrainIdentity(ctx, "testGrain", func(_ context.Context) (Grain, error) {
			return grain, nil
		}, WithGrainDependencies(dependency))
		require.Error(t, err)
		require.Nil(t, identity)

		require.NoError(t, testSystem.Stop(ctx))
	})
}
