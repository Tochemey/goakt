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
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v3/discovery"
	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/cluster"
	"github.com/tochemey/goakt/v3/internal/id"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/pause"
	"github.com/tochemey/goakt/v3/internal/registry"
	"github.com/tochemey/goakt/v3/log"
	mocks "github.com/tochemey/goakt/v3/mocks/cluster"
	mocksremote "github.com/tochemey/goakt/v3/mocks/remote"
	"github.com/tochemey/goakt/v3/remote"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

// nolint
func TestGrainPIDProcessReleasesContexts(t *testing.T) {
	grain := &MockContextReleasingGrain{done: make(chan *GrainContext, 2)}
	pid := &grainPID{
		grain:   grain,
		mailbox: newGrainMailbox(0), // unbounded
		logger:  log.DiscardLogger,
	}

	pid.processing.Store(idle)

	ctx := context.Background()
	identity := &GrainIdentity{kind: "TestKind", name: "TestID"}

	first := getGrainContext().build(ctx, pid, nil, identity, &testpb.TestReply{}, false)
	second := getGrainContext().build(ctx, pid, nil, identity, &testpb.TestReply{}, false)

	pid.mailbox.Enqueue(first)
	pid.mailbox.Enqueue(second)

	pid.process()

	require.Same(t, first, <-grain.done)
	require.Same(t, second, <-grain.done)

	require.Eventually(t, func() bool {
		return pid.mailbox.IsEmpty() && pid.processing.Load() == idle
	}, time.Second, 10*time.Millisecond)

	require.Nil(t, first.Message())
	require.Nil(t, first.self)
	require.Nil(t, first.getError())
	require.Nil(t, first.getResponse())
	require.Nil(t, first.pid)
}

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
		require.Eventually(t, func() bool {
			return gp.uptime() > 0
		}, 2*time.Second, 10*time.Millisecond)

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
	t.Run("With activation error when GetGrain in cluster mode returns error", func(t *testing.T) {
		ctx := t.Context()
		clmock := mocks.NewCluster(t)
		testSystem := &actorSystem{
			cluster:       clmock,
			clusterConfig: NewClusterConfig(),
			logger:        log.DiscardLogger,
		}
		testSystem.started.Store(true)
		testSystem.shuttingDown.Store(false)
		testSystem.clusterEnabled.Store(true)

		// create a grain instance
		grain := NewMockGrainActivationFailure()
		name := "testGrain"
		kind := registry.Name(grain)
		identityStr := fmt.Sprintf("%s%s%s", kind, id.GrainIdentitySeparator, name)

		clmock.EXPECT().GrainExists(ctx, identityStr).Return(true, nil)
		clmock.EXPECT().GetGrain(ctx, identityStr).Return(nil, errors.New("test error"))

		identity, err := testSystem.GrainIdentity(ctx, "testGrain", func(_ context.Context) (Grain, error) {
			return grain, nil
		})
		require.Error(t, err)
		require.Nil(t, identity)
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

		grains := testSystem.Grains(ctx, time.Second)
		require.Empty(t, grains)

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
		wg.Go(func() {
			pause.For(500 * time.Millisecond)
		})
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

func TestFindActivationPeer(t *testing.T) {
	t.Run("fails when peers lookup fails", func(t *testing.T) {
		ctx := t.Context()
		clmock := mocks.NewCluster(t)
		sys := &actorSystem{
			cluster: clmock,
			logger:  log.DiscardLogger,
		}

		clmock.EXPECT().Members(ctx).Return(nil, errors.New("boom"))

		peer, err := sys.findActivationPeer(ctx, newGrainConfig())
		require.Error(t, err)
		require.Nil(t, peer)
		require.ErrorContains(t, err, "failed to fetch cluster nodes")
	})

	t.Run("fails when no peer matches required role", func(t *testing.T) {
		ctx := t.Context()
		clmock := mocks.NewCluster(t)
		sys := &actorSystem{
			cluster: clmock,
			logger:  log.DiscardLogger,
		}

		config := newGrainConfig(WithActivationRole("analytics"))
		peers := []*cluster.Peer{{Host: "10.0.0.2", Roles: []string{"api"}}}
		clmock.EXPECT().Members(ctx).Return(peers, nil)

		peer, err := sys.findActivationPeer(ctx, config)
		require.Error(t, err)
		require.Nil(t, peer)
		require.ErrorContains(t, err, "no nodes with role analytics")
	})

	t.Run("returns nil when only one peer is available", func(t *testing.T) {
		ctx := t.Context()
		clmock := mocks.NewCluster(t)
		sys := &actorSystem{
			cluster: clmock,
			logger:  log.DiscardLogger,
		}

		peers := []*cluster.Peer{{Host: "10.0.0.2"}}
		clmock.EXPECT().Members(ctx).Return(peers, nil)

		peer, err := sys.findActivationPeer(ctx, newGrainConfig())
		require.NoError(t, err)
		require.Nil(t, peer)
	})

	t.Run("random activation picks a remote peer", func(t *testing.T) {
		ctx := t.Context()
		clmock := mocks.NewCluster(t)
		sys := &actorSystem{
			cluster: clmock,
			logger:  log.DiscardLogger,
		}

		peers := []*cluster.Peer{
			{Host: "10.0.0.2"},
			{Host: "10.0.0.3"},
			{Host: "10.0.0.4"},
		}
		clmock.EXPECT().Members(ctx).Return(peers, nil)

		peer, err := sys.findActivationPeer(ctx, newGrainConfig(WithActivationStrategy(RandomActivation)))
		require.NoError(t, err)
		require.NotNil(t, peer)
		isCandidate := slices.Contains(peers, peer)
		require.True(t, isCandidate, "peer %v not part of candidates", peer)
	})

	t.Run("round robin cycles through peers", func(t *testing.T) {
		ctx := t.Context()
		clmock := mocks.NewCluster(t)
		sys := &actorSystem{
			cluster: clmock,
			logger:  log.DiscardLogger,
		}

		peers := []*cluster.Peer{
			{Host: "10.0.0.2"},
			{Host: "10.0.0.3"},
		}
		clmock.EXPECT().Members(ctx).Return(peers, nil).Twice()
		clmock.EXPECT().NextRoundRobinValue(ctx, cluster.GrainsRoundRobinKey).Return(1, nil).Once()
		clmock.EXPECT().NextRoundRobinValue(ctx, cluster.GrainsRoundRobinKey).Return(2, nil).Once()

		config := newGrainConfig(WithActivationStrategy(RoundRobinActivation))
		peer1, err := sys.findActivationPeer(ctx, config)
		require.NoError(t, err)
		require.Equal(t, peers[0], peer1)

		peer2, err := sys.findActivationPeer(ctx, config)
		require.NoError(t, err)
		require.Equal(t, peers[1], peer2)
	})

	t.Run("round robin cycles when getting next value failed", func(t *testing.T) {
		ctx := t.Context()
		clmock := mocks.NewCluster(t)
		sys := &actorSystem{
			cluster: clmock,
			logger:  log.DiscardLogger,
		}

		peers := []*cluster.Peer{
			{Host: "10.0.0.2"},
			{Host: "10.0.0.3"},
		}
		clmock.EXPECT().Members(ctx).Return(peers, nil).Once()
		clmock.EXPECT().NextRoundRobinValue(ctx, cluster.GrainsRoundRobinKey).Return(-1, assert.AnError).Once()

		config := newGrainConfig(WithActivationStrategy(RoundRobinActivation))
		peer1, err := sys.findActivationPeer(ctx, config)
		require.Error(t, err)
		require.Nil(t, peer1)
	})

	t.Run("least load prefers the lightest node", func(t *testing.T) {
		ctx := t.Context()
		httpClient := &http.Client{}
		clmock := mocks.NewCluster(t)
		remoting := mocksremote.NewRemoting(t)

		peerLow := MockClusterPeer(t, 1, nil)
		peerHigh := MockClusterPeer(t, 10, nil)
		peers := []*cluster.Peer{peerHigh, peerLow}

		clmock.EXPECT().Members(ctx).Return(peers, nil)
		remoting.EXPECT().MaxReadFrameSize().Return(0).Times(len(peers))
		remoting.EXPECT().Compression().Return(remote.NoCompression).Times(len(peers))
		remoting.EXPECT().HTTPClient().Return(httpClient).Times(len(peers))

		sys := &actorSystem{
			cluster:  clmock,
			logger:   log.DiscardLogger,
			remoting: remoting,
			clusterNode: &discovery.Node{
				Host:         "127.0.0.1",
				RemotingPort: 9000,
			},
		}
		sys.actorsCounter.Store(5)

		peer, err := sys.findActivationPeer(ctx, newGrainConfig(WithActivationStrategy(LeastLoadActivation)))
		require.NoError(t, err)
		require.Equal(t, peerLow, peer)
	})

	t.Run("least load reports metric failures", func(t *testing.T) {
		ctx := t.Context()
		httpClient := &http.Client{}
		clmock := mocks.NewCluster(t)
		remoting := mocksremote.NewRemoting(t)

		errPeer := MockClusterPeer(t, 0, errors.New("metrics down"))
		successPeer := MockClusterPeer(t, 2, nil)
		peers := []*cluster.Peer{errPeer, successPeer}

		clmock.EXPECT().Members(ctx).Return(peers, nil)
		remoting.EXPECT().MaxReadFrameSize().Return(0).Times(len(peers))
		remoting.EXPECT().Compression().Return(remote.NoCompression).Times(len(peers))
		remoting.EXPECT().HTTPClient().Return(httpClient).Times(len(peers))

		sys := &actorSystem{
			cluster:  clmock,
			logger:   log.DiscardLogger,
			remoting: remoting,
			clusterNode: &discovery.Node{
				Host:         "127.0.0.1",
				RemotingPort: 9100,
			},
		}

		peer, err := sys.findActivationPeer(ctx, newGrainConfig(WithActivationStrategy(LeastLoadActivation)))
		require.Error(t, err)
		require.Nil(t, peer)
		require.ErrorContains(t, err, "failed to fetch node metrics")
	})
}

// nolint
func TestRecreateGrain(t *testing.T) {
	t.Run("returns error for reserved name", func(t *testing.T) {
		ctx := t.Context()
		sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, sys)

		require.NoError(t, sys.Start(ctx))
		defer func() { _ = sys.Stop(ctx) }()

		// Value starts with reserved prefix, triggers early reserved-name check
		grain := &internalpb.Grain{GrainId: &internalpb.GrainId{Value: "GoAkt_reserved_value"}}
		err = sys.(*actorSystem).recreateGrain(ctx, grain)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrReservedName)
	})

	t.Run("returns error for invalid identity", func(t *testing.T) {
		ctx := t.Context()
		sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, sys)

		require.NoError(t, sys.Start(ctx))
		defer func() { _ = sys.Stop(ctx) }()

		// Not reserved, but invalid identity format (no kind/name)
		grain := &internalpb.Grain{GrainId: &internalpb.GrainId{Value: "invalid"}}
		err = sys.(*actorSystem).recreateGrain(ctx, grain)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrInvalidGrainIdentity)
	})

	t.Run("fails when grain kind not registered", func(t *testing.T) {
		ctx := t.Context()
		sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, sys)

		require.NoError(t, sys.Start(ctx))
		defer func() { _ = sys.Stop(ctx) }()

		// Kind not present in registry -> reflection.NewGrain fails
		grain := &internalpb.Grain{
			GrainId: &internalpb.GrainId{
				Kind:  "actor.FakeGrain",
				Name:  "g1",
				Value: "actor.FakeGrain/g1",
			},
			ActivationTimeout: durationpb.New(500 * time.Millisecond),
			ActivationRetries: 1,
		}
		err = sys.(*actorSystem).recreateGrain(ctx, grain)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrGrainNotRegistered)
	})

	t.Run("creates and activates new grain successfully", func(t *testing.T) {
		ctx := t.Context()
		sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, sys)

		require.NoError(t, sys.Start(ctx))
		defer func() { _ = sys.Stop(ctx) }()

		// Register the grain type so reflection can instantiate it
		sys.(*actorSystem).registry.Register(NewMockGrain())

		idName := "MyGrain"
		kind := registry.Name(NewMockGrain()) // e.g., actor.mockgrain
		// Use cased kind in Value as recreateGrain accepts any case
		value := "actor.MockGrain/" + idName
		grain := &internalpb.Grain{
			GrainId: &internalpb.GrainId{
				Kind:  kind,
				Name:  idName,
				Value: value,
			},
			ActivationTimeout: durationpb.New(1 * time.Second),
			ActivationRetries: 1,
		}

		err = sys.(*actorSystem).recreateGrain(ctx, grain)
		require.NoError(t, err)

		// Confirm process is created, registered and active
		gp, ok := sys.(*actorSystem).grains.Get(value)
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isActive())
	})

	t.Run("activates existing inactive process and registers it", func(t *testing.T) {
		ctx := t.Context()
		sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, sys)

		require.NoError(t, sys.Start(ctx))
		defer func() { _ = sys.Stop(ctx) }()

		// Create identity consistent with the serialized value
		idName := "G2"
		value := "actor.MockGrain/" + idName
		identity, ierr := toIdentity(value)
		require.NoError(t, ierr)

		// Pre-create a process that is not active and not registered
		pid := newGrainPID(identity, NewMockGrain(), sys, newGrainConfig())
		sys.(*actorSystem).grains.Set(identity.String(), pid)

		grain := &internalpb.Grain{
			GrainId: &internalpb.GrainId{
				Kind:  identity.Kind(),
				Name:  identity.Name(),
				Value: identity.String(),
			},
			ActivationTimeout: durationpb.New(1 * time.Second),
			ActivationRetries: 1,
		}

		err = sys.(*actorSystem).recreateGrain(ctx, grain)
		require.NoError(t, err)

		// Should be registered now and activated
		require.True(t, sys.(*actorSystem).registry.Exists(pid.getGrain()))
		gp, ok := sys.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		require.NotNil(t, gp)
		require.True(t, gp.isActive())
	})

	t.Run("fails when dependency type not registered", func(t *testing.T) {
		ctx := t.Context()
		sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, sys)

		require.NoError(t, sys.Start(ctx))
		defer func() { _ = sys.Stop(ctx) }()

		// Register grain type but not dependency type
		sys.(*actorSystem).registry.Register(NewMockGrain())

		idName := "G3"
		value := "actor.MockGrain/" + idName
		dep := &internalpb.Dependency{ // unknown type name -> reflection.NewDependency fails
			Id:       "dep1",
			TypeName: "actor.UnknownDependency",
			Bytea:    []byte("noop"),
		}
		grain := &internalpb.Grain{
			GrainId:           &internalpb.GrainId{Kind: registry.Name(NewMockGrain()), Name: idName, Value: value},
			Dependencies:      []*internalpb.Dependency{dep},
			ActivationTimeout: durationpb.New(1 * time.Second),
			ActivationRetries: 1,
		}

		err = sys.(*actorSystem).recreateGrain(ctx, grain)
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrDependencyTypeNotRegistered)
	})

	t.Run("fails when dependency id invalid", func(t *testing.T) {
		ctx := t.Context()
		sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, sys)

		require.NoError(t, sys.Start(ctx))
		defer func() { _ = sys.Stop(ctx) }()

		// Register grain and dependency types so decode works
		sys.(*actorSystem).registry.Register(NewMockGrain())
		sys.(*actorSystem).registry.Register(new(MockDependency))

		// Create dependency with invalid ID (violates ID validator)
		bad := NewMockDependency("$omeN@me", "user", "email")
		bytea, mErr := bad.MarshalBinary()
		require.NoError(t, mErr)

		idName := "G4"
		value := "actor.MockGrain/" + idName
		dep := &internalpb.Dependency{
			Id:       bad.ID(),
			TypeName: registry.Name(bad),
			Bytea:    bytea,
		}
		grain := &internalpb.Grain{
			GrainId:           &internalpb.GrainId{Kind: registry.Name(NewMockGrain()), Name: idName, Value: value},
			Dependencies:      []*internalpb.Dependency{dep},
			ActivationTimeout: durationpb.New(1 * time.Second),
			ActivationRetries: 1,
		}

		err = sys.(*actorSystem).recreateGrain(ctx, grain)
		require.Error(t, err)
	})
}

