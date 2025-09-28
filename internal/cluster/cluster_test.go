/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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

package cluster

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kapetan-io/tackle/autotls"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/discovery"
	natsdiscovery "github.com/tochemey/goakt/v3/discovery/nats"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/pause"
	"github.com/tochemey/goakt/v3/log"
	discoverymock "github.com/tochemey/goakt/v3/mocks/discovery"
	gtls "github.com/tochemey/goakt/v3/tls"
)

func TestStartStop(t *testing.T) {
	cluster, _, ctx := setupCluster(t)

	assert.True(t, cluster.IsRunning())

	peers, err := cluster.Peers(ctx)
	require.NoError(t, err)
	assert.Len(t, peers, 0)
}

func TestNotRunningReturnsErrEngineNotRunning(t *testing.T) {
	ctx := context.Background()

	provider := new(discoverymock.Provider)
	node := &discovery.Node{
		Name:          "test-node",
		Host:          "127.0.0.1",
		DiscoveryPort: 0,
		PeersPort:     0,
		RemotingPort:  0,
	}

	cluster := New("test", provider, node, WithLogger(log.DiscardLogger))
	require.NotNil(t, cluster)

	assert.False(t, cluster.IsRunning())
	assert.False(t, cluster.IsLeader(ctx))

	actor := &internalpb.Actor{Address: &goaktpb.Address{Name: "actor"}}
	require.ErrorIs(t, cluster.PutActor(ctx, actor), ErrEngineNotRunning)

	_, err := cluster.GetActor(ctx, actor.GetAddress().GetName())
	require.ErrorIs(t, err, ErrEngineNotRunning)

	require.ErrorIs(t, cluster.RemoveActor(ctx, actor.GetAddress().GetName()), ErrEngineNotRunning)

	actorExists, err := cluster.ActorExists(ctx, actor.GetAddress().GetName())
	require.False(t, actorExists)
	require.ErrorIs(t, err, ErrEngineNotRunning)

	actors, err := cluster.Actors(ctx, time.Second)
	require.ErrorIs(t, err, ErrEngineNotRunning)
	require.Nil(t, actors)

	grain := &internalpb.Grain{GrainId: &internalpb.GrainId{Value: "grain-id"}}
	require.ErrorIs(t, cluster.PutGrain(ctx, grain), ErrEngineNotRunning)

	_, err = cluster.GetGrain(ctx, grain.GetGrainId().GetValue())
	require.ErrorIs(t, err, ErrEngineNotRunning)

	grainExists, err := cluster.GrainExists(ctx, grain.GetGrainId().GetValue())
	require.False(t, grainExists)
	require.ErrorIs(t, err, ErrEngineNotRunning)

	require.ErrorIs(t, cluster.RemoveGrain(ctx, grain.GetGrainId().GetValue()), ErrEngineNotRunning)

	grains, err := cluster.Grains(ctx, time.Second)
	require.ErrorIs(t, err, ErrEngineNotRunning)
	require.Nil(t, grains)

	require.ErrorIs(t, cluster.PutKind(ctx, "some-kind"), ErrEngineNotRunning)

	kind, err := cluster.LookupKind(ctx, "some-kind")
	require.Equal(t, "", kind)
	require.ErrorIs(t, err, ErrEngineNotRunning)

	require.ErrorIs(t, cluster.RemoveKind(ctx, "some-kind"), ErrEngineNotRunning)

	require.ErrorIs(t, cluster.PutJobKey(ctx, "job-id", []byte("metadata")), ErrEngineNotRunning)

	_, err = cluster.JobKey(ctx, "job-id")
	require.ErrorIs(t, err, ErrEngineNotRunning)

	require.ErrorIs(t, cluster.DeleteJobKey(ctx, "job-id"), ErrEngineNotRunning)

	peers, err := cluster.Peers(ctx)
	require.ErrorIs(t, err, ErrEngineNotRunning)
	require.Nil(t, peers)

	require.Zero(t, cluster.GetPartition(actor.GetAddress().GetName()))

	require.NoError(t, cluster.Stop(ctx))

	provider.AssertExpectations(t)
}

