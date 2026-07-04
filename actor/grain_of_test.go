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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/discovery"
	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	mockcluster "github.com/tochemey/goakt/v4/mocks/cluster"
	mockremote "github.com/tochemey/goakt/v4/mocks/remoteclient"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// grainOfCounterActivations counts OnActivate calls of grainOfCounterGrain across a test.
var grainOfCounterActivations atomic.Int32

// grainOfCounterGrain is a zero-value constructible grain that records its activations.
type grainOfCounterGrain struct{}

func (g *grainOfCounterGrain) OnActivate(context.Context, *GrainProps) error {
	grainOfCounterActivations.Add(1)
	return nil
}

func (g *grainOfCounterGrain) OnReceive(ctx *GrainContext) {
	ctx.NoErr()
}

func (g *grainOfCounterGrain) OnDeactivate(context.Context, *GrainProps) error {
	return nil
}

// collisionGrain and CollisionGrain deliberately differ only in case: the kind
// registry lowercases type names, so both map to the kind "actor.collisiongrain".
// They exercise the kind-conflict detection in GrainOf.
type collisionGrain struct{}

func (g *collisionGrain) OnActivate(context.Context, *GrainProps) error   { return nil }
func (g *collisionGrain) OnReceive(ctx *GrainContext)                     { ctx.NoErr() }
func (g *collisionGrain) OnDeactivate(context.Context, *GrainProps) error { return nil }

// CollisionGrain collides by kind name with collisionGrain.
type CollisionGrain struct{}

func (g *CollisionGrain) OnActivate(context.Context, *GrainProps) error   { return nil }
func (g *CollisionGrain) OnReceive(ctx *GrainContext)                     { ctx.NoErr() }
func (g *CollisionGrain) OnDeactivate(context.Context, *GrainProps) error { return nil }

// valueKindGrain implements Grain with value receivers so the non-pointer
// type itself satisfies the Grain interface.
type valueKindGrain struct{}

func (valueKindGrain) OnActivate(context.Context, *GrainProps) error   { return nil }
func (valueKindGrain) OnReceive(ctx *GrainContext)                     { ctx.NoErr() }
func (valueKindGrain) OnDeactivate(context.Context, *GrainProps) error { return nil }

func TestGrainOf_LocalActivation(t *testing.T) {
	ctx := t.Context()
	testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, testSystem)

	require.NoError(t, testSystem.Start(ctx))
	pause.For(time.Second)

	identity, err := GrainOf[*MockGrain](ctx, testSystem, "grainof-local")
	require.NoError(t, err)
	require.NotNil(t, identity)
	require.Equal(t, types.Name((*MockGrain)(nil)), identity.Kind())

	// the kind is auto-registered without a RegisterGrainKind call
	sys := testSystem.(*actorSystem)
	require.True(t, sys.registry.Exists((*MockGrain)(nil)))

	// the grain is live and processes messages
	response, err := testSystem.AskGrain(ctx, identity, new(testpb.TestReply), time.Second)
	require.NoError(t, err)
	require.NotNil(t, response)

	require.NoError(t, testSystem.Stop(ctx))
}

func TestGrainOf_Idempotent(t *testing.T) {
	ctx := t.Context()
	testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, testSystem)

	require.NoError(t, testSystem.Start(ctx))
	pause.For(time.Second)

	grainOfCounterActivations.Store(0)

	first, err := GrainOf[*grainOfCounterGrain](ctx, testSystem, "grainof-idempotent")
	require.NoError(t, err)
	require.NotNil(t, first)

	second, err := GrainOf[*grainOfCounterGrain](ctx, testSystem, "grainof-idempotent")
	require.NoError(t, err)
	require.NotNil(t, second)

	require.True(t, first.Equal(second))
	require.EqualValues(t, 1, grainOfCounterActivations.Load())

	require.NoError(t, testSystem.Stop(ctx))
}

func TestGrainOf_OptionsHonored(t *testing.T) {
	ctx := t.Context()
	testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, testSystem)

	require.NoError(t, testSystem.Start(ctx))
	pause.For(time.Second)

	identity, err := GrainOf[*MockGrain](ctx, testSystem, "grainof-options",
		WithGrainInitMaxRetries(7),
		WithGrainMailboxCapacity(32))
	require.NoError(t, err)
	require.NotNil(t, identity)

	sys := testSystem.(*actorSystem)
	pid, ok := sys.grains.Get(identity.String())
	require.True(t, ok)
	require.EqualValues(t, 7, pid.config.initMaxRetries.Load())
	require.EqualValues(t, 32, pid.mailbox.Capacity())

	require.NoError(t, testSystem.Stop(ctx))
}

