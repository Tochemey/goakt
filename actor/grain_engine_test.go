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
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v4/datacenter"
	"github.com/tochemey/goakt/v4/discovery"
	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	internalnet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/refusal"
	"github.com/tochemey/goakt/v4/internal/remoteclient"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	mockcluster "github.com/tochemey/goakt/v4/mocks/cluster"
	mockdiscovery "github.com/tochemey/goakt/v4/mocks/discovery"
	mockremote "github.com/tochemey/goakt/v4/mocks/remoteclient"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

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
	sys := newClusterReadySystem(rem, cl, node)

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
	actorSystem := newClusterReadySystem(rem, cl, node, remote.WithCompression(remote.BrotliCompression))

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
		return req != nil && req.Name == identity.Name() && req.Kind == identity.Kind()
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
	actorSystem := newClusterReadySystem(rem, cl, node, remote.WithCompression(remote.ZstdCompression))

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
		return req != nil && req.Name == identity.Name() && req.Kind == identity.Kind()
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
	actorSystem := newClusterReadySystem(rem, cl, node, remote.WithCompression(remote.GzipCompression))

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
		return req != nil && req.Name == identity.Name() && req.Kind == identity.Kind()
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
	sys := newClusterReadySystem(rem, cl, node)

	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
	cl.EXPECT().GetGrain(ctx, identity.String()).Return(nil, cluster.ErrGrainNotFound).Once()
	cl.EXPECT().Members(ctx).Return([]*cluster.Peer{remotePeer, alternatePeer}, nil)
	cl.EXPECT().NextRoundRobinValue(ctx, cluster.GrainsRoundRobinKey).Return(1, nil)
	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Once()
	cl.EXPECT().PutGrain(mock.Anything, mock.MatchedBy(func(actual *internalpb.Grain) bool {
		return actual != nil && actual.GetGrainId().GetValue() == identity.String()
	})).Return(nil).Once()
	rem.EXPECT().RemoteActivateGrain(ctx, remotePeer.Host, remotePeer.RemotingPort, mock.Anything).Return(clientErr)
	// the peer answered with an error, so the claim made for it is rolled
	// back, conditionally on the record still naming the peer
	cl.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(remotePeer.Host, remotePeer.RemotingPort)).Return(nil, nil).Once()

	got, err := sys.GrainIdentity(ctx, name, func(context.Context) (Grain, error) {
		return grain, nil
	}, WithActivationStrategy(RoundRobinActivation))

	require.Error(t, err)
	require.ErrorIs(t, err, clientErr)
	require.Nil(t, got)
}

func TestGrainIdentity_EmptyOwnerRecordIsInherited(t *testing.T) {
	ctx := t.Context()
	grain := NewMockGrain()
	name := "empty-owner-grain"
	identity := newGrainIdentity(grain, name)
	sys, cl, _, _ := newActivationTestSystem(t, grain, name, true)

	// an empty record is inherited without a claim and overwritten by the
	// publication of the local activation, as before
	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
	cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(&internalpb.Grain{}, nil).Once()
	cl.EXPECT().PutGrain(mock.Anything, mock.MatchedBy(func(actual *internalpb.Grain) bool {
		return actual != nil && actual.GetGrainId().GetValue() == identity.String() && actual.GetHost() == sys.Host()
	})).Return(nil).Once()

	got, err := sys.GrainIdentity(ctx, name, func(context.Context) (Grain, error) {
		return grain, nil
	})
	require.NoError(t, err)
	require.NotNil(t, got)

	process, ok := sys.grains.Get(identity.String())
	require.True(t, ok)
	require.True(t, process.isActive())
}

func TestIsTransportFailure(t *testing.T) {
	live := t.Context()
	gaveUp, cancel := context.WithCancel(live)
	cancel()

	cases := []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{"dial refused", live, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, true},
		{"dial timeout", live, &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}, true},
		{"remote send failure", live, gerrors.NewErrRemoteSendFailure(errors.New("connection reset")), true},
		{"peer closed", live, io.EOF, true},
		{"peer closed mid frame", live, io.ErrUnexpectedEOF, true},
		{"duplex closed", live, internalnet.ErrDuplexClosed, true},
		{"caller context expired", gaveUp, &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}, false},
		{"bare context deadline", live, context.DeadlineExceeded, false},
		{"bare context canceled", live, context.Canceled, false},
		{"remote activation failure", live, gerrors.NewErrGrainActivationFailure(errors.New("OnActivate failed")), false},
		{"opaque wire error", live, errors.New("remote activate failed"), false},
		{"owner shutting down", live, gerrors.ErrSystemShuttingDown, false},
		{"owner remoting disabled", live, gerrors.ErrRemotingDisabled, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isTransportFailure(tc.ctx, tc.err))
		})
	}
}

func TestGrainIdentity_DepartedOwnerReleasedAndClaimedLocally(t *testing.T) {
	ctx := t.Context()
	grain := NewMockGrain()
	name := "departed-owner-grain"
	identity := newGrainIdentity(grain, name)
	localPeer := &cluster.Peer{Host: "127.0.0.1", PeersPort: 16600, RemotingPort: 8282}
	survivor := &cluster.Peer{Host: "192.0.2.22", PeersPort: 17002, RemotingPort: 18002}

	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewClient(t)
	node := &discovery.Node{Host: localPeer.Host, PeersPort: localPeer.PeersPort, RemotingPort: localPeer.RemotingPort}
	sys := newClusterReadySystem(rem, cl, node)

	// the registry still names a node that is no longer a cluster member
	departedOwner := internalpb.Grain_builder{
		GrainId: internalpb.GrainId_builder{Value: identity.String()}.Build(),
		Host:    "192.0.2.23",
		Port:    18003,
	}.Build()

	// owner resolution
	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
	cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(departedOwner, nil).Once()
	rem.EXPECT().RemoteActivateGrain(ctx, departedOwner.GetHost(), int(departedOwner.GetPort()), mock.Anything).Return(gerrors.NewErrRemoteSendFailure(errors.New("connection refused"))).Once()
	// membership confirms the departure and the entry is released while it
	// still names the departed node
	cl.EXPECT().Members(ctx).Return([]*cluster.Peer{localPeer, survivor}, nil).Once()
	cl.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(departedOwner.GetHost(), int(departedOwner.GetPort()))).Return(nil, nil).Once()
	// local activation must claim atomically instead of inheriting the dead
	// record: one put for the claim, one for the post-activation publication
	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Once()
	cl.EXPECT().PutGrain(mock.Anything, mock.MatchedBy(func(actual *internalpb.Grain) bool {
		return actual != nil && actual.GetGrainId().GetValue() == identity.String() &&
			actual.GetHost() == localPeer.Host && int(actual.GetPort()) == localPeer.RemotingPort
	})).Return(nil).Twice()

	got, err := sys.GrainIdentity(ctx, name, func(context.Context) (Grain, error) {
		return grain, nil
	})
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, identity.String(), got.String())

	process, ok := sys.grains.Get(identity.String())
	require.True(t, ok)
	require.True(t, process.isActive())
}

func TestGrainIdentity_ShuttingDownOwnerReleasedAndClaimedLocally(t *testing.T) {
	cases := []struct {
		name   string
		answer error
	}{
		{"owner is shutting down", gerrors.ErrSystemShuttingDown},
		{"owner has shut down", gerrors.ErrRemotingDisabled},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			grain := NewMockGrain()
			name := "shutting-down-owner-grain"
			identity := newGrainIdentity(grain, name)
			localPeer := &cluster.Peer{Host: "127.0.0.1", PeersPort: 16700, RemotingPort: 8383}

			cl := mockcluster.NewCluster(t)
			rem := mockremote.NewClient(t)
			node := &discovery.Node{Host: localPeer.Host, PeersPort: localPeer.PeersPort, RemotingPort: localPeer.RemotingPort}
			sys := newClusterReadySystem(rem, cl, node)

			// the owner is still a cluster member but answers over an open connection
			owner := internalpb.Grain_builder{
				GrainId: internalpb.GrainId_builder{Value: identity.String()}.Build(),
				Host:    "192.0.2.24",
				Port:    18004,
			}.Build()

			cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
			cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil).Once()
			rem.EXPECT().RemoteActivateGrain(ctx, owner.GetHost(), int(owner.GetPort()), mock.Anything).Return(tc.answer).Once()
			cl.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(owner.GetHost(), int(owner.GetPort()))).Return(nil, nil).Once()
			cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Once()
			cl.EXPECT().PutGrain(mock.Anything, mock.MatchedBy(func(actual *internalpb.Grain) bool {
				return actual != nil && actual.GetGrainId().GetValue() == identity.String() &&
					actual.GetHost() == localPeer.Host && int(actual.GetPort()) == localPeer.RemotingPort
			})).Return(nil).Twice()

			got, err := sys.GrainIdentity(ctx, name, func(context.Context) (Grain, error) {
				return grain, nil
			})
			require.NoError(t, err)
			require.Equal(t, identity.String(), got.String())

			process, ok := sys.grains.Get(identity.String())
			require.True(t, ok)
			require.True(t, process.isActive())
			// the owner answered for itself, so membership is not consulted
			cl.AssertNotCalled(t, "Members", mock.Anything)
		})
	}
}

func TestActivateGrainLocally_ShuttingDownReleasesClaim(t *testing.T) {
	ctx := t.Context()
	grain := NewMockGrain()
	sys, cl, _, identity := newActivationTestSystem(t, grain, "activate-locally-shutting-down", true)
	sys.shuttingDown.Store(true)

	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Once()
	cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(nil).Once()
	cl.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(sys.Host(), sys.Port())).Return(nil, nil).Once()

	err := sys.activateGrainLocally(ctx, identity, staticGrainProvider(grain), newGrainConfig(), nil)
	require.ErrorIs(t, err, gerrors.ErrSystemShuttingDown)

	process, ok := sys.grains.Get(identity.String())
	require.False(t, ok && process.isActive())
}

func TestAdmitGrainActivation(t *testing.T) {
	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewClient(t)
	node := &discovery.Node{Host: "127.0.0.1", PeersPort: 14001, RemotingPort: 15001}

	t.Run("admits when the node is running", func(t *testing.T) {
		sys := newClusterReadySystem(rem, cl, node)
		require.NoError(t, sys.admitGrainActivation(t.Context()))
	})

	t.Run("refuses when the node is shutting down", func(t *testing.T) {
		sys := newClusterReadySystem(rem, cl, node)
		sys.shuttingDown.Store(true)
		err := sys.admitGrainActivation(t.Context())
		require.ErrorIs(t, err, gerrors.ErrSystemShuttingDown)
		require.True(t, refusal.Marked(err), "a refused activation is marked as the node's refusal")
	})
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
	sys := newClusterReadySystem(rem, cl, node)

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
		owner := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{Value: identity.String()}.Build(),
			Host:    "192.0.2.50",
			Port:    16050,
		}.Build()

		rem.EXPECT().RemoteActivateGrain(ctx, owner.GetHost(), int(owner.GetPort()), mock.Anything).Return(nil)

		handled, err := sys.tryRemoteGrainActivation(ctx, identity, newGrainConfig(), owner)
		require.NoError(t, err)
		require.True(t, handled)
	})

	t.Run("owner answered or caller gave up keeps the owner", func(t *testing.T) {
		// none of these errors puts the owner's liveness in question, so the
		// membership is not even consulted: the owner keeps the grain and the
		// caller gets the error back
		cases := []struct {
			name string
			err  error
		}{
			{"caller deadline", context.DeadlineExceeded},
			{"caller canceled", context.Canceled},
			{"remote activation failure", gerrors.NewErrGrainActivationFailure(errors.New("OnActivate failed"))},
			{"opaque wire error", errors.New("remote activate failed")},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				ctx := t.Context()
				grain := NewMockGrain()
				sys, cl, rem, identity := newActivationTestSystem(t, grain, "owner-remote-error", true)
				owner := internalpb.Grain_builder{
					GrainId: internalpb.GrainId_builder{Value: identity.String()}.Build(),
					Host:    "192.0.2.51",
					Port:    16051,
				}.Build()

				rem.EXPECT().RemoteActivateGrain(ctx, owner.GetHost(), int(owner.GetPort()), mock.Anything).Return(tc.err).Once()

				handled, err := sys.tryRemoteGrainActivation(ctx, identity, newGrainConfig(), owner)
				require.ErrorIs(t, err, tc.err)
				require.False(t, handled)
				cl.AssertNotCalled(t, "Members", mock.Anything)
				cl.AssertNotCalled(t, "ReleaseGrain", mock.Anything, mock.Anything, mock.Anything)
			})
		}
	})

	t.Run("caller context expired keeps the owner", func(t *testing.T) {
		// even a transport-shaped error proves nothing once the caller's own
		// context is done, so the membership is not consulted
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		grain := NewMockGrain()
		sys, cl, rem, identity := newActivationTestSystem(t, grain, "owner-remote-caller-gave-up", true)
		owner := remoteGrainRecord(identity, "192.0.2.51", 16051)
		expectedErr := &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}

		rem.EXPECT().RemoteActivateGrain(ctx, owner.GetHost(), int(owner.GetPort()), mock.Anything).Return(expectedErr).Once()

		handled, err := sys.tryRemoteGrainActivation(ctx, identity, newGrainConfig(), owner)
		require.ErrorIs(t, err, expectedErr)
		require.False(t, handled)
		cl.AssertNotCalled(t, "Members", mock.Anything)
		cl.AssertNotCalled(t, "ReleaseGrain", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("transport failure to a live owner keeps the owner", func(t *testing.T) {
		// the owner never answered, so its liveness is checked against the
		// membership; a node still in the cluster keeps the grain
		cases := []struct {
			name string
			err  error
		}{
			{"remote send failure", gerrors.NewErrRemoteSendFailure(errors.New("connection reset"))},
			{"dial refused", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}},
			{"dial timeout", &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}},
			{"peer closed", io.EOF},
			{"duplex closed", internalnet.ErrDuplexClosed},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				ctx := t.Context()
				grain := NewMockGrain()
				sys, cl, rem, identity := newActivationTestSystem(t, grain, "owner-remote-transport-error", true)
				owner := internalpb.Grain_builder{
					GrainId: internalpb.GrainId_builder{Value: identity.String()}.Build(),
					Host:    "192.0.2.51",
					Port:    16051,
				}.Build()
				ownerPeer := &cluster.Peer{Host: owner.GetHost(), RemotingPort: int(owner.GetPort()), PeersPort: 15051}
				localPeer := localClusterPeer(sys)

				rem.EXPECT().RemoteActivateGrain(ctx, owner.GetHost(), int(owner.GetPort()), mock.Anything).Return(tc.err).Once()
				cl.EXPECT().Members(ctx).Return([]*cluster.Peer{localPeer, ownerPeer}, nil).Once()

				handled, err := sys.tryRemoteGrainActivation(ctx, identity, newGrainConfig(), owner)
				require.ErrorIs(t, err, tc.err)
				require.False(t, handled)
				cl.AssertNotCalled(t, "ReleaseGrain", mock.Anything, mock.Anything, mock.Anything)
			})
		}
	})

	t.Run("transport failure with membership lookup failure", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, rem, identity := newActivationTestSystem(t, grain, "owner-remote-members-error", true)
		owner := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{Value: identity.String()}.Build(),
			Host:    "192.0.2.51",
			Port:    16051,
		}.Build()
		expectedErr := gerrors.NewErrRemoteSendFailure(errors.New("connection reset"))

		rem.EXPECT().RemoteActivateGrain(ctx, owner.GetHost(), int(owner.GetPort()), mock.Anything).Return(expectedErr).Once()
		cl.EXPECT().Members(ctx).Return(nil, errors.New("members unavailable")).Once()

		// without a membership verdict the owner is presumed alive
		handled, err := sys.tryRemoteGrainActivation(ctx, identity, newGrainConfig(), owner)
		require.ErrorIs(t, err, expectedErr)
		require.False(t, handled)
		cl.AssertNotCalled(t, "ReleaseGrain", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("departed owner entry is released", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, rem, identity := newActivationTestSystem(t, grain, "owner-departed", true)
		owner := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{Value: identity.String()}.Build(),
			Host:    "192.0.2.51",
			Port:    16051,
		}.Build()
		localPeer := localClusterPeer(sys)

		rem.EXPECT().RemoteActivateGrain(ctx, owner.GetHost(), int(owner.GetPort()), mock.Anything).Return(gerrors.NewErrRemoteSendFailure(errors.New("connection refused"))).Once()
		cl.EXPECT().Members(ctx).Return([]*cluster.Peer{localPeer}, nil).Once()
		cl.EXPECT().ReleaseGrain(ctx, identity.String(), address.FormatHostPort(owner.GetHost(), int(owner.GetPort()))).Return(nil, nil).Once()

		handled, err := sys.tryRemoteGrainActivation(ctx, identity, newGrainConfig(), owner)
		require.NoError(t, err)
		require.False(t, handled)
	})

	t.Run("departed owner entry already re-owned is left alone", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, rem, identity := newActivationTestSystem(t, grain, "owner-departed-reowned", true)
		owner := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{Value: identity.String()}.Build(),
			Host:    "192.0.2.51",
			Port:    16051,
		}.Build()
		newOwner := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{Value: identity.String()}.Build(),
			Host:    "192.0.2.52",
			Port:    16052,
		}.Build()
		localPeer := localClusterPeer(sys)

		rem.EXPECT().RemoteActivateGrain(ctx, owner.GetHost(), int(owner.GetPort()), mock.Anything).Return(gerrors.NewErrRemoteSendFailure(errors.New("connection refused"))).Once()
		cl.EXPECT().Members(ctx).Return([]*cluster.Peer{localPeer}, nil).Once()
		cl.EXPECT().ReleaseGrain(ctx, identity.String(), address.FormatHostPort(owner.GetHost(), int(owner.GetPort()))).Return(newOwner, nil).Once()

		handled, err := sys.tryRemoteGrainActivation(ctx, identity, newGrainConfig(), owner)
		require.NoError(t, err)
		require.False(t, handled)
	})

	t.Run("departed owner entry already gone", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, rem, identity := newActivationTestSystem(t, grain, "owner-departed-gone", true)
		owner := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{Value: identity.String()}.Build(),
			Host:    "192.0.2.51",
			Port:    16051,
		}.Build()
		localPeer := localClusterPeer(sys)

		rem.EXPECT().RemoteActivateGrain(ctx, owner.GetHost(), int(owner.GetPort()), mock.Anything).Return(gerrors.NewErrRemoteSendFailure(errors.New("connection refused"))).Once()
		cl.EXPECT().Members(ctx).Return([]*cluster.Peer{localPeer}, nil).Once()
		cl.EXPECT().ReleaseGrain(ctx, identity.String(), address.FormatHostPort(owner.GetHost(), int(owner.GetPort()))).Return(nil, nil).Once()

		handled, err := sys.tryRemoteGrainActivation(ctx, identity, newGrainConfig(), owner)
		require.NoError(t, err)
		require.False(t, handled)
	})

	t.Run("departed owner release errors propagate", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, rem, identity := newActivationTestSystem(t, grain, "owner-departed-release-error", true)
		owner := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{Value: identity.String()}.Build(),
			Host:    "192.0.2.51",
			Port:    16051,
		}.Build()
		localPeer := localClusterPeer(sys)
		releaseErr := errors.New("release failed")

		rem.EXPECT().RemoteActivateGrain(ctx, owner.GetHost(), int(owner.GetPort()), mock.Anything).Return(gerrors.NewErrRemoteSendFailure(errors.New("connection refused"))).Once()
		cl.EXPECT().Members(ctx).Return([]*cluster.Peer{localPeer}, nil).Once()
		// a failing release is tried grainRegistryWriteAttempts times
		cl.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(owner.GetHost(), int(owner.GetPort()))).Return(nil, releaseErr).Times(grainRegistryWriteAttempts)

		handled, err := sys.tryRemoteGrainActivation(ctx, identity, newGrainConfig(), owner)
		require.ErrorIs(t, err, releaseErr)
		require.False(t, handled)
	})

	t.Run("owner local returns false", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, _, rem, identity := newActivationTestSystem(t, grain, "owner-local", true)
		owner := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{Value: identity.String()}.Build(),
			Host:    sys.Host(),
			Port:    int32(sys.Port()),
		}.Build()

		handled, err := sys.tryRemoteGrainActivation(ctx, identity, newGrainConfig(), owner)
		require.NoError(t, err)
		require.False(t, handled)
		rem.AssertNotCalled(t, "RemoteActivateGrain", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("owner empty returns false", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, _, rem, identity := newActivationTestSystem(t, grain, "owner-empty", true)
		owner := &internalpb.Grain{}

		handled, err := sys.tryRemoteGrainActivation(ctx, identity, newGrainConfig(), owner)
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

		handled, err := sys.tryRemoteGrainActivation(ctx, identity, config, nil)
		require.Error(t, err)
		require.False(t, handled)
	})

	t.Run("no activation peer available", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, _, identity := newActivationTestSystem(t, grain, "peer-none", true)
		localPeer := &cluster.Peer{Host: sys.clusterNode.Host, PeersPort: sys.clusterNode.PeersPort}

		cl.EXPECT().Members(ctx).Return([]*cluster.Peer{localPeer}, nil)

		handled, err := sys.tryRemoteGrainActivation(ctx, identity, newGrainConfig(), nil)
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

		handled, err := sys.tryRemoteGrainActivation(ctx, identity, newGrainConfig(WithActivationStrategy(RoundRobinActivation)), nil)
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

		// a failing claim is tried grainRegistryWriteAttempts times
		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, expectedErr).Times(grainRegistryWriteAttempts)

		handled, err := sys.tryPeerActivation(ctx, identity, newGrainConfig(), peer)
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

		handled, err := sys.tryPeerActivation(ctx, identity, newGrainConfig(), peer)
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

		// a failing claim is tried grainRegistryWriteAttempts times
		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Times(grainRegistryWriteAttempts)
		cl.EXPECT().PutGrain(mock.Anything, mock.MatchedBy(func(actual *internalpb.Grain) bool {
			return actual != nil && actual.GetGrainId().GetValue() == identity.String()
		})).Return(expectedErr).Times(grainRegistryWriteAttempts)

		handled, err := sys.tryPeerActivation(ctx, identity, newGrainConfig(), peer)
		require.ErrorIs(t, err, expectedErr)
		require.False(t, handled)
		rem.AssertNotCalled(t, "RemoteActivateGrain", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("keeps the claim when the activation outcome is unknown", func(t *testing.T) {
		gaveUp, cancel := context.WithCancel(t.Context())
		cancel()

		cases := []struct {
			name string
			ctx  context.Context
			err  error
		}{
			{"transport failure", t.Context(), gerrors.NewErrRemoteSendFailure(errors.New("connection reset"))},
			{"dial timeout", t.Context(), &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}},
			{"caller context expired", gaveUp, context.DeadlineExceeded},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				grain := NewMockGrain()
				sys, cl, rem, identity := newActivationTestSystem(t, grain, "peer-activate-unknown", true)
				peer := &cluster.Peer{Host: "192.0.2.65", PeersPort: 15065, RemotingPort: 16065}

				cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Once()
				cl.EXPECT().PutGrain(mock.Anything, mock.MatchedBy(func(actual *internalpb.Grain) bool {
					return actual != nil && actual.GetGrainId().GetValue() == identity.String() &&
						actual.GetHost() == peer.Host && int(actual.GetPort()) == peer.RemotingPort
				})).Return(nil).Once()
				rem.EXPECT().RemoteActivateGrain(tc.ctx, peer.Host, peer.RemotingPort, mock.Anything).Return(tc.err).Once()

				handled, err := sys.tryPeerActivation(tc.ctx, identity, newGrainConfig(), peer)
				require.ErrorIs(t, err, tc.err)
				require.False(t, handled)
				// the peer may have activated the grain, so the claim is kept
				cl.AssertNotCalled(t, "ReleaseGrain", mock.Anything, mock.Anything, mock.Anything)
			})
		}
	})

	t.Run("rolls back the claim when the peer rejects the activation", func(t *testing.T) {
		cases := []struct {
			name string
			err  error
		}{
			{"remote activation failure", gerrors.NewErrGrainActivationFailure(errors.New("OnActivate failed"))},
			{"kind not registered on the peer", gerrors.ErrTypeNotRegistered},
			{"opaque wire error", errors.New("remote activate failed")},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				ctx := t.Context()
				grain := NewMockGrain()
				sys, cl, rem, identity := newActivationTestSystem(t, grain, "peer-activate-rejected", true)
				peer := &cluster.Peer{Host: "192.0.2.65", PeersPort: 15065, RemotingPort: 16065}

				cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Once()
				cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(nil).Once()
				rem.EXPECT().RemoteActivateGrain(ctx, peer.Host, peer.RemotingPort, mock.Anything).Return(tc.err).Once()
				// the activation is known not to have happened: the claim is
				// released while it still names the peer
				cl.EXPECT().ReleaseGrain(ctx, identity.String(), address.FormatHostPort(peer.Host, peer.RemotingPort)).Return(nil, nil).Once()

				handled, err := sys.tryPeerActivation(ctx, identity, newGrainConfig(), peer)
				require.ErrorIs(t, err, tc.err)
				require.False(t, handled)
			})
		}
	})

	t.Run("activates here when the peer is shutting down", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, rem, identity := newActivationTestSystem(t, grain, "peer-shutting-down", true)
		peer := &cluster.Peer{Host: "192.0.2.66", PeersPort: 15066, RemotingPort: 16066}

		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Once()
		cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(nil).Once()
		rem.EXPECT().RemoteActivateGrain(ctx, peer.Host, peer.RemotingPort, mock.Anything).Return(gerrors.ErrSystemShuttingDown).Once()
		cl.EXPECT().ReleaseGrain(ctx, identity.String(), address.FormatHostPort(peer.Host, peer.RemotingPort)).Return(nil, nil).Once()

		handled, err := sys.tryPeerActivation(ctx, identity, newGrainConfig(), peer)
		require.NoError(t, err)
		require.False(t, handled)
	})

	t.Run("reports the refusal when the claim cannot be rolled back", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, rem, identity := newActivationTestSystem(t, grain, "peer-shutting-down-no-rollback", true)
		peer := &cluster.Peer{Host: "192.0.2.67", PeersPort: 15067, RemotingPort: 16067}

		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Once()
		cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(nil).Once()
		rem.EXPECT().RemoteActivateGrain(ctx, peer.Host, peer.RemotingPort, mock.Anything).Return(gerrors.ErrSystemShuttingDown).Once()
		cl.EXPECT().ReleaseGrain(ctx, identity.String(), address.FormatHostPort(peer.Host, peer.RemotingPort)).Return(nil, assert.AnError).Once()

		handled, err := sys.tryPeerActivation(ctx, identity, newGrainConfig(), peer)
		require.ErrorIs(t, err, gerrors.ErrSystemShuttingDown)
		require.False(t, handled)
	})

	t.Run("rollback leaves a record re-owned elsewhere alone", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, rem, identity := newActivationTestSystem(t, grain, "peer-activate-reowned", true)
		peer := &cluster.Peer{Host: "192.0.2.65", PeersPort: 15065, RemotingPort: 16065}
		expectedErr := errors.New("remote activate failed")

		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Once()
		cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(nil).Once()
		rem.EXPECT().RemoteActivateGrain(ctx, peer.Host, peer.RemotingPort, mock.Anything).Return(expectedErr).Once()
		cl.EXPECT().ReleaseGrain(ctx, identity.String(), address.FormatHostPort(peer.Host, peer.RemotingPort)).Return(remoteGrainRecord(identity, "192.0.2.66", 16066), nil).Once()

		handled, err := sys.tryPeerActivation(ctx, identity, newGrainConfig(), peer)
		require.ErrorIs(t, err, expectedErr)
		require.False(t, handled)
	})

	t.Run("rollback failure keeps the activation error", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, rem, identity := newActivationTestSystem(t, grain, "peer-activate-rollback-error", true)
		peer := &cluster.Peer{Host: "192.0.2.65", PeersPort: 15065, RemotingPort: 16065}
		expectedErr := errors.New("remote activate failed")

		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Once()
		cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(nil).Once()
		rem.EXPECT().RemoteActivateGrain(ctx, peer.Host, peer.RemotingPort, mock.Anything).Return(expectedErr).Once()
		cl.EXPECT().ReleaseGrain(ctx, identity.String(), address.FormatHostPort(peer.Host, peer.RemotingPort)).Return(nil, errors.New("release failed")).Once()

		handled, err := sys.tryPeerActivation(ctx, identity, newGrainConfig(), peer)
		require.ErrorIs(t, err, expectedErr)
		require.False(t, handled)
	})

	t.Run("returns handled when owner already exists", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, rem, identity := newActivationTestSystem(t, grain, "peer-owner-exists", true)
		peer := &cluster.Peer{Host: "192.0.2.63", PeersPort: 15063, RemotingPort: 16063}
		owner := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{Value: identity.String()}.Build(),
			Host:    "192.0.2.64",
			Port:    16064,
		}.Build()

		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil).Once()

		handled, err := sys.tryPeerActivation(ctx, identity, newGrainConfig(), peer)
		require.NoError(t, err)
		require.True(t, handled)
		rem.AssertNotCalled(t, "RemoteActivateGrain", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})
}