// nolint
func TestEnsureGrainProcess(t *testing.T) {
	t.Run("returns existing process when registered", func(t *testing.T) {
		ctx := t.Context()
		sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, sys)
		require.NoError(t, sys.Start(ctx))
		defer func() { _ = sys.Stop(ctx) }()

		// Register the grain type so reflection.Exists returns true
		sys.(*actorSystem).registry.Register(NewMockGrain())

		id := newGrainIdentity(NewMockGrain(), "g1")
		pid := newGrainPID(id, NewMockGrain(), sys, newGrainConfig())
		sys.(*actorSystem).grains.Set(id.String(), pid)

		got, e := sys.(*actorSystem).ensureGrainProcess(ctx, id)
		require.NoError(t, e)
		require.NotNil(t, got)
		require.Equal(t, pid, got)
		require.True(t, got.isActive())
		require.Equal(t, "g1", got.getGrain().(*MockGrain).name)

		// still present
		_, ok := sys.(*actorSystem).grains.Get(id.String())
		require.True(t, ok)
	})

	t.Run("deletes and errors when process grain type not registered", func(t *testing.T) {
		ctx := t.Context()
		sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, sys)
		require.NoError(t, sys.Start(ctx))
		defer func() { _ = sys.Stop(ctx) }()

		// Do NOT register the grain type
		id := newGrainIdentity(NewMockGrain(), "g2")
		pid := newGrainPID(id, NewMockGrain(), sys, newGrainConfig())
		sys.(*actorSystem).grains.Set(id.String(), pid)

		got, e := sys.(*actorSystem).ensureGrainProcess(ctx, id)
		require.Error(t, e)
		require.ErrorIs(t, e, gerrors.ErrGrainNotRegistered)
		require.Nil(t, got)

		// entry should be removed
		_, ok := sys.(*actorSystem).grains.Get(id.String())
		require.False(t, ok)
	})

	t.Run("creates process when missing", func(t *testing.T) {
		ctx := t.Context()
		sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, sys)
		require.NoError(t, sys.Start(ctx))
		defer func() { _ = sys.Stop(ctx) }()

		sys.(*actorSystem).registry.Register(NewMockGrain())
		id := newGrainIdentity(NewMockGrain(), "missing")
		got, e := sys.(*actorSystem).ensureGrainProcess(ctx, id)
		require.NoError(t, e)
		require.NotNil(t, got)
		require.True(t, got.isActive())
		require.Equal(t, "missing", got.getGrain().(*MockGrain).name)
		_, ok := sys.(*actorSystem).grains.Get(id.String())
		require.True(t, ok)
	})

	t.Run("errors when missing and grain type not registered", func(t *testing.T) {
		ctx := t.Context()
		sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, sys)
		require.NoError(t, sys.Start(ctx))
		defer func() { _ = sys.Stop(ctx) }()

		id := newGrainIdentity(NewMockGrain(), "unregistered")
		got, e := sys.(*actorSystem).ensureGrainProcess(ctx, id)
		require.Error(t, e)
		require.ErrorIs(t, e, gerrors.ErrGrainNotRegistered)
		require.Nil(t, got)
		_, ok := sys.(*actorSystem).grains.Get(id.String())
		require.False(t, ok)
	})
}

