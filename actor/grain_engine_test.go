package actor

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v3/discovery"
	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/internal/cluster"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	mockcluster "github.com/tochemey/goakt/v3/mocks/cluster"
	mockremote "github.com/tochemey/goakt/v3/mocks/remote"
	"github.com/tochemey/goakt/v3/remote"
	"github.com/tochemey/goakt/v3/test/data/testpb"
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
	client := &MockRemotingServiceClient{}
	node := &discovery.Node{Host: localPeer.Host, PeersPort: localPeer.PeersPort, RemotingPort: localPeer.RemotingPort}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)

	cl.EXPECT().GetGrain(ctx, identity.String()).Return(nil, cluster.ErrGrainNotFound)
	cl.EXPECT().Members(ctx).Return([]*cluster.Peer{remotePeer, alternatePeer}, nil)
	cl.EXPECT().NextRoundRobinValue(ctx, cluster.GrainsRoundRobinKey).Return(1, nil)
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

func TestGrainIdentity_RemoteActivationOnDifferentPeer_WithBrotliCompression(t *testing.T) {
	ctx := t.Context()
	grain := NewMockGrain()
	name := "remote-grain-brotli"
	identity := newGrainIdentity(grain, name)

	httpClient := &http.Client{}
	remotePeer := &cluster.Peer{Host: "192.0.2.40", PeersPort: 15010, RemotingPort: 16010}
	alternatePeer := &cluster.Peer{Host: "192.0.2.41", PeersPort: 15011, RemotingPort: 16011}
	localPeer := &cluster.Peer{Host: "127.0.0.1", PeersPort: 14010, RemotingPort: 8085}

	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewRemoting(t)
	client := &MockRemotingServiceClient{}
	node := &discovery.Node{Host: localPeer.Host, PeersPort: localPeer.PeersPort, RemotingPort: localPeer.RemotingPort}
	actorSystem := MockSimpleClusterReadyActorSystem(rem, cl, node)

	cl.EXPECT().GetGrain(ctx, identity.String()).Return(nil, cluster.ErrGrainNotFound)
	cl.EXPECT().Members(ctx).Return([]*cluster.Peer{remotePeer, alternatePeer}, nil)
	cl.EXPECT().NextRoundRobinValue(ctx, cluster.GrainsRoundRobinKey).Return(1, nil)
	rem.EXPECT().RemotingServiceClient(remotePeer.Host, remotePeer.RemotingPort).Return(client)
	rem.EXPECT().MaxReadFrameSize().Return(0)
	rem.EXPECT().Compression().Return(remote.BrotliCompression)
	rem.EXPECT().HTTPClient().Return(httpClient)

	got, err := actorSystem.GrainIdentity(ctx, name, func(context.Context) (Grain, error) {
		return grain, nil
	}, WithActivationStrategy(RoundRobinActivation))

	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, identity.String(), got.String())
	require.True(t, client.called)
	require.NotNil(t, client.lastRequest)
	require.Equal(t, identity.String(), client.lastRequest.GetGrain().GetGrainId().GetValue())

	clusterSvc := actorSystem.clusterClient(remotePeer)
	require.NotNil(t, clusterSvc)
}

func TestGrainIdentity_RemoteActivationOnDifferentPeer_WithZstandardCompression(t *testing.T) {
	ctx := t.Context()
	grain := NewMockGrain()
	name := "remote-grain-brotli"
	identity := newGrainIdentity(grain, name)

	httpClient := &http.Client{}
	remotePeer := &cluster.Peer{Host: "192.0.2.40", PeersPort: 15010, RemotingPort: 16010}
	alternatePeer := &cluster.Peer{Host: "192.0.2.41", PeersPort: 15011, RemotingPort: 16011}
	localPeer := &cluster.Peer{Host: "127.0.0.1", PeersPort: 14010, RemotingPort: 8085}

	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewRemoting(t)
	client := &MockRemotingServiceClient{}
	node := &discovery.Node{Host: localPeer.Host, PeersPort: localPeer.PeersPort, RemotingPort: localPeer.RemotingPort}
	actorSystem := MockSimpleClusterReadyActorSystem(rem, cl, node)

	cl.EXPECT().GetGrain(ctx, identity.String()).Return(nil, cluster.ErrGrainNotFound)
	cl.EXPECT().Members(ctx).Return([]*cluster.Peer{remotePeer, alternatePeer}, nil)
	cl.EXPECT().NextRoundRobinValue(ctx, cluster.GrainsRoundRobinKey).Return(1, nil)
	rem.EXPECT().RemotingServiceClient(remotePeer.Host, remotePeer.RemotingPort).Return(client)
	rem.EXPECT().MaxReadFrameSize().Return(0)
	rem.EXPECT().Compression().Return(remote.ZstdCompression)
	rem.EXPECT().HTTPClient().Return(httpClient)

	got, err := actorSystem.GrainIdentity(ctx, name, func(context.Context) (Grain, error) {
		return grain, nil
	}, WithActivationStrategy(RoundRobinActivation))

	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, identity.String(), got.String())
	require.True(t, client.called)
	require.NotNil(t, client.lastRequest)
	require.Equal(t, identity.String(), client.lastRequest.GetGrain().GetGrainId().GetValue())

	clusterSvc := actorSystem.clusterClient(remotePeer)
	require.NotNil(t, clusterSvc)
}