func TestActivateGrainLocally(t *testing.T) {
	t.Run("provider failure", func(t *testing.T) {
		ctx := t.Context()
		sys, _, _, identity := newActivationTestSystem(t, NewMockGrain(), "local-provider-error", true)
		expectedErr := errors.New("factory failed")

		err := sys.activateGrainLocally(ctx, identity, func() (Grain, error) { return nil, expectedErr }, newGrainConfig(), nil)
		require.ErrorIs(t, err, expectedErr)
	})

	t.Run("returns error when claim fails", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, _, identity := newActivationTestSystem(t, grain, "local-claim-error", false)
		expectedErr := errors.New("claim error")

		// a failing claim is tried grainRegistryWriteAttempts times
		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, expectedErr).Times(grainRegistryWriteAttempts)

		err := sys.activateGrainLocally(ctx, identity, staticGrainProvider(grain), newGrainConfig(), nil)
		require.ErrorIs(t, err, expectedErr)
		require.True(t, sys.registry.Exists(grain))
	})

	t.Run("returns error when wire grain encoding fails", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, _, _, identity := newActivationTestSystem(t, grain, "local-wire-error", false)
		expectedErr := errors.New("wire encode failed")
		config := newGrainConfig(WithGrainDependencies(&MockFailingDependency{err: expectedErr}))

		err := sys.activateGrainLocally(ctx, identity, staticGrainProvider(grain), config, nil)
		require.ErrorIs(t, err, expectedErr)
	})

	t.Run("returns error when put grain fails", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, _, identity := newActivationTestSystem(t, grain, "local-put-error", false)
		expectedErr := errors.New("put grain failed")

		// a failing claim is tried grainRegistryWriteAttempts times
		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Times(grainRegistryWriteAttempts)
		cl.EXPECT().PutGrain(mock.Anything, mock.MatchedBy(func(actual *internalpb.Grain) bool {
			return actual != nil && actual.GetGrainId().GetValue() == identity.String()
		})).Return(expectedErr).Times(grainRegistryWriteAttempts)

		err := sys.activateGrainLocally(ctx, identity, staticGrainProvider(grain), newGrainConfig(), nil)
		require.ErrorIs(t, err, expectedErr)
	})

	t.Run("returns nil when claim owner mismatch", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, _, identity := newActivationTestSystem(t, grain, "local-claim-mismatch", true)
		remoteOwner := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{Value: identity.String()}.Build(),
			Host:    "192.0.2.70",
			Port:    17070,
		}.Build()

		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(remoteOwner, nil).Once()

		err := sys.activateGrainLocally(ctx, identity, staticGrainProvider(grain), newGrainConfig(), nil)
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
		cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(nil).Once()

		err := sys.activateGrainLocally(ctx, identity, staticGrainProvider(grain), newGrainConfig(), nil)
		require.NoError(t, err)
		require.Equal(t, identity.Name(), grain.name)

		_, ok := sys.grains.Get(identity.String())
		require.True(t, ok)
	})

	t.Run("activation failure cleans up claim", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockActivationFailingGrain()
		sys, cl, _, identity := newActivationTestSystem(t, grain, "local-activate-fail", true)
		config := newGrainConfig(
			WithGrainInitMaxRetries(1),
			WithGrainInitTimeout(10*time.Millisecond),
		)

		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Once()
		cl.EXPECT().PutGrain(mock.Anything, mock.MatchedBy(func(actual *internalpb.Grain) bool {
			return actual != nil && actual.GetGrainId().GetValue() == identity.String()
		})).Return(nil).Once()
		cl.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(sys.Host(), sys.Port())).Return(nil, nil).Once()

		err := sys.activateGrainLocally(ctx, identity, staticGrainProvider(grain), config, nil)
		require.ErrorIs(t, err, gerrors.ErrGrainActivationFailure)
	})

	t.Run("returns publish error when owner set", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, _, identity := newActivationTestSystem(t, grain, "local-publish-error", true)
		expectedErr := errors.New("dependency encode failure")
		config := newGrainConfig(WithGrainDependencies(&MockFailingDependency{err: expectedErr}))
		owner := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{Value: identity.String()}.Build(),
			Host:    sys.Host(),
			Port:    int32(sys.Port()),
		}.Build()

		// the publish failure deactivates the grain, which releases its
		// cluster record, so a failed activation leaves nothing behind
		cl.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(sys.Host(), sys.Port())).Return(nil, nil).Once()

		err := sys.activateGrainLocally(ctx, identity, staticGrainProvider(grain), config, owner)
		require.ErrorIs(t, err, expectedErr)

		_, ok := sys.grains.Get(identity.String())
		require.False(t, ok)
	})

	t.Run("skips activation when already active", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		grain.name = "pre-activated"
		sys, cl, _, identity := newActivationTestSystem(t, grain, "local-active", true)
		pid := newGrainPID(identity, grain, sys, newGrainConfig())
		pid.activated.Store(true)
		sys.grains.Set(identity.String(), pid)
		owner := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{Value: identity.String()}.Build(),
			Host:    sys.Host(),
			Port:    int32(sys.Port()),
		}.Build()

		// the registry record is refreshed even when activation is skipped
		cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(nil).Once()

		err := sys.activateGrainLocally(ctx, identity, staticGrainProvider(grain), newGrainConfig(), owner)
		require.NoError(t, err)
		require.Equal(t, "pre-activated", grain.name)
	})

	t.Run("returns error when activation barrier times out", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, _, _, identity := newActivationTestSystem(t, grain, "local-barrier-timeout", false)
		sys.grainBarrier = newGrainActivationBarrier(2, 10*time.Millisecond)
		owner := internalpb.Grain_builder{GrainId: internalpb.GrainId_builder{Value: identity.String()}.Build()}.Build()

		err := sys.activateGrainLocally(ctx, identity, staticGrainProvider(grain), newGrainConfig(), owner)
		require.ErrorIs(t, err, gerrors.ErrGrainActivationBarrierTimeout)
	})
}

func TestActivateGrainLocalActiveFastPath(t *testing.T) {
	t.Run("locally active grain skips the cluster registry", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, _, _, identity := newActivationTestSystem(t, grain, "fast-path-active", true)
		pid := newGrainPID(identity, grain, sys, newGrainConfig())
		pid.activated.Store(true)
		sys.grains.Set(identity.String(), pid)

		// no cluster expectations: any registry call fails the test
		got, err := sys.GrainIdentity(ctx, identity.Name(), func(context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)
		require.True(t, identity.Equal(got))
	})

	t.Run("deregistered active grain is registered again", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, _, _, identity := newActivationTestSystem(t, grain, "fast-path-deregistered", true)
		sys.clusterEnabled.Store(false)

		pid := newGrainPID(identity, grain, sys, newGrainConfig())
		pid.activated.Store(true)
		sys.grains.Set(identity.String(), pid)
		require.NoError(t, sys.DeregisterGrainKind(ctx, grain))
		require.False(t, sys.registry.Exists(grain))

		got, err := sys.GrainIdentity(ctx, identity.Name(), func(context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)
		require.True(t, identity.Equal(got))
		require.True(t, sys.registry.Exists(grain))
	})

	t.Run("repeat GrainOf resolves without registry traffic", func(t *testing.T) {
		ctx := t.Context()
		name := "fast-path-grainof"
		identity := newGrainIdentity((*MockActivationCountingGrain)(nil), name)
		localPeer := &cluster.Peer{Host: "127.0.0.1", PeersPort: 14040, RemotingPort: 8100}

		cl := mockcluster.NewCluster(t)
		rem := mockremote.NewClient(t)
		node := &discovery.Node{Host: localPeer.Host, PeersPort: localPeer.PeersPort, RemotingPort: localPeer.RemotingPort}
		sys := newClusterReadySystem(rem, cl, node)

		// the slow path runs exactly once; any repeat run would exceed these counts
		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Twice()
		cl.EXPECT().Members(ctx).Return([]*cluster.Peer{localPeer}, nil).Once()
		cl.EXPECT().PutGrain(mock.Anything, mock.MatchedBy(func(actual *internalpb.Grain) bool {
			return actual != nil && actual.GetGrainId().GetValue() == identity.String()
		})).Return(nil).Twice()

		activationCount.Store(0)

		first, err := GrainOf[*MockActivationCountingGrain](ctx, sys, name)
		require.NoError(t, err)
		require.NotNil(t, first)

		// the issue's repro: resolving the same identity in a loop from the
		// same node must produce no further registry traffic
		for range 10 {
			got, err := GrainOf[*MockActivationCountingGrain](ctx, sys, name)
			require.NoError(t, err)
			require.True(t, first.Equal(got))
		}

		require.EqualValues(t, 1, activationCount.Load())
	})

	t.Run("deactivated grain reactivates through the slow path", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, _, identity := newActivationTestSystem(t, grain, "fast-path-reactivate", true)

		// a deactivated local entry with no cluster record, as left behind by
		// an idle deactivation
		pid := newGrainPID(identity, grain, sys, newGrainConfig())
		sys.grains.Set(identity.String(), pid)

		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Twice()
		cl.EXPECT().Members(ctx).Return([]*cluster.Peer{{Host: sys.clusterNode.Host, PeersPort: sys.clusterNode.PeersPort, RemotingPort: sys.clusterNode.RemotingPort}}, nil).Once()
		cl.EXPECT().PutGrain(mock.Anything, mock.MatchedBy(func(actual *internalpb.Grain) bool {
			return actual != nil && actual.GetGrainId().GetValue() == identity.String()
		})).Return(nil).Twice()

		got, err := sys.activateGrain(ctx, identity, staticGrainProvider(grain), newGrainConfig())
		require.NoError(t, err)
		require.True(t, identity.Equal(got))
		require.True(t, pid.isActive())
	})

	t.Run("inactive local grain falls through to ownership resolution", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, rem, identity := newActivationTestSystem(t, grain, "fast-path-inactive", true)

		// a deactivated local entry, as left behind by a relocation handoff
		pid := newGrainPID(identity, grain, sys, newGrainConfig())
		sys.grains.Set(identity.String(), pid)

		owner := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind(), Name: identity.Name()}.Build(),
			Host:    "192.0.2.80",
			Port:    16080,
		}.Build()

		// the registry stays authoritative for a grain that is not live here
		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil).Once()
		rem.EXPECT().RemoteActivateGrain(ctx, owner.GetHost(), int(owner.GetPort()), mock.MatchedBy(func(req *remote.GrainRequest) bool {
			return req != nil && req.Name == identity.Name() && req.Kind == identity.Kind()
		})).Return(nil).Once()

		got, err := sys.activateGrain(ctx, identity, staticGrainProvider(grain), newGrainConfig())
		require.NoError(t, err)
		require.True(t, identity.Equal(got))
	})

	t.Run("deregistered kind falls through and re-registers", func(t *testing.T) {
		ctx := t.Context()
		grain := NewMockGrain()
		sys, cl, _, identity := newActivationTestSystem(t, grain, "fast-path-deregistered-cluster", true)

		pid := newGrainPID(identity, grain, sys, newGrainConfig())
		pid.activated.Store(true)
		sys.grains.Set(identity.String(), pid)
		sys.registry.Deregister(grain)

		owner := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind(), Name: identity.Name()}.Build(),
			Host:    sys.Host(),
			Port:    int32(sys.Port()),
		}.Build()

		// the slow path runs: ownership is re-confirmed and the record republished
		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil).Once()
		cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(nil).Once()

		got, err := sys.GrainIdentity(ctx, identity.Name(), func(context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)
		require.True(t, identity.Equal(got))
		require.True(t, sys.registry.Exists(grain))
	})
}

func TestFindActivationPeer_ErrorsWhenRoleMissingEverywhere(t *testing.T) {
	ctx := t.Context()
	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewClient(t)
	node := &discovery.Node{Host: "127.0.0.1", PeersPort: 14000, RemotingPort: 8080, Roles: []string{"api"}}
	sys := newClusterReadySystem(rem, cl, node)

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
	sys := newClusterReadySystem(rem, cl, node)

	grain := NewMockGrain()
	sys.registry.Register(grain)
	identity := newGrainIdentity(grain, "auto-provision")

	cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(nil, cluster.ErrGrainNotFound).Once()
	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Twice()
	// one PutGrain for the ownership claim, one for the post-activation publication
	cl.EXPECT().PutGrain(mock.Anything, mock.MatchedBy(func(actual *internalpb.Grain) bool {
		return actual != nil && actual.GetGrainId().GetValue() == identity.String()
	})).Return(nil).Twice()

	resp, err := sys.AskGrain(ctx, identity, &testpb.TestReply{}, time.Second)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, "received message", resp.(*testpb.Reply).GetContent())

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

// TestRecreateGrainRestoresActivationRole verifies a relocated or remotely
// activated grain keeps its WithActivationRole constraint: the reconstructed
// config must carry the role so the next departure's wire record (toWireGrain)
// still advertises it, otherwise the constraint would be lost after the first
// relocation (issue #1334).
func TestRecreateGrainRestoresActivationRole(t *testing.T) {
	ctx := t.Context()
	sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	t.Cleanup(func() { _ = sys.Stop(ctx) })

	as := sys.(*actorSystem)
	as.registry.Register(&MockGrain{})

	identity := newGrainIdentity(&MockGrain{}, "role-restore")
	pid := newGrainPID(identity, &MockGrain{}, sys, newGrainConfig(WithActivationRole("game-worker"), WithGrainEagerRelocation()))
	wire, err := pid.toWireGrain()
	require.NoError(t, err)
	require.Equal(t, "game-worker", wire.GetRole())

	require.NoError(t, as.recreateGrain(ctx, wire))

	process, ok := as.grains.Get(identity.String())
	require.True(t, ok)
	require.NotNil(t, process.config.role)
	require.Equal(t, "game-worker", *process.config.role)
	require.True(t, process.config.eagerRelocation)

	// the role must survive the config -> wire round trip
	republished, err := process.toWireGrain()
	require.NoError(t, err)
	require.True(t, republished.HasRole())
	require.Equal(t, "game-worker", republished.GetRole())
}

func TestRecreateGrain_SingleflightActivation(t *testing.T) {
	ctx := t.Context()
	sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	t.Cleanup(func() { _ = sys.Stop(ctx) })

	as := sys.(*actorSystem)
	as.registry.Register(&MockActivationProbeGrain{})

	identity := newGrainIdentity(&MockActivationProbeGrain{}, "singleflight")
	pid := newGrainPID(identity, &MockActivationProbeGrain{}, sys, newGrainConfig())
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

	resp, err := sys.localSendGrain(ctx, identity, &testpb.TestReply{}, time.Second, grainAsk)
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
	owner := internalpb.Grain_builder{Host: "192.0.2.90", Port: 9090}.Build()
	err := (&grainOwnerMismatchError{owner: owner}).Error()
	require.Equal(t, "grain is owned by 192.0.2.90:9090", err)
}

func TestTryClaimGrain_AlreadyExistsButMissingOwner(t *testing.T) {
	ctx := t.Context()
	grain := NewMockGrain()
	sys, cl, _, identity := newActivationTestSystem(t, grain, "missing-owner", false)

	wire := internalpb.Grain_builder{GrainId: internalpb.GrainId_builder{Value: identity.String()}.Build()}.Build()

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

	wire := internalpb.Grain_builder{GrainId: internalpb.GrainId_builder{Value: identity.String()}.Build()}.Build()
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
	ports := internalnet.Get(1)
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
	ports := internalnet.Get(1)
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
	ports := internalnet.Get(1)
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
	sys := newClusterReadySystem(rem, cl, node)

	resp, _, err := sys.sendToGrainOwner(ctx, newGrainIdentity(NewMockGrain(), "missing-owner"), nil, &testpb.TestReply{}, time.Second, grainAsk)
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
		sys := newClusterReadySystem(rem, cl, node)
		sys.registry.Register(NewMockGrain())

		identity := newGrainIdentity(NewMockGrain(), "tell-cluster-grain")
		owner := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind()}.Build(),
			Host:    "192.0.2.1",
			Port:    16000,
		}.Build()
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil).Once()
		rem.EXPECT().RemoteTellGrain(mock.Anything, owner.GetHost(), int(owner.GetPort()), mock.Anything, mock.Anything).
			Return(gerrors.NewErrRemoteSendFailure(errors.New("connection refused"))).Once()
		// the owner is still a member, so its entry is kept
		cl.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{{Host: owner.GetHost(), RemotingPort: int(owner.GetPort())}}, nil).Once()

		err := sys.TellGrain(ctx, identity, &testpb.TestSend{})
		require.Error(t, err)
	})

	t.Run("cluster mode delivers in-process when grain owned by current node", func(t *testing.T) {
		ctx := context.Background()
		cl := mockcluster.NewCluster(t)
		rem := mockremote.NewClient(t)
		node := &discovery.Node{Host: "127.0.0.1", PeersPort: 9017, RemotingPort: 9117}
		sys := newClusterReadySystem(rem, cl, node)
		sys.registry.Register(NewMockGrain())

		identity := newGrainIdentity(NewMockGrain(), "tell-local-owner-grain")
		// Owner endpoint matches the current node, so delivery must stay in-process.
		owner := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind()}.Build(),
			Host:    node.Host,
			Port:    int32(node.RemotingPort),
		}.Build()
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil)
		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
		cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(nil).Once()

		// No RemoteTellGrain expectation: a loopback round trip would fail the
		// mockremote client, proving the message was delivered locally.
		err := sys.TellGrain(ctx, identity, &testpb.TestSend{})
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			_, ok := sys.grains.Get(identity.String())
			return ok
		}, 100*time.Millisecond, 5*time.Millisecond, "grain should be activated locally after TellGrain")
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

	t.Run("subsequent Ask succeeds after a timeout", func(t *testing.T) {
		ctx := context.Background()
		sys, err := NewActorSystem("ask-grain-after-timeout", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, sys.Start(ctx))
		t.Cleanup(func() { _ = sys.Stop(ctx) })

		started := make(chan struct{})
		release := make(chan struct{})
		grain := &MockScriptedGrain{receive: func(gctx *GrainContext) {
			switch gctx.Message().(type) {
			case *testpb.TestTimeout:
				close(started)
				<-release
				gctx.Response(new(testpb.Reply))
			case *testpb.TestReply:
				gctx.Response(new(testpb.Reply))
			default:
				gctx.Unhandled()
			}
		}}

		identity, err := sys.GrainIdentity(ctx, "after-timeout", func(context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)

		resp, err := sys.AskGrain(ctx, identity, new(testpb.TestTimeout), 50*time.Millisecond)
		require.ErrorIs(t, err, gerrors.ErrRequestTimeout)
		require.Nil(t, resp)

		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("grain never started the timed-out ask")
		}
		close(release)

		resp, err = sys.AskGrain(ctx, identity, new(testpb.TestReply), time.Second)
		require.NoError(t, err)
		require.IsType(t, &testpb.Reply{}, resp)
	})

	t.Run("cluster mode with GetGrain success", func(t *testing.T) {
		ctx := context.Background()
		cl := mockcluster.NewCluster(t)
		rem := mockremote.NewClient(t)
		node := &discovery.Node{Host: "127.0.0.1", PeersPort: 9016, RemotingPort: 9116}
		sys := newClusterReadySystem(rem, cl, node)
		sys.registry.Register(NewMockGrain())

		identity := newGrainIdentity(NewMockGrain(), "ask-cluster-grain")
		owner := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind()}.Build(),
			Host:    "192.0.2.1",
			Port:    16000,
		}.Build()
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil).Once()
		rem.EXPECT().RemoteAskGrain(mock.Anything, owner.GetHost(), int(owner.GetPort()), mock.Anything, mock.Anything, time.Second).
			Return(nil, errors.New("connection refused")).Once()

		resp, err := sys.AskGrain(ctx, identity, &testpb.TestReply{}, time.Second)
		require.Error(t, err)
		require.Nil(t, resp)
	})

	t.Run("cluster mode delivers in-process when grain owned by current node", func(t *testing.T) {
		ctx := context.Background()
		cl := mockcluster.NewCluster(t)
		rem := mockremote.NewClient(t)
		node := &discovery.Node{Host: "127.0.0.1", PeersPort: 9018, RemotingPort: 9118}
		sys := newClusterReadySystem(rem, cl, node)
		sys.registry.Register(NewMockGrain())

		identity := newGrainIdentity(NewMockGrain(), "ask-local-owner-grain")
		// Owner endpoint matches the current node, so delivery must stay in-process.
		owner := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind()}.Build(),
			Host:    node.Host,
			Port:    int32(node.RemotingPort),
		}.Build()
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil)
		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
		cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(nil).Once()

		// No RemoteAskGrain expectation: a loopback round trip would fail the
		// mockremote client, proving the request was served locally.
		resp, err := sys.AskGrain(ctx, identity, &testpb.TestReply{}, time.Second)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, "received message", resp.(*testpb.Reply).GetContent())
	})
}

