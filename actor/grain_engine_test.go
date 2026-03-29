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
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/datacenter"
	"github.com/tochemey/goakt/v4/discovery"
	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	internalnet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/remoteclient"
	"github.com/tochemey/goakt/v4/log"
	mockcluster "github.com/tochemey/goakt/v4/mocks/cluster"
	mockremote "github.com/tochemey/goakt/v4/mocks/remoteclient"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// grainTestCtxKey is a custom type for context keys in grain tests (avoids SA1029 empty struct key).
type grainTestCtxKey struct{}

func newActivationTestSystem(t *testing.T, grain Grain, name string, register bool) (*actorSystem, *mockcluster.Cluster, *mockremote.Client, *GrainIdentity) {
	t.Helper()

	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewClient(t)
	node := &discovery.Node{Host: "127.0.0.1", PeersPort: 14000, RemotingPort: 15000}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)
	if register {
		sys.registry.Register(grain)
	}

	return sys, cl, rem, newGrainIdentity(grain, name)
}

type activationProbe struct {
	started chan struct{}
	release chan struct{}
	count   atomic.Int32
}

var activationProbePtr atomic.Pointer[activationProbe]

type activationProbeGrain struct{}

func (g *activationProbeGrain) OnActivate(ctx context.Context, props *GrainProps) error {
	probe := activationProbePtr.Load()
	if probe != nil {
		probe.count.Add(1)
		select {
		case probe.started <- struct{}{}:
		default:
		}
		<-probe.release
	}
	return nil
}

func (g *activationProbeGrain) OnReceive(ctx *GrainContext) {
	ctx.NoErr()
}

func (g *activationProbeGrain) OnDeactivate(ctx context.Context, props *GrainProps) error {
	return nil
}

func TestGrainIdentity_RemoteActivationOnDifferentPeer(t *testing.T) {
	ctx := t.Context()
	grain := NewMockGrain()
	name := "remote-grain"
	identity := newGrainIdentity(grain, name)
	remotePeer := &cluster.Peer{Host: "192.0.2.10", PeersPort: 15000, RemotingPort: 16000}
	alternatePeer := &cluster.Peer{Host: "192.0.2.11", PeersPort: 15001, RemotingPort: 16001}
	localPeer := &cluster.Peer{Host: "127.0.0.1", PeersPort: 14000, RemotingPort: 8080}

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
		return req != nil && req.Name == identity.String()
	})).Return(nil)

	got, err := sys.GrainIdentity(ctx, name, func(context.Context) (Grain, error) {
		return grain, nil
	}, WithActivationStrategy(RoundRobinActivation))

	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, identity.String(), got.String())
}

func TestGrainIdentity_RemoteActivationOnDifferentPeer_WithBrotliCompression(t *testing.T) {
	ctx := t.Context()
	grain := NewMockGrain()
	name := "remote-grain-brotli"
	identity := newGrainIdentity(grain, name)

	remotePeer := &cluster.Peer{Host: "192.0.2.40", PeersPort: 15010, RemotingPort: 16010}
	alternatePeer := &cluster.Peer{Host: "192.0.2.41", PeersPort: 15011, RemotingPort: 16011}
	localPeer := &cluster.Peer{Host: "127.0.0.1", PeersPort: 14010, RemotingPort: 8085}

	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewClient(t)
	node := &discovery.Node{Host: localPeer.Host, PeersPort: localPeer.PeersPort, RemotingPort: localPeer.RemotingPort}
	actorSystem := MockSimpleClusterReadyActorSystem(rem, cl, node, remote.WithCompression(remote.BrotliCompression))

	// Assert the system's remote config has the expected compression.
	require.Equal(t, remote.BrotliCompression, actorSystem.remoteConfig.Compression())

	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
	cl.EXPECT().GetGrain(ctx, identity.String()).Return(nil, cluster.ErrGrainNotFound)
	cl.EXPECT().Members(ctx).Return([]*cluster.Peer{remotePeer, alternatePeer}, nil)
	cl.EXPECT().NextRoundRobinValue(ctx, cluster.GrainsRoundRobinKey).Return(1, nil)
	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Once()
	cl.EXPECT().PutGrain(mock.Anything, mock.MatchedBy(func(actual *internalpb.Grain) bool {
		return actual != nil && actual.GetGrainId().GetValue() == identity.String()
	})).Return(nil).Once()
	rem.EXPECT().RemoteActivateGrain(ctx, remotePeer.Host, remotePeer.RemotingPort, mock.MatchedBy(func(req *remote.GrainRequest) bool {
		return req != nil && req.Name == identity.String()
	})).Return(nil)

	got, err := actorSystem.GrainIdentity(ctx, name, func(context.Context) (Grain, error) {
		return grain, nil
	}, WithActivationStrategy(RoundRobinActivation))

	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, identity.String(), got.String())
}

func TestGrainIdentity_RemoteActivationOnDifferentPeer_WithZstandardCompression(t *testing.T) {
	ctx := t.Context()
	grain := NewMockGrain()
	name := "remote-grain-zstd"
	identity := newGrainIdentity(grain, name)

	remotePeer := &cluster.Peer{Host: "192.0.2.40", PeersPort: 15010, RemotingPort: 16010}
	alternatePeer := &cluster.Peer{Host: "192.0.2.41", PeersPort: 15011, RemotingPort: 16011}
	localPeer := &cluster.Peer{Host: "127.0.0.1", PeersPort: 14010, RemotingPort: 8085}

	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewClient(t)
	node := &discovery.Node{Host: localPeer.Host, PeersPort: localPeer.PeersPort, RemotingPort: localPeer.RemotingPort}
	actorSystem := MockSimpleClusterReadyActorSystem(rem, cl, node, remote.WithCompression(remote.ZstdCompression))

	// Assert the system's remote config has the expected compression.
	require.Equal(t, remote.ZstdCompression, actorSystem.remoteConfig.Compression())

	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
	cl.EXPECT().GetGrain(ctx, identity.String()).Return(nil, cluster.ErrGrainNotFound)
	cl.EXPECT().Members(ctx).Return([]*cluster.Peer{remotePeer, alternatePeer}, nil)
	cl.EXPECT().NextRoundRobinValue(ctx, cluster.GrainsRoundRobinKey).Return(1, nil)
	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Once()
	cl.EXPECT().PutGrain(mock.Anything, mock.MatchedBy(func(actual *internalpb.Grain) bool {
		return actual != nil && actual.GetGrainId().GetValue() == identity.String()
	})).Return(nil).Once()
	rem.EXPECT().RemoteActivateGrain(ctx, remotePeer.Host, remotePeer.RemotingPort, mock.MatchedBy(func(req *remote.GrainRequest) bool {
		return req != nil && req.Name == identity.String()
	})).Return(nil)

	got, err := actorSystem.GrainIdentity(ctx, name, func(context.Context) (Grain, error) {
		return grain, nil
	}, WithActivationStrategy(RoundRobinActivation))

	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, identity.String(), got.String())
}