// nolint
func TestEnsureGrainProcessCluster(t *testing.T) {
	t.Run("existing process returns owner lookup error", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, id := MockClusterEnsureGrainSystem(t, grain, "cluster-existing-owner-error")
		seedInactiveGrainPID(sys, id, grain, newGrainConfig())
		expectedErr := errors.New("owner lookup failed")

		cl.EXPECT().GrainExists(ctx, id.String()).Return(false, expectedErr).Once()

		got, err := sys.ensureGrainProcess(ctx, id)
		require.ErrorIs(t, err, expectedErr)
		require.Nil(t, got)
	})

	t.Run("existing process returns owner mismatch", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, id := MockClusterEnsureGrainSystem(t, grain, "cluster-existing-owner-mismatch")
		seedInactiveGrainPID(sys, id, grain, newGrainConfig())
		remoteOwner := &internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: id.String()},
			Host:    "192.0.2.10",
			Port:    9001,
		}

		cl.EXPECT().GrainExists(ctx, id.String()).Return(true, nil).Once()
		cl.EXPECT().GetGrain(ctx, id.String()).Return(remoteOwner, nil).Once()

		got, err := sys.ensureGrainProcess(ctx, id)
		require.Nil(t, got)
		var ownerErr *grainOwnerMismatchError
		require.ErrorAs(t, err, &ownerErr)
		require.Equal(t, remoteOwner, ownerErr.owner)
	})

	t.Run("existing process returns toWireGrain error when claiming", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, id := MockClusterEnsureGrainSystem(t, grain, "cluster-existing-wire-error")
		expectedErr := errors.New("dependency marshal failed")
		config := newGrainConfig(WithGrainDependencies(&MockFailingDependency{err: expectedErr}))
		seedInactiveGrainPID(sys, id, grain, config)

		cl.EXPECT().GrainExists(ctx, id.String()).Return(false, nil).Once()

		got, err := sys.ensureGrainProcess(ctx, id)
		require.ErrorIs(t, err, expectedErr)
		require.Nil(t, got)
	})

	t.Run("existing process returns claim error", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, id := MockClusterEnsureGrainSystem(t, grain, "cluster-existing-claim-error")
		seedInactiveGrainPID(sys, id, grain, newGrainConfig())
		expectedErr := errors.New("claim failed")

		cl.EXPECT().GrainExists(ctx, id.String()).Return(false, nil).Once()
		cl.EXPECT().GrainExists(ctx, id.String()).Return(false, expectedErr).Once()

		got, err := sys.ensureGrainProcess(ctx, id)
		require.ErrorIs(t, err, expectedErr)
		require.Nil(t, got)
	})

	t.Run("existing process returns claim owner mismatch", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, id := MockClusterEnsureGrainSystem(t, grain, "cluster-existing-claim-mismatch")
		seedInactiveGrainPID(sys, id, grain, newGrainConfig())
		remoteOwner := &internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: id.String()},
			Host:    "192.0.2.11",
			Port:    9002,
		}

		cl.EXPECT().GrainExists(ctx, id.String()).Return(false, nil).Once()
		cl.EXPECT().GrainExists(ctx, id.String()).Return(true, nil).Once()
		cl.EXPECT().GetGrain(ctx, id.String()).Return(remoteOwner, nil).Once()

		got, err := sys.ensureGrainProcess(ctx, id)
		require.Nil(t, got)
		var ownerErr *grainOwnerMismatchError
		require.ErrorAs(t, err, &ownerErr)
		require.Equal(t, remoteOwner, ownerErr.owner)
	})

	t.Run("existing process cleans up claim on activation failure", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrainActivationFailure()
		sys, cl, id := MockClusterEnsureGrainSystem(t, grain, "cluster-existing-activate-fail")
		config := newGrainConfig(
			WithGrainInitMaxRetries(1),
			WithGrainInitTimeout(10*time.Millisecond),
		)
		seedInactiveGrainPID(sys, id, grain, config)

		cl.EXPECT().GrainExists(ctx, id.String()).Return(false, nil).Once()
		cl.EXPECT().GrainExists(ctx, id.String()).Return(false, nil).Once()
		cl.EXPECT().PutGrain(ctx, mock.MatchedBy(func(actual *internalpb.Grain) bool {
			return actual != nil && actual.GetGrainId().GetValue() == id.String()
		})).Return(nil).Once()
		cl.EXPECT().RemoveGrain(ctx, id.String()).Return(nil).Once()

		got, err := sys.ensureGrainProcess(ctx, id)
		require.ErrorIs(t, err, gerrors.ErrGrainActivationFailure)
		require.Nil(t, got)
	})

	t.Run("existing process returns cluster publish error", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, id := MockClusterEnsureGrainSystem(t, grain, "cluster-existing-publish-error")
		expectedErr := errors.New("encode failed")
		config := newGrainConfig(WithGrainDependencies(&MockFailingDependency{err: expectedErr}))
		seedInactiveGrainPID(sys, id, grain, config)
		localOwner := &internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: id.String()},
			Host:    sys.Host(),
			Port:    int32(sys.Port()),
		}

		cl.EXPECT().GrainExists(ctx, id.String()).Return(true, nil).Once()
		cl.EXPECT().GetGrain(ctx, id.String()).Return(localOwner, nil).Once()

		got, err := sys.ensureGrainProcess(ctx, id)
		require.ErrorIs(t, err, expectedErr)
		require.Nil(t, got)
	})

	t.Run("missing process returns owner lookup error", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, id := MockClusterEnsureGrainSystem(t, grain, "cluster-missing-owner-error")
		expectedErr := errors.New("owner lookup failed")

		cl.EXPECT().GrainExists(ctx, id.String()).Return(false, expectedErr).Once()

		got, err := sys.ensureGrainProcess(ctx, id)
		require.ErrorIs(t, err, expectedErr)
		require.Nil(t, got)
	})

	t.Run("missing process returns owner mismatch", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, id := MockClusterEnsureGrainSystem(t, grain, "cluster-missing-owner-mismatch")
		remoteOwner := &internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: id.String()},
			Host:    "192.0.2.12",
			Port:    9003,
		}

		cl.EXPECT().GrainExists(ctx, id.String()).Return(true, nil).Once()
		cl.EXPECT().GetGrain(ctx, id.String()).Return(remoteOwner, nil).Once()

		got, err := sys.ensureGrainProcess(ctx, id)
		require.Nil(t, got)
		var ownerErr *grainOwnerMismatchError
		require.ErrorAs(t, err, &ownerErr)
		require.Equal(t, remoteOwner, ownerErr.owner)
	})

	t.Run("missing process returns claim error", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, id := MockClusterEnsureGrainSystem(t, grain, "cluster-missing-claim-error")
		expectedErr := errors.New("claim failed")

		cl.EXPECT().GrainExists(ctx, id.String()).Return(false, nil).Once()
		cl.EXPECT().GrainExists(ctx, id.String()).Return(false, expectedErr).Once()

		got, err := sys.ensureGrainProcess(ctx, id)
		require.ErrorIs(t, err, expectedErr)
		require.Nil(t, got)
	})

	t.Run("missing process returns claim owner mismatch", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, id := MockClusterEnsureGrainSystem(t, grain, "cluster-missing-claim-mismatch")
		remoteOwner := &internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: id.String()},
			Host:    "192.0.2.13",
			Port:    9004,
		}

		cl.EXPECT().GrainExists(ctx, id.String()).Return(false, nil).Once()
		cl.EXPECT().GrainExists(ctx, id.String()).Return(true, nil).Once()
		cl.EXPECT().GetGrain(ctx, id.String()).Return(remoteOwner, nil).Once()

		got, err := sys.ensureGrainProcess(ctx, id)
		require.Nil(t, got)
		var ownerErr *grainOwnerMismatchError
		require.ErrorAs(t, err, &ownerErr)
		require.Equal(t, remoteOwner, ownerErr.owner)
	})

	t.Run("missing process cleans up claim on activation failure", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrainActivationFailure()
		sys, cl, id := MockClusterEnsureGrainSystem(t, grain, "cluster-missing-activate-fail")

		cl.EXPECT().GrainExists(ctx, id.String()).Return(false, nil).Once()
		cl.EXPECT().GrainExists(ctx, id.String()).Return(false, nil).Once()
		cl.EXPECT().PutGrain(ctx, mock.MatchedBy(func(actual *internalpb.Grain) bool {
			return actual != nil && actual.GetGrainId().GetValue() == id.String()
		})).Return(nil).Once()
		cl.EXPECT().RemoveGrain(ctx, id.String()).Return(nil).Once()

		got, err := sys.ensureGrainProcess(ctx, id)
		require.ErrorIs(t, err, gerrors.ErrGrainActivationFailure)
		require.Nil(t, got)
	})

	t.Run("missing process claims and activates", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, id := MockClusterEnsureGrainSystem(t, grain, "cluster-missing-claim-success")

		cl.EXPECT().GrainExists(ctx, id.String()).Return(false, nil).Once()
		cl.EXPECT().GrainExists(ctx, id.String()).Return(false, nil).Once()
		cl.EXPECT().PutGrain(ctx, mock.MatchedBy(func(actual *internalpb.Grain) bool {
			return actual != nil && actual.GetGrainId().GetValue() == id.String()
		})).Return(nil).Once()

		got, err := sys.ensureGrainProcess(ctx, id)
		require.NoError(t, err)
		require.NotNil(t, got)
		require.True(t, got.isActive())
		_, ok := sys.grains.Get(id.String())
		require.True(t, ok)
	})
}