func TestAskGrain_ReplyChannelPooling(t *testing.T) {
	t.Run("a reply returns the channel to the grain shard", func(t *testing.T) {
		ctx := context.Background()
		sys, err := NewActorSystem("ask-grain-reply-pooled", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, sys.Start(ctx))
		t.Cleanup(func() { _ = sys.Stop(ctx) })

		grain := &MockScriptedGrain{receive: func(gctx *GrainContext) {
			gctx.Response(new(testpb.Reply))
		}}

		identity, err := sys.GrainIdentity(ctx, "reply-pooled", func(context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)

		// A first ask activates the grain, which is what assigns its home shard.
		_, err = sys.AskGrain(ctx, identity, new(testpb.TestReply), time.Second)
		require.NoError(t, err)

		pid, ok := sys.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)

		shard := pid.ctxShard
		drainGrainChannelShard(grainReplyChannelPool, shard)

		resp, err := sys.AskGrain(ctx, identity, new(testpb.TestReply), time.Second)
		require.NoError(t, err)
		require.IsType(t, &testpb.Reply{}, resp)

		pooled := grainReplyChannelPool.shards[shard&grainReplyChannelPool.mask].pop()
		require.NotNil(t, pooled, "a completed ask must return its reply channel to the grain shard")
	})

	t.Run("a timed out ask abandons the channel", func(t *testing.T) {
		ctx := context.Background()
		sys, err := NewActorSystem("ask-grain-reply-abandoned", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, sys.Start(ctx))
		t.Cleanup(func() { _ = sys.Stop(ctx) })

		started := make(chan struct{})
		release := make(chan struct{})
		// Registered after the shutdown cleanup so it runs first: the blocked
		// handler must be released before the system stops.
		t.Cleanup(func() { close(release) })

		grain := &MockScriptedGrain{receive: func(gctx *GrainContext) {
			switch gctx.Message().(type) {
			case *testpb.TestTimeout:
				close(started)
				<-release
				gctx.Response(new(testpb.Reply))
			default:
				gctx.Response(new(testpb.Reply))
			}
		}}

		identity, err := sys.GrainIdentity(ctx, "reply-abandoned", func(context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)

		// A first ask activates the grain, which is what assigns its home shard.
		_, err = sys.AskGrain(ctx, identity, new(testpb.TestReply), time.Second)
		require.NoError(t, err)

		pid, ok := sys.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)

		shard := pid.ctxShard
		drainGrainChannelShard(grainReplyChannelPool, shard)

		resp, err := sys.AskGrain(ctx, identity, new(testpb.TestTimeout), 50*time.Millisecond)
		require.ErrorIs(t, err, gerrors.ErrRequestTimeout)
		require.Nil(t, resp)

		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("grain never started the timed-out ask")
		}

		pooled := grainReplyChannelPool.shards[shard&grainReplyChannelPool.mask].pop()
		require.Nil(t, pooled, "a timed out ask must abandon its reply channel, a late reply could still reach it")
	})
}

func TestSelectActivationPeer_LeastLoadActivation(t *testing.T) {
	ctx := t.Context()
	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewClient(t)
	node := &discovery.Node{Host: "127.0.0.1", PeersPort: 14000, RemotingPort: 8080}
	sys := newClusterReadySystem(rem, cl, node)

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
		return internalpb.GetNodeMetricResponse_builder{NodeAddress: "127.0.0.1:16000", Load: 5}.Build(), nil
	}
	ps, err := internalnet.NewRemotingServer("127.0.0.1:0", internalnet.WithProtoHandler("internalpb.GetNodeMetricRequest", handler))
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
	sys := newClusterReadySystem(rem, cl, node)

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
	sys := newClusterReadySystem(rem, cl, node)

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
	sys := newClusterReadySystem(rem, cl, node)

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
	sys := newClusterReadySystem(rem, cl, node)

	owner := internalpb.Grain_builder{
		GrainId: internalpb.GrainId_builder{Value: "grain|test", Kind: "TestGrain"}.Build(),
		Host:    "192.0.2.1",
		Port:    16000,
	}.Build()

	rem.EXPECT().RemoteTellGrain(mock.Anything, owner.GetHost(), int(owner.GetPort()), mock.Anything, mock.Anything).
		Return(gerrors.NewErrRemoteSendFailure(errors.New("connection refused"))).Once()
	// the owner is still a member, so its entry is kept
	cl.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{{Host: owner.GetHost(), RemotingPort: int(owner.GetPort())}}, nil).Once()

	resp, refused, err := sys.sendToGrainOwner(ctx, newGrainIdentity(NewMockGrain(), "test"), owner, &testpb.TestSend{}, time.Second, grainTell)
	require.Error(t, err)
	require.False(t, refused)
	require.Nil(t, resp)
}

func TestSendToGrainOwner_OneWayMode(t *testing.T) {
	ctx := t.Context()
	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewClient(t)
	node := &discovery.Node{Host: "127.0.0.1", PeersPort: 9027, RemotingPort: 9127}
	sys := newClusterReadySystem(rem, cl, node)

	owner := internalpb.Grain_builder{
		GrainId: internalpb.GrainId_builder{Value: "grain|test", Kind: "TestGrain", Name: "test"}.Build(),
		Host:    "192.0.2.1",
		Port:    16000,
	}.Build()

	// No RemoteTellGrain expectation: the acknowledged call would fail the mock.
	rem.EXPECT().RemoteTellGrainOneWay(mock.Anything, owner.GetHost(), int(owner.GetPort()),
		mock.MatchedBy(func(request *remote.GrainRequest) bool {
			return request.Name == "test" && request.Kind == "TestGrain"
		}), mock.Anything).Return(nil).Once()

	resp, _, err := sys.sendToGrainOwner(ctx, newGrainIdentity(NewMockGrain(), "test"), owner, &testpb.TestSend{}, time.Second, grainOneWay)
	require.NoError(t, err)
	require.Nil(t, resp)
}

func TestLocalSend_OneWayForwardsToRemoteOwner(t *testing.T) {
	ctx := t.Context()
	grain := NewMockGrain()
	sys, cl, rem, identity := newActivationTestSystem(t, grain, "one-way-owner-mismatch", true)

	owner := internalpb.Grain_builder{
		GrainId: internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind(), Name: identity.Name()}.Build(),
		Host:    "192.0.2.1",
		Port:    16000,
	}.Build()
	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
	cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil).Once()
	rem.EXPECT().RemoteTellGrainOneWay(mock.Anything, owner.GetHost(), int(owner.GetPort()), mock.Anything, mock.Anything).
		Return(nil).Once()

	resp, err := sys.localSendGrain(ctx, identity, &testpb.TestSend{}, time.Second, grainOneWay)
	require.NoError(t, err)
	require.Nil(t, resp)

	_, ok := sys.grains.Get(identity.String())
	require.False(t, ok, "a grain owned elsewhere must not be activated locally")
}

func TestRemoteTellGrain_FallbackPaths(t *testing.T) {
	ctx := context.Background()

	t.Run("GetGrain returns non-ErrGrainNotFound error", func(t *testing.T) {
		cl := mockcluster.NewCluster(t)
		rem := mockremote.NewClient(t)
		node := &discovery.Node{Host: "127.0.0.1", PeersPort: 9017, RemotingPort: 9117}
		sys := newClusterReadySystem(rem, cl, node)
		sys.registry.Register(NewMockGrain())

		identity := newGrainIdentity(NewMockGrain(), "tell-err-grain")
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(nil, errors.New("cluster error")).Once()

		err := sys.remoteTellGrain(ctx, identity, &testpb.TestSend{}, time.Second, grainTell)
		require.Error(t, err)
		require.ErrorContains(t, err, "cluster error")
	})

	t.Run("GetGrain ErrGrainNotFound then tellGrainAcrossDataCenters succeeds", func(t *testing.T) {
		rem := mockremote.NewClient(t)
		sys := startDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
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

		err := sys.remoteTellGrain(ctx, identity, &testpb.TestSend{}, time.Second, grainTell)
		require.NoError(t, err)
	})

	t.Run("one-way GetGrain ErrGrainNotFound then tellGrainAcrossDataCenters uses the one-way call", func(t *testing.T) {
		rem := mockremote.NewClient(t)
		sys := startDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{{
				ID: "dc-1", State: datacenter.DataCenterActive,
				Endpoints: []string{"127.0.0.1:9000"},
			}}, nil
		}, rem)
		sys.clusterEnabled.Store(true)
		cl := mockcluster.NewCluster(t)
		cl.EXPECT().GetGrain(mock.Anything, mock.Anything).Return(nil, cluster.ErrGrainNotFound).Once()
		sys.cluster = cl

		identity := newGrainIdentity(NewMockGrain(), "tell-one-way-dc-grain")
		rem.EXPECT().RemoteTellGrainOneWay(mock.Anything, "127.0.0.1", 9000, mock.Anything, mock.Anything).Return(nil).Once()

		err := sys.remoteTellGrain(ctx, identity, &testpb.TestSend{}, time.Second, grainOneWay)
		require.NoError(t, err)
	})
}

func TestRemoteAskGrain_FallbackPaths(t *testing.T) {
	ctx := context.Background()

	t.Run("GetGrain returns non-ErrGrainNotFound error", func(t *testing.T) {
		cl := mockcluster.NewCluster(t)
		rem := mockremote.NewClient(t)
		node := &discovery.Node{Host: "127.0.0.1", PeersPort: 9018, RemotingPort: 9118}
		sys := newClusterReadySystem(rem, cl, node)
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
		sys := startDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
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
	sys := newClusterReadySystem(rem, cl, node)

	owner := internalpb.Grain_builder{
		GrainId: internalpb.GrainId_builder{Value: "grain|test", Kind: "TestGrain"}.Build(),
		Host:    "192.0.2.1",
		Port:    16000,
	}.Build()

	rem.EXPECT().RemoteAskGrain(mock.Anything, owner.GetHost(), int(owner.GetPort()), mock.Anything, mock.Anything, time.Second).
		Return(nil, errors.New("connection refused")).Once()

	resp, _, err := sys.sendToGrainOwner(ctx, newGrainIdentity(NewMockGrain(), "test"), owner, &testpb.TestReply{}, time.Second, grainAsk)
	require.Error(t, err)
	require.Nil(t, resp)
}

// TestAskAndTellGrain_RefusedOwnerReleasedAndMessageResent covers an owner that
// refused a message it did not run: AskGrain and TellGrain release its entry,
// claim the grain for this node with the entry's configuration, and deliver
// the message here in the same call, without consulting the membership.
func TestAskAndTellGrain_RefusedOwnerReleasedAndMessageResent(t *testing.T) {
	answers := []struct {
		name string
		err  error
	}{
		{"owner refused while shutting down", refusal.Mark(gerrors.ErrSystemShuttingDown)},
		{"owner has shut down", gerrors.ErrRemotingDisabled},
	}

	modes := []struct {
		name string
		mode grainContextMode
	}{
		{"ask", grainAsk},
		{"tell", grainTell},
		{"one-way tell", grainOneWay},
	}

	for _, answer := range answers {
		for _, tc := range modes {
			t.Run(answer.name+"/"+tc.name, func(t *testing.T) {
				ctx := t.Context()
				sys, cl, rem, identity := newActivationTestSystem(t, NewMockGrain(), "refused-owner-grain", true)

				staleOwner := internalpb.Grain_builder{
					GrainId:         internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind(), Name: identity.Name()}.Build(),
					Host:            "192.0.2.30",
					Port:            18030,
					MailboxCapacity: proto.Int64(16),
				}.Build()

				claim := proto.CloneOf(staleOwner)
				claim.SetHost(sys.Host())
				claim.SetPort(int32(sys.Port()))

				namesThisNodeWithTheEntryConfig := mock.MatchedBy(func(actual *internalpb.Grain) bool {
					return actual.GetGrainId().GetValue() == identity.String() &&
						sys.isLocalGrainOwner(actual) && actual.GetMailboxCapacity() == 16
				})

				cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(staleOwner, nil).Once()
				switch tc.mode {
				case grainAsk:
					rem.EXPECT().RemoteAskGrain(mock.Anything, staleOwner.GetHost(), int(staleOwner.GetPort()), mock.Anything, mock.Anything, time.Second).Return(nil, answer.err).Once()
				case grainTell:
					rem.EXPECT().RemoteTellGrain(mock.Anything, staleOwner.GetHost(), int(staleOwner.GetPort()), mock.Anything, mock.Anything).Return(answer.err).Once()
				default:
					rem.EXPECT().RemoteTellGrainOneWay(mock.Anything, staleOwner.GetHost(), int(staleOwner.GetPort()), mock.Anything, mock.Anything).Return(answer.err).Once()
				}

				cl.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(staleOwner.GetHost(), int(staleOwner.GetPort()))).Return(nil, nil).Once()
				// the claim for this node, then the owner resolution of the activation
				cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Once()
				cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
				cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(claim, nil).Once()
				// one put for the claim, one for the publication of the activated grain
				cl.EXPECT().PutGrain(mock.Anything, namesThisNodeWithTheEntryConfig).Return(nil).Twice()

				switch tc.mode {
				case grainAsk:
					reply, err := sys.AskGrain(ctx, identity, new(testpb.TestReply), time.Second)
					require.NoError(t, err)
					require.Equal(t, "received message", reply.(*testpb.Reply).GetContent())
				case grainTell:
					require.NoError(t, sys.TellGrain(ctx, identity, new(testpb.TestSend)))
				default:
					require.NoError(t, sys.TellGrain(ctx, identity, new(testpb.TestSend), WithOneWay()))
				}

				process, ok := sys.grains.Get(identity.String())
				require.True(t, ok)
				require.True(t, process.isActive())
				require.EqualValues(t, 16, process.config.capacity)
				// the owner answered for itself, so membership is not consulted
				cl.AssertNotCalled(t, "Members", mock.Anything)
			})
		}
	}
}

// TestAskGrain_OwnerFailureKeepsTheEntry covers failures that do not show the
// owner is gone: the error is returned and the entry is neither released nor
// claimed, since the mocks fail on any unexpected registry call.
func TestAskGrain_OwnerFailureKeepsTheEntry(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	ownerHost, ownerPort := "192.0.2.31", 18031
	member := []*cluster.Peer{{Host: ownerHost, RemotingPort: ownerPort}}

	cases := []struct {
		name    string
		ctx     context.Context
		err     error
		members []*cluster.Peer
		listErr error
	}{
		{name: "shutting down reported by a grain handler", ctx: context.Background(), err: gerrors.ErrSystemShuttingDown},
		{name: "grain gone", ctx: context.Background(), err: gerrors.ErrDead},
		{name: "mailbox full", ctx: context.Background(), err: gerrors.ErrMailboxFull},
		{name: "request timeout", ctx: context.Background(), err: gerrors.ErrRequestTimeout},
		{name: "caller canceled", ctx: canceled, err: context.Canceled},
		{name: "handler error", ctx: context.Background(), err: errors.New("handler failed")},
		{name: "transport failure while the owner is a member", ctx: context.Background(), err: gerrors.NewErrRemoteSendFailure(errors.New("connection refused")), members: member},
		{name: "transport failure while the membership is unknown", ctx: context.Background(), err: gerrors.NewErrRemoteSendFailure(errors.New("connection refused")), listErr: errors.New("membership unavailable")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sys, cl, rem, identity := newActivationTestSystem(t, NewMockGrain(), "kept-owner-grain", true)
			owner := internalpb.Grain_builder{
				GrainId: internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind(), Name: identity.Name()}.Build(),
				Host:    ownerHost,
				Port:    int32(ownerPort),
			}.Build()

			cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil).Once()
			rem.EXPECT().RemoteAskGrain(mock.Anything, ownerHost, ownerPort, mock.Anything, mock.Anything, time.Second).Return(nil, tc.err).Once()
			if tc.members != nil || tc.listErr != nil {
				cl.EXPECT().Members(mock.Anything).Return(tc.members, tc.listErr).Once()
			}

			reply, err := sys.AskGrain(tc.ctx, identity, new(testpb.TestReply), time.Second)
			require.ErrorIs(t, err, tc.err)
			require.Nil(t, reply)

			_, ok := sys.grains.Get(identity.String())
			require.False(t, ok)
		})
	}
}

// TestAskAndTellGrain_DepartedOwnerReleasedWithoutResend covers a transport
// failure towards an owner the membership confirms has left: the entry is
// released and the error returned, because the message may have run before
// the owner died. The next call reaches the grain on this node without
// GrainOf.
func TestAskAndTellGrain_DepartedOwnerReleasedWithoutResend(t *testing.T) {
	for _, mode := range []grainContextMode{grainAsk, grainTell} {
		t.Run(map[grainContextMode]string{grainAsk: "ask", grainTell: "tell"}[mode], func(t *testing.T) {
			ctx := t.Context()
			sys, cl, rem, identity := newActivationTestSystem(t, NewMockGrain(), "departed-owner-grain", true)
			departed := internalpb.Grain_builder{
				GrainId: internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind(), Name: identity.Name()}.Build(),
				Host:    "192.0.2.32",
				Port:    18032,
			}.Build()
			// the connection broke with the request in flight
			transportErr := &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}

			cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(departed, nil).Once()
			if mode == grainAsk {
				rem.EXPECT().RemoteAskGrain(mock.Anything, departed.GetHost(), int(departed.GetPort()), mock.Anything, mock.Anything, time.Second).Return(nil, transportErr).Once()
			} else {
				rem.EXPECT().RemoteTellGrain(mock.Anything, departed.GetHost(), int(departed.GetPort()), mock.Anything, mock.Anything).Return(transportErr).Once()
			}

			cl.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{{Host: sys.Host(), RemotingPort: sys.Port()}}, nil).Once()
			cl.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(departed.GetHost(), int(departed.GetPort()))).Return(nil, nil).Once()

			send := func() error {
				if mode == grainAsk {
					_, err := sys.AskGrain(ctx, identity, new(testpb.TestReply), time.Second)
					return err
				}
				return sys.TellGrain(ctx, identity, new(testpb.TestSend))
			}

			require.ErrorIs(t, send(), transportErr)
			_, ok := sys.grains.Get(identity.String())
			require.False(t, ok, "the message must not be sent again")

			// the entry is gone, so the next call activates the grain here
			cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(nil, cluster.ErrGrainNotFound).Once()
			cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Twice()
			cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(nil).Twice()

			require.NoError(t, send())
			process, ok := sys.grains.Get(identity.String())
			require.True(t, ok)
			require.True(t, process.isActive())
		})
	}
}

// TestAskAndTellGrain_DepartedOwnerNeverReachedIsResent covers an owner the
// membership confirms has left and that this node could not even connect to:
// the request never left this node, so the entry is released and the message
// is delivered on this node in the same call.
func TestAskAndTellGrain_DepartedOwnerNeverReachedIsResent(t *testing.T) {
	for _, mode := range []grainContextMode{grainAsk, grainTell} {
		t.Run(map[grainContextMode]string{grainAsk: "ask", grainTell: "tell"}[mode], func(t *testing.T) {
			ctx := t.Context()
			sys, cl, rem, identity := newActivationTestSystem(t, NewMockGrain(), "never-reached-owner-grain", true)
			departed := internalpb.Grain_builder{
				GrainId: internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind(), Name: identity.Name()}.Build(),
				Host:    "192.0.2.46",
				Port:    18046,
			}.Build()
			dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")}

			claim := proto.CloneOf(departed)
			claim.SetHost(sys.Host())
			claim.SetPort(int32(sys.Port()))

			cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(departed, nil).Once()
			if mode == grainAsk {
				rem.EXPECT().RemoteAskGrain(mock.Anything, departed.GetHost(), int(departed.GetPort()), mock.Anything, mock.Anything, time.Second).Return(nil, dialErr).Once()
			} else {
				rem.EXPECT().RemoteTellGrain(mock.Anything, departed.GetHost(), int(departed.GetPort()), mock.Anything, mock.Anything).Return(dialErr).Once()
			}

			cl.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{{Host: sys.Host(), RemotingPort: sys.Port()}}, nil).Once()
			cl.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(departed.GetHost(), int(departed.GetPort()))).Return(nil, nil).Once()
			cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Once()
			cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
			cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(claim, nil).Once()
			cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(nil).Twice()

			if mode == grainAsk {
				reply, err := sys.AskGrain(ctx, identity, new(testpb.TestReply), time.Second)
				require.NoError(t, err)
				require.Equal(t, "received message", reply.(*testpb.Reply).GetContent())
			} else {
				require.NoError(t, sys.TellGrain(ctx, identity, new(testpb.TestSend)))
			}

			process, ok := sys.grains.Get(identity.String())
			require.True(t, ok)
			require.True(t, process.isActive())
		})
	}
}

// TestAskGrain_UnreachableMemberNeverReachedIsNotResent covers an owner this
// node could not connect to while the membership still lists it: the owner may
// only be unreachable from here, so its entry is kept and the failure returned.
func TestAskGrain_UnreachableMemberNeverReachedIsNotResent(t *testing.T) {
	sys, cl, rem, identity := newActivationTestSystem(t, NewMockGrain(), "unreachable-member-grain", true)
	owner := internalpb.Grain_builder{
		GrainId: internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind(), Name: identity.Name()}.Build(),
		Host:    "192.0.2.47",
		Port:    18047,
	}.Build()
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")}

	cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil).Once()
	rem.EXPECT().RemoteAskGrain(mock.Anything, owner.GetHost(), int(owner.GetPort()), mock.Anything, mock.Anything, time.Second).Return(nil, dialErr).Once()
	cl.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{{Host: owner.GetHost(), RemotingPort: int(owner.GetPort())}}, nil).Once()

	reply, err := sys.AskGrain(t.Context(), identity, new(testpb.TestReply), time.Second)
	require.Nil(t, reply)
	require.ErrorIs(t, err, dialErr)

	_, ok := sys.grains.Get(identity.String())
	require.False(t, ok)
}