func TestGrainIdentity_RemoteActivationOnDifferentPeer_WithGzipCompression(t *testing.T) {
	ctx := t.Context()
	grain := NewMockGrain()
	name := "remote-grain-gzip"
	identity := newGrainIdentity(grain, name)

	remotePeer := &cluster.Peer{Host: "192.0.2.40", PeersPort: 15010, RemotingPort: 16010}
	alternatePeer := &cluster.Peer{Host: "192.0.2.41", PeersPort: 15011, RemotingPort: 16011}
	localPeer := &cluster.Peer{Host: "127.0.0.1", PeersPort: 14010, RemotingPort: 8085}

	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewClient(t)
	node := &discovery.Node{Host: localPeer.Host, PeersPort: localPeer.PeersPort, RemotingPort: localPeer.RemotingPort}
	actorSystem := MockSimpleClusterReadyActorSystem(rem, cl, node, remote.WithCompression(remote.GzipCompression))

	// Assert the system's remote config has the expected compression.
	require.Equal(t, remote.GzipCompression, actorSystem.remoteConfig.Compression())

	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
	cl.EXPECT().GetGrain(ctx, identity.String()).Return(nil, cluster.ErrGrainNotFound)
	cl.EXPECT().Members(ctx).Return([]*cluster.Peer{remotePeer, alternatePeer}, nil)
	cl.EXPECT().NextRoundRobinValue(ctx, cluster.GrainsRoundRobinKey).Return(1, nil)
	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Once()
	cl.EXPECT().PutGrain(mock.Anything, mock.MatchedBy(func(actual *internalpb.Grain) bool {
		return actual != nil && actual.GetGrainId().GetValue() == identity.String()
	})).Return(nil).Once()
	rem.EXPECT().RemoteActivateGrain(ctx, remotePeer.Host, remotePeer.RemotingPort, mock.MatchedBy(func(req *remote.GrainRequest) bool {
		return req != nil && req.Name == identity.String()
	})).Return(nil)

	got, err := actorSystem.GrainIdentity(ctx, name, func(context.Context) (Grain, error) {
		return grain, nil
	}, WithActivationStrategy(RoundRobinActivation))

	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, identity.String(), got.String())
}

func TestGrainIdentity_RemoteActivationErrorPropagates(t *testing.T) {
	ctx := t.Context()
	grain := NewMockGrain()
	name := "remote-grain-error"
	identity := newGrainIdentity(grain, name)
	remotePeer := &cluster.Peer{Host: "192.0.2.20", PeersPort: 17000, RemotingPort: 18000}
	alternatePeer := &cluster.Peer{Host: "192.0.2.21", PeersPort: 17001, RemotingPort: 18001}
	localPeer := &cluster.Peer{Host: "127.0.0.1", PeersPort: 16500, RemotingPort: 8181}

	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewClient(t)
	clientErr := errors.New("remote activate failed")
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
	cl.EXPECT().RemoveGrain(mock.Anything, identity.String()).Return(nil).Once()
	rem.EXPECT().RemoteActivateGrain(ctx, remotePeer.Host, remotePeer.RemotingPort, mock.Anything).Return(clientErr)

	got, err := sys.GrainIdentity(ctx, name, func(context.Context) (Grain, error) {
		return grain, nil
	}, WithActivationStrategy(RoundRobinActivation))

	require.Error(t, err)
	require.ErrorIs(t, err, clientErr)
	require.Nil(t, got)
}

func TestGrainIdentity_RemoteActivationWireEncodingError(t *testing.T) {
	ctx := t.Context()
	grain := NewMockGrain()
	name := "remote-wire-error"
	identity := newGrainIdentity(grain, name)
	remotePeer := &cluster.Peer{Host: "192.0.2.30", PeersPort: 17500, RemotingPort: 18500}
	alternatePeer := &cluster.Peer{Host: "192.0.2.31", PeersPort: 17501, RemotingPort: 18501}
	localPeer := &cluster.Peer{Host: "127.0.0.1", PeersPort: 17550, RemotingPort: 8250}
	failErr := errors.New("dependency encode failure")

	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewClient(t)
	node := &discovery.Node{Host: localPeer.Host, PeersPort: localPeer.PeersPort, RemotingPort: localPeer.RemotingPort}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)

	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
	cl.EXPECT().GetGrain(ctx, identity.String()).Return(nil, cluster.ErrGrainNotFound)
	cl.EXPECT().Members(ctx).Return([]*cluster.Peer{remotePeer, alternatePeer}, nil)
	cl.EXPECT().NextRoundRobinValue(ctx, cluster.GrainsRoundRobinKey).Return(1, nil)

	got, err := sys.GrainIdentity(ctx, name, func(context.Context) (Grain, error) {
		return grain, nil
	}, WithGrainDependencies(&MockFailingDependency{err: failErr}), WithActivationStrategy(RoundRobinActivation))

	require.Error(t, err)
	require.ErrorIs(t, err, failErr)
	require.Nil(t, got)
	rem.AssertNotCalled(t, "RemoteActivateGrain", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestTryRemoteGrainActivation(t *testing.T) {
	t.Run("owner remote activates", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, _, rem, identity := newActivationTestSystem(t, grain, "owner-remote", true)
		owner := &internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: identity.String()},
			Host:    "192.0.2.50",
			Port:    16050,
		}

		rem.EXPECT().RemoteActivateGrain(ctx, owner.Host, int(owner.Port), mock.Anything).Return(nil)

		handled, err := sys.tryRemoteGrainActivation(ctx, identity, grain, newGrainConfig(), owner)
		require.NoError(t, err)
		require.True(t, handled)
	})

	t.Run("owner remote activation error", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, rem, identity := newActivationTestSystem(t, grain, "owner-remote-error", true)
		owner := &internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: identity.String()},
			Host:    "192.0.2.51",
			Port:    16051,
		}
		expectedErr := errors.New("remote activate failed")

		rem.EXPECT().RemoteActivateGrain(ctx, owner.Host, int(owner.Port), mock.Anything).Return(expectedErr)
		cl.EXPECT().RemoveGrain(ctx, identity.String()).Return(nil).Once()

		handled, err := sys.tryRemoteGrainActivation(ctx, identity, grain, newGrainConfig(), owner)
		require.NoError(t, err)
		require.False(t, handled)
	})

	t.Run("owner local returns false", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, _, rem, identity := newActivationTestSystem(t, grain, "owner-local", true)
		owner := &internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: identity.String()},
			Host:    sys.Host(),
			Port:    int32(sys.Port()),
		}

		handled, err := sys.tryRemoteGrainActivation(ctx, identity, grain, newGrainConfig(), owner)
		require.NoError(t, err)
		require.False(t, handled)
		rem.AssertNotCalled(t, "RemoteActivateGrain", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("owner empty returns false", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, _, rem, identity := newActivationTestSystem(t, grain, "owner-empty", true)
		owner := &internalpb.Grain{}

		handled, err := sys.tryRemoteGrainActivation(ctx, identity, grain, newGrainConfig(), owner)
		require.NoError(t, err)
		require.False(t, handled)
		rem.AssertNotCalled(t, "RemoteActivateGrain", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("activation peer selection error", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, _, identity := newActivationTestSystem(t, grain, "peer-error", true)
		config := newGrainConfig(WithActivationRole("billing"))

		cl.EXPECT().Members(ctx).Return([]*cluster.Peer{{Host: "192.0.2.52", Roles: []string{"api"}}}, nil)

		handled, err := sys.tryRemoteGrainActivation(ctx, identity, grain, config, nil)
		require.Error(t, err)
		require.False(t, handled)
	})

	t.Run("no activation peer available", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, _, identity := newActivationTestSystem(t, grain, "peer-none", true)
		localPeer := &cluster.Peer{Host: sys.clusterNode.Host, PeersPort: sys.clusterNode.PeersPort}

		cl.EXPECT().Members(ctx).Return([]*cluster.Peer{localPeer}, nil)

		handled, err := sys.tryRemoteGrainActivation(ctx, identity, grain, newGrainConfig(), nil)
		require.NoError(t, err)
		require.False(t, handled)
	})

	t.Run("activation peer is local", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, _, identity := newActivationTestSystem(t, grain, "peer-local", true)
		localPeer := &cluster.Peer{
			Host:         sys.clusterNode.Host,
			PeersPort:    sys.clusterNode.PeersPort,
			RemotingPort: sys.clusterNode.RemotingPort,
		}
		remotePeer := &cluster.Peer{Host: "192.0.2.53", PeersPort: 14001, RemotingPort: 15001}

		cl.EXPECT().Members(ctx).Return([]*cluster.Peer{localPeer, remotePeer}, nil)
		cl.EXPECT().NextRoundRobinValue(ctx, cluster.GrainsRoundRobinKey).Return(1, nil)

		handled, err := sys.tryRemoteGrainActivation(ctx, identity, grain, newGrainConfig(WithActivationStrategy(RoundRobinActivation)), nil)
		require.NoError(t, err)
		require.False(t, handled)
	})
}