func TestIsLeader(t *testing.T) {
	cluster, _, ctx := setupCluster(t)

	require.Eventually(t, func() bool {
		return cluster.IsLeader(ctx)
	}, 5*time.Second, 50*time.Millisecond)

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, cluster.Stop(stopCtx))
	assert.False(t, cluster.IsLeader(ctx))
}

func TestSingleNode(t *testing.T) {
	cluster, _, ctx := setupCluster(t)

	actorName := uuid.NewString()
	actor := &internalpb.Actor{Address: &goaktpb.Address{Name: actorName}}
	require.NoError(t, cluster.PutActor(ctx, actor))

	exists, err := cluster.ActorExists(ctx, actorName)
	require.NoError(t, err)
	assert.True(t, exists)

	storedActor, err := cluster.GetActor(ctx, actorName)
	require.NoError(t, err)
	assert.True(t, proto.Equal(actor, storedActor))

	list, err := cluster.Actors(ctx, time.Second)
	require.NoError(t, err)
	assert.NotEmpty(t, list)

	partition := cluster.GetPartition(actorName)
	assert.GreaterOrEqual(t, partition, uint64(0))

	require.NoError(t, cluster.RemoveActor(ctx, actorName))

	exists, err = cluster.ActorExists(ctx, actorName)
	require.NoError(t, err)
	assert.False(t, exists)

	_, err = cluster.GetActor(ctx, actorName)
	assert.ErrorIs(t, err, ErrActorNotFound)

	grainID := &internalpb.GrainId{Kind: "test", Name: "grain", Value: uuid.NewString()}
	grain := &internalpb.Grain{GrainId: grainID}
	require.NoError(t, cluster.PutGrain(ctx, grain))

	grainExists, err := cluster.GrainExists(ctx, grainID.GetValue())
	require.NoError(t, err)
	assert.True(t, grainExists)

	storedGrain, err := cluster.GetGrain(ctx, grainID.GetValue())
	require.NoError(t, err)
	assert.True(t, proto.Equal(grain, storedGrain))

	grains, err := cluster.Grains(ctx, time.Second)
	require.NoError(t, err)
	assert.NotEmpty(t, grains)

	require.NoError(t, cluster.RemoveGrain(ctx, grainID.GetValue()))

	grainExists, err = cluster.GrainExists(ctx, grainID.GetValue())
	require.NoError(t, err)
	assert.False(t, grainExists)

	_, err = cluster.GetGrain(ctx, grainID.GetValue())
	assert.ErrorIs(t, err, ErrGrainNotFound)

	kind := "singleton-kind"
	require.NoError(t, cluster.PutKind(ctx, kind))

	actualKind, err := cluster.LookupKind(ctx, kind)
	require.NoError(t, err)
	assert.Equal(t, kind, actualKind)

	require.NoError(t, cluster.RemoveKind(ctx, kind))

	actualKind, err = cluster.LookupKind(ctx, kind)
	require.NoError(t, err)
	assert.Empty(t, actualKind)

	jobID := uuid.NewString()
	metadata := []byte("job-metadata")
	require.NoError(t, cluster.PutJobKey(ctx, jobID, metadata))

	storedMetadata, err := cluster.JobKey(ctx, jobID)
	require.NoError(t, err)
	assert.Equal(t, metadata, storedMetadata)

	require.NoError(t, cluster.DeleteJobKey(ctx, jobID))

	_, err = cluster.JobKey(ctx, jobID)
	assert.Error(t, err)
}