func TestGrainIdentity_RemoteActivationOnDifferentPeer_WithGzipCompression(t *testing.T) {
	ctx := t.Context()
	grain := NewMockGrain()
	name := "remote-grain-brotli"
	identity := newGrainIdentity(grain, name)

	httpClient := &http.Client{}
	remotePeer := &cluster.Peer{Host: "192.0.2.40", PeersPort: 15010, RemotingPort: 16010}
	alternatePeer := &cluster.Peer{Host: "192.0.2.41", PeersPort: 15011, RemotingPort: 16011}
	localPeer := &cluster.Peer{Host: "127.0.0.1", PeersPort: 14010, RemotingPort: 8085}

	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewRemoting(t)
	client := &MockRemotingServiceClient{}
	node := &discovery.Node{Host: localPeer.Host, PeersPort: localPeer.PeersPort, RemotingPort: localPeer.RemotingPort}
	actorSystem := MockSimpleClusterReadyActorSystem(rem, cl, node)

	cl.EXPECT().GetGrain(ctx, identity.String()).Return(nil, cluster.ErrGrainNotFound)
	cl.EXPECT().Members(ctx).Return([]*cluster.Peer{remotePeer, alternatePeer}, nil)
	cl.EXPECT().NextRoundRobinValue(ctx, cluster.GrainsRoundRobinKey).Return(1, nil)
	rem.EXPECT().RemotingServiceClient(remotePeer.Host, remotePeer.RemotingPort).Return(client)
	rem.EXPECT().MaxReadFrameSize().Return(0)
	rem.EXPECT().Compression().Return(remote.GzipCompression)
	rem.EXPECT().HTTPClient().Return(httpClient)

	got, err := actorSystem.GrainIdentity(ctx, name, func(context.Context) (Grain, error) {
		return grain, nil
	}, WithActivationStrategy(RoundRobinActivation))

	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, identity.String(), got.String())
	require.True(t, client.called)
	require.NotNil(t, client.lastRequest)
	require.Equal(t, identity.String(), client.lastRequest.GetGrain().GetGrainId().GetValue())

	clusterSvc := actorSystem.clusterClient(remotePeer)
	require.NotNil(t, clusterSvc)
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
	client := &MockRemotingServiceClient{activateErr: clientErr}
	node := &discovery.Node{Host: localPeer.Host, PeersPort: localPeer.PeersPort, RemotingPort: localPeer.RemotingPort}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)

	cl.EXPECT().GetGrain(ctx, identity.String()).Return(nil, cluster.ErrGrainNotFound)
	cl.EXPECT().Members(ctx).Return([]*cluster.Peer{remotePeer, alternatePeer}, nil)
	cl.EXPECT().NextRoundRobinValue(ctx, cluster.GrainsRoundRobinKey).Return(1, nil)
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
	cl.EXPECT().Members(ctx).Return([]*cluster.Peer{remotePeer, alternatePeer}, nil)
	cl.EXPECT().NextRoundRobinValue(ctx, cluster.GrainsRoundRobinKey).Return(1, nil)

	got, err := sys.GrainIdentity(ctx, name, func(context.Context) (Grain, error) {
		return grain, nil
	}, WithGrainDependencies(&MockFailingDependency{err: failErr}), WithActivationStrategy(RoundRobinActivation))

	require.Error(t, err)
	require.ErrorIs(t, err, failErr)
	require.Nil(t, got)
	rem.AssertNotCalled(t, "RemotingServiceClient", mock.Anything, mock.Anything)
}