func TestTryPeerActivation(t *testing.T) {
	t.Run("returns error when claim fails", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, rem, identity := newActivationTestSystem(t, grain, "peer-claim-error", true)
		peer := &cluster.Peer{Host: "192.0.2.60", PeersPort: 15060, RemotingPort: 16060}
		expectedErr := errors.New("claim error")

		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, expectedErr).Once()

		handled, err := sys.tryPeerActivation(ctx, identity, grain, newGrainConfig(), peer)
		require.ErrorIs(t, err, expectedErr)
		require.False(t, handled)
		rem.AssertNotCalled(t, "RemoteActivateGrain", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("returns handled when claim not acquired", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, rem, identity := newActivationTestSystem(t, grain, "peer-claim-miss", true)
		peer := &cluster.Peer{Host: "192.0.2.61", PeersPort: 15061, RemotingPort: 16061}

		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(nil, cluster.ErrGrainNotFound).Once()

		handled, err := sys.tryPeerActivation(ctx, identity, grain, newGrainConfig(), peer)
		require.NoError(t, err)
		require.True(t, handled)
		rem.AssertNotCalled(t, "RemoteActivateGrain", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("returns error when put grain fails", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, rem, identity := newActivationTestSystem(t, grain, "peer-put-error", true)
		peer := &cluster.Peer{Host: "192.0.2.62", PeersPort: 15062, RemotingPort: 16062}
		expectedErr := errors.New("put failed")

		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Once()
		cl.EXPECT().PutGrain(mock.Anything, mock.MatchedBy(func(actual *internalpb.Grain) bool {
			return actual != nil && actual.GetGrainId().GetValue() == identity.String()
		})).Return(expectedErr).Once()

		handled, err := sys.tryPeerActivation(ctx, identity, grain, newGrainConfig(), peer)
		require.ErrorIs(t, err, expectedErr)
		require.False(t, handled)
		rem.AssertNotCalled(t, "RemoteActivateGrain", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("returns handled when owner already exists", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, rem, identity := newActivationTestSystem(t, grain, "peer-owner-exists", true)
		peer := &cluster.Peer{Host: "192.0.2.63", PeersPort: 15063, RemotingPort: 16063}
		owner := &internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: identity.String()},
			Host:    "192.0.2.64",
			Port:    16064,
		}

		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil).Once()

		handled, err := sys.tryPeerActivation(ctx, identity, grain, newGrainConfig(), peer)
		require.NoError(t, err)
		require.True(t, handled)
		rem.AssertNotCalled(t, "RemoteActivateGrain", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})
}

func TestActivateGrainLocally(t *testing.T) {
	t.Run("returns error when claim fails", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, _, identity := newActivationTestSystem(t, grain, "local-claim-error", false)
		expectedErr := errors.New("claim error")

		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, expectedErr).Once()

		err := sys.activateGrainLocally(ctx, identity, grain, newGrainConfig(), nil)
		require.ErrorIs(t, err, expectedErr)
		require.True(t, sys.registry.Exists(grain))
	})

	t.Run("returns error when wire grain encoding fails", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, _, _, identity := newActivationTestSystem(t, grain, "local-wire-error", false)
		expectedErr := errors.New("wire encode failed")
		config := newGrainConfig(WithGrainDependencies(&MockFailingDependency{err: expectedErr}))

		err := sys.activateGrainLocally(ctx, identity, grain, config, nil)
		require.ErrorIs(t, err, expectedErr)
	})

	t.Run("returns error when put grain fails", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, _, identity := newActivationTestSystem(t, grain, "local-put-error", false)
		expectedErr := errors.New("put grain failed")

		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Once()
		cl.EXPECT().PutGrain(mock.Anything, mock.MatchedBy(func(actual *internalpb.Grain) bool {
			return actual != nil && actual.GetGrainId().GetValue() == identity.String()
		})).Return(expectedErr).Once()

		err := sys.activateGrainLocally(ctx, identity, grain, newGrainConfig(), nil)
		require.ErrorIs(t, err, expectedErr)
	})

	t.Run("returns nil when claim owner mismatch", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, _, identity := newActivationTestSystem(t, grain, "local-claim-mismatch", true)
		remoteOwner := &internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: identity.String()},
			Host:    "192.0.2.70",
			Port:    17070,
		}

		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(remoteOwner, nil).Once()

		err := sys.activateGrainLocally(ctx, identity, grain, newGrainConfig(), nil)
		require.NoError(t, err)
		require.Empty(t, grain.name)

		_, ok := sys.grains.Get(identity.String())
		require.False(t, ok)
	})

	t.Run("continues when claim owner missing", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, _, identity := newActivationTestSystem(t, grain, "local-claim-missing", true)

		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(nil, cluster.ErrGrainNotFound).Once()

		err := sys.activateGrainLocally(ctx, identity, grain, newGrainConfig(), nil)
		require.NoError(t, err)
		require.Equal(t, identity.Name(), grain.name)

		_, ok := sys.grains.Get(identity.String())
		require.True(t, ok)
	})

	t.Run("activation failure cleans up claim", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrainActivationFailure()
		sys, cl, _, identity := newActivationTestSystem(t, grain, "local-activate-fail", true)
		config := newGrainConfig(
			WithGrainInitMaxRetries(1),
			WithGrainInitTimeout(10*time.Millisecond),
		)

		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Once()
		cl.EXPECT().PutGrain(mock.Anything, mock.MatchedBy(func(actual *internalpb.Grain) bool {
			return actual != nil && actual.GetGrainId().GetValue() == identity.String()
		})).Return(nil).Once()
		cl.EXPECT().RemoveGrain(mock.Anything, identity.String()).Return(nil).Once()

		err := sys.activateGrainLocally(ctx, identity, grain, config, nil)
		require.ErrorIs(t, err, gerrors.ErrGrainActivationFailure)
	})

	t.Run("returns publish error when owner set", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, _, _, identity := newActivationTestSystem(t, grain, "local-publish-error", true)
		expectedErr := errors.New("dependency encode failure")
		config := newGrainConfig(WithGrainDependencies(&MockFailingDependency{err: expectedErr}))
		owner := &internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: identity.String()},
			Host:    sys.Host(),
			Port:    int32(sys.Port()),
		}

		err := sys.activateGrainLocally(ctx, identity, grain, config, owner)
		require.ErrorIs(t, err, expectedErr)
	})

	t.Run("skips activation when already active", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		grain.name = "pre-activated"
		sys, _, _, identity := newActivationTestSystem(t, grain, "local-active", true)
		pid := newGrainPID(identity, grain, sys, newGrainConfig())
		pid.activated.Store(true)
		sys.grains.Set(identity.String(), pid)
		owner := &internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: identity.String()},
			Host:    sys.Host(),
			Port:    int32(sys.Port()),
		}

		err := sys.activateGrainLocally(ctx, identity, grain, newGrainConfig(), owner)
		require.NoError(t, err)
		require.Equal(t, "pre-activated", grain.name)
	})

	t.Run("returns error when activation barrier times out", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, _, _, identity := newActivationTestSystem(t, grain, "local-barrier-timeout", false)
		sys.grainBarrier = newGrainActivationBarrier(2, 10*time.Millisecond)
		owner := &internalpb.Grain{GrainId: &internalpb.GrainId{Value: identity.String()}}

		err := sys.activateGrainLocally(ctx, identity, grain, newGrainConfig(), owner)
		require.ErrorIs(t, err, gerrors.ErrGrainActivationBarrierTimeout)
	})
}