func TestMultipleNodes(t *testing.T) {
	t.Run("Without TLS", func(t *testing.T) {
		ctx := context.TODO()

		srv := startClusterNatsServer(t)

		node1 := startClusterHarness(t, srv.Addr().String(), nil)
		pause.For(2 * time.Second)

		node2 := startClusterHarness(t, srv.Addr().String(), nil)
		node2Addr := net.JoinHostPort(node2.node.Host, strconv.Itoa(node2.node.PeersPort))
		pause.For(time.Second)

		node3 := startClusterHarness(t, srv.Addr().String(), nil)
		pause.For(time.Second)

		events := collectEvents(node1.Events(), time.Second)
		require.Len(t, events, 2)
		event := events[0]
		msg, err := event.Payload.UnmarshalNew()
		require.NoError(t, err)
		nodeJoined, ok := msg.(*goaktpb.NodeJoined)
		require.True(t, ok)
		require.NotNil(t, nodeJoined)
		require.Equal(t, node2Addr, nodeJoined.GetAddress())
		peers, err := node1.Peers(ctx)
		require.NoError(t, err)
		require.Len(t, peers, 2)
		require.Equal(t, node2Addr, net.JoinHostPort(peers[0].Host, strconv.Itoa(peers[0].PeersPort)))

		actorName := uuid.NewString()
		actor := &internalpb.Actor{Address: &goaktpb.Address{Name: actorName}, Type: "actorKind"}
		require.NoError(t, node2.PutActor(ctx, actor))
		pause.For(time.Second)

		identity := "grainKind/grainName"
		grain := &internalpb.Grain{
			GrainId: &internalpb.GrainId{Kind: "grainKind", Name: "grainName", Value: identity},
			Host:    node2.node.Host,
			Port:    int32(node2.node.RemotingPort),
		}
		require.NoError(t, node2.PutGrain(ctx, grain))

		actual, err := node1.GetActor(ctx, actorName)
		require.NoError(t, err)
		require.True(t, proto.Equal(actor, actual))

		actualGrain, err := node1.GetGrain(ctx, identity)
		require.NoError(t, err)
		require.True(t, proto.Equal(grain, actualGrain))

		actual, err = node3.GetActor(ctx, actorName)
		require.NoError(t, err)
		require.True(t, proto.Equal(actor, actual))

		actualGrain, err = node3.GetGrain(ctx, identity)
		require.NoError(t, err)
		require.True(t, proto.Equal(grain, actualGrain))

		actorName2 := uuid.NewString()
		actor2 := &internalpb.Actor{Address: &goaktpb.Address{Name: actorName2}, Type: "actorKind"}
		require.NoError(t, node1.PutActor(ctx, actor2))
		pause.For(time.Second)

		actors, err := node1.Actors(ctx, time.Second)
		require.NoError(t, err)
		require.Len(t, actors, 2)

		grains, err := node1.Grains(ctx, time.Second)
		require.NoError(t, err)
		require.Len(t, grains, 1)

		actors, err = node3.Actors(ctx, time.Second)
		require.NoError(t, err)
		require.Len(t, actors, 2)

		actors, err = node2.Actors(ctx, time.Second)
		require.NoError(t, err)
		require.Len(t, actors, 2)

		require.NoError(t, node2.Stop(ctx))
		pause.For(time.Second)

		events = collectEvents(node1.Events(), time.Second)
		require.Len(t, events, 1)
		event = events[0]
		msg, err = event.Payload.UnmarshalNew()
		require.NoError(t, err)
		nodeLeft, ok := msg.(*goaktpb.NodeLeft)
		require.True(t, ok)
		require.NotNil(t, nodeLeft)
		require.Equal(t, node2Addr, nodeLeft.GetAddress())

		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))
		require.NoError(t, node1.CloseProvider())
		require.NoError(t, node2.CloseProvider())
		require.NoError(t, node3.CloseProvider())
		srv.Shutdown()
	})

	t.Run("With TLS", func(t *testing.T) {
		ctx := context.TODO()

		serverConf := &autotls.Config{
			CaFile:           "../../test/data/certs/ca.cert",
			CertFile:         "../../test/data/certs/auto.pem",
			KeyFile:          "../../test/data/certs/auto.key",
			ClientAuthCaFile: "../../test/data/certs/client-auth-ca.pem",
			ClientAuth:       tls.RequireAndVerifyClientCert,
		}
		require.NoError(t, autotls.Setup(serverConf))

		clientConf := &autotls.Config{
			CertFile:           "../../test/data/certs/client-auth.pem",
			KeyFile:            "../../test/data/certs/client-auth.key",
			InsecureSkipVerify: true,
		}
		require.NoError(t, autotls.Setup(clientConf))

		srv := startClusterNatsServer(t)
		tlsInfo := &gtls.Info{ServerConfig: serverConf.ServerTLS, ClientConfig: clientConf.ClientTLS}

		node1 := startClusterHarness(t, srv.Addr().String(), tlsInfo)
		pause.For(2 * time.Second)

		node2 := startClusterHarness(t, srv.Addr().String(), tlsInfo)
		node2Addr := net.JoinHostPort(node2.node.Host, strconv.Itoa(node2.node.PeersPort))
		pause.For(time.Second)

		node3 := startClusterHarness(t, srv.Addr().String(), tlsInfo)
		pause.For(time.Second)

		events := collectEvents(node1.Events(), time.Second)
		require.Len(t, events, 2)
		event := events[0]
		msg, err := event.Payload.UnmarshalNew()
		require.NoError(t, err)
		nodeJoined, ok := msg.(*goaktpb.NodeJoined)
		require.True(t, ok)
		require.NotNil(t, nodeJoined)
		require.Equal(t, node2Addr, nodeJoined.GetAddress())
		peers, err := node1.Peers(ctx)
		require.NoError(t, err)
		require.Len(t, peers, 2)
		require.Equal(t, node2Addr, net.JoinHostPort(peers[0].Host, strconv.Itoa(peers[0].PeersPort)))

		actorName := uuid.NewString()
		actor := &internalpb.Actor{Address: &goaktpb.Address{Name: actorName}, Type: "actorKind"}
		require.NoError(t, node2.PutActor(ctx, actor))

		identity := "grainKind/grainName"
		grain := &internalpb.Grain{
			GrainId: &internalpb.GrainId{Kind: "grainKind", Name: "grainName", Value: identity},
			Host:    node2.node.Host,
			Port:    int32(node2.node.RemotingPort),
		}
		require.NoError(t, node2.PutGrain(ctx, grain))

		actual, err := node1.GetActor(ctx, actorName)
		require.NoError(t, err)
		require.True(t, proto.Equal(actor, actual))

		actualGrain, err := node1.GetGrain(ctx, identity)
		require.NoError(t, err)
		require.True(t, proto.Equal(grain, actualGrain))

		actual, err = node3.GetActor(ctx, actorName)
		require.NoError(t, err)
		require.True(t, proto.Equal(actor, actual))

		actualGrain, err = node3.GetGrain(ctx, identity)
		require.NoError(t, err)
		require.True(t, proto.Equal(grain, actualGrain))

		actorName2 := uuid.NewString()
		actor2 := &internalpb.Actor{Address: &goaktpb.Address{Name: actorName2}, Type: "actorKind"}
		require.NoError(t, node1.PutActor(ctx, actor2))
		pause.For(time.Second)

		actors, err := node1.Actors(ctx, time.Second)
		require.NoError(t, err)
		require.Len(t, actors, 2)

		grains, err := node1.Grains(ctx, time.Second)
		require.NoError(t, err)
		require.Len(t, grains, 1)

		require.NoError(t, node2.Stop(ctx))
		pause.For(time.Second)

		events = collectEvents(node1.Events(), time.Second)
		require.Len(t, events, 1)
		event = events[0]
		msg, err = event.Payload.UnmarshalNew()
		require.NoError(t, err)
		nodeLeft, ok := msg.(*goaktpb.NodeLeft)
		require.True(t, ok)
		require.NotNil(t, nodeLeft)
		require.Equal(t, node2Addr, nodeLeft.GetAddress())

		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))
		require.NoError(t, node1.CloseProvider())
		require.NoError(t, node2.CloseProvider())
		require.NoError(t, node3.CloseProvider())
		srv.Shutdown()
	})
}