// TestIsDialFailure covers what counts as a request that never left this
// node: only a failure to connect, wrapped or not, and never a failure on an
// established connection.
func TestIsDialFailure(t *testing.T) {
	dial := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")}

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"dial failure", dial, true},
		{"wrapped dial failure", gerrors.NewErrRemoteSendFailure(dial), true},
		{"read failure", &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}, false},
		{"write failure", &net.OpError{Op: "write", Net: "tcp", Err: errors.New("broken pipe")}, false},
		{"send failure without a network error", gerrors.ErrRemoteSendFailure, false},
		{"end of stream", io.EOF, false},
		{"no error", nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isDialFailure(tc.err))
		})
	}
}

// TestAskGrain_RefusedOwnerNotResent covers a refusal after which this node
// does not send the message again: the release failed, or this node cannot
// host the grain.
func TestAskGrain_RefusedOwnerNotResent(t *testing.T) {
	ownerRefusal := refusal.Mark(gerrors.ErrSystemShuttingDown)

	cases := []struct {
		name       string
		register   bool
		role       *string
		releaseErr error
		wantErr    error
	}{
		{name: "the release fails", register: true, releaseErr: errors.New("quorum lost")},
		{name: "the grain kind is not registered here", register: false, wantErr: gerrors.ErrSystemShuttingDown},
		{name: "this node lacks the grain's role", register: true, role: proto.String("payments"), wantErr: gerrors.ErrSystemShuttingDown},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sys, cl, rem, identity := newActivationTestSystem(t, NewMockGrain(), "not-resent-grain", tc.register)
			owner := internalpb.Grain_builder{
				GrainId: internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind(), Name: identity.Name()}.Build(),
				Host:    "192.0.2.33",
				Port:    18033,
				Role:    tc.role,
			}.Build()

			cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil).Once()
			rem.EXPECT().RemoteAskGrain(mock.Anything, owner.GetHost(), int(owner.GetPort()), mock.Anything, mock.Anything, time.Second).Return(nil, ownerRefusal).Once()
			// a failing release is tried grainRegistryWriteAttempts times
			release := cl.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(owner.GetHost(), int(owner.GetPort()))).Return(nil, tc.releaseErr)
			if tc.releaseErr != nil {
				release.Times(grainRegistryWriteAttempts)
			} else {
				release.Once()
			}

			reply, err := sys.AskGrain(t.Context(), identity, new(testpb.TestReply), time.Second)
			require.Nil(t, reply)
			if tc.releaseErr != nil {
				require.ErrorIs(t, err, tc.releaseErr)
				require.ErrorContains(t, err, "failed to release registry entry")
				// the failure of the message itself is still reported
				require.ErrorIs(t, err, gerrors.ErrSystemShuttingDown)
			} else {
				require.ErrorIs(t, err, tc.wantErr)
			}

			require.False(t, refusal.Marked(err), "an error returned to application code carries no node refusal mark")

			_, ok := sys.grains.Get(identity.String())
			require.False(t, ok)
		})
	}
}

// TestGrainHandlerCannotPassOnANodeRefusal covers a grain whose handler asks
// another grain, is refused by that grain's owner, and reports the error as
// its own. The handler ran, so the remote caller of the first grain must not
// be told that the node refused its message: it would release the record of a
// live owner and run the message a second time.
func TestGrainHandlerCannotPassOnANodeRefusal(t *testing.T) {
	ctx := t.Context()
	sys, cl, rem, _ := newActivationTestSystem(t, NewMockGrain(), "unused", false)

	// the target's kind is not registered here, so this node cannot host it
	// and the refusal of its owner comes back from the nested ask
	target := newGrainIdentity(NewMockGrain(), "refusing-target")
	targetOwner := internalpb.Grain_builder{
		GrainId: internalpb.GrainId_builder{Value: target.String(), Kind: target.Kind(), Name: target.Name()}.Build(),
		Host:    "192.0.2.39",
		Port:    18039,
	}.Build()

	relay := &MockRelayGrain{target: target}
	relayID := newGrainIdentity(relay, "relay")
	process := newGrainPID(relayID, relay, sys, newGrainConfig())
	require.NoError(t, process.activate(ctx))
	sys.grains.Set(relayID.String(), process)

	cl.EXPECT().GetGrain(mock.Anything, target.String()).Return(targetOwner, nil).Once()
	rem.EXPECT().RemoteAskGrain(mock.Anything, targetOwner.GetHost(), int(targetOwner.GetPort()), mock.Anything, mock.Anything, time.Second).
		Return(nil, refusal.Mark(gerrors.ErrSystemShuttingDown)).Once()
	cl.EXPECT().ReleaseGrain(mock.Anything, target.String(), address.FormatHostPort(targetOwner.GetHost(), int(targetOwner.GetPort()))).Return(nil, nil).Once()

	// what the remote ask handler does for a message from another node
	_, err := sys.localSendGrain(ctx, relayID, new(testpb.TestReply), 5*time.Second, grainAsk)
	require.ErrorIs(t, err, gerrors.ErrSystemShuttingDown)
	require.False(t, refusal.Marked(err), "an error reported by a handler is not a node refusal")
	require.False(t, sys.grainSendError(relayID, sys.Host(), int32(sys.Port()), err).GetRefused())
}

// TestEnvelopeAsk_KeepsTheNodeRefusalMark covers an ask against a
// reentrancy-enabled grain whose reply comes back through the pending-asks
// table: a failure the node flagged as its own refusal is marked again for
// the waiting caller, and any other failure is not.
func TestEnvelopeAsk_KeepsTheNodeRefusalMark(t *testing.T) {
	for name, refused := range map[string]bool{"a node refusal": true, "another failure": false} {
		t.Run(name, func(t *testing.T) {
			sys, ctx := newReentrancySystem(t)
			system := sys.(*actorSystem)

			// the grain keeps the reply for itself, so the test answers the ask
			correlationIDs := make(chan string, 1)
			grain := &MockScriptedGrain{receive: func(gctx *GrainContext) {
				gctx.DeferResponse()
				correlationIDs <- gctx.CorrelationID()
			}}

			identity, err := system.GrainIdentity(ctx, "envelope-ask-refusal", func(context.Context) (Grain, error) {
				return grain, nil
			}, WithGrainReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))
			require.NoError(t, err)

			pid, ok := system.grains.Get(identity.String())
			require.True(t, ok)

			go func() {
				system.pendingAsks.Complete(&commands.AsyncResponse{
					CorrelationID: <-correlationIDs,
					Error:         gerrors.ErrSystemShuttingDown.Error(),
					Refused:       refused,
				})
			}()

			_, err = system.envelopeAsk(ctx, pid, new(testpb.TestReply), 5*time.Second)
			require.ErrorIs(t, err, gerrors.ErrSystemShuttingDown)
			require.Equal(t, refused, refusal.Marked(err))
		})
	}
}

// TestTellGrain_RefusalReturnedWithoutTheMark covers a refusal TellGrain
// returns because this node cannot host the grain: the error reaches
// application code without the internal mark, acknowledged or one-way.
func TestTellGrain_RefusalReturnedWithoutTheMark(t *testing.T) {
	for name, oneWay := range map[string]bool{"tell": false, "one-way tell": true} {
		t.Run(name, func(t *testing.T) {
			sys, cl, rem, identity := newActivationTestSystem(t, NewMockGrain(), "tell-not-hosted", false)
			owner := internalpb.Grain_builder{
				GrainId: internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind(), Name: identity.Name()}.Build(),
				Host:    "192.0.2.41",
				Port:    18041,
			}.Build()
			ownerRefusal := refusal.Mark(gerrors.ErrSystemShuttingDown)

			cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil).Once()
			cl.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(owner.GetHost(), int(owner.GetPort()))).Return(nil, nil).Once()

			var opts []TellGrainOption
			if oneWay {
				opts = append(opts, WithOneWay())
				rem.EXPECT().RemoteTellGrainOneWay(mock.Anything, owner.GetHost(), int(owner.GetPort()), mock.Anything, mock.Anything).Return(ownerRefusal).Once()
			} else {
				rem.EXPECT().RemoteTellGrain(mock.Anything, owner.GetHost(), int(owner.GetPort()), mock.Anything, mock.Anything).Return(ownerRefusal).Once()
			}

			err := sys.TellGrain(t.Context(), identity, new(testpb.TestSend), opts...)
			require.ErrorIs(t, err, gerrors.ErrSystemShuttingDown)
			require.False(t, refusal.Marked(err))
		})
	}
}

// TestGrainIdentity_RefusalReturnedWithoutTheMark covers an activation this
// node refuses because its shutdown began while the call was running:
// GrainIdentity returns the refusal without the internal mark.
func TestGrainIdentity_RefusalReturnedWithoutTheMark(t *testing.T) {
	grain := NewMockGrain()
	sys, cl, _, identity := newActivationTestSystem(t, grain, "identity-refused", true)

	// no record and no other member: this node claims the grain for itself
	cl.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{{Host: sys.Host(), PeersPort: 14000, RemotingPort: sys.Port()}}, nil).Once()
	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Twice()
	cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(nil).Once()
	cl.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(sys.Host(), sys.Port())).Return(nil, nil).Once()

	// the shutdown begins once the call is past its own started check
	got, err := sys.GrainIdentity(t.Context(), identity.Name(), func(context.Context) (Grain, error) {
		sys.shuttingDown.Store(true)
		return grain, nil
	}, WithActivationStrategy(LocalActivation))
	require.Nil(t, got)
	require.ErrorIs(t, err, gerrors.ErrSystemShuttingDown)
	require.False(t, refusal.Marked(err))
}

// TestAskGrain_RefusedOwnerClaimFails covers a refusal after which the claim
// for this node cannot be written: the failure of the claim is returned and
// the message is not sent again.
func TestAskGrain_RefusedOwnerClaimFails(t *testing.T) {
	sys, cl, rem, identity := newActivationTestSystem(t, NewMockGrain(), "claim-fails-grain", true)
	owner := internalpb.Grain_builder{
		GrainId: internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind(), Name: identity.Name()}.Build(),
		Host:    "192.0.2.42",
		Port:    18042,
	}.Build()
	claimErr := errors.New("registry unavailable")

	cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil).Once()
	rem.EXPECT().RemoteAskGrain(mock.Anything, owner.GetHost(), int(owner.GetPort()), mock.Anything, mock.Anything, time.Second).
		Return(nil, refusal.Mark(gerrors.ErrSystemShuttingDown)).Once()
	cl.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(owner.GetHost(), int(owner.GetPort()))).Return(nil, nil).Once()
	// a failing claim is tried grainRegistryWriteAttempts times
	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, claimErr).Times(grainRegistryWriteAttempts)

	reply, err := sys.AskGrain(t.Context(), identity, new(testpb.TestReply), time.Second)
	require.Nil(t, reply)
	require.ErrorIs(t, err, claimErr)

	_, ok := sys.grains.Get(identity.String())
	require.False(t, ok)
}

// TestAskGrain_RefusedOwnerActivationFailsAfterTheClaim covers a refusal after
// which this node claims the grain and then fails to activate it: the
// activation error is returned and the claim is kept, since it names a live
// node that activates the grain on the next message. The mocks fail on a
// release of this node's entry.
func TestAskGrain_RefusedOwnerActivationFailsAfterTheClaim(t *testing.T) {
	grain := NewMockActivationFailingGrain()
	sys, cl, rem, identity := newActivationTestSystem(t, grain, "activation-fails-after-claim", true)
	owner := internalpb.Grain_builder{
		GrainId:           internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind(), Name: identity.Name()}.Build(),
		Host:              "192.0.2.43",
		Port:              18043,
		ActivationTimeout: durationpb.New(time.Second),
	}.Build()

	claim := proto.CloneOf(owner)
	claim.SetHost(sys.Host())
	claim.SetPort(int32(sys.Port()))

	cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil).Once()
	rem.EXPECT().RemoteAskGrain(mock.Anything, owner.GetHost(), int(owner.GetPort()), mock.Anything, mock.Anything, time.Second).
		Return(nil, refusal.Mark(gerrors.ErrSystemShuttingDown)).Once()
	cl.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(owner.GetHost(), int(owner.GetPort()))).Return(nil, nil).Once()
	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Once()
	cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(nil).Once()
	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
	cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(claim, nil).Once()

	reply, err := sys.AskGrain(t.Context(), identity, new(testpb.TestReply), time.Second)
	require.Nil(t, reply)
	require.ErrorIs(t, err, gerrors.ErrGrainActivationFailure)

	_, ok := sys.grains.Get(identity.String())
	require.False(t, ok)
}

// TestAskGrain_ReleaseRetriedWhileTheOwnerLeaves covers the registry failing
// the release of a refused owner's entry while that owner leaves the cluster:
// the release is tried again, and once it succeeds the message is delivered on
// this node in the same call.
func TestAskGrain_ReleaseRetriedWhileTheOwnerLeaves(t *testing.T) {
	sys, cl, rem, identity := newActivationTestSystem(t, NewMockGrain(), "release-retried-grain", true)
	owner := internalpb.Grain_builder{
		GrainId: internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind(), Name: identity.Name()}.Build(),
		Host:    "192.0.2.44",
		Port:    18044,
	}.Build()
	ownerNode := address.FormatHostPort(owner.GetHost(), int(owner.GetPort()))

	claim := proto.CloneOf(owner)
	claim.SetHost(sys.Host())
	claim.SetPort(int32(sys.Port()))

	cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil).Once()
	rem.EXPECT().RemoteAskGrain(mock.Anything, owner.GetHost(), int(owner.GetPort()), mock.Anything, mock.Anything, 5*time.Second).
		Return(nil, refusal.Mark(gerrors.ErrSystemShuttingDown)).Once()
	// the leaving node fails the first release, the second one goes through
	cl.EXPECT().ReleaseGrain(mock.Anything, identity.String(), ownerNode).Return(nil, errors.New("context canceled")).Once()
	cl.EXPECT().ReleaseGrain(mock.Anything, identity.String(), ownerNode).Return(nil, nil).Once()
	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Once()
	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
	cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(claim, nil).Once()
	cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(nil).Twice()

	reply, err := sys.AskGrain(t.Context(), identity, new(testpb.TestReply), 5*time.Second)
	require.NoError(t, err)
	require.Equal(t, "received message", reply.(*testpb.Reply).GetContent())
}

// TestTryClaimGrain_OwnerLookupFails covers a claim that finds the grain
// already claimed and then cannot read who owns it: the failure of that read
// is returned.
func TestTryClaimGrain_OwnerLookupFails(t *testing.T) {
	sys, cl, _, identity := newActivationTestSystem(t, NewMockGrain(), "claim-owner-lookup-fails", true)
	record, err := wireGrain(identity, newGrainConfig(), sys.Host(), sys.Port())
	require.NoError(t, err)
	lookupErr := errors.New("registry unavailable")

	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
	cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(nil, lookupErr).Once()

	claimed, owner, err := sys.tryClaimGrain(t.Context(), record)
	require.ErrorIs(t, err, lookupErr)
	require.False(t, claimed)
	require.Nil(t, owner)
}

// TestRetryGrainRegistryWrite covers the bounded retry of a write to the grain
// registry: a write the registry fails is tried again, except when the grain
// is already claimed, when this node is stopping or when the caller gave up.
func TestRetryGrainRegistryWrite(t *testing.T) {
	transient := errors.New("context canceled")
	node := &discovery.Node{Host: "127.0.0.1", PeersPort: 14002, RemotingPort: 15002}

	// failingWrite returns a write that fails with err on its first failures
	// calls, and the number of calls it received.
	failingWrite := func(failures int, err error) (func(context.Context) error, *int) {
		calls := 0
		return func(context.Context) error {
			calls++
			if calls <= failures {
				return err
			}

			return nil
		}, &calls
	}

	t.Run("a write that succeeds runs once", func(t *testing.T) {
		sys := newClusterReadySystem(mockremote.NewClient(t), mockcluster.NewCluster(t), node)
		write, calls := failingWrite(0, transient)

		require.NoError(t, sys.retryGrainRegistryWrite(t.Context(), write))
		require.Equal(t, 1, *calls)
	})

	t.Run("a write the registry fails is tried again until it succeeds", func(t *testing.T) {
		sys := newClusterReadySystem(mockremote.NewClient(t), mockcluster.NewCluster(t), node)
		write, calls := failingWrite(grainRegistryWriteAttempts-1, transient)

		require.NoError(t, sys.retryGrainRegistryWrite(t.Context(), write))
		require.Equal(t, grainRegistryWriteAttempts, *calls)
	})

	t.Run("a write that keeps failing returns its error after the last attempt", func(t *testing.T) {
		sys := newClusterReadySystem(mockremote.NewClient(t), mockcluster.NewCluster(t), node)
		write, calls := failingWrite(grainRegistryWriteAttempts, transient)

		require.ErrorIs(t, sys.retryGrainRegistryWrite(t.Context(), write), transient)
		require.Equal(t, grainRegistryWriteAttempts, *calls)
	})

	t.Run("a grain that is already claimed is not tried again", func(t *testing.T) {
		sys := newClusterReadySystem(mockremote.NewClient(t), mockcluster.NewCluster(t), node)
		write, calls := failingWrite(grainRegistryWriteAttempts, cluster.ErrGrainAlreadyExists)

		require.ErrorIs(t, sys.retryGrainRegistryWrite(t.Context(), write), cluster.ErrGrainAlreadyExists)
		require.Equal(t, 1, *calls)
	})

	t.Run("a stopping node does not try again", func(t *testing.T) {
		sys := newClusterReadySystem(mockremote.NewClient(t), mockcluster.NewCluster(t), node)
		sys.shuttingDown.Store(true)
		write, calls := failingWrite(grainRegistryWriteAttempts, transient)

		require.ErrorIs(t, sys.retryGrainRegistryWrite(t.Context(), write), transient)
		require.Equal(t, 1, *calls)
	})

	t.Run("a caller that gave up gets the error of the write", func(t *testing.T) {
		sys := newClusterReadySystem(mockremote.NewClient(t), mockcluster.NewCluster(t), node)
		ctx, cancel := context.WithCancel(t.Context())
		calls := 0

		err := sys.retryGrainRegistryWrite(ctx, func(context.Context) error {
			calls++
			cancel()
			return transient
		})
		require.ErrorIs(t, err, transient)
		require.Equal(t, 1, calls)
	})
}

// TestAskGrain_ClaimAndPublicationRetriedWhileTheOwnerLeaves covers the
// registry failing the claim and the publication of a grain this node takes
// over from a refused owner, while that owner leaves the cluster: both writes
// are tried again and the message is delivered in the same call.
func TestAskGrain_ClaimAndPublicationRetriedWhileTheOwnerLeaves(t *testing.T) {
	sys, cl, rem, identity := newActivationTestSystem(t, NewMockGrain(), "writes-retried-grain", true)
	owner := internalpb.Grain_builder{
		GrainId: internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind(), Name: identity.Name()}.Build(),
		Host:    "192.0.2.45",
		Port:    18045,
	}.Build()
	transient := errors.New("context canceled")

	claim := proto.CloneOf(owner)
	claim.SetHost(sys.Host())
	claim.SetPort(int32(sys.Port()))

	cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil).Once()
	rem.EXPECT().RemoteAskGrain(mock.Anything, owner.GetHost(), int(owner.GetPort()), mock.Anything, mock.Anything, 5*time.Second).
		Return(nil, refusal.Mark(gerrors.ErrSystemShuttingDown)).Once()
	cl.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(owner.GetHost(), int(owner.GetPort()))).Return(nil, nil).Once()
	// the claim: the leaving node fails the first write, the second goes through
	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Twice()
	cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(transient).Once()
	cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(nil).Once()
	// the activation finds the claim, then its publication fails once too
	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
	cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(claim, nil).Once()
	cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(transient).Once()
	cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(nil).Once()

	reply, err := sys.AskGrain(t.Context(), identity, new(testpb.TestReply), 5*time.Second)
	require.NoError(t, err)
	require.Equal(t, "received message", reply.(*testpb.Reply).GetContent())

	process, ok := sys.grains.Get(identity.String())
	require.True(t, ok)
	require.True(t, process.isActive())
}

// TestAskGrain_RefusedOwnerClaimLost covers a refusal after which another node
// wins the claim: the message is forwarded to the winner. When the winner
// refuses too, its entry is released and the refusal returned, with no second
// resend.
func TestAskGrain_RefusedOwnerClaimLost(t *testing.T) {
	ownerRefusal := refusal.Mark(gerrors.ErrSystemShuttingDown)

	cases := []struct {
		name        string
		winnerReply any
		winnerErr   error
	}{
		{name: "the winner answers", winnerReply: testpb.Reply_builder{Content: "from the winner"}.Build()},
		{name: "the winner refuses too", winnerErr: ownerRefusal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sys, cl, rem, identity := newActivationTestSystem(t, NewMockGrain(), "claim-lost-grain", true)
			grainID := internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind(), Name: identity.Name()}.Build()
			staleOwner := internalpb.Grain_builder{GrainId: grainID, Host: "192.0.2.34", Port: 18034}.Build()
			winner := internalpb.Grain_builder{GrainId: grainID, Host: "192.0.2.35", Port: 18035}.Build()

			cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(staleOwner, nil).Once()
			rem.EXPECT().RemoteAskGrain(mock.Anything, staleOwner.GetHost(), int(staleOwner.GetPort()), mock.Anything, mock.Anything, time.Second).Return(nil, ownerRefusal).Once()
			cl.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(staleOwner.GetHost(), int(staleOwner.GetPort()))).Return(nil, nil).Once()
			// the claim finds the winner's entry, and so does the activation
			cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Twice()
			cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(winner, nil).Twice()
			rem.EXPECT().RemoteAskGrain(mock.Anything, winner.GetHost(), int(winner.GetPort()), mock.Anything, mock.Anything, mock.Anything).Return(tc.winnerReply, tc.winnerErr).Once()
			if tc.winnerErr != nil {
				cl.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(winner.GetHost(), int(winner.GetPort()))).Return(nil, nil).Once()
			}

			reply, err := sys.AskGrain(t.Context(), identity, new(testpb.TestReply), time.Second)
			if tc.winnerErr != nil {
				require.ErrorIs(t, err, gerrors.ErrSystemShuttingDown)
				require.Nil(t, reply)
			} else {
				require.NoError(t, err)
				require.Equal(t, "from the winner", reply.(*testpb.Reply).GetContent())
			}

			_, ok := sys.grains.Get(identity.String())
			require.False(t, ok)
		})
	}
}

// TestAskGrain_ResendUsesTheRemainingTimeout checks that the resend after a
// refusal waits only for what is left of the caller's timeout, not for a
// fresh one.
func TestAskGrain_ResendUsesTheRemainingTimeout(t *testing.T) {
	const (
		timeout     = 600 * time.Millisecond
		ownerAnswer = 300 * time.Millisecond
	)

	sys, cl, rem, identity := newActivationTestSystem(t, NewMockGrain(), "remaining-timeout-grain", true)
	staleOwner := internalpb.Grain_builder{
		GrainId: internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind(), Name: identity.Name()}.Build(),
		Host:    "192.0.2.36",
		Port:    18036,
	}.Build()

	claim := proto.CloneOf(staleOwner)
	claim.SetHost(sys.Host())
	claim.SetPort(int32(sys.Port()))

	cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(staleOwner, nil).Once()
	rem.EXPECT().RemoteAskGrain(mock.Anything, staleOwner.GetHost(), int(staleOwner.GetPort()), mock.Anything, mock.Anything, timeout).
		Run(func(context.Context, string, int, *remote.GrainRequest, any, time.Duration) { pause.For(ownerAnswer) }).
		Return(nil, refusal.Mark(gerrors.ErrSystemShuttingDown)).Once()
	cl.EXPECT().ReleaseGrain(mock.Anything, identity.String(), mock.Anything).Return(nil, nil).Once()
	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Once()
	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
	cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(claim, nil).Once()
	cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(nil).Twice()

	// the handler of TestTimeout outlasts any timeout, so the resend times out
	start := time.Now()
	_, err := sys.AskGrain(t.Context(), identity, new(testpb.TestTimeout), timeout)
	elapsed := time.Since(start)

	require.ErrorIs(t, err, gerrors.ErrRequestTimeout)
	require.Less(t, elapsed, timeout+ownerAnswer, "the resend must not wait for a fresh timeout")
}