func TestFindActivationPeer_ErrorsWhenRoleMissingEverywhere(t *testing.T) {
	ctx := t.Context()
	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewClient(t)
	node := &discovery.Node{Host: "127.0.0.1", PeersPort: 14000, RemotingPort: 8080, Roles: []string{"api"}}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)

	role := "analytics"
	cl.EXPECT().Members(ctx).Return([]*cluster.Peer{{Host: "198.51.100.5", Roles: []string{"billing"}}}, nil)

	peer, err := sys.findActivationPeer(ctx, newGrainConfig(WithActivationRole(role)))
	require.Error(t, err)
	require.Nil(t, peer)
	require.ErrorContains(t, err, role)
}

func TestAskGrain_ClusterFallbackAutoProvisions(t *testing.T) {
	ctx := t.Context()
	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewClient(t)
	node := &discovery.Node{Host: "127.0.0.1", PeersPort: 9003, RemotingPort: 9103}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)

	grain := NewMockGrain()
	sys.registry.Register(grain)
	identity := newGrainIdentity(grain, "auto-provision")

	cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(nil, cluster.ErrGrainNotFound).Once()
	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Twice()
	cl.EXPECT().PutGrain(mock.Anything, mock.MatchedBy(func(actual *internalpb.Grain) bool {
		return actual != nil && actual.GetGrainId().GetValue() == identity.String()
	})).Return(nil).Once()

	resp, err := sys.AskGrain(ctx, identity, &testpb.TestReply{}, time.Second)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, "received message", resp.(*testpb.Reply).Content)

	// AskGrain activates the grain synchronously via ensureGrainProcess,
	// so it should be available immediately. However, use Eventually to
	// handle any potential race conditions in CI environments where
	// scheduling might cause slight delays.
	require.Eventually(t, func() bool {
		_, ok := sys.grains.Get(identity.String())
		return ok
	}, 100*time.Millisecond, 5*time.Millisecond, "grain should be activated and stored after AskGrain returns")
}

func TestEnsureNewGrainProcess_ActivationBarrierTimeout(t *testing.T) {
	ctx := t.Context()
	grain := NewMockGrain()
	sys, _, _, identity := newActivationTestSystem(t, grain, "barrier-new", true)

	sys.grainBarrier = newGrainActivationBarrier(2, 10*time.Millisecond)

	process, err := sys.ensureNewGrainProcess(ctx, identity)
	require.ErrorIs(t, err, gerrors.ErrGrainActivationBarrierTimeout)
	require.Nil(t, process)
}

func TestEnsureExistingGrainProcess_ActivationBarrierTimeout(t *testing.T) {
	ctx := t.Context()
	grain := NewMockGrain()
	sys, _, _, identity := newActivationTestSystem(t, grain, "barrier-existing", true)
	pid := newGrainPID(identity, grain, sys, newGrainConfig())
	sys.grains.Set(identity.String(), pid)

	sys.grainBarrier = newGrainActivationBarrier(2, 10*time.Millisecond)

	process, err := sys.ensureExistingGrainProcess(ctx, identity, pid)
	require.ErrorIs(t, err, gerrors.ErrGrainActivationBarrierTimeout)
	require.Nil(t, process)
}