func TestFindActivationPeer_ErrorsWhenRoleMissingEverywhere(t *testing.T) {
	ctx := t.Context()
	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewRemoting(t)
	node := &discovery.Node{Host: "127.0.0.1", PeersPort: 14000, RemotingPort: 8080, Roles: []string{"api"}}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)

	role := "analytics"
	cl.EXPECT().Members(ctx).Return([]*cluster.Peer{{Host: "198.51.100.5", Roles: []string{"billing"}}}, nil)

	peer, err := sys.findActivationPeer(ctx, newGrainConfig(WithActivationRole(role)))
	require.Error(t, err)
	require.Nil(t, peer)
	require.ErrorContains(t, err, role)
}

func TestRemoteAskGrain_InjectsContextValues(t *testing.T) {
	ctxKey := struct{}{}
	headerKey := "x-goakt-propagated"
	headerVal := "abc-123"

	ctx := context.WithValue(context.Background(), ctxKey, headerVal)
	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewRemoting(t)
	node := &discovery.Node{Host: "127.0.0.1", PeersPort: 9000, RemotingPort: 9100}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)
	sys.remoteConfig = remote.NewConfig(node.Host, node.RemotingPort, remote.WithContextPropagator(&headerPropagator{headerKey: headerKey, ctxKey: ctxKey}))

	grain := NewMockGrain()
	identity := newGrainIdentity(grain, "remote-grain")

	grainInfo := &internalpb.Grain{
		GrainId: &internalpb.GrainId{Value: identity.String()},
		Host:    "192.0.2.10",
		Port:    16010,
	}

	client := &MockRemotingServiceClient{
		askResponse: &testpb.Reply{Content: "ok"},
	}

	cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(grainInfo, nil)
	rem.EXPECT().RemotingServiceClient(grainInfo.Host, int(grainInfo.Port)).Return(client)

	_, err := sys.AskGrain(ctx, identity, &testpb.TestReply{}, time.Second)
	require.NoError(t, err)
	require.Equal(t, headerVal, client.askHeaders.Get(headerKey))
}

func TestRemoteTellGrain_InjectsContextValues(t *testing.T) {
	ctxKey := struct{}{}
	headerKey := "x-goakt-propagated"
	headerVal := "tell-abc"

	ctx := context.WithValue(context.Background(), ctxKey, headerVal)
	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewRemoting(t)
	node := &discovery.Node{Host: "127.0.0.1", PeersPort: 9001, RemotingPort: 9101}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)
	sys.remoteConfig = remote.NewConfig(node.Host, node.RemotingPort, remote.WithContextPropagator(&headerPropagator{headerKey: headerKey, ctxKey: ctxKey}))

	grain := NewMockGrain()
	identity := newGrainIdentity(grain, "remote-grain-tell")

	grainInfo := &internalpb.Grain{
		GrainId: &internalpb.GrainId{Value: identity.String()},
		Host:    "192.0.2.11",
		Port:    16011,
	}

	client := &MockRemotingServiceClient{}

	cl.EXPECT().GetGrain(mock.Anything, identity.String()).Return(grainInfo, nil)
	rem.EXPECT().RemotingServiceClient(grainInfo.Host, int(grainInfo.Port)).Return(client)

	require.NoError(t, sys.TellGrain(ctx, identity, &testpb.TestSend{}))
	require.Equal(t, headerVal, client.tellHeaders.Get(headerKey))
}