func TestGrainOf_RemoteActivationDoesNotConstructInstance(t *testing.T) {
	ctx := t.Context()
	name := "grainof-remote"
	identity := newGrainIdentity((*grainOfCounterGrain)(nil), name)
	remotePeer := &cluster.Peer{Host: "192.0.2.20", PeersPort: 15020, RemotingPort: 16020}
	alternatePeer := &cluster.Peer{Host: "192.0.2.21", PeersPort: 15021, RemotingPort: 16021}
	localPeer := &cluster.Peer{Host: "127.0.0.1", PeersPort: 14020, RemotingPort: 8090}

	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewClient(t)
	node := &discovery.Node{Host: localPeer.Host, PeersPort: localPeer.PeersPort, RemotingPort: localPeer.RemotingPort}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)

	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
	cl.EXPECT().GetGrain(ctx, identity.String()).Return(nil, cluster.ErrGrainNotFound)
	cl.EXPECT().Members(ctx).Return([]*cluster.Peer{remotePeer, alternatePeer}, nil)
	cl.EXPECT().NextRoundRobinValue(ctx, cluster.GrainsRoundRobinKey).Return(1, nil)
	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Once()
	cl.EXPECT().PutGrain(mock.Anything, mock.MatchedBy(func(actual *internalpb.Grain) bool {
		return actual != nil && actual.GetGrainId().GetValue() == identity.String()
	})).Return(nil).Once()
	rem.EXPECT().RemoteActivateGrain(ctx, remotePeer.Host, remotePeer.RemotingPort, mock.MatchedBy(func(req *remote.GrainRequest) bool {
		return req != nil && req.Name == identity.Name() && req.Kind == identity.Kind()
	})).Return(nil)

	grainOfCounterActivations.Store(0)

	got, err := GrainOf[*grainOfCounterGrain](ctx, sys, name, WithActivationStrategy(RoundRobinActivation))
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, identity.String(), got.String())

	// remote activation never constructs nor activates a local instance
	require.EqualValues(t, 0, grainOfCounterActivations.Load())
	_, ok := sys.grains.Get(identity.String())
	require.False(t, ok)

	// the kind is registered locally so this node can host the grain later
	require.True(t, sys.registry.Exists((*grainOfCounterGrain)(nil)))
}

func TestGrainOf_ExistingPidSkipsProvider(t *testing.T) {
	ctx := t.Context()
	testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, testSystem)

	require.NoError(t, testSystem.Start(ctx))
	pause.For(time.Second)

	// activate through the factory path with a marker instance
	marker := NewMockGrain()
	identity, err := testSystem.GrainIdentity(ctx, "grainof-existing", func(context.Context) (Grain, error) {
		return marker, nil
	})
	require.NoError(t, err)
	require.NotNil(t, identity)

	// resolving the same grain via GrainOf must not construct a new instance
	resolved, err := GrainOf[*MockGrain](ctx, testSystem, "grainof-existing")
	require.NoError(t, err)
	require.True(t, identity.Equal(resolved))

	sys := testSystem.(*actorSystem)
	pid, ok := sys.grains.Get(identity.String())
	require.True(t, ok)
	require.Same(t, marker, pid.getGrain())

	require.NoError(t, testSystem.Stop(ctx))
}

func TestGrainIdentity_DeprecatedPathUsesFactoryInstance(t *testing.T) {
	ctx := t.Context()
	testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, testSystem)

	require.NoError(t, testSystem.Start(ctx))
	pause.For(time.Second)

	marker := NewMockGrain()
	identity, err := testSystem.GrainIdentity(ctx, "factory-instance", func(context.Context) (Grain, error) {
		return marker, nil
	})
	require.NoError(t, err)
	require.NotNil(t, identity)

	// the activated grain is the exact instance the factory returned
	sys := testSystem.(*actorSystem)
	pid, ok := sys.grains.Get(identity.String())
	require.True(t, ok)
	require.Same(t, marker, pid.getGrain())

	require.NoError(t, testSystem.Stop(ctx))
}

func TestGrainOf_KindConflict(t *testing.T) {
	ctx := t.Context()
	testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, testSystem)

	require.NoError(t, testSystem.Start(ctx))
	pause.For(time.Second)

	// registers the kind "actor.collisiongrain" for collisionGrain
	identity, err := GrainOf[*collisionGrain](ctx, testSystem, "conflict-original")
	require.NoError(t, err)
	require.NotNil(t, identity)

	// a different type mapping to the same kind must be rejected instead of
	// silently instantiating the registered type
	conflicting, err := GrainOf[*CollisionGrain](ctx, testSystem, "conflict-other")
	require.Error(t, err)
	require.ErrorIs(t, err, gerrors.ErrGrainKindConflict)
	require.Nil(t, conflicting)

	require.NoError(t, testSystem.Stop(ctx))
}

func TestGrainOf_NonPointerKind(t *testing.T) {
	ctx := t.Context()
	testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, testSystem)

	require.NoError(t, testSystem.Start(ctx))
	pause.For(time.Second)

	identity, err := GrainOf[valueKindGrain](ctx, testSystem, "grainof-value-kind")
	require.Error(t, err)
	require.ErrorIs(t, err, gerrors.ErrInvalidGrainKind)
	require.Nil(t, identity)

	require.NoError(t, testSystem.Stop(ctx))
}

func TestGrainOf_ReservedName(t *testing.T) {
	ctx := t.Context()
	testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, testSystem)

	require.NoError(t, testSystem.Start(ctx))
	pause.For(time.Second)

	identity, err := GrainOf[*MockGrain](ctx, testSystem, "GoAktGrain")
	require.Error(t, err)
	require.ErrorIs(t, err, gerrors.ErrReservedName)
	require.Nil(t, identity)

	require.NoError(t, testSystem.Stop(ctx))
}

func TestGrainOf_SystemNotStarted(t *testing.T) {
	ctx := t.Context()

	identity, err := GrainOf[*MockGrain](ctx, nil, "grainof-nil-system")
	require.Error(t, err)
	require.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
	require.Nil(t, identity)

	testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, testSystem)

	identity, err = GrainOf[*MockGrain](ctx, testSystem, "grainof-not-started")
	require.Error(t, err)
	require.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
	require.Nil(t, identity)
}

func TestGrainOf_InvalidName(t *testing.T) {
	ctx := t.Context()
	testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, testSystem)

	require.NoError(t, testSystem.Start(ctx))
	pause.For(time.Second)

	identity, err := GrainOf[*MockGrain](ctx, testSystem, strings.Repeat("a", 300))
	require.Error(t, err)
	require.Nil(t, identity)

	require.NoError(t, testSystem.Stop(ctx))
}
