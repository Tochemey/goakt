package actor

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/discovery"
	"github.com/tochemey/goakt/v3/internal/cluster"
	mockcluster "github.com/tochemey/goakt/v3/mocks/cluster"
	mockremote "github.com/tochemey/goakt/v3/mocks/remote"
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
	rem := mockremote.NewRemoting(t)
	client := &RemotingServiceClientStub{}
	node := &discovery.Node{Host: localPeer.Host, PeersPort: localPeer.PeersPort, RemotingPort: localPeer.RemotingPort}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)

	cl.EXPECT().GetGrain(ctx, identity.String()).Return(nil, cluster.ErrGrainNotFound)
	cl.EXPECT().Peers(ctx).Return([]*cluster.Peer{remotePeer, alternatePeer}, nil)
	rem.EXPECT().RemotingServiceClient(remotePeer.Host, remotePeer.RemotingPort).Return(client)

	got, err := sys.GrainIdentity(ctx, name, func(context.Context) (Grain, error) {
		return grain, nil
	}, WithActivationStrategy(RoundRobinActivation))

	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, identity.String(), got.String())
	require.True(t, client.called)
	require.NotNil(t, client.lastRequest)
	require.Equal(t, identity.String(), client.lastRequest.GetGrain().GetGrainId().GetValue())
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
	rem := mockremote.NewRemoting(t)
	clientErr := errors.New("remote activate failed")
	client := &RemotingServiceClientStub{activateErr: clientErr}
	node := &discovery.Node{Host: localPeer.Host, PeersPort: localPeer.PeersPort, RemotingPort: localPeer.RemotingPort}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)

	cl.EXPECT().GetGrain(ctx, identity.String()).Return(nil, cluster.ErrGrainNotFound)
	cl.EXPECT().Peers(ctx).Return([]*cluster.Peer{remotePeer, alternatePeer}, nil)
	rem.EXPECT().RemotingServiceClient(remotePeer.Host, remotePeer.RemotingPort).Return(client)

	got, err := sys.GrainIdentity(ctx, name, func(context.Context) (Grain, error) {
		return grain, nil
	}, WithActivationStrategy(RoundRobinActivation))

	require.Error(t, err)
	require.ErrorIs(t, err, clientErr)
	require.Nil(t, got)
	require.True(t, client.called)
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
	rem := mockremote.NewRemoting(t)
	node := &discovery.Node{Host: localPeer.Host, PeersPort: localPeer.PeersPort, RemotingPort: localPeer.RemotingPort}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)

	cl.EXPECT().GetGrain(ctx, identity.String()).Return(nil, cluster.ErrGrainNotFound)
	cl.EXPECT().Peers(ctx).Return([]*cluster.Peer{remotePeer, alternatePeer}, nil)

	got, err := sys.GrainIdentity(ctx, name, func(context.Context) (Grain, error) {
		return grain, nil
	}, WithGrainDependencies(&MockFailingDependency{err: failErr}), WithActivationStrategy(RoundRobinActivation))

	require.Error(t, err)
	require.ErrorIs(t, err, failErr)
	require.Nil(t, got)
	rem.AssertNotCalled(t, "RemotingServiceClient", mock.Anything, mock.Anything)
}

func TestFindActivationPeer_AllowsLocalRoleFallback(t *testing.T) {
	ctx := t.Context()
	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewRemoting(t)
	node := &discovery.Node{Host: "127.0.0.1", PeersPort: 14000, RemotingPort: 8080, Roles: []string{"payments"}}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)

	cl.EXPECT().Peers(ctx).Return([]*cluster.Peer{{Host: "198.51.100.5", Roles: []string{"billing"}}}, nil)

	peer, err := sys.findActivationPeer(ctx, newGrainConfig(WithActivationRole("payments")))
	require.NoError(t, err)
	require.Nil(t, peer)
}

func TestFindActivationPeer_ErrorsWhenRoleMissingEverywhere(t *testing.T) {
	ctx := t.Context()
	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewRemoting(t)
	node := &discovery.Node{Host: "127.0.0.1", PeersPort: 14000, RemotingPort: 8080, Roles: []string{"api"}}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)

	role := "analytics"
	cl.EXPECT().Peers(ctx).Return([]*cluster.Peer{{Host: "198.51.100.5", Roles: []string{"billing"}}}, nil)

	peer, err := sys.findActivationPeer(ctx, newGrainConfig(WithActivationRole(role)))
	require.Error(t, err)
	require.Nil(t, peer)
	require.ErrorContains(t, err, role)
}