// TestLocalSendGrain_ForwardReleasesWithoutResend covers the forward to a
// remote owner found while activating: a refusal releases the owner's entry
// and is returned, and the message is not sent again from here.
func TestLocalSendGrain_ForwardReleasesWithoutResend(t *testing.T) {
	sys, cl, rem, identity := newActivationTestSystem(t, NewMockGrain(), "forward-refused-grain", true)
	owner := internalpb.Grain_builder{
		GrainId: internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind(), Name: identity.Name()}.Build(),
		Host:    "192.0.2.37",
		Port:    18037,
	}.Build()

	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
	cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil).Once()
	rem.EXPECT().RemoteTellGrain(mock.Anything, owner.GetHost(), int(owner.GetPort()), mock.Anything, mock.Anything).
		Return(refusal.Mark(gerrors.ErrSystemShuttingDown)).Once()
	cl.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(owner.GetHost(), int(owner.GetPort()))).Return(nil, nil).Once()

	_, err := sys.localSendGrain(t.Context(), identity, new(testpb.TestSend), time.Second, grainTell)
	require.ErrorIs(t, err, gerrors.ErrSystemShuttingDown)
	// the message did not run, so a node that forwarded it for a peer tells
	// that peer so
	require.True(t, refusal.Marked(err))
	require.True(t, sys.grainSendError(identity, sys.Host(), int32(sys.Port()), err).GetRefused())

	_, ok := sys.grains.Get(identity.String())
	require.False(t, ok)
}

// TestDeliverAsyncEnvelope_RefusedOwnerReleasedWithoutResend covers an
// envelope forwarded to a remote owner that refuses it: the owner's entry is
// released and the refusal returned, and the envelope is not sent again.
func TestDeliverAsyncEnvelope_RefusedOwnerReleasedWithoutResend(t *testing.T) {
	sys, cl, rem, identity := newActivationTestSystem(t, NewMockGrain(), "envelope-refused-grain", true)
	owner := internalpb.Grain_builder{
		GrainId: internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind(), Name: identity.Name()}.Build(),
		Host:    "192.0.2.38",
		Port:    18038,
	}.Build()

	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
	cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil).Once()
	rem.EXPECT().RemoteTellGrain(mock.Anything, owner.GetHost(), int(owner.GetPort()), mock.Anything, mock.Anything).
		Return(refusal.Mark(gerrors.ErrSystemShuttingDown)).Once()
	cl.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(owner.GetHost(), int(owner.GetPort()))).Return(nil, nil).Once()

	err := sys.deliverAsyncEnvelope(t.Context(), identity, &commands.AsyncRequest{CorrelationID: "envelope-refused"})
	require.ErrorIs(t, err, gerrors.ErrSystemShuttingDown)

	_, ok := sys.grains.Get(identity.String())
	require.False(t, ok)
}

func TestSetupGrainActivationBarrier(t *testing.T) {
	ctx := t.Context()
	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewClient(t)
	node := &discovery.Node{Host: "127.0.0.1", PeersPort: 14001, RemotingPort: 8081}

	t.Run("no-op when cluster disabled", func(t *testing.T) {
		sys := newClusterReadySystem(rem, cl, node)
		sys.clusterEnabled.Store(false)
		sys.setupGrainActivationBarrier(ctx)
		require.Nil(t, sys.grainBarrier)
	})

	t.Run("no-op when clusterConfig nil", func(t *testing.T) {
		sys := newClusterReadySystem(rem, cl, node)
		sys.clusterConfig = nil
		sys.setupGrainActivationBarrier(ctx)
		require.Nil(t, sys.grainBarrier)
	})

	t.Run("no-op when barrier disabled", func(t *testing.T) {
		sys := newClusterReadySystem(rem, cl, node)
		sys.clusterConfig = NewClusterConfig()
		sys.setupGrainActivationBarrier(ctx)
		require.Nil(t, sys.grainBarrier)
	})

	t.Run("opens immediately when minPeers <= 1", func(t *testing.T) {
		sys := newClusterReadySystem(rem, cl, node)
		sys.clusterConfig = NewClusterConfig().
			WithMinimumPeersQuorum(1).
			WithGrainActivationBarrier(5 * time.Second)
		sys.setupGrainActivationBarrier(ctx)
		require.NotNil(t, sys.grainBarrier)
		err := sys.waitForGrainActivationBarrier(ctx)
		require.NoError(t, err)
	})

	t.Run("calls tryOpenGrainActivationBarrier when minPeers > 1", func(t *testing.T) {
		sys := newClusterReadySystem(rem, cl, node)
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
		sys := newClusterReadySystem(rem, cl, node)
		sys.grainBarrier = nil
		sys.tryOpenGrainActivationBarrier(ctx)
	})

	t.Run("no-op when cluster nil", func(t *testing.T) {
		sys := newClusterReadySystem(rem, cl, node)
		sys.clusterConfig = NewClusterConfig().WithMinimumPeersQuorum(2).WithGrainActivationBarrier(5 * time.Second)
		sys.grainBarrier = newGrainActivationBarrier(2, 5*time.Second)
		sys.cluster = nil
		sys.tryOpenGrainActivationBarrier(ctx)
	})

	t.Run("opens when Members returns enough peers", func(t *testing.T) {
		sys := newClusterReadySystem(rem, cl, node)
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
		sys := newClusterReadySystem(rem, cl, node)
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
		sys := newClusterReadySystem(rem, cl, node)
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
		sys := newClusterReadySystem(rem, cl, node)
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

		propagator := &MockHeaderPropagator{headerKey: headerKey, ctxKey: ctxKey}
		grain := &MockContextEchoGrain{key: ctxKey}

		node1, sd1 := startNATsSystem(t, srv.Addr().String(),
			withTestContextPropagator(propagator),
			withTestExtraGrains(grain))
		node2, sd2 := startNATsSystem(t, srv.Addr().String(),
			withTestContextPropagator(propagator),
			withTestExtraGrains(&MockContextEchoGrain{key: ctxKey}))

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

		propagator := &MockHeaderPropagator{headerKey: headerKey, ctxKey: ctxKey}
		grain := &MockContextEchoGrain{key: ctxKey}

		node1, sd1 := startNATsSystem(t, srv.Addr().String(),
			withTestContextPropagator(propagator),
			withTestExtraGrains(grain))
		node2, sd2 := startNATsSystem(t, srv.Addr().String(),
			withTestContextPropagator(propagator),
			withTestExtraGrains(&MockContextEchoGrain{key: ctxKey}))

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

	t.Run("one-way TellGrain propagates context across nodes", func(t *testing.T) {
		ctx := context.Background()
		srv := startNatsServer(t)

		propagator := &MockHeaderPropagator{headerKey: headerKey, ctxKey: ctxKey}
		grain := &MockContextEchoGrain{key: ctxKey}

		node1, sd1 := startNATsSystem(t, srv.Addr().String(),
			withTestContextPropagator(propagator),
			withTestExtraGrains(grain))
		node2, sd2 := startNATsSystem(t, srv.Addr().String(),
			withTestContextPropagator(propagator),
			withTestExtraGrains(&MockContextEchoGrain{key: ctxKey}))

		defer func() {
			assert.NoError(t, node2.Stop(ctx))
			assert.NoError(t, node1.Stop(ctx))
			sd2.Close()
			sd1.Close()
			srv.Shutdown()
		}()

		pause.For(time.Second)

		// Activate grain on node1
		identity, err := node1.GrainIdentity(ctx, "tell-one-way-ctx-grain", func(_ context.Context) (Grain, error) {
			return grain, nil
		})
		require.NoError(t, err)

		// First call from node1 to activate the grain
		_, err = node1.AskGrain(ctx, identity, new(testpb.TestReply), time.Second)
		require.NoError(t, err)
		pause.For(time.Second)

		// A one-way send from node2 rides the one-way remote call to node1,
		// where the handler enqueues it with the propagated context value.
		headerVal := "cross-node-one-way-value"
		propagatedCtx := context.WithValue(ctx, ctxKey, headerVal)
		err = node2.TellGrain(propagatedCtx, identity, new(testpb.TestSend), WithOneWay())
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			return grain.Seen() == headerVal
		}, 2*time.Second, 50*time.Millisecond)
	})
}

// TestGrainActivationDuringShutdownNoRace covers the shutdown race originally
// reported in issue https://github.com/Tochemey/goakt/issues/1205: in-flight
// grain publications overlapping actorSystem shutdown. Several goroutines keep
// publishing grains to the cluster right up to and during Stop(), which must
// neither race shutdown state nor panic; publications after the cluster is
// torn down simply fail or no-op. Must be run under -race.
func TestGrainActivationDuringShutdownNoRace(t *testing.T) {
	ctx := context.TODO()
	nodePorts := internalnet.Get(3)
	gossipPort := nodePorts[0]
	clusterPort := nodePorts[1]
	remotingPort := nodePorts[2]
	host := "127.0.0.1"

	addrs := []string{net.JoinHostPort(host, strconv.Itoa(gossipPort))}

	provider := new(mockdiscovery.Provider)
	system, err := NewActorSystem(
		"test",
		WithLogger(log.DiscardLogger),
		WithRemote(remote.NewConfig(host, remotingPort)),
		WithCluster(
			NewClusterConfig().
				WithKinds(new(MockActor)).
				WithGrains(new(MockGrain)).
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

	require.NoError(t, system.Start(ctx))
	pause.For(time.Second)

	sys := system.(*actorSystem)

	// Several publishers keep calling putGrainOnCluster so the synchronous
	// cluster writes overlap the shutdown state transitions during Stop.
	// Each goroutine owns its grain/identity to avoid unrelated shared state.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := range 8 {
		grain := NewMockGrain()
		pid := newGrainPID(newGrainIdentity(grain, "race-grain-"+strconv.Itoa(i)), grain, sys, newGrainConfig())
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					_ = sys.putGrainOnCluster(ctx, pid)
				}
			}
		})
	}

	require.NoError(t, system.Stop(ctx))
	close(stop)
	wg.Wait()

	provider.AssertExpectations(t)
}

// TestRemoteTellGrainLocalShortCircuit verifies that a Tell to a grain already
// activated on this node delivers in-process without the per-send cluster
// registry lookup. No GetGrain expectation is set, so any cluster lookup fails
// the mock.
func TestRemoteTellGrainLocalShortCircuit(t *testing.T) {
	grain := NewMockGrain()
	sys, cl, _, identity := newActivationTestSystem(t, grain, "local-fast-tell", true)
	pid := seedInactiveGrainPID(sys, identity, grain, newGrainConfig())
	pid.activated.Store(true)

	err := sys.remoteTellGrain(context.Background(), identity, new(testpb.TestSend), time.Second, grainTell)
	require.NoError(t, err)
	cl.AssertNotCalled(t, "GetGrain", mock.Anything, mock.Anything)
}

// TestRemoteTellGrainOneWayLocalShortCircuit is the one-way counterpart of
// TestRemoteTellGrainLocalShortCircuit.
func TestRemoteTellGrainOneWayLocalShortCircuit(t *testing.T) {
	grain := NewMockGrain()
	sys, cl, _, identity := newActivationTestSystem(t, grain, "local-fast-one-way-tell", true)
	pid := seedInactiveGrainPID(sys, identity, grain, newGrainConfig())
	pid.activated.Store(true)

	err := sys.remoteTellGrain(context.Background(), identity, new(testpb.TestSend), time.Second, grainOneWay)
	require.NoError(t, err)
	cl.AssertNotCalled(t, "GetGrain", mock.Anything, mock.Anything)
}

// TestRemoteAskGrainLocalShortCircuit is the Ask counterpart of
// TestRemoteTellGrainLocalShortCircuit.
func TestRemoteAskGrainLocalShortCircuit(t *testing.T) {
	grain := NewMockGrain()
	sys, cl, _, identity := newActivationTestSystem(t, grain, "local-fast-ask", true)
	pid := seedInactiveGrainPID(sys, identity, grain, newGrainConfig())
	pid.activated.Store(true)

	resp, err := sys.remoteAskGrain(context.Background(), identity, new(testpb.TestReply), time.Second)
	require.NoError(t, err)
	require.NotNil(t, resp)
	cl.AssertNotCalled(t, "GetGrain", mock.Anything, mock.Anything)
}

// BenchmarkTellGrainNodeLocal measures node-local TellGrain throughput, where
// the grain is owned by the calling node and the cluster lookup is skipped.
func BenchmarkTellGrainNodeLocal(b *testing.B) {
	system, identity := benchmarkNodeLocalGrainSystem(b)
	ctx := context.TODO()
	msg := new(testpb.TestSend)

	b.ReportAllocs()
	for b.Loop() {
		if err := system.TellGrain(ctx, identity, msg); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAskGrainNodeLocal is the Ask counterpart of BenchmarkTellGrainNodeLocal.
func BenchmarkAskGrainNodeLocal(b *testing.B) {
	system, identity := benchmarkNodeLocalGrainSystem(b)
	ctx := context.TODO()
	msg := new(testpb.TestReply)

	b.ReportAllocs()
	for b.Loop() {
		if _, err := system.AskGrain(ctx, identity, msg, time.Second); err != nil {
			b.Fatal(err)
		}
	}
}

func TestWireGrainDisableRelocation(t *testing.T) {
	grain := NewMockGrain()
	sys, _, _, identity := newActivationTestSystem(t, grain, "claim-wire", true)

	wire, err := wireGrain(identity, newGrainConfig(WithGrainMailboxCapacity(16)), sys.Host(), sys.Port())
	require.NoError(t, err)
	require.Equal(t, identity.String(), wire.GetGrainId().GetValue())
	require.Equal(t, sys.Host(), wire.GetHost())
	require.EqualValues(t, sys.Port(), wire.GetPort())
	require.EqualValues(t, 16, wire.GetMailboxCapacity())
	require.False(t, wire.GetDisableRelocation())

	wire, err = wireGrain(identity, newGrainConfig(WithGrainDisableRelocation()), sys.Host(), sys.Port())
	require.NoError(t, err)
	require.True(t, wire.GetDisableRelocation())
}

func TestRecreateGrainPreservesDisableRelocation(t *testing.T) {
	ctx := t.Context()
	sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	t.Cleanup(func() { _ = sys.Stop(ctx) })

	as := sys.(*actorSystem)
	as.registry.Register(&MockGrain{})

	identity := newGrainIdentity(&MockGrain{}, "recreate-disable-relocation")
	wire, err := wireGrain(identity, newGrainConfig(WithGrainDisableRelocation()), as.Host(), as.Port())
	require.NoError(t, err)
	require.True(t, wire.GetDisableRelocation())

	require.NoError(t, as.recreateGrain(ctx, wire))

	// the rebuilt config carries the flag, so the republished wire record keeps it
	pid, ok := as.grains.Get(identity.String())
	require.True(t, ok)
	require.True(t, pid.config.disableRelocation)

	republished, err := pid.toWireGrain()
	require.NoError(t, err)
	require.True(t, republished.GetDisableRelocation())
}

func TestWireGrainEagerRelocation(t *testing.T) {
	grain := NewMockGrain()
	sys, _, _, identity := newActivationTestSystem(t, grain, "claim-wire-eager", true)

	// default is lazy
	wire, err := wireGrain(identity, newGrainConfig(), sys.Host(), sys.Port())
	require.NoError(t, err)
	require.False(t, wire.GetEagerRelocation())

	wire, err = wireGrain(identity, newGrainConfig(WithGrainEagerRelocation()), sys.Host(), sys.Port())
	require.NoError(t, err)
	require.True(t, wire.GetEagerRelocation())
}

func TestRecreateGrainPreservesEagerRelocation(t *testing.T) {
	ctx := t.Context()
	sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	t.Cleanup(func() { _ = sys.Stop(ctx) })

	as := sys.(*actorSystem)
	as.registry.Register(&MockGrain{})

	identity := newGrainIdentity(&MockGrain{}, "recreate-eager-relocation")
	wire, err := wireGrain(identity, newGrainConfig(WithGrainEagerRelocation()), as.Host(), as.Port())
	require.NoError(t, err)
	require.True(t, wire.GetEagerRelocation())

	require.NoError(t, as.recreateGrain(ctx, wire))

	pid, ok := as.grains.Get(identity.String())
	require.True(t, ok)
	require.True(t, pid.config.eagerRelocation)

	republished, err := pid.toWireGrain()
	require.NoError(t, err)
	require.True(t, republished.GetEagerRelocation())
}

func TestReleaseGrainForLazyRelocation(t *testing.T) {
	ctx := context.Background()
	departedNode := net.JoinHostPort("127.0.0.9", "16000")

	grainOnDeparted := func(identity *GrainIdentity) *internalpb.Grain {
		return internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{
				Kind:  identity.Kind(),
				Name:  identity.Name(),
				Value: identity.String(),
			}.Build(),
			Host: "127.0.0.9",
			Port: 16000,
		}.Build()
	}

	t.Run("removes the directory entry when it still points at the departed node", func(t *testing.T) {
		sys, cl, _, identity := newActivationTestSystem(t, &MockGrain{}, "lazy-release-hit", false)
		wire := grainOnDeparted(identity)

		cl.EXPECT().ReleaseGrain(ctx, identity.String(), departedNode).Return(nil, nil).Once()

		require.NoError(t, sys.releaseGrainForLazyRelocation(ctx, wire, departedNode))
	})

	t.Run("leaves an entry already re-owned elsewhere untouched", func(t *testing.T) {
		sys, cl, _, identity := newActivationTestSystem(t, &MockGrain{}, "lazy-release-moved", false)
		wire := grainOnDeparted(identity)

		// the live entry now points at a surviving node
		existing := internalpb.Grain_builder{
			GrainId: wire.GetGrainId(),
			Host:    "127.0.0.8",
			Port:    16001,
		}.Build()
		cl.EXPECT().ReleaseGrain(ctx, identity.String(), departedNode).Return(existing, nil).Once()

		require.NoError(t, sys.releaseGrainForLazyRelocation(ctx, wire, departedNode))
	})

	t.Run("reports a failed release", func(t *testing.T) {
		sys, cl, _, identity := newActivationTestSystem(t, &MockGrain{}, "lazy-release-failed", false)
		wire := grainOnDeparted(identity)
		storeErr := errors.New("store down")

		cl.EXPECT().ReleaseGrain(ctx, identity.String(), departedNode).Return(nil, storeErr).Once()

		require.ErrorIs(t, sys.releaseGrainForLazyRelocation(ctx, wire, departedNode), storeErr)
	})

	t.Run("skips system grains without touching the cluster", func(t *testing.T) {
		sys, _, _, _ := newActivationTestSystem(t, &MockGrain{}, "lazy-release-system", false)
		wire := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{Kind: "k", Name: reservedNames[deathWatchType], Value: "k/" + reservedNames[deathWatchType]}.Build(),
			Host:    "127.0.0.9",
			Port:    16000,
		}.Build()

		// no ReleaseGrain expectation: the method must return early
		require.NoError(t, sys.releaseGrainForLazyRelocation(ctx, wire, departedNode))
	})

	t.Run("releases relocation-disabled grains like any other", func(t *testing.T) {
		sys, cl, _, identity := newActivationTestSystem(t, &MockGrain{}, "lazy-release-disabled", false)
		wire := grainOnDeparted(identity)
		wire.SetDisableRelocation(true)

		// the grain is lost with its node, but its entry must not outlive it
		cl.EXPECT().ReleaseGrain(ctx, identity.String(), departedNode).Return(nil, nil).Once()

		require.NoError(t, sys.releaseGrainForLazyRelocation(ctx, wire, departedNode))
	})
}

func TestRecreateGrainFromWire(t *testing.T) {
	ctx := context.Background()
	departedNode := address.FormatHostPort("127.0.0.9", 16000)

	t.Run("skips system grains without touching the cluster", func(t *testing.T) {
		sys, _, _, _ := newActivationTestSystem(t, NewMockGrain(), "recreate-wire-system", true)
		wire := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{Kind: "k", Name: reservedNames[deathWatchType], Value: "k/" + reservedNames[deathWatchType]}.Build(),
			Host:    "127.0.0.9",
			Port:    16000,
		}.Build()

		// no ReleaseGrain expectation: the method must return early
		require.NoError(t, sys.recreateGrainFromWire(ctx, wire, departedNode))
	})

	t.Run("releases relocation-disabled grains instead of recreating them", func(t *testing.T) {
		sys, cl, _, identity := newActivationTestSystem(t, NewMockGrain(), "recreate-wire-disabled", true)
		wire, err := wireGrain(identity, newGrainConfig(WithGrainDisableRelocation()), "127.0.0.9", 16000)
		require.NoError(t, err)

		// the entry is released and nothing else touches the cluster, so a
		// recreation would fail the test as an unexpected call
		cl.EXPECT().ReleaseGrain(ctx, identity.String(), departedNode).Return(nil, nil).Once()

		require.NoError(t, sys.recreateGrainFromWire(ctx, wire, departedNode))

		_, active := sys.grains.Get(identity.String())
		assert.False(t, active, "a relocation-disabled grain must not be recreated")
	})

	t.Run("reports a failed release", func(t *testing.T) {
		sys, cl, _, identity := newActivationTestSystem(t, NewMockGrain(), "recreate-wire-release-error", true)
		wire, err := wireGrain(identity, newGrainConfig(), "127.0.0.9", 16000)
		require.NoError(t, err)
		storeErr := errors.New("store down")

		cl.EXPECT().ReleaseGrain(ctx, identity.String(), departedNode).Return(nil, storeErr).Once()

		require.ErrorIs(t, sys.recreateGrainFromWire(ctx, wire, departedNode), storeErr)
		_, ok := sys.grains.Get(identity.String())
		require.False(t, ok)
	})

	t.Run("leaves a grain re-owned elsewhere alone", func(t *testing.T) {
		sys, cl, _, identity := newActivationTestSystem(t, NewMockGrain(), "recreate-wire-reowned", true)
		wire, err := wireGrain(identity, newGrainConfig(), "127.0.0.9", 16000)
		require.NoError(t, err)

		cl.EXPECT().ReleaseGrain(ctx, identity.String(), departedNode).Return(remoteGrainRecord(identity, "127.0.0.8", 16001), nil).Once()

		require.NoError(t, sys.recreateGrainFromWire(ctx, wire, departedNode))
		_, ok := sys.grains.Get(identity.String())
		require.False(t, ok)
	})

	t.Run("recreates the grain once its entry is released", func(t *testing.T) {
		sys, cl, _, identity := newActivationTestSystem(t, NewMockGrain(), "recreate-wire-released", true)
		wire, err := wireGrain(identity, newGrainConfig(), "127.0.0.9", 16000)
		require.NoError(t, err)

		cl.EXPECT().ReleaseGrain(ctx, identity.String(), departedNode).Return(nil, nil).Once()
		cl.EXPECT().PutGrain(mock.Anything, mock.MatchedBy(func(actual *internalpb.Grain) bool {
			return actual != nil && actual.GetGrainId().GetValue() == identity.String() && actual.GetHost() == sys.Host()
		})).Return(nil).Once()

		require.NoError(t, sys.recreateGrainFromWire(ctx, wire, departedNode))
		process, ok := sys.grains.Get(identity.String())
		require.True(t, ok)
		require.True(t, process.isActive())
	})
}