func TestRemoteAskGrain_ExtractsContextValues(t *testing.T) {
	ctxKey := struct{}{}
	headerKey := "x-goakt-propagated"
	headerVal := "inbound-ask"
	host := "127.0.0.1"
	port := 9102

	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewRemoting(t)
	node := &discovery.Node{Host: host, PeersPort: 9002, RemotingPort: port}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)
	sys.remoteConfig = remote.NewConfig(node.Host, node.RemotingPort, remote.WithContextPropagator(&headerPropagator{headerKey: headerKey, ctxKey: ctxKey}))
	sys.remotingEnabled.Store(true)

	grain := &contextEchoGrain{key: ctxKey}
	sys.registry.Register(grain)
	identity := newGrainIdentity(grain, "local-grain")
	pid := newGrainPID(identity, grain, sys, newGrainConfig())
	pid.activated.Store(true)
	sys.grains.Set(identity.String(), pid)

	msg, _ := anypb.New(&testpb.TestReply{})
	req := connect.NewRequest(&internalpb.RemoteAskGrainRequest{
		Grain: &internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: identity.String()},
			Host:    host,
			Port:    int32(port),
		},
		Message:        msg,
		RequestTimeout: durationpb.New(2 * time.Second),
	})
	req.Header().Set(headerKey, headerVal)

	_, err := sys.RemoteAskGrain(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, headerVal, grain.seen)
}

func TestRemoteAskGrain_ContextExtractionError(t *testing.T) {
	extractErr := errors.New("extract failed")
	host := "127.0.0.1"
	port := 9104

	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewRemoting(t)
	node := &discovery.Node{Host: host, PeersPort: 9004, RemotingPort: port}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)
	sys.remoteConfig = remote.NewConfig(node.Host, node.RemotingPort, remote.WithContextPropagator(&MockFailingContextPropagator{err: extractErr}))
	sys.remotingEnabled.Store(true)

	identity := newGrainIdentity(NewMockGrain(), "extract-error-grain")
	msg, _ := anypb.New(&testpb.TestReply{})
	req := connect.NewRequest(&internalpb.RemoteAskGrainRequest{
		Grain: &internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: identity.String()},
			Host:    host,
			Port:    int32(port),
		},
		Message:        msg,
		RequestTimeout: durationpb.New(time.Second),
	})

	_, err := sys.RemoteAskGrain(context.Background(), req)
	require.Error(t, err)

	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	require.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
	require.ErrorIs(t, connectErr, extractErr)
}

func TestRemoteAskGrain_InvalidGrainIdentity(t *testing.T) {
	host := "127.0.0.1"
	port := 9200

	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewRemoting(t)
	node := &discovery.Node{Host: host, PeersPort: 9020, RemotingPort: port}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)
	sys.remotingEnabled.Store(true)

	invalidIdentity := "actor.MockGrain/invalid name"
	msg, _ := anypb.New(&testpb.TestReply{})
	req := connect.NewRequest(&internalpb.RemoteAskGrainRequest{
		Grain: &internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: invalidIdentity},
			Host:    host,
			Port:    int32(port),
		},
		Message:        msg,
		RequestTimeout: durationpb.New(time.Second),
	})

	_, err := sys.RemoteAskGrain(context.Background(), req)
	require.Error(t, err)

	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	require.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
	require.ErrorIs(t, connectErr, gerrors.ErrInvalidGrainIdentity)
}

func TestRemoteAskGrain_LocalSendError(t *testing.T) {
	host := "127.0.0.1"
	port := 9201

	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewRemoting(t)
	node := &discovery.Node{Host: host, PeersPort: 9021, RemotingPort: port}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)
	sys.remotingEnabled.Store(true)

	grain := NewMockGrainReceiveFailure()
	sys.registry.Register(grain)
	identity := newGrainIdentity(grain, "receive-failure")
	pid := newGrainPID(identity, grain, sys, newGrainConfig())
	pid.activated.Store(true)
	sys.grains.Set(identity.String(), pid)

	msg, _ := anypb.New(&testpb.TestSend{})
	req := connect.NewRequest(&internalpb.RemoteAskGrainRequest{
		Grain: &internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: identity.String()},
			Host:    host,
			Port:    int32(port),
		},
		Message:        msg,
		RequestTimeout: durationpb.New(time.Second),
	})

	_, err := sys.RemoteAskGrain(context.Background(), req)
	require.Error(t, err)

	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	require.Equal(t, connect.CodeInternal, connectErr.Code())
	require.Contains(t, connectErr.Message(), "failed to process message")
}