func TestRecreateGrain_SingleflightActivation(t *testing.T) {
	ctx := t.Context()
	sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	t.Cleanup(func() { _ = sys.Stop(ctx) })

	as := sys.(*actorSystem)
	as.registry.Register(&activationProbeGrain{})

	identity := newGrainIdentity(&activationProbeGrain{}, "singleflight")
	pid := newGrainPID(identity, &activationProbeGrain{}, sys, newGrainConfig())
	wire, err := pid.toWireGrain()
	require.NoError(t, err)

	probe := &activationProbe{
		started: make(chan struct{}, 16),
		release: make(chan struct{}),
	}
	activationProbePtr.Store(probe)

	closeRelease := sync.OnceFunc(func() {
		close(probe.release)
	})
	t.Cleanup(func() {
		activationProbePtr.Store(nil)
		closeRelease()
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- as.recreateGrain(ctx, wire)
	}()

	select {
	case <-probe.started:
	case <-time.After(1 * time.Second):
		t.Fatal("activation did not start")
	}

	const concurrent = 25
	var wg sync.WaitGroup
	wg.Add(concurrent)
	errs := make(chan error, concurrent)
	for range concurrent {
		go func() {
			defer wg.Done()
			errs <- as.recreateGrain(ctx, wire)
		}()
	}

	select {
	case <-probe.started:
		t.Fatalf("expected single activation while activation is in flight")
	case <-time.After(200 * time.Millisecond):
	}

	closeRelease()
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	require.NoError(t, <-errCh)
	require.Equal(t, int32(1), probe.count.Load())
}

func TestLocalSend_ErrorsWhenEnsureGrainProcessFails(t *testing.T) {
	ctx := t.Context()
	grain := NewMockGrain()
	sys, _, _, identity := newActivationTestSystem(t, grain, "missing-registry", false)

	process := newGrainPID(identity, grain, sys, newGrainConfig())
	sys.grains.Set(identity.String(), process)

	resp, err := sys.localSend(ctx, identity, &testpb.TestReply{}, time.Second, true)
	require.ErrorIs(t, err, gerrors.ErrGrainNotRegistered)
	require.Nil(t, resp)

	_, ok := sys.grains.Get(identity.String())
	require.False(t, ok)
}

func TestGrainOwnerMismatchError_ErrorUnknownOwner(t *testing.T) {
	err := (&grainOwnerMismatchError{}).Error()
	require.Equal(t, "grain owner is unknown", err)
}

func TestGrainOwnerMismatchError_ErrorWithOwner(t *testing.T) {
	owner := &internalpb.Grain{Host: "192.0.2.90", Port: 9090}
	err := (&grainOwnerMismatchError{owner: owner}).Error()
	require.Equal(t, "grain is owned by 192.0.2.90:9090", err)
}

func TestTryClaimGrain_AlreadyExistsButMissingOwner(t *testing.T) {
	ctx := t.Context()
	grain := NewMockGrain()
	sys, cl, _, identity := newActivationTestSystem(t, grain, "missing-owner", false)

	wire := &internalpb.Grain{GrainId: &internalpb.GrainId{Value: identity.String()}}

	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
	cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(nil, cluster.ErrGrainNotFound).Once()

	claimed, owner, err := sys.tryClaimGrain(ctx, wire)
	require.NoError(t, err)
	require.False(t, claimed)
	require.Nil(t, owner)
}

func TestTryClaimGrain_AlreadyExistsOwnerLookupError(t *testing.T) {
	ctx := t.Context()
	grain := NewMockGrain()
	sys, cl, _, identity := newActivationTestSystem(t, grain, "owner-lookup-error", false)

	wire := &internalpb.Grain{GrainId: &internalpb.GrainId{Value: identity.String()}}
	expectedErr := errors.New("owner lookup failed")

	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
	cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(nil, expectedErr).Once()

	claimed, owner, err := sys.tryClaimGrain(ctx, wire)
	require.ErrorIs(t, err, expectedErr)
	require.False(t, claimed)
	require.Nil(t, owner)
}

func TestRemoting_RemoteActivateGrain_WithActorSystem(t *testing.T) {
	ctx := context.TODO()
	logger := log.DiscardLogger
	ports := dynaport.Get(1)
	remotingPort := ports[0]
	host := "0.0.0.0"

	sys, err := NewActorSystem(
		"remote-grain-activate",
		WithLogger(logger),
		WithRemote(remote.NewConfig(host, remotingPort)),
	)
	require.NoError(t, err)

	err = sys.Start(ctx)
	assert.NoError(t, err)

	pause.For(time.Second)

	err = sys.RegisterGrainKind(ctx, &MockGrain{})
	require.NoError(t, err)

	remoting := remoteclient.NewClient()

	identity := newGrainIdentity(NewMockGrain(), "grain-activate")
	err = remoting.RemoteActivateGrain(ctx, sys.Host(), sys.Port(), &remote.GrainRequest{
		Name: identity.Name(),
		Kind: identity.Kind(),
	})
	require.NoError(t, err)

	grains := sys.Grains(ctx, time.Second)
	found := false
	for _, grain := range grains {
		if grain.String() == identity.String() {
			found = true
			break
		}
	}
	assert.True(t, found)

	pause.For(time.Second)

	remoting.Close()
	err = sys.Stop(ctx)
	assert.NoError(t, err)
}

func TestRemoting_RemoteTellGrain_WithActorSystem(t *testing.T) {
	ctx := context.TODO()
	logger := log.DiscardLogger
	ports := dynaport.Get(1)
	remotingPort := ports[0]
	host := "0.0.0.0"

	sys, err := NewActorSystem(
		"remote-grain-tell",
		WithLogger(logger),
		WithRemote(remote.NewConfig(host, remotingPort)),
	)
	require.NoError(t, err)

	err = sys.Start(ctx)
	assert.NoError(t, err)

	pause.For(time.Second)

	err = sys.RegisterGrainKind(ctx, &MockGrain{})
	require.NoError(t, err)

	remoting := remoteclient.NewClient()

	identity := newGrainIdentity(NewMockGrain(), "grain-tell")
	for range 10 {
		err = remoting.RemoteTellGrain(ctx, sys.Host(), sys.Port(), &remote.GrainRequest{
			Name: identity.Name(),
			Kind: identity.Kind(),
		}, &testpb.TestSend{})
		require.NoError(t, err)
	}

	grains := sys.Grains(ctx, time.Second)
	found := false
	for _, grain := range grains {
		if grain.String() == identity.String() {
			found = true
			break
		}
	}
	assert.True(t, found)

	pause.For(time.Second)

	remoting.Close()
	err = sys.Stop(ctx)
	assert.NoError(t, err)
}

func TestRemoting_RemoteAskGrain_WithActorSystem(t *testing.T) {
	ctx := context.TODO()
	logger := log.DiscardLogger
	ports := dynaport.Get(1)
	remotingPort := ports[0]
	host := "0.0.0.0"

	sys, err := NewActorSystem(
		"remote-grain-ask",
		WithLogger(logger),
		WithRemote(remote.NewConfig(host, remotingPort)),
	)
	require.NoError(t, err)

	err = sys.Start(ctx)
	assert.NoError(t, err)

	pause.For(time.Second)

	err = sys.RegisterGrainKind(ctx, &MockGrain{})
	require.NoError(t, err)

	remoting := remoteclient.NewClient()

	identity := newGrainIdentity(NewMockGrain(), "grain-ask")
	resp, err := remoting.RemoteAskGrain(ctx, sys.Host(), sys.Port(), &remote.GrainRequest{
		Name: identity.Name(),
		Kind: identity.Kind(),
	}, &testpb.TestReply{}, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, resp)

	actual, ok := resp.(*testpb.Reply)
	require.True(t, ok)
	assert.Equal(t, "received message", actual.GetContent())

	grains := sys.Grains(ctx, time.Second)
	found := false
	for _, grain := range grains {
		if grain.String() == identity.String() {
			found = true
			break
		}
	}
	assert.True(t, found)

	pause.For(time.Second)

	remoting.Close()
	err = sys.Stop(ctx)
	assert.NoError(t, err)
}

func TestSendToGrainOwner_ErrorsWhenOwnerMissing(t *testing.T) {
	ctx := t.Context()
	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewClient(t)
	node := &discovery.Node{Host: "127.0.0.1", PeersPort: 9012, RemotingPort: 9112}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)

	resp, err := sys.sendToGrainOwner(ctx, nil, &testpb.TestReply{}, time.Second, true)
	require.Error(t, err)
	require.ErrorContains(t, err, "grain owner is unknown")
	require.Nil(t, resp)
}

func TestTellGrain(t *testing.T) {
	t.Run("local mode happy path", func(t *testing.T) {
		ctx := context.Background()
		sys, err := NewActorSystem("tell-grain-local", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, sys.Start(ctx))
		t.Cleanup(func() { _ = sys.Stop(ctx) })

		require.NoError(t, sys.RegisterGrainKind(ctx, &MockGrain{}))
		identity := newGrainIdentity(NewMockGrain(), "tell-local-grain")

		err = sys.TellGrain(ctx, identity, &testpb.TestSend{})
		require.NoError(t, err)
		pause.For(200 * time.Millisecond)
	})

	t.Run("returns ErrActorSystemNotStarted when not started", func(t *testing.T) {
		ctx := context.Background()
		sys, err := NewActorSystem("tell-grain-not-started", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		identity := newGrainIdentity(NewMockGrain(), "grain")

		err = sys.TellGrain(ctx, identity, &testpb.TestSend{})
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
	})

	t.Run("returns ErrInvalidGrainIdentity when identity invalid", func(t *testing.T) {
		ctx := context.Background()
		sys, err := NewActorSystem("tell-grain-invalid", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, sys.Start(ctx))
		t.Cleanup(func() { _ = sys.Stop(ctx) })

		invalidID := &GrainIdentity{kind: "", name: ""}
		err = sys.TellGrain(ctx, invalidID, &testpb.TestSend{})
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrInvalidGrainIdentity)
	})

	t.Run("cluster mode with GetGrain success", func(t *testing.T) {
		ctx := context.Background()
		cl := mockcluster.NewCluster(t)
		rem := mockremote.NewClient(t)
		node := &discovery.Node{Host: "127.0.0.1", PeersPort: 9015, RemotingPort: 9115}
		sys := MockSimpleClusterReadyActorSystem(rem, cl, node)
		sys.registry.Register(NewMockGrain())

		identity := newGrainIdentity(NewMockGrain(), "tell-cluster-grain")
		owner := &internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: identity.String(), Kind: identity.Kind()},
			Host:    "192.0.2.1",
			Port:    16000,
		}
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil).Once()
		rem.EXPECT().RemoteTellGrain(mock.Anything, owner.GetHost(), int(owner.GetPort()), mock.Anything, mock.Anything).
			Return(errors.New("connection refused")).Once()

		err := sys.TellGrain(ctx, identity, &testpb.TestSend{})
		require.Error(t, err)
	})
}