func TestPeerActivationPropagatesGrainConfig(t *testing.T) {
	ctx := t.Context()
	name := "remote-config-grain"
	identity := newGrainIdentity((*MockActivationCountingGrain)(nil), name)
	remotePeer := &cluster.Peer{Host: "192.0.2.30", PeersPort: 15030, RemotingPort: 16030}
	alternatePeer := &cluster.Peer{Host: "192.0.2.31", PeersPort: 15031, RemotingPort: 16031}
	localPeer := &cluster.Peer{Host: "127.0.0.1", PeersPort: 14030, RemotingPort: 8095}

	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewClient(t)
	node := &discovery.Node{Host: localPeer.Host, PeersPort: localPeer.PeersPort, RemotingPort: localPeer.RemotingPort}
	sys := newClusterReadySystem(rem, cl, node)

	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
	cl.EXPECT().GetGrain(ctx, identity.String()).Return(nil, cluster.ErrGrainNotFound)
	cl.EXPECT().Members(ctx).Return([]*cluster.Peer{remotePeer, alternatePeer}, nil)
	cl.EXPECT().NextRoundRobinValue(ctx, cluster.GrainsRoundRobinKey).Return(1, nil)
	cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Once()

	// the claim record must carry the caller's configuration
	cl.EXPECT().PutGrain(mock.Anything, mock.MatchedBy(func(actual *internalpb.Grain) bool {
		return actual.GetGrainId().GetValue() == identity.String() &&
			actual.GetDisableRelocation() &&
			actual.GetActivationTimeout().AsDuration() == 9*time.Second &&
			actual.GetActivationRetries() == 3 &&
			actual.GetMailboxCapacity() == 64
	})).Return(nil).Once()

	// the remote activation request must carry the same configuration instead of defaults
	rem.EXPECT().RemoteActivateGrain(ctx, remotePeer.Host, remotePeer.RemotingPort, mock.MatchedBy(func(req *remote.GrainRequest) bool {
		return req.Name == identity.Name() &&
			req.Kind == identity.Kind() &&
			req.DisableRelocation &&
			req.ActivationTimeout == 9*time.Second &&
			req.ActivationRetries == 3 &&
			req.MailboxCapacity == 64
	})).Return(nil)

	got, err := GrainOf[*MockActivationCountingGrain](ctx, sys, name,
		WithActivationStrategy(RoundRobinActivation),
		WithGrainDisableRelocation(),
		WithGrainInitTimeout(9*time.Second),
		WithGrainInitMaxRetries(3),
		WithGrainMailboxCapacity(64))
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, identity.String(), got.String())
}

func TestLogGrainActivationFailure(t *testing.T) {
	const message = "failed to attempt remote activation for grain"

	t.Run("a caller that gave up is a debug line", func(t *testing.T) {
		buf := &safeBuffer{}
		logger := log.NewSlog(log.DebugLevel, buf)
		system := &actorSystem{logger: logger}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		system.logGrainActivationFailure(ctx, context.Canceled, message+": %v", context.Canceled)
		_ = logger.Flush()
		require.Equal(t, "debug", logLevelOf(t, buf.String(), message))
	})

	t.Run("a caller whose deadline passed during backpressure is a debug line", func(t *testing.T) {
		buf := &safeBuffer{}
		logger := log.NewSlog(log.DebugLevel, buf)
		system := &actorSystem{logger: logger}

		ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
		defer cancel()
		<-ctx.Done()

		err := errors.Join(gerrors.ErrRemoteSendBackpressure, ctx.Err())
		system.logGrainActivationFailure(ctx, err, message+": %v", err)
		_ = logger.Flush()
		require.Equal(t, "debug", logLevelOf(t, buf.String(), message))
	})

	t.Run("a full outbound queue is a warning", func(t *testing.T) {
		buf := &safeBuffer{}
		logger := log.NewSlog(log.DebugLevel, buf)
		system := &actorSystem{logger: logger}

		err := errors.Join(gerrors.ErrRemoteSendBackpressure, errors.New("tcp: duplex outbound queue full"))
		system.logGrainActivationFailure(context.Background(), err, message+": %v", err)
		_ = logger.Flush()
		require.Equal(t, "warn", logLevelOf(t, buf.String(), message))
	})

	t.Run("any other failure is an error", func(t *testing.T) {
		buf := &safeBuffer{}
		logger := log.NewSlog(log.DebugLevel, buf)
		system := &actorSystem{logger: logger}

		system.logGrainActivationFailure(context.Background(), assert.AnError, message+": %v", assert.AnError)
		_ = logger.Flush()
		require.Equal(t, "error", logLevelOf(t, buf.String(), message))
	})
}

func TestFinalizeGrainActivation(t *testing.T) {
	ctx := context.Background()

	t.Run("publish failure with failed deactivation still leaves nothing behind", func(t *testing.T) {
		clusterMock := new(mockcluster.Cluster)
		system := newReplicationSystem(clusterMock)
		buf := &safeBuffer{}
		logger := log.NewSlog(log.DebugLevel, buf)
		system.logger = logger

		grain := NewMockDeactivationFailingGrain()
		identity := newGrainIdentity(grain, "finalize-deactivate-failure")
		process := newGrainPID(identity, grain, system, newGrainConfig())
		process.activated.Store(true)

		clusterMock.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(assert.AnError).Times(grainRegistryWriteAttempts)
		// deactivation fails before its own cleanup runs, so the fallback
		// removes the local entry and releases the claim explicitly
		clusterMock.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(system.Host(), system.Port())).Return(nil, nil).Once()

		err := system.finalizeGrainActivation(ctx, process, true, true)
		require.Error(t, err)
		assert.ErrorIs(t, err, assert.AnError)

		_, ok := system.grains.Get(identity.String())
		require.False(t, ok, "a failed activation must leave no grain behind")
		clusterMock.AssertExpectations(t)

		// a running node reports the failed rollback as an error
		_ = logger.Flush()
		require.Equal(t, "error", logLevelOf(t, buf.String(), "after failed cluster publication"))
	})

	t.Run("an activation published while the shutdown began is rolled back and refused", func(t *testing.T) {
		clusterMock := new(mockcluster.Cluster)
		system := newReplicationSystem(clusterMock)

		grain := NewMockGrain()
		identity := newGrainIdentity(grain, "finalize-shutdown-during-publication")
		process := newGrainPID(identity, grain, system, newGrainConfig())
		process.activated.Store(true)

		// the shutdown begins while the record is being written
		clusterMock.EXPECT().PutGrain(mock.Anything, mock.Anything).Run(func(context.Context, *internalpb.Grain) {
			system.shuttingDown.Store(true)
		}).Return(nil).Once()
		clusterMock.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(system.Host(), system.Port())).Return(nil, nil).Once()

		err := system.finalizeGrainActivation(ctx, process, true, true)
		require.ErrorIs(t, err, gerrors.ErrSystemShuttingDown)
		require.True(t, refusal.Marked(err), "a message waiting for this activation has not run")

		_, ok := system.grains.Get(identity.String())
		require.False(t, ok, "a rolled back activation must leave no grain behind")
		require.False(t, process.isActive())
		clusterMock.AssertExpectations(t)
	})

	t.Run("a failed rollback on a stopping node is a warning and still leaves nothing behind", func(t *testing.T) {
		clusterMock := new(mockcluster.Cluster)
		system := newReplicationSystem(clusterMock)
		system.shuttingDown.Store(true)
		buf := &safeBuffer{}
		logger := log.NewSlog(log.DebugLevel, buf)
		system.logger = logger

		grain := NewMockDeactivationFailingGrain()
		identity := newGrainIdentity(grain, "finalize-stopping-deactivate-failure")
		process := newGrainPID(identity, grain, system, newGrainConfig())
		process.activated.Store(true)

		// nothing is published, and the fallback releases the claim
		clusterMock.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(system.Host(), system.Port())).Return(nil, nil).Once()

		err := system.finalizeGrainActivation(ctx, process, true, true)
		require.ErrorIs(t, err, gerrors.ErrSystemShuttingDown)
		require.True(t, refusal.Marked(err), "an activation rolled back by the shutdown is marked as the node's refusal")

		_, ok := system.grains.Get(identity.String())
		require.False(t, ok, "a rolled back activation must leave no grain behind")
		clusterMock.AssertExpectations(t)
		clusterMock.AssertNotCalled(t, "PutGrain", mock.Anything, mock.Anything)

		_ = logger.Flush()
		require.Equal(t, "warn", logLevelOf(t, buf.String(), "after failed cluster publication while stopping"))
	})

	t.Run("publish failure releases the claim without disturbing an already-active grain", func(t *testing.T) {
		clusterMock := new(mockcluster.Cluster)
		system := newReplicationSystem(clusterMock)

		grain := NewMockGrain()
		identity := newGrainIdentity(grain, "finalize-claim-release")
		process := newGrainPID(identity, grain, system, newGrainConfig())
		process.activated.Store(true)

		clusterMock.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(assert.AnError).Times(grainRegistryWriteAttempts)
		clusterMock.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(system.Host(), system.Port())).Return(nil, nil).Once()

		err := system.finalizeGrainActivation(ctx, process, true, false)
		require.Error(t, err)
		assert.ErrorIs(t, err, assert.AnError)

		got, ok := system.grains.Get(identity.String())
		require.True(t, ok, "an already-active grain must stay registered")
		require.True(t, got.isActive(), "an already-active grain must not be deactivated")
		clusterMock.AssertExpectations(t)
	})

	t.Run("rolls back an activation that finishes once the node is shutting down", func(t *testing.T) {
		clusterMock := new(mockcluster.Cluster)
		system := newReplicationSystem(clusterMock)
		system.shuttingDown.Store(true)

		grain := NewMockGrain()
		identity := newGrainIdentity(grain, "finalize-shutting-down")
		process := newGrainPID(identity, grain, system, newGrainConfig())
		process.activated.Store(true)

		// nothing is published: the deactivation releases the claim
		clusterMock.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(system.Host(), system.Port())).Return(nil, nil).Once()

		err := system.finalizeGrainActivation(ctx, process, true, true)
		require.ErrorIs(t, err, gerrors.ErrSystemShuttingDown)

		_, ok := system.grains.Get(identity.String())
		require.False(t, ok, "the late activation must leave no grain behind")
		clusterMock.AssertExpectations(t)
		clusterMock.AssertNotCalled(t, "PutGrain", mock.Anything, mock.Anything)
	})

	t.Run("a stopping node publishes nothing for an already-active grain", func(t *testing.T) {
		clusterMock := new(mockcluster.Cluster)
		system := newReplicationSystem(clusterMock)
		system.shuttingDown.Store(true)

		grain := NewMockGrain()
		identity := newGrainIdentity(grain, "finalize-shutting-down-active")
		process := newGrainPID(identity, grain, system, newGrainConfig())
		process.activated.Store(true)
		system.grains.Set(identity.String(), process)

		err := system.finalizeGrainActivation(ctx, process, false, false)
		require.ErrorIs(t, err, gerrors.ErrSystemShuttingDown)

		got, ok := system.grains.Get(identity.String())
		require.True(t, ok, "the grain must stay registered locally")
		require.True(t, got.isActive(), "the grain must stay active until the shutdown deactivates it")
		clusterMock.AssertNotCalled(t, "PutGrain", mock.Anything, mock.Anything)
		clusterMock.AssertNotCalled(t, "ReleaseGrain", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("a stopping node releases its claim without publishing for an already-active grain", func(t *testing.T) {
		clusterMock := new(mockcluster.Cluster)
		system := newReplicationSystem(clusterMock)
		system.shuttingDown.Store(true)

		grain := NewMockGrain()
		identity := newGrainIdentity(grain, "finalize-shutting-down-claimed")
		process := newGrainPID(identity, grain, system, newGrainConfig())
		process.activated.Store(true)
		system.grains.Set(identity.String(), process)

		clusterMock.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(system.Host(), system.Port())).Return(nil, nil).Once()

		err := system.finalizeGrainActivation(ctx, process, true, false)
		require.ErrorIs(t, err, gerrors.ErrSystemShuttingDown)

		got, ok := system.grains.Get(identity.String())
		require.True(t, ok)
		require.True(t, got.isActive())
		clusterMock.AssertExpectations(t)
		clusterMock.AssertNotCalled(t, "PutGrain", mock.Anything, mock.Anything)
	})

	t.Run("a running node publishes an already-active grain", func(t *testing.T) {
		clusterMock := new(mockcluster.Cluster)
		system := newReplicationSystem(clusterMock)

		grain := NewMockGrain()
		identity := newGrainIdentity(grain, "finalize-running-active")
		process := newGrainPID(identity, grain, system, newGrainConfig())
		process.activated.Store(true)
		system.grains.Set(identity.String(), process)

		clusterMock.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(nil).Once()

		require.NoError(t, system.finalizeGrainActivation(ctx, process, false, false))

		got, ok := system.grains.Get(identity.String())
		require.True(t, ok)
		require.Same(t, process, got)
		require.True(t, got.isActive())
		clusterMock.AssertExpectations(t)
		clusterMock.AssertNotCalled(t, "ReleaseGrain", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("an activated grain becomes reachable once its record is published", func(t *testing.T) {
		clusterMock := new(mockcluster.Cluster)
		system := newReplicationSystem(clusterMock)

		grain := NewMockGrain()
		identity := newGrainIdentity(grain, "finalize-reachable-after-publish")
		process := newGrainPID(identity, grain, system, newGrainConfig())
		process.activated.Store(true)

		var visibleWhilePublishing bool
		clusterMock.EXPECT().PutGrain(mock.Anything, mock.Anything).Run(func(context.Context, *internalpb.Grain) {
			_, visibleWhilePublishing = system.grains.Get(identity.String())
		}).Return(nil).Once()

		require.NoError(t, system.finalizeGrainActivation(ctx, process, true, true))
		require.False(t, visibleWhilePublishing, "the grain must not be reachable before its record is published")

		got, ok := system.grains.Get(identity.String())
		require.True(t, ok, "the grain must be reachable once finalizeGrainActivation returns")
		require.Same(t, process, got)
		clusterMock.AssertExpectations(t)
	})

	t.Run("publish failure without claim or activation only returns the error", func(t *testing.T) {
		clusterMock := new(mockcluster.Cluster)
		system := newReplicationSystem(clusterMock)

		grain := NewMockGrain()
		identity := newGrainIdentity(grain, "finalize-no-rollback")
		process := newGrainPID(identity, grain, system, newGrainConfig())
		process.activated.Store(true)

		clusterMock.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(assert.AnError).Times(grainRegistryWriteAttempts)

		err := system.finalizeGrainActivation(ctx, process, false, false)
		require.Error(t, err)
		assert.ErrorIs(t, err, assert.AnError)

		got, ok := system.grains.Get(identity.String())
		require.True(t, ok, "an already-active grain must stay registered")
		require.True(t, got.isActive(), "an already-active grain must not be deactivated")
		clusterMock.AssertNotCalled(t, "ReleaseGrain", mock.Anything, mock.Anything, mock.Anything)
	})
}

// TestFinalizeGrainActivationRacingLocalSend sends to a grain from this node
// while its activation publishes the registry record. The grain is not
// reachable until the record is published, so the send waits for the
// activation: a failed publication rolls the grain back without OnDeactivate
// overlapping an OnReceive turn, and a successful one serves the send once.
func TestFinalizeGrainActivationRacingLocalSend(t *testing.T) {
	t.Run("a failed publication never deactivates the grain during a turn", func(t *testing.T) {
		ctx := t.Context()
		grain, probe := NewMockTurnOverlapGrain()
		sys, cl, _, identity := newActivationTestSystem(t, grain, "racing-failed-publication", true)
		config := newGrainConfig()
		owner, err := wireGrain(identity, config, sys.Host(), sys.Port())
		require.NoError(t, err)

		publishing := make(chan types.Unit)
		proceed := make(chan types.Unit)

		cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Run(func(context.Context, *internalpb.Grain) {
			close(publishing)
			<-proceed
		}).Return(assert.AnError).Once()
		// the publication is tried again and keeps failing
		cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(assert.AnError).Times(grainRegistryWriteAttempts - 1)

		// the rollback releases the record; a send that comes after the
		// failed activation activates the grain again and is served
		cl.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(sys.Host(), sys.Port())).Return(nil, nil).Maybe()
		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil).Maybe()
		cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(nil).Maybe()

		activation := make(chan error, 1)
		go func() {
			activation <- sys.activateGrainLocally(ctx, identity, staticGrainProvider(grain), config, owner)
		}()

		<-publishing

		sent := make(chan error, 1)
		go func() {
			_, err := sys.localSendGrain(ctx, identity, new(testpb.TestSend), time.Second, grainTell)
			sent <- err
		}()

		// a send that reaches the grain during the publication starts its
		// turn here; one that waits for the activation does not
		select {
		case <-probe.received:
		case <-time.After(200 * time.Millisecond):
		}

		close(proceed)

		require.ErrorIs(t, <-activation, assert.AnError)
		sendErr := <-sent
		require.Zero(t, probe.overlaps.Load(), "OnDeactivate ran while an OnReceive turn was in progress")
		if sendErr != nil {
			require.ErrorIs(t, sendErr, assert.AnError, "a send that waited for the failed activation must report its failure")
		}
	})

	t.Run("a failed publication of a reactivated grain never deactivates it during a turn", func(t *testing.T) {
		ctx := t.Context()
		grain, probe := NewMockTurnOverlapGrain()
		sys, cl, _, identity := newActivationTestSystem(t, grain, "racing-failed-reactivation", true)

		// an entry left inactive in the grains map, as a failed OnDeactivate leaves it
		seedInactiveGrainPID(sys, identity, grain, newGrainConfig())
		owner, err := wireGrain(identity, newGrainConfig(), sys.Host(), sys.Port())
		require.NoError(t, err)

		publishing := make(chan types.Unit)
		proceed := make(chan types.Unit)

		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Maybe()
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil).Maybe()
		cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Run(func(context.Context, *internalpb.Grain) {
			close(publishing)
			<-proceed
		}).Return(assert.AnError).Once()
		// the publication is tried again and keeps failing
		cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(assert.AnError).Times(grainRegistryWriteAttempts - 1)

		// the rollback releases the record; a send that comes after the
		// failed activation activates the grain again and is served
		cl.EXPECT().ReleaseGrain(mock.Anything, identity.String(), address.FormatHostPort(sys.Host(), sys.Port())).Return(nil, nil).Maybe()
		cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(nil).Maybe()

		reactivation := make(chan error, 1)
		go func() {
			_, err := sys.localSendGrain(ctx, identity, new(testpb.TestSend), time.Second, grainTell)
			reactivation <- err
		}()

		<-publishing

		sent := make(chan error, 1)
		go func() {
			_, err := sys.localSendGrain(ctx, identity, new(testpb.TestSend), time.Second, grainTell)
			sent <- err
		}()

		select {
		case <-probe.received:
		case <-time.After(200 * time.Millisecond):
		}

		close(proceed)

		require.ErrorIs(t, <-reactivation, assert.AnError)
		sendErr := <-sent
		require.Zero(t, probe.overlaps.Load(), "OnDeactivate ran while an OnReceive turn was in progress")
		if sendErr != nil {
			require.ErrorIs(t, sendErr, assert.AnError, "a send that waited for the failed activation must report its failure")
		}
	})

	t.Run("a successful publication serves a waiting send once", func(t *testing.T) {
		ctx := t.Context()
		grain, probe := NewMockTurnOverlapGrain()
		sys, cl, _, identity := newActivationTestSystem(t, grain, "racing-successful-publication", true)
		config := newGrainConfig()
		owner, err := wireGrain(identity, config, sys.Host(), sys.Port())
		require.NoError(t, err)

		publishing := make(chan types.Unit)
		proceed := make(chan types.Unit)

		cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Run(func(context.Context, *internalpb.Grain) {
			close(publishing)
			<-proceed
		}).Return(nil).Once()

		activation := make(chan error, 1)
		go func() {
			activation <- sys.activateGrainLocally(ctx, identity, staticGrainProvider(grain), config, owner)
		}()

		<-publishing

		type askResult struct {
			reply any
			err   error
		}

		asked := make(chan askResult, 1)
		go func() {
			reply, err := sys.localSendGrain(ctx, identity, new(testpb.TestReply), time.Second, grainAsk)
			asked <- askResult{reply: reply, err: err}
		}()

		select {
		case <-probe.received:
			t.Fatal("the grain was reached before its record was published")
		case <-time.After(200 * time.Millisecond):
		}

		close(proceed)
		require.NoError(t, <-activation)

		result := <-asked
		require.NoError(t, result.err)
		require.Equal(t, "received message", result.reply.(*testpb.Reply).GetContent())
		require.EqualValues(t, 1, probe.activations.Load(), "the send must use the activation it waited for")
		require.EqualValues(t, 1, probe.receives.Load(), "the send must be served exactly once")
	})
}

func TestActivateUnreachable(t *testing.T) {
	failingConfig := newGrainConfig(WithGrainInitMaxRetries(1), WithGrainInitTimeout(10*time.Millisecond))

	t.Run("a failed activation puts an inactive entry back", func(t *testing.T) {
		grain := NewMockActivationFailingGrain()
		sys, _, _, identity := newActivationTestSystem(t, grain, "unreachable-restore", true)
		process := seedInactiveGrainPID(sys, identity, grain, failingConfig)

		err := sys.activateUnreachable(t.Context(), process)
		require.ErrorIs(t, err, gerrors.ErrGrainActivationFailure)

		got, ok := sys.grains.Get(identity.String())
		require.True(t, ok, "the inactive entry must be put back")
		require.Same(t, process, got)
		require.False(t, got.isActive())
	})

	t.Run("a failed activation of an unregistered process registers nothing", func(t *testing.T) {
		grain := NewMockActivationFailingGrain()
		sys, _, _, identity := newActivationTestSystem(t, grain, "unreachable-new", true)
		process := newGrainPID(identity, grain, sys, failingConfig)

		err := sys.activateUnreachable(t.Context(), process)
		require.ErrorIs(t, err, gerrors.ErrGrainActivationFailure)

		_, ok := sys.grains.Get(identity.String())
		require.False(t, ok)
	})

	t.Run("a successful activation leaves registration to the publication", func(t *testing.T) {
		grain := NewMockGrain()
		sys, _, _, identity := newActivationTestSystem(t, grain, "unreachable-success", true)
		process := seedInactiveGrainPID(sys, identity, grain, newGrainConfig())

		require.NoError(t, sys.activateUnreachable(t.Context(), process))
		require.True(t, process.isActive())

		_, ok := sys.grains.Get(identity.String())
		require.False(t, ok, "the grain must stay unreachable until finalizeGrainActivation publishes it")
	})
}

func TestEnvelopeAskCompletesInTurn(t *testing.T) {
	sys, _, identity := startEnvelopeGrainFixture(t, &MockEnvelopeReplyingGrain{}, "envelopeGrain")

	response, err := sys.AskGrain(context.Background(), identity, new(testpb.TestPing), time.Second)
	require.NoError(t, err)

	reply, ok := response.(*testpb.Reply)
	require.True(t, ok)
	require.Equal(t, "in-turn", reply.GetContent())
	require.Zero(t, sys.pendingAsks.Len())
}

func TestEnvelopeAskCarriesFailure(t *testing.T) {
	sys, _, identity := startEnvelopeGrainFixture(t, &MockEnvelopeReplyingGrain{}, "envelopeGrain")

	response, err := sys.AskGrain(context.Background(), identity, new(testpb.TestBye), time.Second)
	require.Nil(t, response)
	require.EqualError(t, err, "grain boom")
	require.Zero(t, sys.pendingAsks.Len())
}

func TestEnvelopeAskDeferredReply(t *testing.T) {
	grain := &MockEnvelopeDeferringGrain{requests: make(chan string, 1)}
	sys, _, identity := startEnvelopeGrainFixture(t, grain, "deferringGrain")
	ctx := context.Background()

	type askResult struct {
		response any
		err      error
	}
	results := make(chan askResult, 1)

	go func() {
		response, err := sys.AskGrain(ctx, identity, new(testpb.TestReply), 2*time.Second)
		results <- askResult{response: response, err: err}
	}()

	select {
	case <-grain.requests:
	case <-time.After(2 * time.Second):
		t.Fatal("grain did not receive the request")
	}

	// The turn that carried the request has ended; the flush replies from a
	// later one while the caller still blocks.
	require.NoError(t, sys.TellGrain(ctx, identity, new(testpb.TestSend)))

	select {
	case result := <-results:
		require.NoError(t, result.err)
		reply, ok := result.response.(*testpb.Reply)
		require.True(t, ok)
		require.Equal(t, "deferred", reply.GetContent())
	case <-time.After(2 * time.Second):
		t.Fatal("deferred reply never completed the ask")
	}
	require.Zero(t, sys.pendingAsks.Len())
}

func TestEnvelopeAskTimeoutAbandons(t *testing.T) {
	// TestReply hits the replying grain's default arm: no reply ever comes.
	sys, _, identity := startEnvelopeGrainFixture(t, &MockEnvelopeReplyingGrain{}, "envelopeGrain")

	response, err := sys.AskGrain(context.Background(), identity, new(testpb.TestReply), 200*time.Millisecond)
	require.Nil(t, response)
	require.ErrorIs(t, err, gerrors.ErrRequestTimeout)
	require.Zero(t, sys.pendingAsks.Len())
}

func TestEnvelopeAskCanceledContext(t *testing.T) {
	sys, pid, _ := startEnvelopeGrainFixture(t, &MockEnvelopeReplyingGrain{}, "envelopeGrain")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	response, err := sys.envelopeAsk(ctx, pid, new(testpb.TestReply), time.Second)
	require.Nil(t, response)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, gerrors.ErrRequestTimeout)
	require.Zero(t, sys.pendingAsks.Len())
}