func TestRemoteTellGrain_ExtractsContextValues(t *testing.T) {
	ctxKey := struct{}{}
	headerKey := "x-goakt-propagated"
	headerVal := "inbound-tell"
	host := "127.0.0.1"
	port := 9103

	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewRemoting(t)
	node := &discovery.Node{Host: host, PeersPort: 9003, RemotingPort: port}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)
	sys.remoteConfig = remote.NewConfig(node.Host, node.RemotingPort, remote.WithContextPropagator(&headerPropagator{headerKey: headerKey, ctxKey: ctxKey}))
	sys.remotingEnabled.Store(true)

	grain := &contextEchoGrain{key: ctxKey}
	sys.registry.Register(grain)
	identity := newGrainIdentity(grain, "local-grain-tell")
	pid := newGrainPID(identity, grain, sys, newGrainConfig())
	pid.activated.Store(true)
	sys.grains.Set(identity.String(), pid)

	msg, _ := anypb.New(&testpb.TestSend{})
	req := connect.NewRequest(&internalpb.RemoteTellGrainRequest{
		Grain: &internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: identity.String()},
			Host:    host,
			Port:    int32(port),
		},
		Message: msg,
	})
	req.Header().Set(headerKey, headerVal)

	_, err := sys.RemoteTellGrain(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, headerVal, grain.seen)
}

func TestRemoteTellGrain_ContextExtractionError(t *testing.T) {
	extractErr := errors.New("extract failed")
	host := "127.0.0.1"
	port := 9202

	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewRemoting(t)
	node := &discovery.Node{Host: host, PeersPort: 9022, RemotingPort: port}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)
	sys.remoteConfig = remote.NewConfig(node.Host, node.RemotingPort, remote.WithContextPropagator(&MockFailingContextPropagator{err: extractErr}))
	sys.remotingEnabled.Store(true)

	grain := NewMockGrain()
	identity := newGrainIdentity(grain, "tell-extract-error")
	pid := newGrainPID(identity, grain, sys, newGrainConfig())
	pid.activated.Store(true)
	sys.registry.Register(grain)
	sys.grains.Set(identity.String(), pid)

	msg, _ := anypb.New(&testpb.TestSend{})
	req := connect.NewRequest(&internalpb.RemoteTellGrainRequest{
		Grain: &internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: identity.String()},
			Host:    host,
			Port:    int32(port),
		},
		Message: msg,
	})

	_, err := sys.RemoteTellGrain(context.Background(), req)
	require.Error(t, err)

	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	require.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
	require.ErrorIs(t, connectErr, extractErr)
}

func TestRemoteTellGrain_InvalidGrainIdentity(t *testing.T) {
	host := "127.0.0.1"
	port := 9203

	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewRemoting(t)
	node := &discovery.Node{Host: host, PeersPort: 9023, RemotingPort: port}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)
	sys.remotingEnabled.Store(true)

	invalidIdentity := "actor.MockGrain/invalid name"
	msg, _ := anypb.New(&testpb.TestSend{})
	req := connect.NewRequest(&internalpb.RemoteTellGrainRequest{
		Grain: &internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: invalidIdentity},
			Host:    host,
			Port:    int32(port),
		},
		Message: msg,
	})

	_, err := sys.RemoteTellGrain(context.Background(), req)
	require.Error(t, err)

	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	require.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
	require.ErrorIs(t, connectErr, gerrors.ErrInvalidGrainIdentity)
}

func TestRemoteTellGrain_LocalSendError(t *testing.T) {
	host := "127.0.0.1"
	port := 9204

	cl := mockcluster.NewCluster(t)
	rem := mockremote.NewRemoting(t)
	node := &discovery.Node{Host: host, PeersPort: 9024, RemotingPort: port}
	sys := MockSimpleClusterReadyActorSystem(rem, cl, node)
	sys.remotingEnabled.Store(true)

	grain := NewMockGrainReceiveFailure()
	sys.registry.Register(grain)
	identity := newGrainIdentity(grain, "tell-receive-failure")
	pid := newGrainPID(identity, grain, sys, newGrainConfig())
	pid.activated.Store(true)
	sys.grains.Set(identity.String(), pid)

	msg, _ := anypb.New(&testpb.TestSend{})
	req := connect.NewRequest(&internalpb.RemoteTellGrainRequest{
		Grain: &internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: identity.String()},
			Host:    host,
			Port:    int32(port),
		},
		Message: msg,
	})

	_, err := sys.RemoteTellGrain(context.Background(), req)
	require.Error(t, err)

	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	require.Equal(t, connect.CodeInternal, connectErr.Code())
	require.Contains(t, connectErr.Message(), "failed to process message")
}