func TestAskGrain(t *testing.T) {
	t.Run("returns ErrActorSystemNotStarted when not started", func(t *testing.T) {
		ctx := context.Background()
		sys, err := NewActorSystem("ask-grain-not-started", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		identity := newGrainIdentity(NewMockGrain(), "grain")

		resp, err := sys.AskGrain(ctx, identity, &testpb.TestReply{}, time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
		assert.Nil(t, resp)
	})

	t.Run("returns ErrInvalidGrainIdentity when identity invalid", func(t *testing.T) {
		ctx := context.Background()
		sys, err := NewActorSystem("ask-grain-invalid", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, sys.Start(ctx))
		t.Cleanup(func() { _ = sys.Stop(ctx) })

		invalidID := &GrainIdentity{kind: "", name: ""}
		resp, err := sys.AskGrain(ctx, invalidID, &testpb.TestReply{}, time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrInvalidGrainIdentity)
		assert.Nil(t, resp)
	})

	t.Run("local mode happy path", func(t *testing.T) {
		ctx := context.Background()
		sys, err := NewActorSystem("ask-grain-local", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, sys.Start(ctx))
		t.Cleanup(func() { _ = sys.Stop(ctx) })

		require.NoError(t, sys.RegisterGrainKind(ctx, &MockGrain{}))
		identity := newGrainIdentity(NewMockGrain(), "ask-local-grain")

		resp, err := sys.AskGrain(ctx, identity, &testpb.TestReply{}, time.Second)
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("cluster mode with GetGrain success", func(t *testing.T) {
		ctx := context.Background()
		cl := mockcluster.NewCluster(t)
		rem := mockremote.NewClient(t)
		node := &discovery.Node{Host: "127.0.0.1", PeersPort: 9016, RemotingPort: 9116}
		sys := MockSimpleClusterReadyActorSystem(rem, cl, node)
		sys.registry.Register(NewMockGrain())

		identity := newGrainIdentity(NewMockGrain(), "ask-cluster-grain")
		owner := &internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: identity.String(), Kind: identity.Kind()},
			Host:    "192.0.2.1",
			Port:    16000,
		}
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil).Once()
		rem.EXPECT().RemoteAskGrain(mock.Anything, owner.GetHost(), int(owner.GetPort()), mock.Anything, mock.Anything, time.Second).
			Return(nil, errors.New("connection refused")).Once()

		resp, err := sys.AskGrain(ctx, identity, &testpb.TestReply{}, time.Second)
		require.Error(t, err)
		require.Nil(t, resp)
	})
}

func TestSelectActivationPeer_LeastLoadActivation(t *testing.T) {
	ctx := t.Context()
	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewClient(t)
	node := &discovery.Node{Host: "127.0.0.1", PeersPort: 14000, RemotingPort: 8080}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)

	peer1 := &cluster.Peer{Host: "192.0.2.1", PeersPort: 15000, RemotingPort: 16000}
	peer2 := &cluster.Peer{Host: "192.0.2.2", PeersPort: 15001, RemotingPort: 16001}

	cl.EXPECT().Members(ctx).Return([]*cluster.Peer{peer1, peer2}, nil)
	netClient := internalnet.NewClient("127.0.0.1:1", internalnet.WithDialTimeout(50*time.Millisecond))
	rem.EXPECT().NetClient("192.0.2.1", 16000).Return(netClient).Once()
	rem.EXPECT().NetClient("192.0.2.2", 16001).Return(netClient).Once()

	config := newGrainConfig(WithActivationStrategy(LeastLoadActivation))
	peer, err := sys.findActivationPeer(ctx, config)
	require.Error(t, err)
	require.Nil(t, peer)
	require.ErrorContains(t, err, "failed to fetch node metric")
}

func TestSelectActivationPeer_LeastLoadActivation_Success(t *testing.T) {
	ctx := t.Context()
	handler := func(_ context.Context, _ internalnet.Connection, req proto.Message) (proto.Message, error) {
		return &internalpb.GetNodeMetricResponse{NodeAddress: "127.0.0.1:16000", Load: 5}, nil
	}
	ps, err := internalnet.NewProtoServer("127.0.0.1:0", internalnet.WithProtoHandler("internalpb.GetNodeMetricRequest", handler))
	require.NoError(t, err)
	require.NoError(t, ps.Listen())
	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	pause.For(100 * time.Millisecond)
	t.Cleanup(func() { _ = ps.Shutdown(time.Second); <-done })

	addr := ps.ListenAddr().String()
	host, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewClient(t)
	node := &discovery.Node{Host: "127.0.0.1", PeersPort: 14003, RemotingPort: 8083}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)

	peer1 := &cluster.Peer{Host: host, PeersPort: port, RemotingPort: port}
	netClient := internalnet.NewClient(addr)
	rem.EXPECT().NetClient(peer1.Host, peer1.RemotingPort).Return(netClient).Once()

	peer, err := sys.leastLoadedPeer(ctx, []*cluster.Peer{peer1})
	require.NoError(t, err)
	require.NotNil(t, peer)
	require.Equal(t, host, peer.Host)
	require.Equal(t, port, peer.RemotingPort)
}

func TestSelectActivationPeer_RandomActivation(t *testing.T) {
	ctx := t.Context()
	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewClient(t)
	node := &discovery.Node{Host: "127.0.0.1", PeersPort: 14000, RemotingPort: 8080}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)

	peer1 := &cluster.Peer{Host: "192.0.2.1", PeersPort: 15000, RemotingPort: 16000}
	peer2 := &cluster.Peer{Host: "192.0.2.2", PeersPort: 15001, RemotingPort: 16001}

	cl.EXPECT().Members(ctx).Return([]*cluster.Peer{peer1, peer2}, nil)

	config := newGrainConfig(WithActivationStrategy(RandomActivation))
	peer, err := sys.findActivationPeer(ctx, config)
	require.NoError(t, err)
	require.NotNil(t, peer)
	require.Contains(t, []string{"192.0.2.1", "192.0.2.2"}, peer.Host)
}

func TestSelectActivationPeer_DefaultStrategy(t *testing.T) {
	ctx := t.Context()
	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewClient(t)
	node := &discovery.Node{Host: "127.0.0.1", PeersPort: 14000, RemotingPort: 8080}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)

	peer1 := &cluster.Peer{Host: "192.0.2.1", PeersPort: 15000, RemotingPort: 16000}
	// selectActivationPeer is called directly with peers - no Members call
	config := newGrainConfig()
	peer, err := sys.selectActivationPeer(ctx, []*cluster.Peer{peer1}, config.activationStrategy)
	require.NoError(t, err)
	require.Nil(t, peer)
}

func TestSendToGrainOwner_TellMode(t *testing.T) {
	ctx := t.Context()
	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewClient(t)
	node := &discovery.Node{Host: "127.0.0.1", PeersPort: 9013, RemotingPort: 9113}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)

	owner := &internalpb.Grain{
		GrainId: &internalpb.GrainId{Value: "grain|test", Kind: "TestGrain"},
		Host:    "192.0.2.1",
		Port:    16000,
	}

	rem.EXPECT().RemoteTellGrain(mock.Anything, owner.GetHost(), int(owner.GetPort()), mock.Anything, mock.Anything).
		Return(errors.New("connection refused")).Once()

	resp, err := sys.sendToGrainOwner(ctx, owner, &testpb.TestSend{}, time.Second, false)
	require.Error(t, err)
	require.Nil(t, resp)
}