// nolint
func TestRemoteActivateGrain_Failures(t *testing.T) {
	t.Run("remoting disabled", func(t *testing.T) {
		ctx := t.Context()
		sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, sys)

		// Remoting is disabled by default (no WithRemote)
		grain := &internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: "actor.MockGrain/g1"},
			Host:    "127.0.0.1",
			Port:    12345,
		}

		_, rerr := sys.(*actorSystem).RemoteActivateGrain(ctx, connect.NewRequest(&internalpb.RemoteActivateGrainRequest{Grain: grain}))
		require.Error(t, rerr)
		var cErr *connect.Error
		require.True(t, errors.As(rerr, &cErr))
		require.Equal(t, connect.CodeFailedPrecondition, cErr.Code())
		u := cErr.Unwrap()
		require.ErrorContains(t, u, gerrors.ErrRemotingDisabled.Error())
	})

	t.Run("invalid host/port", func(t *testing.T) {
		ctx := t.Context()
		ports := dynaport.Get(2)
		remotingPort := ports[0]
		wrongPort := ports[1]
		host := "127.0.0.1"

		sys, err := NewActorSystem(
			"testSys",
			WithLogger(log.DiscardLogger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)
		require.NotNil(t, sys)

		// Provide a mismatching port to trigger validateRemoteHost failure
		grain := &internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: "actor.MockGrain/g1"},
			Host:    host,
			Port:    int32(wrongPort),
		}

		_, rerr := sys.(*actorSystem).RemoteActivateGrain(ctx, connect.NewRequest(&internalpb.RemoteActivateGrainRequest{Grain: grain}))
		require.Error(t, rerr)
		var cErr *connect.Error
		require.True(t, errors.As(rerr, &cErr))
		require.Equal(t, connect.CodeInvalidArgument, cErr.Code())
		u := cErr.Unwrap()
		require.ErrorContains(t, u, gerrors.ErrInvalidHost.Error())
	})

	t.Run("toWireGrainEncodingError", func(t *testing.T) {
		sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)

		depErr := errors.New("dependency marshal failure")
		cfg := newGrainConfig(WithGrainDependencies(&MockFailingDependency{err: depErr}))
		identity := newGrainIdentity(NewMockGrain(), "wire-error")
		pid := newGrainPID(identity, NewMockGrain(), sys, cfg)

		wire, wErr := pid.toWireGrain()
		require.ErrorIs(t, wErr, depErr)
		require.Nil(t, wire)
	})

	t.Run("recreateGrain failure (reserved name)", func(t *testing.T) {
		ctx := t.Context()
		ports := dynaport.Get(1)
		remotingPort := ports[0]
		host := "127.0.0.1"

		sys, err := NewActorSystem(
			"testSys",
			WithLogger(log.DiscardLogger),
			WithRemote(remote.NewConfig(host, remotingPort)),
		)
		require.NoError(t, err)
		require.NotNil(t, sys)

		// Use the system's configured host/port to pass host validation
		cfg := sys.(*actorSystem).remoteConfig
		grain := &internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: "GoAkt_Reserved"}, // triggers reserved name error in recreateGrain
			Host:    cfg.BindAddr(),
			Port:    int32(cfg.BindPort()),
		}

		_, rerr := sys.(*actorSystem).RemoteActivateGrain(ctx, connect.NewRequest(&internalpb.RemoteActivateGrainRequest{Grain: grain}))
		require.Error(t, rerr)
		var cErr *connect.Error
		require.True(t, errors.As(rerr, &cErr))
		require.Equal(t, connect.CodeInternal, cErr.Code())
		u := cErr.Unwrap()
		require.ErrorContains(t, u, gerrors.ErrReservedName.Error())
	})
}