func TestEnvelopeAskEnqueueFailureAbandons(t *testing.T) {
	sys, pid, _ := startEnvelopeGrainFixture(t, &MockEnvelopeReplyingGrain{}, "envelopeGrain")

	pid.activated.Store(false)
	response, err := sys.envelopeAsk(context.Background(), pid, new(testpb.TestPing), time.Second)
	pid.activated.Store(true)

	require.Nil(t, response)
	require.ErrorIs(t, err, gerrors.ErrDead)
	require.Zero(t, sys.pendingAsks.Len())
}

func TestEnvelopeAskEmptyResponseCompletesWithNil(t *testing.T) {
	grain := &MockEnvelopeDeferringGrain{requests: make(chan string, 1)}
	sys, _, identity := startEnvelopeGrainFixture(t, grain, "deferringGrain")

	type askResult struct {
		response any
		err      error
	}
	results := make(chan askResult, 1)

	go func() {
		response, err := sys.AskGrain(context.Background(), identity, new(testpb.TestReply), 2*time.Second)
		results <- askResult{response: response, err: err}
	}()

	var correlationID string
	select {
	case correlationID = <-grain.requests:
	case <-time.After(2 * time.Second):
		t.Fatal("grain did not receive the request")
	}

	// A response carrying neither payload nor error is the wire form of NoErr:
	// the caller observes success with a nil result.
	require.True(t, sys.pendingAsks.Complete(&commands.AsyncResponse{CorrelationID: correlationID}))

	select {
	case result := <-results:
		require.NoError(t, result.err)
		require.Nil(t, result.response)
	case <-time.After(2 * time.Second):
		t.Fatal("ask did not complete")
	}
}

func TestDeliverAsyncEnvelope(t *testing.T) {
	ctx := context.Background()

	t.Run("rejected while stopping", func(t *testing.T) {
		sys, _, _, identity := startReentrantGrainFixture(t, reentrancy.AllowAll)

		sys.shuttingDown.Store(true)
		err := sys.deliverAsyncEnvelope(ctx, identity, &commands.AsyncRequest{CorrelationID: "late", Message: new(testpb.TestReply)})
		sys.shuttingDown.Store(false)

		require.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
	})

	t.Run("fast path delivers to the active grain", func(t *testing.T) {
		sys, _, grain, identity := startReentrantGrainFixture(t, reentrancy.AllowAll)

		require.NoError(t, sys.deliverAsyncEnvelope(ctx, identity, &commands.AsyncRequest{
			CorrelationID: "fast",
			Message:       testpb.Reply_builder{Content: "fast"}.Build(),
		}))

		require.Eventually(t, func() bool {
			return len(grain.recorded()) == 1
		}, 2*time.Second, 10*time.Millisecond)
		require.Equal(t, "fast", grain.recorded()[0].requestID)
	})

	t.Run("activates an idle grain", func(t *testing.T) {
		sys, pid, _, identity := startReentrantGrainFixture(t, reentrancy.AllowAll)

		// Deactivate the grain so delivery has to go through activation.
		ack := pid.enqueuePoisonPill(ctx)

		select {
		case err := <-ack:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("grain did not deactivate")
		}

		require.NoError(t, sys.deliverAsyncEnvelope(ctx, identity, &commands.AsyncRequest{
			CorrelationID: "wake",
			Message:       new(testpb.TestReply),
		}))

		process, ok := sys.grains.Get(identity.String())
		require.True(t, ok)
		require.True(t, process.isActive())
	})

	t.Run("propagates activation errors", func(t *testing.T) {
		sys, _, _, _ := startReentrantGrainFixture(t, reentrancy.AllowAll)

		// An identity whose kind was never registered cannot activate.
		unknown := newGrainIdentity(&MockActivationFailingGrain{}, "never-registered")
		err := sys.deliverAsyncEnvelope(ctx, unknown, &commands.AsyncRequest{CorrelationID: "corr", Message: new(testpb.TestReply)})
		require.Error(t, err)
	})

	t.Run("owner mismatch forwards to the owning node", func(t *testing.T) {
		grain := &MockReentrantRecordingGrain{}
		sys, cl, rem, identity := newActivationTestSystem(t, grain, "away", true)

		owner := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind(), Name: identity.Name()}.Build(),
			Host:    "192.0.2.9",
			Port:    16000,
		}.Build()

		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil).Once()
		rem.EXPECT().RemoteTellGrain(mock.Anything, owner.GetHost(), int(owner.GetPort()), mock.Anything, mock.Anything).
			Return(nil).Once()

		err := sys.deliverAsyncEnvelope(ctx, identity, &commands.AsyncResponse{
			CorrelationID: "corr",
			Message:       new(testpb.TestReply),
		})
		require.NoError(t, err)
	})
}

func TestWireGrainReentrancy(t *testing.T) {
	grain := NewMockGrain()
	sys, _, _, identity := newActivationTestSystem(t, grain, "reentrancy-wire", true)

	wire, err := wireGrain(identity, newGrainConfig(), sys.Host(), sys.Port())
	require.NoError(t, err)
	require.Nil(t, wire.GetReentrancy())

	policy := reentrancy.New(reentrancy.WithMode(reentrancy.StashNonReentrant), reentrancy.WithMaxInFlight(3))
	wire, err = wireGrain(identity, newGrainConfig(WithGrainReentrancy(policy)), sys.Host(), sys.Port())
	require.NoError(t, err)
	require.NotNil(t, wire.GetReentrancy())
	require.Equal(t, internalpb.ReentrancyMode_REENTRANCY_MODE_STASH_NON_REENTRANT, wire.GetReentrancy().GetMode())
	require.EqualValues(t, 3, wire.GetReentrancy().GetMaxInFlight())
}

func TestRecreateGrainPreservesReentrancy(t *testing.T) {
	ctx := t.Context()
	sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	t.Cleanup(func() { _ = sys.Stop(ctx) })

	as := sys.(*actorSystem)
	as.registry.Register(&MockGrain{})

	identity := newGrainIdentity(&MockGrain{}, "recreate-reentrancy")
	policy := reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll), reentrancy.WithMaxInFlight(5))
	wire, err := wireGrain(identity, newGrainConfig(WithGrainReentrancy(policy)), as.Host(), as.Port())
	require.NoError(t, err)

	require.NoError(t, as.recreateGrain(ctx, wire))

	pid, ok := as.grains.Get(identity.String())
	require.True(t, ok)

	reentrant := pid.reentrancy.Load()
	require.NotNil(t, reentrant)
	require.Equal(t, reentrancy.AllowAll, reentrant.getMode())
	require.EqualValues(t, 5, reentrant.maxInFlight.Load())

	// The republished wire record keeps the policy.
	republished, err := pid.toWireGrain()
	require.NoError(t, err)
	require.NotNil(t, republished.GetReentrancy())
	require.Equal(t, internalpb.ReentrancyMode_REENTRANCY_MODE_ALLOW_ALL, republished.GetReentrancy().GetMode())
}

func TestSendRemoteActivateGrainCarriesReentrancy(t *testing.T) {
	ctx := t.Context()
	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewClient(t)
	node := &discovery.Node{Host: "127.0.0.1", PeersPort: 9031, RemotingPort: 9131}
	sys := newClusterReadySystem(rem, cl, node)

	grain := NewMockGrain()
	sys.registry.Register(grain)
	identity := newGrainIdentity(grain, "remote-reentrancy")

	policy := reentrancy.New(reentrancy.WithMode(reentrancy.StashNonReentrant), reentrancy.WithMaxInFlight(2))
	wire, err := wireGrain(identity, newGrainConfig(WithGrainReentrancy(policy)), "192.0.2.7", 16000)
	require.NoError(t, err)

	rem.EXPECT().RemoteActivateGrain(ctx, "192.0.2.7", 16000, mock.MatchedBy(func(req *remote.GrainRequest) bool {
		return req != nil &&
			req.Reentrancy != nil &&
			req.Reentrancy.Mode() == reentrancy.StashNonReentrant &&
			req.Reentrancy.MaxInFlight() == 2
	})).Return(nil).Once()

	require.NoError(t, sys.sendRemoteActivateGrain(ctx, wire))
}

// TestGrainReactivationUsesDefaultConfig pins the documented lifecycle
// property: reentrancy is an activation-time option, so a grain that
// passivated on idleness comes back from a bare send with default config and
// no reentrancy state.
func TestGrainReactivationUsesDefaultConfig(t *testing.T) {
	ctx := context.Background()
	system, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, system.Start(ctx))
	t.Cleanup(func() { _ = system.Stop(context.Background()) })

	sys := system.(*actorSystem)

	identity, err := system.GrainIdentity(ctx, "reactivated-grain", func(context.Context) (Grain, error) {
		return new(MockReactivationGrain), nil
	},
		WithGrainReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))),
		WithGrainDeactivateAfter(200*time.Millisecond))
	require.NoError(t, err)

	pid, ok := sys.grains.Get(identity.String())
	require.True(t, ok)
	require.NotNil(t, pid.reentrancy.Load())

	require.Eventually(t, func() bool {
		_, exists := sys.grains.Get(identity.String())
		return !exists
	}, 3*time.Second, 20*time.Millisecond)

	require.NoError(t, system.TellGrain(ctx, identity, new(testpb.TestSend)))

	pid, ok = sys.grains.Get(identity.String())
	require.True(t, ok)
	require.Nil(t, pid.reentrancy.Load())
}

// TestNonReentrantAskSkipsPendingAsks proves the legacy channel ask stays in
// place for non-reentrant targets: while the handler holds the ask, no
// pending-asks entry exists, so the envelope path was never involved.
func TestNonReentrantAskSkipsPendingAsks(t *testing.T) {
	ctx := context.Background()
	system, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, system.Start(ctx))
	t.Cleanup(func() { _ = system.Stop(context.Background()) })

	sys := system.(*actorSystem)

	entered := make(chan struct{}, 1)
	release := make(chan struct{})

	grain := &MockScriptedGrain{receive: func(gctx *GrainContext) {
		entered <- struct{}{}
		<-release
		gctx.Response(testpb.Reply_builder{Content: "legacy"}.Build())
	}}

	identity, err := system.GrainIdentity(ctx, "legacy-ask-grain", func(context.Context) (Grain, error) {
		return grain, nil
	})
	require.NoError(t, err)

	type askResult struct {
		response any
		err      error
	}
	results := make(chan askResult, 1)

	go func() {
		response, err := system.AskGrain(ctx, identity, new(testpb.TestPing), 2*time.Second)
		results <- askResult{response: response, err: err}
	}()

	<-entered
	require.Zero(t, sys.pendingAsks.Len())
	close(release)

	select {
	case result := <-results:
		require.NoError(t, result.err)
		reply, ok := result.response.(*testpb.Reply)
		require.True(t, ok)
		require.Equal(t, "legacy", reply.GetContent())
	case <-time.After(2 * time.Second):
		t.Fatal("legacy ask never completed")
	}
}

func TestTellGrainOneWay(t *testing.T) {
	t.Run("local mode returns before the grain processes the message", func(t *testing.T) {
		ctx := context.Background()
		sys, err := NewActorSystem("tell-grain-one-way-local", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, sys.Start(ctx))
		t.Cleanup(func() { _ = sys.Stop(ctx) })

		entered := make(chan struct{}, 1)
		release := make(chan struct{})
		t.Cleanup(func() { close(release) })

		blocking := &MockScriptedGrain{receive: func(gctx *GrainContext) {
			entered <- struct{}{}
			<-release
			gctx.NoErr()
		}}

		identity, err := sys.GrainIdentity(ctx, "one-way-blocking-grain", func(context.Context) (Grain, error) {
			return blocking, nil
		})
		require.NoError(t, err)

		// The handler parks inside OnReceive until release is closed, so the
		// call can only return without waiting for the acknowledgement.
		require.NoError(t, sys.TellGrain(ctx, identity, new(testpb.TestSend), WithOneWay()))

		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("the grain did not receive the one-way message")
		}
	})

	t.Run("returns ErrMailboxFull when the bounded mailbox is full", func(t *testing.T) {
		ctx := context.Background()
		sys, err := NewActorSystem("tell-grain-one-way-bounded", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, sys.Start(ctx))
		t.Cleanup(func() { _ = sys.Stop(ctx) })

		entered := make(chan struct{}, 1)
		release := make(chan struct{})
		t.Cleanup(func() { close(release) })

		blocking := &MockScriptedGrain{receive: func(gctx *GrainContext) {
			entered <- struct{}{}
			<-release
			gctx.NoErr()
		}}

		identity, err := sys.GrainIdentity(ctx, "one-way-bounded-grain", func(context.Context) (Grain, error) {
			return blocking, nil
		}, WithGrainMailboxCapacity(1))
		require.NoError(t, err)

		// The first message parks the turn inside OnReceive.
		require.NoError(t, sys.TellGrain(ctx, identity, new(testpb.TestSend), WithOneWay()))

		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("the grain did not receive the one-way message")
		}

		// The second message fills the single slot and the third is rejected
		// at enqueue time, which is the only failure a one-way tell reports.
		require.NoError(t, sys.TellGrain(ctx, identity, new(testpb.TestSend), WithOneWay()))
		err = sys.TellGrain(ctx, identity, new(testpb.TestSend), WithOneWay())
		require.ErrorIs(t, err, gerrors.ErrMailboxFull)
	})

	t.Run("drops handler failures", func(t *testing.T) {
		ctx := context.Background()
		sys, err := NewActorSystem("tell-grain-one-way-failing", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, sys.Start(ctx))
		t.Cleanup(func() { _ = sys.Stop(ctx) })

		require.NoError(t, sys.RegisterGrainKind(ctx, &MockReceiveFailingGrain{}))
		identity := newGrainIdentity(NewMockReceiveFailingGrain(), "one-way-failing-grain")

		// The acknowledged tell surfaces the handler error, the one-way tell does not.
		require.Error(t, sys.TellGrain(ctx, identity, new(testpb.TestSend)))
		require.NoError(t, sys.TellGrain(ctx, identity, new(testpb.TestSend), WithOneWay()))
		pause.For(100 * time.Millisecond)
	})

	t.Run("drops handler panics and keeps the grain active", func(t *testing.T) {
		ctx := context.Background()
		sys, err := NewActorSystem("tell-grain-one-way-panicking", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, sys.Start(ctx))
		t.Cleanup(func() { _ = sys.Stop(ctx) })

		require.NoError(t, sys.RegisterGrainKind(ctx, &MockPanickingGrain{}))
		identity := newGrainIdentity(NewMockPanickingGrain(), "one-way-panicking-grain")

		require.NoError(t, sys.TellGrain(ctx, identity, new(testpb.TestSend), WithOneWay()))
		pause.For(100 * time.Millisecond)

		process, ok := sys.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)
		assert.True(t, process.isActive())
	})

	t.Run("returns ErrActorSystemNotStarted when not started", func(t *testing.T) {
		ctx := context.Background()
		sys, err := NewActorSystem("tell-grain-one-way-not-started", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		identity := newGrainIdentity(NewMockGrain(), "grain")

		err = sys.TellGrain(ctx, identity, new(testpb.TestSend), WithOneWay())
		require.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
	})

	t.Run("returns ErrInvalidGrainIdentity when identity invalid", func(t *testing.T) {
		ctx := context.Background()
		sys, err := NewActorSystem("tell-grain-one-way-invalid", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, sys.Start(ctx))
		t.Cleanup(func() { _ = sys.Stop(ctx) })

		invalidID := &GrainIdentity{kind: "", name: ""}
		err = sys.TellGrain(ctx, invalidID, new(testpb.TestSend), WithOneWay())
		require.ErrorIs(t, err, gerrors.ErrInvalidGrainIdentity)
	})

	t.Run("cluster mode forwards to the remote owner", func(t *testing.T) {
		ctx := context.Background()
		cl := mockcluster.NewCluster(t)
		rem := mockremote.NewClient(t)
		node := &discovery.Node{Host: "127.0.0.1", PeersPort: 9019, RemotingPort: 9119}
		sys := newClusterReadySystem(rem, cl, node)
		sys.registry.Register(NewMockGrain())

		identity := newGrainIdentity(NewMockGrain(), "one-way-remote-owner-grain")
		owner := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind(), Name: identity.Name()}.Build(),
			Host:    "192.0.2.1",
			Port:    16000,
		}.Build()
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil).Once()
		rem.EXPECT().RemoteTellGrainOneWay(mock.Anything, owner.GetHost(), int(owner.GetPort()),
			mock.MatchedBy(func(request *remote.GrainRequest) bool {
				return request.Name == identity.Name() && request.Kind == identity.Kind()
			}), mock.Anything).Return(nil).Once()

		require.NoError(t, sys.TellGrain(ctx, identity, new(testpb.TestSend), WithOneWay()))
	})

	t.Run("cluster mode surfaces the remote transport failure", func(t *testing.T) {
		ctx := context.Background()
		cl := mockcluster.NewCluster(t)
		rem := mockremote.NewClient(t)
		node := &discovery.Node{Host: "127.0.0.1", PeersPort: 9021, RemotingPort: 9121}
		sys := newClusterReadySystem(rem, cl, node)
		sys.registry.Register(NewMockGrain())

		identity := newGrainIdentity(NewMockGrain(), "one-way-remote-failure-grain")
		owner := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind(), Name: identity.Name()}.Build(),
			Host:    "192.0.2.1",
			Port:    16000,
		}.Build()
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil).Once()
		rem.EXPECT().RemoteTellGrainOneWay(mock.Anything, owner.GetHost(), int(owner.GetPort()), mock.Anything, mock.Anything).
			Return(gerrors.NewErrRemoteSendFailure(errors.New("connection refused"))).Once()
		// the owner is still a member, so its entry is kept
		cl.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{{Host: owner.GetHost(), RemotingPort: int(owner.GetPort())}}, nil).Once()

		err := sys.TellGrain(ctx, identity, new(testpb.TestSend), WithOneWay())
		require.ErrorIs(t, err, gerrors.ErrRemoteSendFailure)
	})

	t.Run("cluster mode enqueues in-process when the grain is owned by the current node", func(t *testing.T) {
		ctx := context.Background()
		cl := mockcluster.NewCluster(t)
		rem := mockremote.NewClient(t)
		node := &discovery.Node{Host: "127.0.0.1", PeersPort: 9023, RemotingPort: 9123}
		sys := newClusterReadySystem(rem, cl, node)
		sys.registry.Register(NewMockGrain())

		identity := newGrainIdentity(NewMockGrain(), "one-way-local-owner-grain")
		owner := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind()}.Build(),
			Host:    node.Host,
			Port:    int32(node.RemotingPort),
		}.Build()
		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil)
		cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(nil).Once()

		// No RemoteTellGrainOneWay expectation: a loopback round trip would
		// fail the mock client, proving the message was enqueued locally.
		require.NoError(t, sys.TellGrain(ctx, identity, new(testpb.TestSend), WithOneWay()))

		require.Eventually(t, func() bool {
			process, ok := sys.grains.Get(identity.String())
			return ok && process.isActive()
		}, time.Second, 5*time.Millisecond, "grain should be activated locally after a one-way TellGrain")
	})

	t.Run("cluster mode activates locally when the grain is not found anywhere", func(t *testing.T) {
		ctx := context.Background()
		cl := mockcluster.NewCluster(t)
		rem := mockremote.NewClient(t)
		node := &discovery.Node{Host: "127.0.0.1", PeersPort: 9029, RemotingPort: 9129}
		sys := newClusterReadySystem(rem, cl, node)
		sys.registry.Register(NewMockGrain())

		identity := newGrainIdentity(NewMockGrain(), "one-way-not-found-grain")

		// The registry lookup misses, there is no datacenter controller, so the
		// send falls back to a local activation: the ownership check and the
		// claim both see no record, and the activation is then published.
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(nil, cluster.ErrGrainNotFound).Once()
		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, nil)
		cl.EXPECT().PutGrain(mock.Anything, mock.MatchedBy(func(actual *internalpb.Grain) bool {
			return actual != nil && actual.GetGrainId().GetValue() == identity.String()
		})).Return(nil)

		require.NoError(t, sys.TellGrain(ctx, identity, new(testpb.TestSend), WithOneWay()))

		require.Eventually(t, func() bool {
			process, ok := sys.grains.Get(identity.String())
			return ok && process.isActive() && process.processedCount.Load() == 1
		}, time.Second, 5*time.Millisecond, "grain should be activated locally and process the one-way message")
	})

	t.Run("cluster mode returns before the remote grain processes the message", func(t *testing.T) {
		ctx := context.Background()
		srv := startNatsServer(t)

		node1, sd1 := startNATsSystem(t, srv.Addr().String(), withTestExtraGrains(&MockScriptedGrain{}))
		node2, sd2 := startNATsSystem(t, srv.Addr().String(), withTestExtraGrains(&MockScriptedGrain{}))

		entered := make(chan struct{}, 1)
		release := make(chan struct{})

		defer func() {
			assert.NoError(t, node2.Stop(ctx))
			assert.NoError(t, node1.Stop(ctx))
			sd2.Close()
			sd1.Close()
			srv.Shutdown()
		}()
		// Deferred after the shutdown, so it runs first and unparks the grain.
		defer close(release)

		pause.For(time.Second)

		blocking := &MockScriptedGrain{receive: func(gctx *GrainContext) {
			entered <- struct{}{}
			<-release
			gctx.NoErr()
		}}

		// Activate the grain on node1.
		identity, err := node1.GrainIdentity(ctx, "one-way-remote-blocking-grain", func(context.Context) (Grain, error) {
			return blocking, nil
		})
		require.NoError(t, err)
		pause.For(time.Second)

		// The handler parks inside OnReceive: an acknowledged tell from node2
		// would hold until DefaultGrainRequestTimeout and fail, a one-way tell
		// returns once node1 enqueued the message.
		start := time.Now()
		require.NoError(t, node2.TellGrain(ctx, identity, new(testpb.TestSend), WithOneWay()))
		require.Less(t, time.Since(start), DefaultGrainRequestTimeout)

		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("the remote grain did not receive the one-way message")
		}
	})
}