func TestRemoteTellGrain_FallbackPaths(t *testing.T) {
	ctx := context.Background()

	t.Run("GetGrain returns non-ErrGrainNotFound error", func(t *testing.T) {
		cl := mockcluster.NewCluster(t)
		rem := mockremote.NewClient(t)
		node := &discovery.Node{Host: "127.0.0.1", PeersPort: 9017, RemotingPort: 9117}
		sys := MockSimpleClusterReadyActorSystem(rem, cl, node)
		sys.registry.Register(NewMockGrain())

		identity := newGrainIdentity(NewMockGrain(), "tell-err-grain")
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(nil, errors.New("cluster error")).Once()

		err := sys.remoteTellGrain(ctx, identity, &testpb.TestSend{}, time.Second)
		require.Error(t, err)
		require.ErrorContains(t, err, "cluster error")
	})

	t.Run("GetGrain ErrGrainNotFound then tellGrainAcrossDataCenters succeeds", func(t *testing.T) {
		rem := mockremote.NewClient(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{{
				ID: "dc-1", State: datacenter.DataCenterActive,
				Endpoints: []string{"127.0.0.1:9000"},
			}}, nil
		}, rem)
		sys.clusterEnabled.Store(true)
		cl := mockcluster.NewCluster(t)
		cl.EXPECT().GetGrain(mock.Anything, mock.Anything).Return(nil, cluster.ErrGrainNotFound).Once()
		sys.cluster = cl

		identity := newGrainIdentity(NewMockGrain(), "tell-dc-grain")
		rem.EXPECT().RemoteTellGrain(mock.Anything, "127.0.0.1", 9000, mock.Anything, mock.Anything).Return(nil).Once()

		err := sys.remoteTellGrain(ctx, identity, &testpb.TestSend{}, time.Second)
		require.NoError(t, err)
	})
}

func TestRemoteAskGrain_FallbackPaths(t *testing.T) {
	ctx := context.Background()

	t.Run("GetGrain returns non-ErrGrainNotFound error", func(t *testing.T) {
		cl := mockcluster.NewCluster(t)
		rem := mockremote.NewClient(t)
		node := &discovery.Node{Host: "127.0.0.1", PeersPort: 9018, RemotingPort: 9118}
		sys := MockSimpleClusterReadyActorSystem(rem, cl, node)
		sys.registry.Register(NewMockGrain())

		identity := newGrainIdentity(NewMockGrain(), "ask-err-grain")
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(nil, errors.New("cluster error")).Once()

		resp, err := sys.remoteAskGrain(ctx, identity, &testpb.TestReply{}, time.Second)
		require.Error(t, err)
		require.Nil(t, resp)
		require.ErrorContains(t, err, "cluster error")
	})

	t.Run("GetGrain ErrGrainNotFound then askGrainAcrossDataCenters succeeds", func(t *testing.T) {
		rem := mockremote.NewClient(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{{
				ID: "dc-1", State: datacenter.DataCenterActive,
				Endpoints: []string{"127.0.0.1:9000"},
			}}, nil
		}, rem)
		sys.clusterEnabled.Store(true)
		cl := mockcluster.NewCluster(t)
		cl.EXPECT().GetGrain(mock.Anything, mock.Anything).Return(nil, cluster.ErrGrainNotFound).Once()
		sys.cluster = cl

		identity := newGrainIdentity(NewMockGrain(), "ask-dc-grain")
		rem.EXPECT().RemoteAskGrain(mock.Anything, "127.0.0.1", 9000, mock.Anything, mock.Anything, time.Second).
			Return(&testpb.TestReply{}, nil).Once()

		resp, err := sys.remoteAskGrain(ctx, identity, &testpb.TestReply{}, time.Second)
		require.NoError(t, err)
		require.NotNil(t, resp)
	})
}

func TestSendToGrainOwner_AskMode(t *testing.T) {
	ctx := t.Context()
	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewClient(t)
	node := &discovery.Node{Host: "127.0.0.1", PeersPort: 9014, RemotingPort: 9114}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)

	owner := &internalpb.Grain{
		GrainId: &internalpb.GrainId{Value: "grain|test", Kind: "TestGrain"},
		Host:    "192.0.2.1",
		Port:    16000,
	}

	rem.EXPECT().RemoteAskGrain(mock.Anything, owner.GetHost(), int(owner.GetPort()), mock.Anything, mock.Anything, time.Second).
		Return(nil, errors.New("connection refused")).Once()

	resp, err := sys.sendToGrainOwner(ctx, owner, &testpb.TestReply{}, time.Second, true)
	require.Error(t, err)
	require.Nil(t, resp)
}

func TestSetupGrainActivationBarrier(t *testing.T) {
	ctx := t.Context()
	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewClient(t)
	node := &discovery.Node{Host: "127.0.0.1", PeersPort: 14001, RemotingPort: 8081}

	t.Run("no-op when cluster disabled", func(t *testing.T) {
		sys := MockSimpleClusterReadyActorSystem(rem, cl, node)
		sys.clusterEnabled.Store(false)
		sys.setupGrainActivationBarrier(ctx)
		require.Nil(t, sys.grainBarrier)
	})

	t.Run("no-op when clusterConfig nil", func(t *testing.T) {
		sys := MockSimpleClusterReadyActorSystem(rem, cl, node)
		sys.clusterConfig = nil
		sys.setupGrainActivationBarrier(ctx)
		require.Nil(t, sys.grainBarrier)
	})

	t.Run("no-op when barrier disabled", func(t *testing.T) {
		sys := MockSimpleClusterReadyActorSystem(rem, cl, node)
		sys.clusterConfig = NewClusterConfig()
		sys.setupGrainActivationBarrier(ctx)
		require.Nil(t, sys.grainBarrier)
	})

	t.Run("opens immediately when minPeers <= 1", func(t *testing.T) {
		sys := MockSimpleClusterReadyActorSystem(rem, cl, node)
		sys.clusterConfig = NewClusterConfig().
			WithMinimumPeersQuorum(1).
			WithGrainActivationBarrier(5 * time.Second)
		sys.setupGrainActivationBarrier(ctx)
		require.NotNil(t, sys.grainBarrier)
		err := sys.waitForGrainActivationBarrier(ctx)
		require.NoError(t, err)
	})

	t.Run("calls tryOpenGrainActivationBarrier when minPeers > 1", func(t *testing.T) {
		sys := MockSimpleClusterReadyActorSystem(rem, cl, node)
		sys.clusterConfig = NewClusterConfig().
			WithMinimumPeersQuorum(2).
			WithGrainActivationBarrier(5 * time.Second)
		peer1 := &cluster.Peer{Host: "192.0.2.1", PeersPort: 15000, RemotingPort: 16000}
		peer2 := &cluster.Peer{Host: "192.0.2.2", PeersPort: 15001, RemotingPort: 16001}
		cl.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{peer1, peer2}, nil).Once()
		sys.setupGrainActivationBarrier(ctx)
		require.NotNil(t, sys.grainBarrier)
		err := sys.waitForGrainActivationBarrier(ctx)
		require.NoError(t, err)
	})
}