func setupCluster(t *testing.T) (Cluster, *discovery.Node, context.Context) {
	t.Helper()

	ctx := context.Background()

	ports := dynaport.Get(3)
	gossipPort := ports[0]
	peersPort := ports[1]
	remotingPort := ports[2]

	addrs := []string{fmt.Sprintf("127.0.0.1:%d", gossipPort)}

	provider := new(discoverymock.Provider)
	provider.EXPECT().ID().Return("testDisco")
	provider.EXPECT().Initialize().Return(nil)
	provider.EXPECT().Register().Return(nil)
	provider.EXPECT().Deregister().Return(nil)
	provider.EXPECT().DiscoverPeers().Return(addrs, nil)
	provider.EXPECT().Close().Return(nil)

	host := "127.0.0.1"
	node := &discovery.Node{
		Name:          host,
		Host:          host,
		DiscoveryPort: gossipPort,
		PeersPort:     peersPort,
		RemotingPort:  remotingPort,
	}

	cluster := New("test", provider, node, WithLogger(log.DiscardLogger))
	require.NotNil(t, cluster)

	require.NoError(t, cluster.Start(ctx))

	t.Cleanup(func() {
		pause.For(200 * time.Millisecond)
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		require.NoError(t, cluster.Stop(stopCtx))
		provider.AssertExpectations(t)
	})

	return cluster, node, ctx
}