func TestTellGrainOneWayDeadletters(t *testing.T) {
	ctx := context.Background()
	sys, err := NewActorSystem("tell-grain-one-way-deadletters", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	t.Cleanup(func() { _ = sys.Stop(ctx) })

	consumer, err := sys.Subscribe()
	require.NoError(t, err)

	require.NoError(t, sys.RegisterGrainKind(ctx, &MockReceiveFailingGrain{}))
	require.NoError(t, sys.RegisterGrainKind(ctx, &MockPanickingGrain{}))
	failing := newGrainIdentity(NewMockReceiveFailingGrain(), "one-way-deadletter-failing")
	panicking := newGrainIdentity(NewMockPanickingGrain(), "one-way-deadletter-panicking")

	// Err on TestSend, Unhandled on TestReply and a panic on TestSend: none
	// reaches the caller, each is recorded as a deadletter naming the grain.
	require.NoError(t, sys.TellGrain(ctx, failing, new(testpb.TestSend), WithOneWay()))
	require.NoError(t, sys.TellGrain(ctx, failing, new(testpb.TestReply), WithOneWay()))
	require.NoError(t, sys.TellGrain(ctx, panicking, new(testpb.TestSend), WithOneWay()))
	pause.For(time.Second)

	reasons := make(map[string]string)
	for message := range consumer.Iterator() {
		deadletter, ok := message.Payload().(*Deadletter)
		if !ok {
			continue
		}

		require.Equal(t, sys.Name(), deadletter.Receiver().System())
		require.Equal(t, sys.NoSender().Name(), deadletter.Sender().Name())

		key := deadletter.Receiver().Name()
		switch deadletter.Message().(type) {
		case *testpb.TestSend:
			key += ":TestSend"
		case *testpb.TestReply:
			key += ":TestReply"
		}
		reasons[key] = deadletter.Reason()
	}

	require.Len(t, reasons, 3)
	require.Contains(t, reasons[failing.String()+":TestSend"], "failed to process message")
	require.Contains(t, reasons[failing.String()+":TestReply"], "unhandled message type")
	require.Contains(t, reasons[panicking.String()+":TestSend"], "test panic")

	// The acknowledged tell hands the failure to its caller, and a handler
	// reporting a nil error on a one-way message succeeded: neither records
	// anything.
	require.Error(t, sys.TellGrain(ctx, failing, new(testpb.TestSend)))

	succeeding := &MockScriptedGrain{receive: func(gctx *GrainContext) {
		gctx.Err(nil)
	}}
	succeedingID, err := sys.GrainIdentity(ctx, "one-way-deadletter-succeeding", func(context.Context) (Grain, error) {
		return succeeding, nil
	})
	require.NoError(t, err)
	require.NoError(t, sys.TellGrain(ctx, succeedingID, new(testpb.TestSend), WithOneWay()))
	pause.For(500 * time.Millisecond)

	for message := range consumer.Iterator() {
		_, ok := message.Payload().(*Deadletter)
		require.False(t, ok, "neither an acknowledged tell nor a nil error must produce a deadletter")
	}
}

// TestGrainDefaults_ABareSendAfterPassivationKeepsTheKindsOptions passivates a
// grain, then reaches it with a bare AskGrain: the grain it reactivates must
// carry the kind's options, not the package defaults.
func TestGrainDefaults_ABareSendAfterPassivationKeepsTheKindsOptions(t *testing.T) {
	ctx := t.Context()
	testSystem, err := NewActorSystem("testSys",
		WithLogger(log.DiscardLogger),
		WithGrainDefaultOptions[*MockGrain](WithGrainMailboxCapacity(3), WithGrainDeactivateAfter(time.Hour)),
	)
	require.NoError(t, err)
	require.NoError(t, testSystem.Start(ctx))
	t.Cleanup(func() { _ = testSystem.Stop(ctx) })

	// activated with the kind's options and a short idle timeout for the test
	identity, err := GrainOf[*MockGrain](ctx, testSystem, "defaults-grain", WithGrainDeactivateAfter(100*time.Millisecond))
	require.NoError(t, err)
	pid, ok := testSystem.(*actorSystem).grains.Get(identity.String())
	require.True(t, ok)
	require.EqualValues(t, 3, pid.config.capacity)
	require.Equal(t, 100*time.Millisecond, pid.config.deactivateAfter)

	// passivated
	require.Eventually(t, func() bool {
		_, ok := testSystem.(*actorSystem).grains.Get(identity.String())
		return !ok
	}, 5*time.Second, 20*time.Millisecond)

	// reactivated by a bare send: the kind's options, not the package's
	_, err = testSystem.AskGrain(ctx, identity, new(testpb.TestReply), time.Second)
	require.NoError(t, err)
	pid, ok = testSystem.(*actorSystem).grains.Get(identity.String())
	require.True(t, ok)
	require.EqualValues(t, 3, pid.config.capacity)
	require.Equal(t, time.Hour, pid.config.deactivateAfter)
}

// TestGrainDefaults_ARemoteActivationKeepsTheKindsOptions activates a grain on
// another node: the registry record carries no idle timeout, so the kind's
// defaults must supply it on the activating node.
func TestGrainDefaults_ARemoteActivationKeepsTheKindsOptions(t *testing.T) {
	ctx := t.Context()
	grain := NewMockGrain()
	name := "remote-defaults-grain"
	identity := newGrainIdentity(grain, name)
	localPeer := &cluster.Peer{Host: "127.0.0.1", PeersPort: 16800, RemotingPort: 8484}

	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewClient(t)
	node := &discovery.Node{Host: localPeer.Host, PeersPort: localPeer.PeersPort, RemotingPort: localPeer.RemotingPort}
	sys := newClusterReadySystem(rem, cl, node)
	sys.registry.Register(grain)
	WithGrainDefaultOptions[*MockGrain](WithGrainMailboxCapacity(3), WithGrainDeactivateAfter(time.Hour)).Apply(sys)

	// the record another node claimed for this one: options as the wire
	// carries them, with no idle timeout, and a mailbox capacity of its own
	record := internalpb.Grain_builder{
		GrainId:           internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind(), Name: identity.Name()}.Build(),
		Host:              localPeer.Host,
		Port:              int32(localPeer.RemotingPort),
		ActivationTimeout: durationpb.New(DefaultInitTimeout),
		ActivationRetries: DefaultInitMaxRetries,
		MailboxCapacity:   proto.Int64(8),
	}.Build()
	cl.EXPECT().PutGrain(mock.Anything, mock.Anything).Return(nil).Once()

	require.NoError(t, sys.recreateGrain(ctx, record))
	pid, ok := sys.grains.Get(identity.String())
	require.True(t, ok)
	require.EqualValues(t, 8, pid.config.capacity, "the record's option wins")
	require.Equal(t, time.Hour, pid.config.deactivateAfter, "the kind's default fills what the record does not carry")
}

// TestRemoteGrainSend_KeepsTheSentinelsAcrossNodes sends to grains on another
// node. A grain whose bounded mailbox is full answers ErrMailboxFull and a
// grain that does not reply in time answers ErrRequestTimeout: the sentinels
// a local caller gets, instead of an untyped internal error carrying their
// text.
func TestRemoteGrainSend_KeepsTheSentinelsAcrossNodes(t *testing.T) {
	ctx := t.Context()
	srv := startNatsServer(t)
	t.Cleanup(srv.Shutdown)

	systems, _ := startNATsSystems(t, srv.Addr().String(), 2)
	node1, node2 := systems[0], systems[1]
	t.Cleanup(func() {
		_ = node2.Stop(ctx)
		_ = node1.Stop(ctx)
	})

	require.Eventually(t, func() bool {
		peers, err := node1.Peers(ctx, time.Second)
		return err == nil && len(peers) == 1
	}, 10*time.Second, 100*time.Millisecond)

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	blocking := &MockScriptedGrain{receive: func(gctx *GrainContext) {
		entered <- struct{}{}
		<-release
		gctx.NoErr()
	}}

	identity, err := node2.GrainIdentity(ctx, "remote-full-grain", func(context.Context) (Grain, error) {
		return blocking, nil
	}, WithGrainMailboxCapacity(1))

	require.NoError(t, err)

	// the first message parks the grain's turn, the second fills its mailbox
	go func() { _ = node2.TellGrain(ctx, identity, new(testpb.TestSend)) }()
	<-entered
	go func() { _ = node2.TellGrain(ctx, identity, new(testpb.TestSend)) }()
	pid, ok := node2.(*actorSystem).grains.Get(identity.String())
	require.True(t, ok)
	require.Eventually(t, func() bool { return pid.boundedMailbox.Len() == 1 }, 2*time.Second, 10*time.Millisecond)

	_, err = node1.AskGrain(ctx, identity, new(testpb.TestReply), time.Second)
	require.ErrorIs(t, err, gerrors.ErrMailboxFull)
	require.ErrorIs(t, node1.TellGrain(ctx, identity, new(testpb.TestSend)), gerrors.ErrMailboxFull)

	// a second grain on node 2 takes the ask and does not reply in time
	silent, err := node2.GrainIdentity(ctx, "remote-silent-grain", func(context.Context) (Grain, error) {
		return blocking, nil
	})

	require.NoError(t, err)
	_, err = node1.AskGrain(ctx, silent, new(testpb.TestReply), 200*time.Millisecond)
	require.ErrorIs(t, err, gerrors.ErrRequestTimeout)
}

// TestRemoteAskGrain_DecodesAReplyOfAnotherSerializer asks a grain from the
// other nodes of a three-node cluster with a CBOR request it answers with a
// proto reply, and with a proto request it answers with a CBOR reply: each
// reply must be decoded with its own serializer, not the request's.
func TestRemoteAskGrain_DecodesAReplyOfAnotherSerializer(t *testing.T) {
	ctx := t.Context()
	srv := startNatsServer(t)
	t.Cleanup(srv.Shutdown)

	systems, _ := startNATsSystems(t, srv.Addr().String(), 3, withTestSerializables(new(MockCBORRequest), new(MockCBORReply)))
	node1, node2, node3 := systems[0], systems[1], systems[2]
	t.Cleanup(func() {
		_ = node3.Stop(ctx)
		_ = node2.Stop(ctx)
		_ = node1.Stop(ctx)
	})

	require.Eventually(t, func() bool {
		peers, err := node1.Peers(ctx, time.Second)
		return err == nil && len(peers) == 2
	}, 10*time.Second, 100*time.Millisecond)

	grain := &MockScriptedGrain{receive: func(gctx *GrainContext) {
		gctx.Response(crossedSerializerReply(gctx.Message()))
	}}

	identity, err := node3.GrainIdentity(ctx, "crossed-reply-grain", func(context.Context) (Grain, error) {
		return grain, nil
	})

	require.NoError(t, err)

	for _, node := range []ActorSystem{node1, node2} {
		reply, err := node.AskGrain(ctx, identity, &MockCBORRequest{Amount: 1}, time.Second)
		require.NoError(t, err)
		require.IsType(t, new(testpb.Reply), reply)

		reply, err = node.AskGrain(ctx, identity, new(testpb.TestSend), time.Second)
		require.NoError(t, err)
		require.Equal(t, &MockCBORReply{Amount: 1}, reply)
	}
}

func TestAskGrain_HandlerContextEndsWithTheAsk(t *testing.T) {
	cases := []struct {
		name      string
		reentrant bool
	}{
		{"channel path", false},
		{"envelope path", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			system := newRequestTestSystem(t)
			ctx := context.Background()

			seen := make(chan context.Context, 1)
			grain := &MockScriptedGrain{receive: func(gctx *GrainContext) {
				seen <- gctx.Context()

				if _, ok := gctx.Message().(*testpb.TestSend); ok {
					gctx.NoErr()
					return
				}

				gctx.Response(new(testpb.Reply))
			}}

			identity, err := system.GrainIdentity(ctx, "ask-deadline", func(context.Context) (Grain, error) {
				return grain, nil
			})
			require.NoError(t, err)

			if tc.reentrant {
				pid, ok := system.grains.Get(identity.String())
				require.True(t, ok)
				pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 0))
				pid.attachResponseQueue()
			}

			sent := time.Now()
			_, err = system.AskGrain(ctx, identity, new(testpb.TestReply), time.Minute)
			require.NoError(t, err)

			handlerCtx := <-seen
			deadline, ok := handlerCtx.Deadline()
			require.True(t, ok)
			require.False(t, deadline.Before(sent.Add(time.Minute)))
			require.False(t, deadline.After(time.Now().Add(time.Minute)))

			// the context ends with the turn, long before the deadline
			require.Eventually(t, func() bool {
				return errors.Is(handlerCtx.Err(), context.Canceled)
			}, 2*time.Second, 10*time.Millisecond)

			// a tell carries no deadline
			require.NoError(t, system.TellGrain(ctx, identity, new(testpb.TestSend)))
			_, ok = (<-seen).Deadline()
			require.False(t, ok)
		})
	}
}

// TestGrainOf_ReleasesTheRecordOfAStoppedOwnerStillConnected reproduces a
// rolling deploy in a three-node cluster: a grain is active on node A, node B
// reaches it over a duplex connection that stays open, A stops, and a registry
// record naming A is left behind. GrainOf on B must release that record and
// activate the grain on B in the same call, instead of reading the answer of
// the stopped node as a live owner refusing the request.
func TestGrainOf_ReleasesTheRecordOfAStoppedOwnerStillConnected(t *testing.T) {
	ctx := t.Context()
	srv := startNatsServer(t)
	t.Cleanup(srv.Shutdown)

	systems, _ := startNATsSystems(t, srv.Addr().String(), 3)
	nodeA, nodeB, nodeC := systems[0], systems[1], systems[2]
	t.Cleanup(func() {
		_ = nodeC.Stop(ctx)
		_ = nodeB.Stop(ctx)
	})

	require.Eventually(t, func() bool {
		peers, err := nodeA.Peers(ctx, time.Second)
		return err == nil && len(peers) == 2
	}, 10*time.Second, 100*time.Millisecond)

	identity, err := GrainOf[*MockGrain](ctx, nodeA, "stale-owner-grain", WithActivationStrategy(LocalActivation))
	require.NoError(t, err)

	// node B reaches the grain on node A, which opens B's duplex connection to A
	_, err = nodeB.AskGrain(ctx, identity, new(testpb.TestReply), time.Second)
	require.NoError(t, err)

	ownerA := nodeA.(*actorSystem)
	ownerHost, ownerPort := ownerA.Host(), ownerA.Port()
	require.NoError(t, nodeA.Stop(ctx))

	sysB := nodeB.(*actorSystem)
	require.Eventually(t, func() bool {
		alive, err := sysB.isEndpointAlive(ctx, ownerHost, ownerPort)
		return err == nil && !alive
	}, 30*time.Second, 100*time.Millisecond)

	// the record a lost release leaves behind
	staleRecord, err := wireGrain(identity, newGrainConfig(), ownerHost, ownerPort)
	require.NoError(t, err)
	require.NoError(t, sysB.getCluster().PutGrain(ctx, staleRecord))

	// A node that has just stopped can still answer on a connection it serves
	// that its remoting is disabled, an answer that releases the record by
	// itself. Wait until node A no longer answers that way, so GrainOf meets
	// the stopped node the way a later request does.
	require.Eventually(t, func() bool {
		_, err := sysB.remoting.RemoteLookup(ctx, ownerHost, ownerPort, "probe")
		return !errors.Is(err, gerrors.ErrRemotingDisabled)
	}, time.Minute, 100*time.Millisecond)

	_, err = GrainOf[*MockGrain](ctx, nodeB, identity.Name())
	require.NoError(t, err)

	owner, err := sysB.getCluster().GetGrain(ctx, identity.String())
	require.NoError(t, err)
	require.True(t, sysB.isLocalGrainOwner(owner), "the grain must be owned by node B")

	reply, err := nodeB.AskGrain(ctx, identity, new(testpb.TestReply), time.Second)
	require.NoError(t, err)
	require.Equal(t, "received message", reply.(*testpb.Reply).GetContent())
}

// TestAskAndTellGrain_ReachTheGrainPastAStaleOwner runs a three-node cluster
// where the registry record of a grain names node A while A no longer runs the
// grain. AskGrain and TellGrain on node B reach the grain without GrainOf:
//
//   - while A is shutting down, A refuses the message, so B releases the
//     record and the same call delivers the message on B;
//   - once A has stopped and left, B cannot connect to it, so the request
//     never leaves B and the same call delivers the message on B.
func TestAskAndTellGrain_ReachTheGrainPastAStaleOwner(t *testing.T) {
	ctx := t.Context()
	srv := startNatsServer(t)
	t.Cleanup(srv.Shutdown)

	systems, _ := startNATsSystems(t, srv.Addr().String(), 3)
	nodeA, nodeB, nodeC := systems[0], systems[1], systems[2]
	sysA, sysB := nodeA.(*actorSystem), nodeB.(*actorSystem)
	t.Cleanup(func() {
		_ = nodeC.Stop(ctx)
		_ = nodeB.Stop(ctx)
	})

	require.Eventually(t, func() bool {
		peers, err := nodeA.Peers(ctx, time.Second)
		return err == nil && len(peers) == 2
	}, 10*time.Second, 100*time.Millisecond)

	ownerHost, ownerPort := sysA.Host(), sysA.Port()
	sysB.registry.Register(new(MockGrain))

	// staleRecordOnA writes the record of a grain named name that names node A,
	// which does not run the grain.
	staleRecordOnA := func(name string) *GrainIdentity {
		identity := newGrainIdentity(new(MockGrain), name)
		record, err := wireGrain(identity, newGrainConfig(), ownerHost, ownerPort)
		require.NoError(t, err)
		require.NoError(t, sysB.getCluster().PutGrain(ctx, record))
		return identity
	}

	requireOwnedByB := func(identity *GrainIdentity) {
		owner, err := sysB.getCluster().GetGrain(ctx, identity.String())
		require.NoError(t, err)
		require.True(t, sysB.isLocalGrainOwner(owner), "the grain must be owned by node B")
	}

	t.Run("an owner that refuses the message", func(t *testing.T) {
		sysA.shuttingDown.Store(true)
		t.Cleanup(func() { sysA.shuttingDown.Store(false) })

		asked := staleRecordOnA("stale-owner-ask")
		reply, err := nodeB.AskGrain(ctx, asked, new(testpb.TestReply), time.Second)
		require.NoError(t, err)
		require.Equal(t, "received message", reply.(*testpb.Reply).GetContent())
		requireOwnedByB(asked)

		told := staleRecordOnA("stale-owner-tell")
		require.NoError(t, nodeB.TellGrain(ctx, told, new(testpb.TestSend)))
		requireOwnedByB(told)
	})

	t.Run("an owner that has stopped", func(t *testing.T) {
		sysA.shuttingDown.Store(false)
		require.NoError(t, nodeA.Stop(ctx))

		require.Eventually(t, func() bool {
			alive, err := sysB.isEndpointAlive(ctx, ownerHost, ownerPort)
			return err == nil && !alive
		}, 30*time.Second, 100*time.Millisecond)

		// A node that has just stopped can still answer on a connection it
		// serves that its remoting is disabled, an answer that is a refusal.
		// Wait until node A no longer answers that way, so the call meets the
		// stopped node the way a later request does.
		require.Eventually(t, func() bool {
			_, err := sysB.remoting.RemoteLookup(ctx, ownerHost, ownerPort, "probe")
			return !errors.Is(err, gerrors.ErrRemotingDisabled)
		}, time.Minute, 100*time.Millisecond)

		// node A cannot be connected to anymore, so the request never leaves
		// node B and the same call delivers the message on node B
		told := staleRecordOnA("stopped-owner-tell")
		require.NoError(t, nodeB.TellGrain(ctx, told, new(testpb.TestSend)))
		requireOwnedByB(told)

		asked := staleRecordOnA("stopped-owner-ask")
		reply, err := nodeB.AskGrain(ctx, asked, new(testpb.TestReply), time.Second)
		require.NoError(t, err)
		require.Equal(t, "received message", reply.(*testpb.Reply).GetContent())
		requireOwnedByB(asked)
	})
}

func TestGrainRegistryReadRetry(t *testing.T) {
	// a registry read that ran out of its own timeout, as registryReadError reports it
	timedOut := fmt.Errorf("%w: %w", gerrors.ErrClusterRegistryTimeout, context.DeadlineExceeded)
	ownerHost, ownerPort := "192.0.2.41", 18041
	ownerOf := func(identity *GrainIdentity) *internalpb.Grain {
		return internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind(), Name: identity.Name()}.Build(),
			Host:    ownerHost,
			Port:    int32(ownerPort),
		}.Build()
	}

	t.Run("AskGrain reads the registry again after a read timeout", func(t *testing.T) {
		sys, cl, rem, identity := newActivationTestSystem(t, NewMockGrain(), "read-retry-ask", true)
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(nil, timedOut).Once()
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(ownerOf(identity), nil).Once()
		rem.EXPECT().RemoteAskGrain(mock.Anything, ownerHost, ownerPort, mock.Anything, mock.Anything, time.Second).Return(new(testpb.TestReply), nil).Once()

		reply, err := sys.AskGrain(context.Background(), identity, new(testpb.TestReply), time.Second)
		require.NoError(t, err)
		require.IsType(t, new(testpb.TestReply), reply)
	})

	t.Run("TellGrain reads the registry again after a read timeout", func(t *testing.T) {
		sys, cl, rem, identity := newActivationTestSystem(t, NewMockGrain(), "read-retry-tell", true)
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(nil, timedOut).Once()
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(ownerOf(identity), nil).Once()
		rem.EXPECT().RemoteTellGrain(mock.Anything, ownerHost, ownerPort, mock.Anything, mock.Anything).Return(nil).Once()

		require.NoError(t, sys.TellGrain(context.Background(), identity, new(testpb.TestSend)))
	})

	t.Run("a read that keeps timing out fails after the bounded attempts", func(t *testing.T) {
		sys, cl, _, identity := newActivationTestSystem(t, NewMockGrain(), "read-retry-exhausted", true)
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(nil, timedOut).Times(grainRegistryReadAttempts)

		_, err := sys.AskGrain(context.Background(), identity, new(testpb.TestReply), time.Second)
		require.ErrorIs(t, err, gerrors.ErrClusterRegistryTimeout)
	})

	t.Run("a read error other than a timeout is returned at once", func(t *testing.T) {
		sys, cl, _, identity := newActivationTestSystem(t, NewMockGrain(), "read-retry-other-error", true)
		readErr := errors.New("registry unavailable")
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(nil, readErr).Once()

		_, err := sys.AskGrain(context.Background(), identity, new(testpb.TestReply), time.Second)
		require.ErrorIs(t, err, readErr)
	})

	t.Run("a stopping node does not read again", func(t *testing.T) {
		sys, cl, _, identity := newActivationTestSystem(t, NewMockGrain(), "read-retry-stopping", true)
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(nil, timedOut).Once()

		sys.shuttingDown.Store(true)
		defer sys.shuttingDown.Store(false)
		_, err := sys.getGrainRecord(context.Background(), identity.String())
		require.ErrorIs(t, err, gerrors.ErrClusterRegistryTimeout)
	})

	t.Run("the owner lookup of GrainOf reads again after a read timeout", func(t *testing.T) {
		sys, cl, _, identity := newActivationTestSystem(t, NewMockGrain(), "read-retry-owner", true)
		owner := ownerOf(identity)
		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(false, timedOut).Once()
		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(nil, timedOut).Once()
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil).Once()

		got, err := sys.getGrainOwner(context.Background(), identity)
		require.NoError(t, err)
		assert.Same(t, owner, got)
	})

	t.Run("a lost claim reads the winner again after a read timeout", func(t *testing.T) {
		sys, cl, _, identity := newActivationTestSystem(t, NewMockGrain(), "read-retry-claim", true)
		owner := ownerOf(identity)
		mine := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{Value: identity.String(), Kind: identity.Kind(), Name: identity.Name()}.Build(),
			Host:    "127.0.0.1",
			Port:    15000,
		}.Build()
		cl.EXPECT().GrainExists(mock.Anything, identity.String()).Return(true, nil).Once()
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(nil, timedOut).Once()
		cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(owner, nil).Once()

		claimed, got, err := sys.tryClaimGrain(context.Background(), mine)
		require.NoError(t, err)
		assert.False(t, claimed)
		assert.Same(t, owner, got)
	})
}