func TestTryOpenGrainActivationBarrier(t *testing.T) {
	ctx := t.Context()
	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewClient(t)
	node := &discovery.Node{Host: "127.0.0.1", PeersPort: 14002, RemotingPort: 8082}

	t.Run("no-op when barrier nil", func(t *testing.T) {
		sys := MockSimpleClusterReadyActorSystem(rem, cl, node)
		sys.grainBarrier = nil
		sys.tryOpenGrainActivationBarrier(ctx)
	})

	t.Run("no-op when cluster nil", func(t *testing.T) {
		sys := MockSimpleClusterReadyActorSystem(rem, cl, node)
		sys.clusterConfig = NewClusterConfig().WithMinimumPeersQuorum(2).WithGrainActivationBarrier(5 * time.Second)
		sys.grainBarrier = newGrainActivationBarrier(2, 5*time.Second)
		sys.cluster = nil
		sys.tryOpenGrainActivationBarrier(ctx)
	})

	t.Run("opens when Members returns enough peers", func(t *testing.T) {
		sys := MockSimpleClusterReadyActorSystem(rem, cl, node)
		sys.clusterConfig = NewClusterConfig().WithMinimumPeersQuorum(2).WithGrainActivationBarrier(5 * time.Second)
		sys.grainBarrier = newGrainActivationBarrier(2, 5*time.Second)
		peer1 := &cluster.Peer{Host: "192.0.2.1", PeersPort: 15000, RemotingPort: 16000}
		peer2 := &cluster.Peer{Host: "192.0.2.2", PeersPort: 15001, RemotingPort: 16001}
		cl.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{peer1, peer2}, nil).Once()
		sys.tryOpenGrainActivationBarrier(ctx)
		err := sys.waitForGrainActivationBarrier(ctx)
		require.NoError(t, err)
	})

	t.Run("does not open when Members returns too few peers", func(t *testing.T) {
		sys := MockSimpleClusterReadyActorSystem(rem, cl, node)
		sys.clusterConfig = NewClusterConfig().WithMinimumPeersQuorum(2).WithGrainActivationBarrier(5 * time.Second)
		sys.grainBarrier = newGrainActivationBarrier(2, 5*time.Second)
		cl.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{{Host: "192.0.2.1", PeersPort: 15000, RemotingPort: 16000}}, nil).Once()
		sys.tryOpenGrainActivationBarrier(ctx)
		waitCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()
		err := sys.waitForGrainActivationBarrier(waitCtx)
		require.Error(t, err)
	})

	t.Run("no-op when Members returns error", func(t *testing.T) {
		sys := MockSimpleClusterReadyActorSystem(rem, cl, node)
		sys.clusterConfig = NewClusterConfig().WithMinimumPeersQuorum(2).WithGrainActivationBarrier(5 * time.Second)
		sys.grainBarrier = newGrainActivationBarrier(2, 5*time.Second)
		cl.EXPECT().Members(mock.Anything).Return(nil, errors.New("members error")).Once()
		sys.tryOpenGrainActivationBarrier(ctx)
		waitCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()
		err := sys.waitForGrainActivationBarrier(waitCtx)
		require.Error(t, err)
	})

	t.Run("no-op when barrier already open", func(t *testing.T) {
		sys := MockSimpleClusterReadyActorSystem(rem, cl, node)
		sys.grainBarrier = newGrainActivationBarrier(2, 5*time.Second)
		sys.grainBarrier.open()
		sys.tryOpenGrainActivationBarrier(ctx)
		err := sys.waitForGrainActivationBarrier(ctx)
		require.NoError(t, err)
	})
}

func TestRunGrainActivation_EmptyID(t *testing.T) {
	sys, _, _, identity := newActivationTestSystem(t, NewMockGrain(), "empty-id-run", true)

	pid, err := sys.runGrainActivation("", func() (*grainPID, error) {
		return newGrainPID(identity, NewMockGrain(), sys, newGrainConfig()), nil
	})
	require.NoError(t, err)
	require.NotNil(t, pid)
}

func TestGrainRegistrationAndDeregistration(t *testing.T) {
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
		err = sys.RegisterGrainKind(ctx, &MockGrain{})
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
		err = sys.RegisterGrainKind(ctx, &MockGrain{})
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
		err = sys.RegisterGrainKind(ctx, &MockGrain{})
		require.NoError(t, err)

		err = sys.DeregisterGrainKind(ctx, &MockGrain{})
		require.NoError(t, err)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
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

		err = sys.DeregisterGrainKind(ctx, &MockGrain{})
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)

		err = sys.Stop(ctx)
		assert.Error(t, err)
	})
}

func TestGrainContextPropagation(t *testing.T) {
	ctxKey := grainTestCtxKey{}
	headerKey := "x-goakt-grain-trace"

	t.Run("AskGrain propagates context across nodes", func(t *testing.T) {
		ctx := context.Background()
		srv := startNatsServer(t)

		propagator := &headerPropagator{headerKey: headerKey, ctxKey: ctxKey}
		grain := &contextEchoGrain{key: ctxKey}

		node1, sd1 := testNATs(t, srv.Addr().String(),
			withTestContextPropagator(propagator),
			withTestExtraGrains(grain))
		node2, sd2 := testNATs(t, srv.Addr().String(),
			withTestContextPropagator(propagator),
			withTestExtraGrains(&contextEchoGrain{key: ctxKey}))

		defer func() {
			assert.NoError(t, node2.Stop(ctx))
			assert.NoError(t, node1.Stop(ctx))
			sd2.Close()
			sd1.Close()
			srv.Shutdown()
		}()

		pause.For(time.Second)

		// Activate grain on node1
		identity, err := node1.GrainIdentity(ctx, "ask-ctx-grain", func(_ context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)

		// First call from node1 to activate the grain
		resp, err := node1.AskGrain(ctx, identity, new(testpb.TestReply), time.Second)
		require.NoError(t, err)
		require.NotNil(t, resp)
		pause.For(time.Second)

		// Send from node2 with propagated context value — routes through TCP remoting
		headerVal := "cross-node-ask-value"
		propagatedCtx := context.WithValue(ctx, ctxKey, headerVal)
		resp, err = node2.AskGrain(propagatedCtx, identity, new(testpb.TestReply), time.Second)
		require.NoError(t, err)
		require.NotNil(t, resp)

		reply, ok := resp.(*testpb.Reply)
		require.True(t, ok)
		require.Equal(t, headerVal, reply.GetContent())
		require.Equal(t, headerVal, grain.Seen())
	})

	t.Run("TellGrain propagates context across nodes", func(t *testing.T) {
		ctx := context.Background()
		srv := startNatsServer(t)

		propagator := &headerPropagator{headerKey: headerKey, ctxKey: ctxKey}
		grain := &contextEchoGrain{key: ctxKey}

		node1, sd1 := testNATs(t, srv.Addr().String(),
			withTestContextPropagator(propagator),
			withTestExtraGrains(grain))
		node2, sd2 := testNATs(t, srv.Addr().String(),
			withTestContextPropagator(propagator),
			withTestExtraGrains(&contextEchoGrain{key: ctxKey}))

		defer func() {
			assert.NoError(t, node2.Stop(ctx))
			assert.NoError(t, node1.Stop(ctx))
			sd2.Close()
			sd1.Close()
			srv.Shutdown()
		}()

		pause.For(time.Second)

		// Activate grain on node1
		identity, err := node1.GrainIdentity(ctx, "tell-ctx-grain", func(_ context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)

		// First call from node1 to activate the grain
		_, err = node1.AskGrain(ctx, identity, new(testpb.TestReply), time.Second)
		require.NoError(t, err)
		pause.For(time.Second)

		// Send from node2 with propagated context value — routes through TCP remoting
		headerVal := "cross-node-tell-value"
		propagatedCtx := context.WithValue(ctx, ctxKey, headerVal)
		err = node2.TellGrain(propagatedCtx, identity, new(testpb.TestSend))
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			return grain.Seen() == headerVal
		}, 2*time.Second, 50*time.Millisecond)
	})
}