type clusterHarness struct {
	Cluster
	node      *discovery.Node
	provider  discovery.Provider
	closeOnce sync.Once
	closeErr  error
}

func (h *clusterHarness) CloseProvider() error {
	h.closeOnce.Do(func() {
		h.closeErr = h.provider.Close()
	})
	return h.closeErr
}

func startClusterHarness(t *testing.T, serverAddr string, tlsInfo *gtls.Info) *clusterHarness {
	t.Helper()

	ctx := context.TODO()
	ports := dynaport.Get(3)
	gossipPort := ports[0]
	clusterPort := ports[1]
	remotingPort := ports[2]

	const (
		host            = "127.0.0.1"
		actorSystemName = "testSystem"
		natsSubject     = "some-subject"
	)

	config := natsdiscovery.Config{
		NatsServer:    fmt.Sprintf("nats://%s", serverAddr),
		NatsSubject:   natsSubject,
		Host:          host,
		DiscoveryPort: gossipPort,
	}

	node := &discovery.Node{
		Name:          host,
		Host:          host,
		DiscoveryPort: gossipPort,
		PeersPort:     clusterPort,
		RemotingPort:  remotingPort,
	}

	provider := natsdiscovery.NewDiscovery(&config)

	opts := []ConfigOption{WithLogger(log.DiscardLogger)}
	if tlsInfo != nil {
		info := &gtls.Info{}
		if tlsInfo.ServerConfig != nil {
			info.ServerConfig = tlsInfo.ServerConfig.Clone()
		}
		if tlsInfo.ClientConfig != nil {
			info.ClientConfig = tlsInfo.ClientConfig.Clone()
		}
		opts = append(opts, WithTLS(info))
	}

	cluster := New(actorSystemName, provider, node, opts...)
	require.NotNil(t, cluster)
	require.NoError(t, cluster.Start(ctx))

	harness := &clusterHarness{
		Cluster:  cluster,
		node:     node,
		provider: provider,
	}

	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = harness.Stop(stopCtx)
		if err := harness.CloseProvider(); err != nil {
			t.Logf("failed to close discovery provider: %v", err)
		}
	})

	return harness
}

func collectEvents(ch <-chan *Event, wait time.Duration) []*Event {
	deadline := time.After(wait)
	events := make([]*Event, 0)
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, evt)
		case <-deadline:
			return events
		}
	}
}

func startClusterNatsServer(t *testing.T) *natsserver.Server {
	t.Helper()

	serv, err := natsserver.NewServer(&natsserver.Options{Host: "127.0.0.1", Port: -1})
	require.NoError(t, err)

	ready := make(chan bool)
	go func() {
		ready <- true
		serv.Start()
	}()
	<-ready

	if !serv.ReadyForConnections(2 * time.Second) {
		t.Fatalf("nats-io server failed to start")
	}

	return serv
}
