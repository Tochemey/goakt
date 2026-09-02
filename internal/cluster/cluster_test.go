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

package cluster

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kapetan-io/tackle/autotls"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/olric"
	oconfig "github.com/tochemey/olric/config"
	"github.com/tochemey/olric/events"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/discovery"
	"github.com/tochemey/goakt/v4/discovery/nats"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	dynaport "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	mocksdiscovery "github.com/tochemey/goakt/v4/mocks/discovery"
	gtls "github.com/tochemey/goakt/v4/tls"
)

func TestNotRunningReturnsErrEngineNotRunning(t *testing.T) {
	ctx := context.Background()

	provider := new(mocksdiscovery.Provider)
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

	addr := address.New("actor", "system", "127.0.0.1", 0)
	actor := internalpb.Actor_builder{Address: addr.String()}.Build()
	require.ErrorIs(t, cluster.PutActor(ctx, actor), ErrEngineNotRunning)

	_, err := cluster.GetActor(ctx, "actor")
	require.ErrorIs(t, err, ErrEngineNotRunning)

	require.ErrorIs(t, cluster.RemoveActor(ctx, "actor"), ErrEngineNotRunning)

	actorExists, err := cluster.ActorExists(ctx, "actor")
	require.False(t, actorExists)
	require.ErrorIs(t, err, ErrEngineNotRunning)

	actors, err := cluster.Actors(ctx, time.Second)
	require.ErrorIs(t, err, ErrEngineNotRunning)
	require.Nil(t, actors)

	grain := internalpb.Grain_builder{GrainId: internalpb.GrainId_builder{Value: "grain-id"}.Build()}.Build()
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

	require.ErrorIs(t, cluster.PutJobKey(ctx, "job-id", []byte("metadata")), ErrEngineNotRunning)

	_, err = cluster.JobKey(ctx, "job-id")
	require.ErrorIs(t, err, ErrEngineNotRunning)

	require.ErrorIs(t, cluster.DeleteJobKey(ctx, "job-id"), ErrEngineNotRunning)

	peers, err := cluster.Peers(ctx)
	require.ErrorIs(t, err, ErrEngineNotRunning)
	require.Nil(t, peers)

	require.Zero(t, cluster.GetPartition("actor"))

	next, err := cluster.NextRoundRobinValue(ctx, ActorsRoundRobinKey)
	require.ErrorIs(t, err, ErrEngineNotRunning)
	require.Equal(t, -1, next)

	next, err = cluster.NextRoundRobinValue(ctx, GrainsRoundRobinKey)
	require.ErrorIs(t, err, ErrEngineNotRunning)
	require.Equal(t, -1, next)

	require.NoError(t, cluster.Stop(ctx))

	provider.AssertExpectations(t)
}

func TestRetryBootstrap(t *testing.T) {
	t.Run("first attempt succeeds without retrying", func(t *testing.T) {
		calls := 0
		err := retryBootstrap(context.Background(), 3, time.Second, log.DiscardLogger, func() error {
			calls++
			return nil
		})

		require.NoError(t, err)
		assert.Equal(t, 1, calls)
	})

	t.Run("transient failure heals within the attempt budget", func(t *testing.T) {
		calls := 0
		err := retryBootstrap(context.Background(), 3, time.Millisecond, log.DiscardLogger, func() error {
			calls++
			if calls < 3 {
				return errors.New("still syncing")
			}

			return nil
		})

		require.NoError(t, err)
		assert.Equal(t, 3, calls)
	})

	t.Run("exhaustion wraps the last error with the attempt count", func(t *testing.T) {
		boom := errors.New("boom")
		calls := 0
		err := retryBootstrap(context.Background(), 3, time.Millisecond, log.DiscardLogger, func() error {
			calls++
			return boom
		})

		require.Error(t, err)
		assert.Equal(t, 3, calls)
		assert.ErrorIs(t, err, boom)
		assert.Contains(t, err.Error(), "after 3 attempts")
	})

	t.Run("context already cancelled stops before the backoff", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		boom := errors.New("boom")
		calls := 0
		err := retryBootstrap(ctx, 3, time.Hour, log.DiscardLogger, func() error {
			calls++
			cancel()
			return boom
		})

		require.Error(t, err)
		assert.Equal(t, 1, calls)
		assert.ErrorIs(t, err, boom)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("context cancelation during backoff stops retrying", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go func() {
			pause.For(100 * time.Millisecond)
			cancel()
		}()

		boom := errors.New("boom")
		calls := 0
		err := retryBootstrap(ctx, 3, time.Hour, log.DiscardLogger, func() error {
			calls++
			return boom
		})

		require.Error(t, err)
		assert.Equal(t, 1, calls)
		assert.ErrorIs(t, err, boom)
		assert.ErrorIs(t, err, context.Canceled)
	})
}

// fakeInitialSyncer stubs the initialSyncer surface to drive the initial-sync
// failure paths that need a multi-replica cluster mid-redistribution to occur
// for real.
type fakeInitialSyncer struct {
	syncErr     error
	shutdownErr error
	shutdowns   int
}

func (f *fakeInitialSyncer) WaitForInitialSync(context.Context) error { return f.syncErr }

func (f *fakeInitialSyncer) Shutdown(context.Context) error {
	f.shutdowns++
	return f.shutdownErr
}

func TestWaitForInitialSync(t *testing.T) {
	t.Run("success leaves the server running", func(t *testing.T) {
		server := &fakeInitialSyncer{}
		err := waitForInitialSync(context.Background(), time.Second, server)
		require.NoError(t, err)
		assert.Zero(t, server.shutdowns)
	})

	t.Run("sync failure tears the started server down", func(t *testing.T) {
		syncErr := errors.New("sync timed out")
		server := &fakeInitialSyncer{syncErr: syncErr}
		err := waitForInitialSync(context.Background(), time.Second, server)
		require.Error(t, err)
		assert.ErrorIs(t, err, syncErr)
		assert.Equal(t, 1, server.shutdowns)
	})

	t.Run("failed teardown is joined to the sync error", func(t *testing.T) {
		syncErr := errors.New("sync timed out")
		shutdownErr := errors.New("shutdown failed")
		server := &fakeInitialSyncer{syncErr: syncErr, shutdownErr: shutdownErr}
		err := waitForInitialSync(context.Background(), time.Second, server)
		require.Error(t, err)
		assert.ErrorIs(t, err, syncErr)
		assert.ErrorIs(t, err, shutdownErr)
		assert.Equal(t, 1, server.shutdowns)
	})
}

// TestBootstrapFailureRetriesAndReleasesPorts drives the real Start with
// unreachable discovery: the bootstrap failure (here in the join phase) is
// retried the full attempt budget instead of killing the process on the first
// error, and every failed attempt tears the partial engine down, so no port is
// left bound once Start finally gives up. The initial-sync failure path, which
// needs a multi-replica cluster mid-redistribution to occur for real, is
// covered by TestWaitForInitialSync.
func TestBootstrapFailureRetriesAndReleasesPorts(t *testing.T) {
	ctx := context.TODO()
	nodePorts := dynaport.Get(4)
	gossipPort, clusterPort, remotingPort, deadPort := nodePorts[0], nodePorts[1], nodePorts[2], nodePorts[3]
	host := "127.0.0.1"

	// point discovery at a port nothing listens on: bootstrap cannot complete.
	// MaxJoinAttempts/ReconnectWait keep the provider's own connect retries from
	// dominating the test's wall clock; the retry under test is retryBootstrap's.
	config := nats.Config{
		NatsServer:      fmt.Sprintf("nats://%s:%d", host, deadPort),
		NatsSubject:     "bootstrap-retry-subject",
		Host:            host,
		DiscoveryPort:   gossipPort,
		MaxJoinAttempts: 1,
		ReconnectWait:   100 * time.Millisecond,
	}

	hostNode := discovery.Node{
		Name:          host,
		Host:          host,
		DiscoveryPort: gossipPort,
		PeersPort:     clusterPort,
		RemotingPort:  remotingPort,
	}

	engine := New("testSystem", nats.NewDiscovery(&config), &hostNode,
		WithLogger(log.DiscardLogger),
		WithBootstrapTimeout(time.Second),
	)

	require.NotNil(t, engine)

	// shrink the retry backoff so the test does not sleep through the
	// production 1s+2s waits; the attempt budget stays at the default
	engine.(*cluster).bootstrapRetryBackoff = 50 * time.Millisecond

	err := engine.Start(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("after %d attempts", defaultBootstrapMaxAttempts), "bootstrap must exhaust its retry budget, not fail on the first attempt")

	for _, port := range []int{gossipPort, clusterPort} {
		ln, lerr := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
		require.NoError(t, lerr, "port %d still bound after a failed bootstrap", port)
		require.NoError(t, ln.Close())
	}
}

func TestSingleNode(t *testing.T) {
	t.Run("With Start and Shutdown", func(t *testing.T) {
		// create the context
		ctx := context.TODO()

		// generate the ports for the single node
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		// define discovered addresses
		addrs := []string{
			fmt.Sprintf("127.0.0.1:%d", gossipPort),
		}

		// mock the discovery provider
		provider := new(mocksdiscovery.Provider)

		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		// create a Node node
		host := "127.0.0.1"

		hostNode := discovery.Node{
			Name:          host,
			Host:          host,
			DiscoveryPort: gossipPort,
			PeersPort:     clusterPort,
			RemotingPort:  remotingPort,
		}

		logger := log.DiscardLogger
		cl := New("test", provider, &hostNode, WithLogger(logger))
		require.NotNil(t, cl)

		// start the Node node
		err := cl.Start(ctx)
		require.NoError(t, err)

		hostNodeAddr := cl.(*cluster).node.Host
		assert.Equal(t, host, hostNodeAddr)

		//  shutdown the Node node
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()

		// stop the node
		require.NoError(t, cl.Stop(ctx))
		provider.AssertExpectations(t)
	})
	t.Run("With PeerSync and GetActor", func(t *testing.T) {
		// create the context
		ctx := context.TODO()

		// generate the ports for the single node
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		// define discovered addresses
		addrs := []string{
			fmt.Sprintf("127.0.0.1:%d", gossipPort),
		}

		// mock the discovery provider
		provider := new(mocksdiscovery.Provider)

		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		// create a Node
		host := "127.0.0.1"
		hostNode := discovery.Node{
			Name:          host,
			Host:          host,
			DiscoveryPort: gossipPort,
			PeersPort:     clusterPort,
			RemotingPort:  remotingPort,
		}

		cluster := New("test", provider, &hostNode, WithLogger(log.DiscardLogger))
		require.NotNil(t, cluster)

		// start the Node
		err := cluster.Start(ctx)
		require.NoError(t, err)

		// create an actor
		actorName := uuid.NewString()
		addr := address.New(actorName, "system", host, remotingPort)
		actor := internalpb.Actor_builder{Address: addr.String()}.Build()

		// replicate the actor in the Node
		err = cluster.PutActor(ctx, actor)
		require.NoError(t, err)

		// test the actor exists
		exists, err := cluster.ActorExists(ctx, actorName)
		require.NoError(t, err)
		assert.True(t, exists)

		// fetch the actor
		actual, err := cluster.GetActor(ctx, actorName)
		require.NoError(t, err)
		require.NotNil(t, actual)

		assert.True(t, proto.Equal(actor, actual))

		// test non-existing actor does not exist
		fakeActorName := "fake"
		exists, err = cluster.ActorExists(ctx, fakeActorName)
		require.NoError(t, err)
		assert.False(t, exists)

		// fetch non-existing actor
		actual, err = cluster.GetActor(ctx, fakeActorName)
		require.Nil(t, actual)
		assert.ErrorIs(t, err, ErrActorNotFound)

		//  shutdown the Node
		pause.For(time.Second)

		// stop the node
		require.NoError(t, cluster.Stop(ctx))
		provider.AssertExpectations(t)
	})
	t.Run("With replica count greater than member count", func(t *testing.T) {
		// a single node started with the default replica count of 2 must
		// bootstrap and serve writes: olric assigns no backup owners below the
		// desired count and the local write satisfies the write quorum of 1
		ctx := context.TODO()

		// generate the ports for the single node
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		// define discovered addresses
		addrs := []string{
			fmt.Sprintf("127.0.0.1:%d", gossipPort),
		}

		// mock the discovery provider
		provider := new(mocksdiscovery.Provider)

		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		// create a Node
		host := "127.0.0.1"
		hostNode := discovery.Node{
			Name:          host,
			Host:          host,
			DiscoveryPort: gossipPort,
			PeersPort:     clusterPort,
			RemotingPort:  remotingPort,
		}

		cluster := New("test", provider, &hostNode,
			WithLogger(log.DiscardLogger),
			WithReplicasCount(2),
			WithMembersWriteQuorum(1),
			WithMembersReadQuorum(1),
			// a lone node completes its initial sync through the
			// empty-partition escape (half this timeout); keep the wait short
			WithBootstrapTimeout(2*time.Second))
		require.NotNil(t, cluster)

		// start the Node
		err := cluster.Start(ctx)
		require.NoError(t, err)

		// create an actor
		actorName := uuid.NewString()
		addr := address.New(actorName, "system", host, remotingPort)
		actor := internalpb.Actor_builder{Address: addr.String()}.Build()

		// writes succeed with no backup member available
		err = cluster.PutActor(ctx, actor)
		require.NoError(t, err)

		// the record is readable back
		actual, err := cluster.GetActor(ctx, actorName)
		require.NoError(t, err)
		require.NotNil(t, actual)
		assert.True(t, proto.Equal(actor, actual))

		// stop the node
		require.NoError(t, cluster.Stop(ctx))
		provider.AssertExpectations(t)
	})
	t.Run("With RemoveActor", func(t *testing.T) {
		// create the context
		ctx := context.TODO()

		// generate the ports for the single node
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		peersPort := nodePorts[1]
		remotingPort := nodePorts[2]

		// define discovered addresses
		addrs := []string{
			fmt.Sprintf("127.0.0.1:%d", gossipPort),
		}

		// mock the discovery provider
		provider := new(mocksdiscovery.Provider)

		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		// create a Node
		host := "127.0.0.1"
		hostNode := discovery.Node{
			Name:          host,
			Host:          host,
			DiscoveryPort: gossipPort,
			PeersPort:     peersPort,
			RemotingPort:  remotingPort,
		}

		logger := log.DiscardLogger
		cluster := New("test", provider, &hostNode, WithLogger(logger))
		require.NotNil(t, cluster)

		// start the Node
		err := cluster.Start(ctx)
		require.NoError(t, err)

		// create an actor
		actorName := uuid.NewString()
		addr := address.New(actorName, "system", host, remotingPort)
		actor := internalpb.Actor_builder{Address: addr.String()}.Build()
		// replicate the actor in the Node
		err = cluster.PutActor(ctx, actor)
		require.NoError(t, err)

		// fetch the actor
		actual, err := cluster.GetActor(ctx, actorName)
		require.NoError(t, err)
		require.NotNil(t, actual)

		assert.True(t, proto.Equal(actor, actual))

		// fetch the partition
		partition := cluster.GetPartition(actorName)
		require.NotZero(t, partition)

		// let us remove the actor
		err = cluster.RemoveActor(ctx, actorName)
		require.NoError(t, err)

		actual, err = cluster.GetActor(ctx, actorName)
		require.Nil(t, actual)
		assert.EqualError(t, err, ErrActorNotFound.Error())

		// fetch the partition
		partition = cluster.GetPartition(actorName)
		require.Zero(t, partition)

		// stop the node
		require.NoError(t, cluster.Stop(ctx))
		provider.AssertExpectations(t)
	})
	t.Run("With NotRunning error", func(t *testing.T) {
		// create the context
		ctx := context.TODO()

		// generate the ports for the single node
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		// mock the discovery provider
		provider := new(mocksdiscovery.Provider)

		// create a Node
		host := "127.0.0.1"

		hostNode := discovery.Node{
			Name:          host,
			Host:          host,
			DiscoveryPort: gossipPort,
			PeersPort:     clusterPort,
			RemotingPort:  remotingPort,
		}

		logger := log.DiscardLogger
		var err error
		cluster := New("test", provider, &hostNode, WithLogger(logger))
		require.NotNil(t, cluster)

		err = cluster.PutActor(ctx, new(internalpb.Actor))
		require.Error(t, err)
		require.EqualError(t, err, ErrEngineNotRunning.Error())

		_, err = cluster.GetActor(ctx, "actorName")
		require.Error(t, err)
		require.EqualError(t, err, ErrEngineNotRunning.Error())

		_, err = cluster.Peers(ctx)
		require.Error(t, err)
		require.EqualError(t, err, ErrEngineNotRunning.Error())

		err = cluster.RemoveActor(ctx, "actorName")
		require.Error(t, err)
		require.EqualError(t, err, ErrEngineNotRunning.Error())

		partition := cluster.GetPartition("actorName")
		require.Zero(t, partition)

		// stop the node
		require.NoError(t, cluster.Stop(ctx))
	})
	t.Run("With PutGrain and GetGrain", func(t *testing.T) {
		// create the context
		ctx := t.Context()

		// generate the ports for the single node
		nodePorts := dynaport.Get(3)
		discoveryPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		// define discovered addresses
		addrs := []string{
			fmt.Sprintf("127.0.0.1:%d", discoveryPort),
		}

		// mock the discovery provider
		provider := new(mocksdiscovery.Provider)

		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		// create a Node
		host := "127.0.0.1"
		hostNode := discovery.Node{
			Name:          host,
			Host:          host,
			DiscoveryPort: discoveryPort,
			PeersPort:     clusterPort,
			RemotingPort:  remotingPort,
		}

		cluster := New("test", provider, &hostNode, WithLogger(log.DiscardLogger))
		require.NotNil(t, cluster)

		// start the Node
		err := cluster.Start(ctx)
		require.NoError(t, err)

		// create an grain
		identity := "grainKind/grainName"
		grain := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{
				Kind:  "grainKind",
				Name:  "grainName",
				Value: identity,
			}.Build(),
			Host: host,
			Port: int32(remotingPort),
		}.Build()

		// replicate the grain in the Node
		err = cluster.PutGrain(ctx, grain)
		require.NoError(t, err)

		exist, err := cluster.GrainExists(ctx, identity)
		require.NoError(t, err)
		require.True(t, exist)

		// fetch the grain
		actual, err := cluster.GetGrain(ctx, identity)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.True(t, proto.Equal(grain, actual))

		//  fetch non-existing actor
		fakeGrainIdentity := "fake"
		actual, err = cluster.GetGrain(ctx, fakeGrainIdentity)
		require.Nil(t, actual)
		require.ErrorIs(t, err, ErrGrainNotFound)

		exist, err = cluster.GrainExists(ctx, fakeGrainIdentity)
		require.NoError(t, err)
		require.False(t, exist)

		//  shutdown the Node
		pause.For(time.Second)

		// stop the node
		require.NoError(t, cluster.Stop(ctx))
		provider.AssertExpectations(t)
	})
	t.Run("With RemoveGrain", func(t *testing.T) {
		// create the context
		ctx := t.Context()

		// generate the ports for the single node
		nodePorts := dynaport.Get(3)
		discoveryPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		// define discovered addresses
		addrs := []string{
			fmt.Sprintf("127.0.0.1:%d", discoveryPort),
		}

		// mock the discovery provider
		provider := new(mocksdiscovery.Provider)

		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		// create a Node
		host := "127.0.0.1"
		hostNode := discovery.Node{
			Name:          host,
			Host:          host,
			DiscoveryPort: discoveryPort,
			PeersPort:     clusterPort,
			RemotingPort:  remotingPort,
		}

		cluster := New("test", provider, &hostNode, WithLogger(log.DiscardLogger))
		require.NotNil(t, cluster)

		// start the Node
		err := cluster.Start(ctx)
		require.NoError(t, err)

		// create an grain
		identity := "grainKind/grainName"
		grain := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{
				Kind:  "grainKind",
				Name:  "grainName",
				Value: identity,
			}.Build(),
			Host: host,
			Port: int32(remotingPort),
		}.Build()

		// replicate the grain in the Node
		err = cluster.PutGrain(ctx, grain)
		require.NoError(t, err)

		exist, err := cluster.GrainExists(ctx, identity)
		require.NoError(t, err)
		require.True(t, exist)

		// fetch the grain
		actual, err := cluster.GetGrain(ctx, identity)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.True(t, proto.Equal(grain, actual))

		// let us remove the grain
		err = cluster.RemoveGrain(ctx, identity)
		require.NoError(t, err)

		exist, err = cluster.GrainExists(ctx, identity)
		require.NoError(t, err)
		require.False(t, exist)

		//  shutdown the Node
		pause.For(time.Second)

		// stop the node
		require.NoError(t, cluster.Stop(ctx))
		provider.AssertExpectations(t)
	})
	t.Run("With PutGrain and GetGrain when NotRunning", func(t *testing.T) {
		// create the context
		ctx := t.Context()

		// generate the ports for the single node
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		// mock the discovery provider
		provider := new(mocksdiscovery.Provider)

		// create a Node
		host := "127.0.0.1"

		hostNode := discovery.Node{
			Name:          host,
			Host:          host,
			DiscoveryPort: gossipPort,
			PeersPort:     clusterPort,
			RemotingPort:  remotingPort,
		}

		logger := log.DiscardLogger
		cluster := New("test", provider, &hostNode, WithLogger(logger))
		require.NotNil(t, cluster)

		// create an grain
		identity := "grainKind/grainName"
		grain := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{
				Kind:  "grainKind",
				Name:  "grainName",
				Value: identity,
			}.Build(),
			Host: host,
			Port: int32(remotingPort),
		}.Build()

		// replicate the grain in the Node
		err := cluster.PutGrain(ctx, grain)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrEngineNotRunning)

		// fetch the grain
		actual, err := cluster.GetGrain(ctx, identity)
		require.Error(t, err)
		require.Nil(t, actual)
		require.ErrorIs(t, err, ErrEngineNotRunning)

		// stop the node
		require.NoError(t, cluster.Stop(ctx))
		provider.AssertExpectations(t)
	})
	t.Run("With GetGrain when decoding failed", func(t *testing.T) {
		// create the context
		ctx := t.Context()

		// generate the ports for the single node
		nodePorts := dynaport.Get(3)
		discoveryPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		// define discovered addresses
		addrs := []string{
			fmt.Sprintf("127.0.0.1:%d", discoveryPort),
		}

		// mock the discovery provider
		provider := new(mocksdiscovery.Provider)

		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		// create a Node
		host := "127.0.0.1"
		hostNode := discovery.Node{
			Name:          host,
			Host:          host,
			DiscoveryPort: discoveryPort,
			PeersPort:     clusterPort,
			RemotingPort:  remotingPort,
		}

		cl := New("test", provider, &hostNode, WithLogger(log.DiscardLogger))
		require.NotNil(t, cl)

		// start the Node
		err := cl.Start(ctx)
		require.NoError(t, err)

		// create an grain
		identity := "grainKind/grainName"

		// replicate the grain in the Node
		err = cl.(*cluster).dmap.Put(ctx, identity, []byte("invalid grain data"))
		require.NoError(t, err)

		// fetch the grain
		actual, err := cl.GetGrain(ctx, identity)
		require.Error(t, err)
		require.Nil(t, actual)

		// stop the node
		require.NoError(t, cl.Stop(ctx))
		provider.AssertExpectations(t)
	})
	t.Run("With GetActor when decoding failed", func(t *testing.T) {
		// create the context
		ctx := t.Context()

		// generate the ports for the single node
		nodePorts := dynaport.Get(3)
		discoveryPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		// define discovered addresses
		addrs := []string{
			fmt.Sprintf("127.0.0.1:%d", discoveryPort),
		}

		// mock the discovery provider
		provider := new(mocksdiscovery.Provider)

		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		// create a Node
		host := "127.0.0.1"
		hostNode := discovery.Node{
			Name:          host,
			Host:          host,
			DiscoveryPort: discoveryPort,
			PeersPort:     clusterPort,
			RemotingPort:  remotingPort,
		}

		cl := New("test", provider, &hostNode, WithLogger(log.DiscardLogger))
		require.NotNil(t, cl)

		// start the Node
		err := cl.Start(ctx)
		require.NoError(t, err)

		actorName := "actorName"
		err = cl.(*cluster).dmap.Put(ctx, actorName, []byte("invalid grain data"))
		require.NoError(t, err)

		actual, err := cl.GetActor(ctx, actorName)
		require.Error(t, err)
		require.Nil(t, actual)

		// stop the node
		require.NoError(t, cl.Stop(ctx))
		provider.AssertExpectations(t)
	})
	t.Run("With RemoveGrain/GrainExists when cluster engine is not running", func(t *testing.T) {
		// create the context
		ctx := t.Context()

		// generate the ports for the single node
		nodePorts := dynaport.Get(3)
		discoveryPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		// mock the discovery provider
		provider := new(mocksdiscovery.Provider)

		// create a Node
		host := "127.0.0.1"
		hostNode := discovery.Node{
			Name:          host,
			Host:          host,
			DiscoveryPort: discoveryPort,
			PeersPort:     clusterPort,
			RemotingPort:  remotingPort,
		}

		cluster := New("test", provider, &hostNode, WithLogger(log.DiscardLogger))
		require.NotNil(t, cluster)

		// create an grain
		identity := "grainKind/grainName"
		exists, err := cluster.GrainExists(ctx, identity)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrEngineNotRunning)
		require.False(t, exists)

		// let us remove the grain
		err = cluster.RemoveGrain(ctx, identity)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrEngineNotRunning)

		// stop the node
		require.NoError(t, cluster.Stop(ctx))
		provider.AssertExpectations(t)
	})
	t.Run("With GetRoundRobinNextValue", func(t *testing.T) {
		// create the context
		ctx := t.Context()

		// generate the ports for the single node
		nodePorts := dynaport.Get(3)
		discoveryPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		// define discovered addresses
		addrs := []string{
			fmt.Sprintf("127.0.0.1:%d", discoveryPort),
		}

		// mock the discovery provider
		provider := new(mocksdiscovery.Provider)

		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		// create a Node
		host := "127.0.0.1"
		hostNode := discovery.Node{
			Name:          host,
			Host:          host,
			DiscoveryPort: discoveryPort,
			PeersPort:     clusterPort,
			RemotingPort:  remotingPort,
		}

		cluster := New("test", provider, &hostNode, WithLogger(log.DiscardLogger))
		require.NotNil(t, cluster)

		// start the Node
		err := cluster.Start(ctx)
		require.NoError(t, err)

		// get next value for actors
		next, err := cluster.NextRoundRobinValue(ctx, ActorsRoundRobinKey)
		require.NoError(t, err)
		require.Equal(t, 1, next)

		next, err = cluster.NextRoundRobinValue(ctx, ActorsRoundRobinKey)
		require.NoError(t, err)
		require.Equal(t, 2, next)

		// get next value for grains
		next, err = cluster.NextRoundRobinValue(ctx, GrainsRoundRobinKey)
		require.NoError(t, err)
		require.Equal(t, 1, next)

		next, err = cluster.NextRoundRobinValue(ctx, GrainsRoundRobinKey)
		require.NoError(t, err)
		require.Equal(t, 2, next)

		//  shutdown the Node
		pause.For(time.Second)

		// stop the node
		require.NoError(t, cluster.Stop(ctx))
		provider.AssertExpectations(t)
	})
	t.Run("With Actors skips round robin counter entry", func(t *testing.T) {
		ctx := t.Context()

		nodePorts := dynaport.Get(3)
		discoveryPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		addrs := []string{
			fmt.Sprintf("127.0.0.1:%d", discoveryPort),
		}

		provider := new(mocksdiscovery.Provider)
		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		host := "127.0.0.1"
		hostNode := discovery.Node{
			Name:          host,
			Host:          host,
			DiscoveryPort: discoveryPort,
			PeersPort:     clusterPort,
			RemotingPort:  remotingPort,
		}

		cluster := New("test", provider, &hostNode, WithLogger(log.DiscardLogger))
		require.NotNil(t, cluster)

		err := cluster.Start(ctx)
		require.NoError(t, err)

		actorName := uuid.NewString()
		addr := address.New(actorName, "system", host, remotingPort)
		actor := internalpb.Actor_builder{Address: addr.String()}.Build()
		err = cluster.PutActor(ctx, actor)
		require.NoError(t, err)

		_, err = cluster.NextRoundRobinValue(ctx, ActorsRoundRobinKey)
		require.NoError(t, err)

		actors, err := cluster.Actors(ctx, time.Second)
		require.NoError(t, err)
		require.Len(t, actors, 1)
		assert.True(t, proto.Equal(actor, actors[0]))

		require.NoError(t, cluster.Stop(ctx))
		provider.AssertExpectations(t)
	})
	t.Run("With Grains skips round robin counter entry", func(t *testing.T) {
		ctx := t.Context()

		nodePorts := dynaport.Get(3)
		discoveryPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		addrs := []string{
			fmt.Sprintf("127.0.0.1:%d", discoveryPort),
		}

		provider := new(mocksdiscovery.Provider)
		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		host := "127.0.0.1"
		hostNode := discovery.Node{
			Name:          host,
			Host:          host,
			DiscoveryPort: discoveryPort,
			PeersPort:     clusterPort,
			RemotingPort:  remotingPort,
		}

		cluster := New("test", provider, &hostNode, WithLogger(log.DiscardLogger))
		require.NotNil(t, cluster)

		err := cluster.Start(ctx)
		require.NoError(t, err)

		identity := "grainKind/grainName"
		grain := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{
				Kind:  "grainKind",
				Name:  "grainName",
				Value: identity,
			}.Build(),
			Host: host,
			Port: int32(remotingPort),
		}.Build()
		err = cluster.PutGrain(ctx, grain)
		require.NoError(t, err)

		_, err = cluster.NextRoundRobinValue(ctx, GrainsRoundRobinKey)
		require.NoError(t, err)

		grains, err := cluster.Grains(ctx, time.Second)
		require.NoError(t, err)
		require.Len(t, grains, 1)
		assert.True(t, proto.Equal(grain, grains[0]))

		require.NoError(t, cluster.Stop(ctx))
		provider.AssertExpectations(t)
	})
	t.Run("With Grains ignores missing entry during scan", func(t *testing.T) {
		ctx := t.Context()

		nodePorts := dynaport.Get(3)
		discoveryPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		addrs := []string{
			fmt.Sprintf("127.0.0.1:%d", discoveryPort),
		}

		provider := new(mocksdiscovery.Provider)
		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		host := "127.0.0.1"
		hostNode := discovery.Node{
			Name:          host,
			Host:          host,
			DiscoveryPort: discoveryPort,
			PeersPort:     clusterPort,
			RemotingPort:  remotingPort,
		}

		cl := New("test", provider, &hostNode, WithLogger(log.DiscardLogger))
		require.NotNil(t, cl)

		err := cl.Start(ctx)
		require.NoError(t, err)

		identity := "grainKind/grainName"
		grain := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{
				Kind:  "grainKind",
				Name:  "grainName",
				Value: identity,
			}.Build(),
			Host: host,
			Port: int32(remotingPort),
		}.Build()
		err = cl.PutGrain(ctx, grain)
		require.NoError(t, err)

		missingKey := composeKey(namespaceGrains, identity)
		cl.(*cluster).dmap = MockFailingGetDMap{DMap: cl.(*cluster).dmap, key: missingKey}

		grains, err := cl.Grains(ctx, time.Second)
		require.NoError(t, err)
		require.Empty(t, grains)

		require.NoError(t, cl.Stop(ctx))
		provider.AssertExpectations(t)
	})
	t.Run("With Grains returns decode error during scan", func(t *testing.T) {
		ctx := t.Context()

		nodePorts := dynaport.Get(3)
		discoveryPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		addrs := []string{
			fmt.Sprintf("127.0.0.1:%d", discoveryPort),
		}

		provider := new(mocksdiscovery.Provider)
		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		host := "127.0.0.1"
		hostNode := discovery.Node{
			Name:          host,
			Host:          host,
			DiscoveryPort: discoveryPort,
			PeersPort:     clusterPort,
			RemotingPort:  remotingPort,
		}

		cl := New("test", provider, &hostNode, WithLogger(log.DiscardLogger))
		require.NotNil(t, cl)

		err := cl.Start(ctx)
		require.NoError(t, err)

		err = cl.(*cluster).dmap.Put(ctx, composeKey(namespaceGrains, "bad-grain"), []byte("invalid-grain"))
		require.NoError(t, err)

		grains, err := cl.Grains(ctx, time.Second)
		require.Error(t, err)
		require.Nil(t, grains)

		require.NoError(t, cl.Stop(ctx))
		provider.AssertExpectations(t)
	})
	t.Run("With Actors ignores missing entry during scan", func(t *testing.T) {
		ctx := t.Context()

		nodePorts := dynaport.Get(3)
		discoveryPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		addrs := []string{
			fmt.Sprintf("127.0.0.1:%d", discoveryPort),
		}

		provider := new(mocksdiscovery.Provider)
		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		host := "127.0.0.1"
		hostNode := discovery.Node{
			Name:          host,
			Host:          host,
			DiscoveryPort: discoveryPort,
			PeersPort:     clusterPort,
			RemotingPort:  remotingPort,
		}

		cl := New("test", provider, &hostNode, WithLogger(log.DiscardLogger))
		require.NotNil(t, cl)

		err := cl.Start(ctx)
		require.NoError(t, err)

		actorName := uuid.NewString()
		addr := address.New(actorName, "system", host, remotingPort)
		actor := internalpb.Actor_builder{Address: addr.String()}.Build()
		err = cl.PutActor(ctx, actor)
		require.NoError(t, err)

		missingKey := composeKey(namespaceActors, actorName)
		cl.(*cluster).dmap = MockFailingGetDMap{DMap: cl.(*cluster).dmap, key: missingKey}

		actors, err := cl.Actors(ctx, time.Second)
		require.NoError(t, err)
		require.Empty(t, actors)

		require.NoError(t, cl.Stop(ctx))
		provider.AssertExpectations(t)
	})
	t.Run("With Actors returns decode error during scan", func(t *testing.T) {
		ctx := t.Context()

		nodePorts := dynaport.Get(3)
		discoveryPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		addrs := []string{
			fmt.Sprintf("127.0.0.1:%d", discoveryPort),
		}

		provider := new(mocksdiscovery.Provider)
		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		host := "127.0.0.1"
		hostNode := discovery.Node{
			Name:          host,
			Host:          host,
			DiscoveryPort: discoveryPort,
			PeersPort:     clusterPort,
			RemotingPort:  remotingPort,
		}

		cl := New("test", provider, &hostNode, WithLogger(log.DiscardLogger))
		require.NotNil(t, cl)

		err := cl.Start(ctx)
		require.NoError(t, err)

		err = cl.(*cluster).dmap.Put(ctx, composeKey(namespaceActors, "bad-actor"), []byte("invalid-actor"))
		require.NoError(t, err)

		actors, err := cl.Actors(ctx, time.Second)
		require.Error(t, err)
		require.Nil(t, actors)

		require.NoError(t, cl.Stop(ctx))
		provider.AssertExpectations(t)
	})
}

func TestMultipleNodes(t *testing.T) {
	t.Run("Without TLS", func(t *testing.T) {
		ctx := context.TODO()

		// start the NATS server
		srv := startNatsServer(t)

		// create a cluster node1
		node1, sd1 := startEngine(t, srv.Addr().String())
		require.NotNil(t, node1)

		// wait for the node to start properly
		pause.For(2 * time.Second)

		// create a cluster node2
		node2, sd2 := startEngine(t, srv.Addr().String())
		require.NotNil(t, node2)
		node2Addr := node2.(*cluster).node.PeersAddress()

		// wait for the node to start properly
		pause.For(time.Second)

		// create a cluster node3
		node3, sd3 := startEngine(t, srv.Addr().String())
		require.NotNil(t, node3)
		require.NotNil(t, sd3)

		// wait for the node to start properly
		pause.For(time.Second)

		// assert the node joined cluster event
		var events []*Event

		// define an events reader loop and read events for some time
	L:
		for {
			select {
			case event, ok := <-node1.Events():
				if ok {
					events = append(events, event)
				}
			case <-time.After(time.Second):
				break L
			}
		}

		require.NotEmpty(t, events)
		require.Len(t, events, 2)
		event := events[0]
		nodeJoined, ok := event.Payload.(*NodeJoinedEvent)
		require.True(t, ok)
		require.NotNil(t, nodeJoined)
		require.Equal(t, node2Addr, nodeJoined.Address)
		peers, err := node1.Peers(ctx)
		require.NoError(t, err)
		require.Len(t, peers, 2)
		require.Equal(t, node2Addr, net.JoinHostPort(peers[0].Host, strconv.Itoa(peers[0].PeersPort)))

		// wait for some time
		pause.For(time.Second)

		// create some actors
		actorName := uuid.NewString()
		node2Imp := node2.(*cluster)
		remotingPort := node2Imp.node.RemotingPort
		host := node2Imp.node.Host
		addr := address.New(actorName, "testSystem", host, remotingPort)
		actor := internalpb.Actor_builder{
			Address: addr.String(),
			Type:    "actorKind",
		}.Build()

		// put an actor
		err = node2.PutActor(ctx, actor)
		require.NoError(t, err)

		// wait for some time
		pause.For(time.Second)

		identity := "grainKind/grainName"
		grain := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{
				Kind:  "grainKind",
				Name:  "grainName",
				Value: identity,
			}.Build(),
			Host: node2.(*cluster).node.Host,
			Port: int32(node2.(*cluster).node.RemotingPort),
		}.Build()

		// replicate the grain in the Node
		err = node2.PutGrain(ctx, grain)
		require.NoError(t, err)

		// get the actor from node1 and node3
		actual, err := node1.GetActor(ctx, actorName)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.True(t, proto.Equal(actor, actual))

		// fetch the grain
		actualGrain, err := node1.GetGrain(ctx, identity)
		require.NoError(t, err)
		require.NotNil(t, actualGrain)
		require.True(t, proto.Equal(grain, actualGrain))

		actual, err = node3.GetActor(ctx, actorName)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.True(t, proto.Equal(actor, actual))

		actualGrain, err = node3.GetGrain(ctx, identity)
		require.NoError(t, err)
		require.NotNil(t, actualGrain)
		require.True(t, proto.Equal(grain, actualGrain))

		// put another actor
		actorName2 := uuid.NewString()
		node1Imp := node1.(*cluster)
		actor2 := internalpb.Actor_builder{
			Address: address.New(actorName2, "testSystem", node1Imp.node.Host, node1Imp.node.RemotingPort).String(),
			Type:    "actorKind",
		}.Build()
		err = node1.PutActor(ctx, actor2)
		require.NoError(t, err)

		// wait for some time
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

		// stop the second node
		require.NoError(t, node2.Stop(ctx))
		// wait for the event to propagate properly
		pause.For(time.Second)

		// reset the slice
		events = []*Event{}

		// define an events reader loop and read events for some time
	L2:
		for {
			select {
			case event, ok := <-node1.Events():
				if ok {
					events = append(events, event)
				}
			case <-time.After(time.Second):
				break L2
			}
		}

		require.NotEmpty(t, events)
		require.Len(t, events, 1)
		event = events[0]
		nodeLeft, ok := event.Payload.(*NodeLeftEvent)
		require.True(t, ok)
		require.NotNil(t, nodeLeft)
		require.Equal(t, node2Addr, nodeLeft.Address)

		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd3.Close())
		srv.Shutdown()
	})
	t.Run("With replica count of two and sequential start", func(t *testing.T) {
		// nodes of a replicated cluster rarely start at the same instant:
		// kubernetes rolling updates and ordered statefulsets bring them up one
		// after the other. each phase of a sequential start must therefore
		// bootstrap within the budget: the first node alone (replica count
		// above member count), then a joiner on a cluster that already holds
		// data
		ctx := context.TODO()

		// start the NATS server
		srv := startNatsServer(t)

		// the first node bootstraps alone; the shorter bootstrap timeout keeps
		// the empty-partition escape (half of it) from dominating the test
		node1, sd1 := startEngine(t, srv.Addr().String(), WithReplicasCount(2), WithBootstrapTimeout(4*time.Second))
		require.NotNil(t, node1)

		// wait for the node to start properly
		pause.For(time.Second)

		// write a record while the cluster is a single member so the joiner
		// receives real fragments during its initial sync
		node1Imp := node1.(*cluster)
		actorName := uuid.NewString()
		actor := internalpb.Actor_builder{
			Address: address.New(actorName, "testSystem", node1Imp.node.Host, node1Imp.node.RemotingPort).String(),
			Type:    "actorKind",
		}.Build()
		require.NoError(t, node1.PutActor(ctx, actor))

		// the second node joins the running cluster
		node2, sd2 := startEngine(t, srv.Addr().String(), WithReplicasCount(2), WithBootstrapTimeout(4*time.Second))
		require.NotNil(t, node2)

		// wait for the node to start properly
		pause.For(time.Second)

		// the record written before the join is visible from the joiner
		actual, err := node2.GetActor(ctx, actorName)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.True(t, proto.Equal(actor, actual))

		// writes on the joiner are visible from the first node
		node2Imp := node2.(*cluster)
		actorName2 := uuid.NewString()
		actor2 := internalpb.Actor_builder{
			Address: address.New(actorName2, "testSystem", node2Imp.node.Host, node2Imp.node.RemotingPort).String(),
			Type:    "actorKind",
		}.Build()
		require.NoError(t, node2.PutActor(ctx, actor2))

		actual, err = node1.GetActor(ctx, actorName2)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.True(t, proto.Equal(actor2, actual))

		require.NoError(t, node2.Stop(ctx))
		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, sd2.Close())
		srv.Shutdown()
	})
	t.Run("With TLS", func(t *testing.T) {
		ctx := context.TODO()
		// AutoGenerate TLS certs
		serverConf := autotls.Config{
			CaFile:           "../../test/data/certs/ca.cert",
			CertFile:         "../../test/data/certs/auto.pem",
			KeyFile:          "../../test/data/certs/auto.key",
			ClientAuthCaFile: "../../test/data/certs/client-auth-ca.pem",
			ClientAuth:       tls.RequireAndVerifyClientCert,
		}
		require.NoError(t, autotls.Setup(&serverConf))

		clientConf := &autotls.Config{
			CertFile:           "../../test/data/certs/client-auth.pem",
			KeyFile:            "../../test/data/certs/client-auth.key",
			InsecureSkipVerify: true,
		}
		require.NoError(t, autotls.Setup(clientConf))

		// start the NATS server
		srv := startNatsServer(t)

		// create a cluster node1
		node1, sd1 := startEngineWithTLS(t, srv.Addr().String(), serverConf.ServerTLS, clientConf.ClientTLS)
		require.NotNil(t, node1)

		// wait for the node to start properly
		pause.For(2 * time.Second)

		// create a cluster node2
		node2, sd2 := startEngineWithTLS(t, srv.Addr().String(), serverConf.ServerTLS, clientConf.ClientTLS)
		require.NotNil(t, node2)
		node2Addr := node2.(*cluster).node.PeersAddress()

		// wait for the node to start properly
		pause.For(time.Second)

		// create a cluster node3
		node3, sd3 := startEngineWithTLS(t, srv.Addr().String(), serverConf.ServerTLS, clientConf.ClientTLS)
		require.NotNil(t, node3)
		require.NotNil(t, sd3)

		// wait for the node to start properly
		pause.For(time.Second)

		// assert the node joined cluster event
		var events []*Event

		// define an events reader loop and read events for some time
	L:
		for {
			select {
			case event, ok := <-node1.Events():
				if ok {
					events = append(events, event)
				}
			case <-time.After(time.Second):
				break L
			}
		}

		require.NotEmpty(t, events)
		require.Len(t, events, 2)
		event := events[0]
		nodeJoined, ok := event.Payload.(*NodeJoinedEvent)
		require.True(t, ok)
		require.NotNil(t, nodeJoined)
		require.Equal(t, node2Addr, nodeJoined.Address)
		peers, err := node1.Peers(ctx)
		require.NoError(t, err)
		require.Len(t, peers, 2)
		require.Equal(t, node2Addr, net.JoinHostPort(peers[0].Host, strconv.Itoa(peers[0].PeersPort)))

		// wait for some time
		pause.For(time.Second)

		// create some actors
		actorName := uuid.NewString()
		node2Imp := node2.(*cluster)
		remotingPort := node2Imp.node.RemotingPort
		host := node2Imp.node.Host
		addr := address.New(actorName, "testSystem", host, remotingPort)
		actor := internalpb.Actor_builder{
			Address: addr.String(),
			Type:    "actorKind",
		}.Build()

		// put an actor
		err = node2.PutActor(ctx, actor)
		require.NoError(t, err)

		identity := "grainKind/grainName"
		grain := internalpb.Grain_builder{
			GrainId: internalpb.GrainId_builder{
				Kind:  "grainKind",
				Name:  "grainName",
				Value: identity,
			}.Build(),
			Host: node2.(*cluster).node.Host,
			Port: int32(node2.(*cluster).node.RemotingPort),
		}.Build()

		// replicate the grain in the Node
		err = node2.PutGrain(ctx, grain)
		require.NoError(t, err)

		// wait for some time
		pause.For(time.Second)

		// get the actor from node1 and node3
		actual, err := node1.GetActor(ctx, actorName)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.True(t, proto.Equal(actor, actual))

		// fetch the grain
		actualGrain, err := node1.GetGrain(ctx, identity)
		require.NoError(t, err)
		require.NotNil(t, actualGrain)
		require.True(t, proto.Equal(grain, actualGrain))

		actual, err = node3.GetActor(ctx, actorName)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.True(t, proto.Equal(actor, actual))

		actualGrain, err = node3.GetGrain(ctx, identity)
		require.NoError(t, err)
		require.NotNil(t, actualGrain)
		require.True(t, proto.Equal(grain, actualGrain))

		// put another actor
		actorName2 := uuid.NewString()
		node1Imp := node1.(*cluster)
		actor2 := internalpb.Actor_builder{
			Address: address.New(actorName2, "testSystem", node1Imp.node.Host, node1Imp.node.RemotingPort).String(),
			Type:    "actorKind",
		}.Build()
		err = node1.PutActor(ctx, actor2)
		require.NoError(t, err)

		// wait for some time
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

		grains, err = node3.Grains(ctx, time.Second)
		require.NoError(t, err)
		require.Len(t, grains, 1)

		actors, err = node2.Actors(ctx, time.Second)
		require.NoError(t, err)
		require.Len(t, actors, 2)

		grains, err = node2.Grains(ctx, time.Second)
		require.NoError(t, err)
		require.Len(t, grains, 1)

		// stop the second node
		require.NoError(t, node2.Stop(ctx))
		// wait for the event to propagate properly
		pause.For(time.Second)

		// reset the slice
		events = []*Event{}

		// define an events reader loop and read events for some time
	L2:
		for {
			select {
			case event, ok := <-node1.Events():
				if ok {
					events = append(events, event)
				}
			case <-time.After(time.Second):
				break L2
			}
		}

		require.NotEmpty(t, events)
		require.Len(t, events, 1)
		event = events[0]
		nodeLeft, ok := event.Payload.(*NodeLeftEvent)
		require.True(t, ok)
		require.NotNil(t, nodeLeft)
		require.Equal(t, node2Addr, nodeLeft.Address)

		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd3.Close())
		srv.Shutdown()
	})
	t.Run("With NextRoundRobinValue", func(t *testing.T) {
		ctx := context.TODO()

		// start the NATS server
		srv := startNatsServer(t)

		// create a cluster node1
		node1, sd1 := startEngine(t, srv.Addr().String())
		require.NotNil(t, node1)

		// wait for the node to start properly
		pause.For(2 * time.Second)

		// create a cluster node2
		node2, sd2 := startEngine(t, srv.Addr().String())
		require.NotNil(t, node2)

		// wait for the node to start properly
		pause.For(time.Second)

		// create a cluster node3
		node3, sd3 := startEngine(t, srv.Addr().String())
		require.NotNil(t, node3)
		require.NotNil(t, sd3)

		// wait for the node to start properly
		pause.For(time.Second)

		// get next value for actors from node2
		next, err := node1.NextRoundRobinValue(ctx, ActorsRoundRobinKey)
		require.NoError(t, err)
		require.Equal(t, 1, next)

		next, err = node2.NextRoundRobinValue(ctx, ActorsRoundRobinKey)
		require.NoError(t, err)
		require.Equal(t, 2, next)

		next, err = node3.NextRoundRobinValue(ctx, ActorsRoundRobinKey)
		require.NoError(t, err)
		require.Equal(t, 3, next)

		next, err = node1.NextRoundRobinValue(ctx, ActorsRoundRobinKey)
		require.NoError(t, err)
		require.Equal(t, 4, next)

		// get next value for grains from node2
		next, err = node1.NextRoundRobinValue(ctx, GrainsRoundRobinKey)
		require.NoError(t, err)
		require.Equal(t, 1, next)

		next, err = node2.NextRoundRobinValue(ctx, GrainsRoundRobinKey)
		require.NoError(t, err)
		require.Equal(t, 2, next)

		next, err = node3.NextRoundRobinValue(ctx, GrainsRoundRobinKey)
		require.NoError(t, err)
		require.Equal(t, 3, next)

		next, err = node1.NextRoundRobinValue(ctx, GrainsRoundRobinKey)
		require.NoError(t, err)
		require.Equal(t, 4, next)

		next, err = node1.NextRoundRobinValue(ctx, GrainsRoundRobinKey)
		require.NoError(t, err)
		require.Equal(t, 5, next)

		next, err = node3.NextRoundRobinValue(ctx, ActorsRoundRobinKey)
		require.NoError(t, err)
		require.Equal(t, 5, next)

		require.NoError(t, node2.Stop(ctx))
		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd3.Close())
		srv.Shutdown()
	})
	t.Run("With NextRoundRobinValue on starting node down", func(t *testing.T) {
		ctx := context.TODO()

		// start the NATS server
		srv := startNatsServer(t)

		// create a cluster node1
		node1, sd1 := startEngine(t, srv.Addr().String())
		require.NotNil(t, node1)

		// wait for the node to start properly
		pause.For(2 * time.Second)

		// create a cluster node2
		node2, sd2 := startEngine(t, srv.Addr().String())
		require.NotNil(t, node2)

		// wait for the node to start properly
		pause.For(time.Second)

		// create a cluster node3
		node3, sd3 := startEngine(t, srv.Addr().String())
		require.NotNil(t, node3)
		require.NotNil(t, sd3)

		// wait for the node to start properly
		pause.For(time.Second)

		// get next value for actors from node2
		next, err := node2.NextRoundRobinValue(ctx, ActorsRoundRobinKey)
		require.NoError(t, err)
		require.Equal(t, 1, next)

		next, err = node1.NextRoundRobinValue(ctx, ActorsRoundRobinKey)
		require.NoError(t, err)
		require.Equal(t, 2, next)

		next, err = node3.NextRoundRobinValue(ctx, ActorsRoundRobinKey)
		require.NoError(t, err)
		require.Equal(t, 3, next)

		next, err = node1.NextRoundRobinValue(ctx, ActorsRoundRobinKey)
		require.NoError(t, err)
		require.Equal(t, 4, next)

		// get next value for grains from node2
		next, err = node2.NextRoundRobinValue(ctx, GrainsRoundRobinKey)
		require.NoError(t, err)
		require.Equal(t, 1, next)

		next, err = node1.NextRoundRobinValue(ctx, GrainsRoundRobinKey)
		require.NoError(t, err)
		require.Equal(t, 2, next)

		next, err = node3.NextRoundRobinValue(ctx, GrainsRoundRobinKey)
		require.NoError(t, err)
		require.Equal(t, 3, next)

		next, err = node1.NextRoundRobinValue(ctx, GrainsRoundRobinKey)
		require.NoError(t, err)
		require.Equal(t, 4, next)

		// stop the second node
		require.NoError(t, node2.Stop(ctx))
		pause.For(time.Second)

		next, err = node1.NextRoundRobinValue(ctx, GrainsRoundRobinKey)
		require.NoError(t, err)
		require.GreaterOrEqual(t, next, 1)

		next, err = node3.NextRoundRobinValue(ctx, ActorsRoundRobinKey)
		require.NoError(t, err)
		require.GreaterOrEqual(t, next, 1)

		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd3.Close())
		srv.Shutdown()
	})
}

func startNatsServer(t *testing.T) *natsserver.Server {
	t.Helper()
	serv, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1",
		Port: -1,
	})

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

func startEngine(t *testing.T, serverAddr string, opts ...ConfigOption) (Cluster, discovery.Provider) {
	// create a context
	ctx := context.TODO()

	// generate the ports for the single node
	nodePorts := dynaport.Get(3)
	gossipPort := nodePorts[0]
	clusterPort := nodePorts[1]
	remotingPort := nodePorts[2]

	// create a Cluster node
	host := "127.0.0.1"
	// create the various config option
	actorSystemName := "testSystem"
	natsSubject := "some-subject"

	// create the config
	config := nats.Config{
		NatsServer:    fmt.Sprintf("nats://%s", serverAddr),
		NatsSubject:   natsSubject,
		Host:          host,
		DiscoveryPort: gossipPort,
	}

	hostNode := discovery.Node{
		Name:          host,
		Host:          host,
		DiscoveryPort: gossipPort,
		PeersPort:     clusterPort,
		RemotingPort:  remotingPort,
	}

	// create the instance of provider
	provider := nats.NewDiscovery(&config)

	// create the node
	engine := New(actorSystemName, provider, &hostNode, append([]ConfigOption{WithLogger(log.DiscardLogger)}, opts...)...)
	require.NotNil(t, engine)

	// start the node
	require.NoError(t, engine.Start(ctx))

	// return the cluster node
	return engine, provider
}

func startEngineWithTLS(t *testing.T, serverAddr string, server, client *tls.Config) (Cluster, discovery.Provider) {
	// create a context
	ctx := context.TODO()

	// generate the ports for the single node
	nodePorts := dynaport.Get(3)
	gossipPort := nodePorts[0]
	clusterPort := nodePorts[1]
	remotingPort := nodePorts[2]

	// create a Cluster node
	host := "127.0.0.1"
	// create the various config option
	actorSystemName := "testSystem"
	natsSubject := "some-subject"

	// create the config
	config := nats.Config{
		NatsServer:    fmt.Sprintf("nats://%s", serverAddr),
		NatsSubject:   natsSubject,
		Host:          host,
		DiscoveryPort: gossipPort,
	}

	hostNode := discovery.Node{
		Name:          host,
		Host:          host,
		DiscoveryPort: gossipPort,
		PeersPort:     clusterPort,
		RemotingPort:  remotingPort,
	}

	// create the instance of provider
	provider := nats.NewDiscovery(&config)

	// create the node
	engine := New(actorSystemName, provider, &hostNode,
		WithTLS(&gtls.Info{
			ClientConfig: client,
			ServerConfig: server,
		}),
		WithLogger(log.DiscardLogger))
	require.NotNil(t, engine)

	// start the node
	require.NoError(t, engine.Start(ctx))

	// return the cluster node
	return engine, provider
}

func TestPutGrainReturnsErrorWhenIDMissing(t *testing.T) {
	cl := &cluster{
		running: atomic.NewBool(true),
	}

	err := cl.PutGrain(context.Background(), &internalpb.Grain{})
	require.EqualError(t, err, "grain id is not set")
}

func TestPutGrainReturnsErrorWhenIDValueEmpty(t *testing.T) {
	cl := &cluster{
		running: atomic.NewBool(true),
	}

	grain := internalpb.Grain_builder{GrainId: internalpb.GrainId_builder{Value: ""}.Build()}.Build()
	err := cl.PutGrain(context.Background(), grain)
	require.EqualError(t, err, "grain id value is empty")
}

func TestPutGrainIfAbsentReturnsErrorWhenClusterNil(t *testing.T) {
	grain := internalpb.Grain_builder{GrainId: internalpb.GrainId_builder{Value: "grain-id"}.Build()}.Build()
	err := PutGrainIfAbsent(context.Background(), nil, grain)
	require.EqualError(t, err, "cluster is nil")
}

func TestPutGrainIfAbsentReturnsErrorWhenIDMissing(t *testing.T) {
	cl := &cluster{
		running: atomic.NewBool(true),
	}

	err := PutGrainIfAbsent(context.Background(), cl, &internalpb.Grain{})
	require.EqualError(t, err, "grain id is not set")
}

func TestPutGrainIfAbsentReturnsErrorWhenIDValueEmpty(t *testing.T) {
	cl := &cluster{
		running: atomic.NewBool(true),
	}

	grain := internalpb.Grain_builder{GrainId: internalpb.GrainId_builder{Value: ""}.Build()}.Build()
	err := PutGrainIfAbsent(context.Background(), cl, grain)
	require.EqualError(t, err, "grain id value is empty")
}

func TestPutGrainIfAbsentReturnsErrorWhenNotRunning(t *testing.T) {
	cl := &cluster{
		running: atomic.NewBool(false),
	}

	grain := internalpb.Grain_builder{GrainId: internalpb.GrainId_builder{Value: "grain-id"}.Build()}.Build()
	err := PutGrainIfAbsent(context.Background(), cl, grain)
	require.ErrorIs(t, err, ErrEngineNotRunning)
}

func TestPutGrainIfAbsentReturnsAlreadyExists(t *testing.T) {
	cl := &cluster{
		running:      atomic.NewBool(true),
		dmap:         &MockDMap{putErr: olric.ErrKeyFound},
		writeTimeout: time.Second,
	}

	grain := internalpb.Grain_builder{GrainId: internalpb.GrainId_builder{Value: "grain-id"}.Build()}.Build()
	err := PutGrainIfAbsent(context.Background(), cl, grain)
	require.ErrorIs(t, err, ErrGrainAlreadyExists)
}

func TestPutGrainIfAbsentPropagatesDMapError(t *testing.T) {
	expectedErr := errors.New("put failure")
	cl := &cluster{
		running:      atomic.NewBool(true),
		dmap:         &MockDMap{putErr: expectedErr},
		writeTimeout: time.Second,
	}

	grain := internalpb.Grain_builder{GrainId: internalpb.GrainId_builder{Value: "grain-id"}.Build()}.Build()
	err := PutGrainIfAbsent(context.Background(), cl, grain)
	require.ErrorIs(t, err, expectedErr)
}

func TestPutGrainIfAbsentSucceeds(t *testing.T) {
	called := false
	cl := &cluster{
		running:      atomic.NewBool(true),
		writeTimeout: time.Second,
		dmap: &MockDMap{
			putFn: func(ctx context.Context, key string, value any, options ...olric.PutOption) error { // nolint
				called = true
				require.NotEmpty(t, options)
				return nil
			},
		},
	}

	grain := internalpb.Grain_builder{GrainId: internalpb.GrainId_builder{Value: "grain-id"}.Build()}.Build()
	err := PutGrainIfAbsent(context.Background(), cl, grain)
	require.NoError(t, err)
	require.True(t, called)
}

func TestPutGrainIfAbsentFallbackPropagatesGrainExistsError(t *testing.T) {
	ctx := context.Background()
	grain := internalpb.Grain_builder{GrainId: internalpb.GrainId_builder{Value: "grain-id"}.Build()}.Build()
	expectedErr := errors.New("grain exists lookup failed")
	cl := &MockCluster{
		grainExistsFn: func(ctx context.Context, identity string) (bool, error) {
			require.Equal(t, grain.GetGrainId().GetValue(), identity)
			return false, expectedErr
		},
	}

	err := PutGrainIfAbsent(ctx, cl, grain)
	require.ErrorIs(t, err, expectedErr)
	require.Equal(t, 1, cl.grainExistsCalls)
	require.Zero(t, cl.putGrainCalls)
}

func TestPutGrainIfAbsentFallbackReturnsAlreadyExists(t *testing.T) {
	ctx := context.Background()
	grain := internalpb.Grain_builder{GrainId: internalpb.GrainId_builder{Value: "grain-id"}.Build()}.Build()
	cl := &MockCluster{
		grainExistsFn: func(ctx context.Context, identity string) (bool, error) {
			require.Equal(t, grain.GetGrainId().GetValue(), identity)
			return true, nil
		},
	}

	err := PutGrainIfAbsent(ctx, cl, grain)
	require.ErrorIs(t, err, ErrGrainAlreadyExists)
	require.Equal(t, 1, cl.grainExistsCalls)
	require.Zero(t, cl.putGrainCalls)
}

func TestPutGrainIfAbsentFallbackPropagatesPutGrainError(t *testing.T) {
	ctx := context.Background()
	grain := internalpb.Grain_builder{GrainId: internalpb.GrainId_builder{Value: "grain-id"}.Build()}.Build()
	expectedErr := errors.New("put grain failed")
	cl := &MockCluster{
		grainExistsFn: func(ctx context.Context, identity string) (bool, error) {
			require.Equal(t, grain.GetGrainId().GetValue(), identity)
			return false, nil
		},
		putGrainFn: func(ctx context.Context, actual *internalpb.Grain) error {
			require.Equal(t, grain, actual)
			return expectedErr
		},
	}

	err := PutGrainIfAbsent(ctx, cl, grain)
	require.ErrorIs(t, err, expectedErr)
	require.Equal(t, 1, cl.grainExistsCalls)
	require.Equal(t, 1, cl.putGrainCalls)
}

func TestPutGrainIfAbsentFallbackCallsPutGrain(t *testing.T) {
	ctx := context.Background()
	grain := internalpb.Grain_builder{GrainId: internalpb.GrainId_builder{Value: "grain-id"}.Build()}.Build()
	cl := &MockCluster{
		grainExistsFn: func(ctx context.Context, identity string) (bool, error) {
			require.Equal(t, grain.GetGrainId().GetValue(), identity)
			return false, nil
		},
		putGrainFn: func(ctx context.Context, actual *internalpb.Grain) error {
			require.Equal(t, grain, actual)
			return nil
		},
	}

	err := PutGrainIfAbsent(ctx, cl, grain)
	require.NoError(t, err)
	require.Equal(t, 1, cl.grainExistsCalls)
	require.Equal(t, 1, cl.putGrainCalls)
}

func TestPutActorPropagatesDMapError(t *testing.T) {
	putErr := errors.New("put failure")
	cl := &cluster{
		running:      atomic.NewBool(true),
		dmap:         &MockDMap{putErr: putErr},
		logger:       log.DiscardLogger,
		writeTimeout: time.Second,
	}

	actor := &internalpb.Actor{}
	err := cl.PutActor(context.Background(), actor)
	require.ErrorIs(t, err, putErr)
}

func TestNextRoundRobinValuePropagatesIncrError(t *testing.T) {
	expectedErr := errors.New("incr failure")
	cl := &cluster{
		running:     atomic.NewBool(true),
		logger:      log.DiscardLogger,
		readTimeout: time.Second,
		dmap: &MockDMap{
			incrFn: func(ctx context.Context, key string, delta int) (int, error) { // nolint
				require.Equal(t, composeKey(namespaceActors, ActorsRoundRobinKey), key)
				require.Equal(t, 1, delta)
				return 0, expectedErr
			},
		},
	}

	_, err := cl.NextRoundRobinValue(context.Background(), ActorsRoundRobinKey)
	require.ErrorIs(t, err, expectedErr)
}

func TestNextRoundRobinValueReturnsErrorForInvalidKey(t *testing.T) {
	cl := &cluster{
		running:     atomic.NewBool(true),
		logger:      log.DiscardLogger,
		readTimeout: time.Second,
	}

	next, err := cl.NextRoundRobinValue(context.Background(), "invalid-key")
	require.Equal(t, -1, next)
	require.EqualError(t, err, "invalid round-robin key: invalid-key")
}

func TestPutActorIfAbsent(t *testing.T) {
	record := internalpb.Actor_builder{Address: address.New("endpoint", "testSystem", "127.0.0.1", 9000).String()}.Build()

	t.Run("With an absent record", func(t *testing.T) {
		cl := &cluster{
			running:      atomic.NewBool(true),
			logger:       log.DiscardLogger,
			writeTimeout: time.Second,
			dmap: &MockDMap{
				putFn: func(_ context.Context, key string, _ any, options ...olric.PutOption) error { // nolint
					require.Equal(t, composeKey(namespaceActors, "endpoint"), key)
					// the write must carry the NX option that makes it conditional
					require.Len(t, options, 1)
					return nil
				},
			},
		}

		require.NoError(t, cl.PutActorIfAbsent(context.Background(), record))
	})

	t.Run("With an existing record", func(t *testing.T) {
		cl := &cluster{
			running:      atomic.NewBool(true),
			logger:       log.DiscardLogger,
			writeTimeout: time.Second,
			dmap:         &MockDMap{putErr: olric.ErrKeyFound},
		}

		err := cl.PutActorIfAbsent(context.Background(), record)
		require.ErrorIs(t, err, ErrActorAlreadyExists)
	})

	t.Run("With a backend failure", func(t *testing.T) {
		putErr := errors.New("put failure")
		cl := &cluster{
			running:      atomic.NewBool(true),
			logger:       log.DiscardLogger,
			writeTimeout: time.Second,
			dmap:         &MockDMap{putErr: putErr},
		}

		err := cl.PutActorIfAbsent(context.Background(), record)
		require.ErrorIs(t, err, putErr)
	})

	t.Run("With the engine not running", func(t *testing.T) {
		cl := &cluster{running: atomic.NewBool(false), logger: log.DiscardLogger}

		err := cl.PutActorIfAbsent(context.Background(), record)
		require.ErrorIs(t, err, ErrEngineNotRunning)
	})
}

func TestGetActorReturnsDMapError(t *testing.T) {
	expectedErr := errors.New("get failure")
	cl := &cluster{
		running:     atomic.NewBool(true),
		logger:      log.DiscardLogger,
		readTimeout: time.Second,
		dmap: &MockDMap{
			getFn: func(ctx context.Context, key string) (*olric.GetResponse, error) { // nolint
				require.Equal(t, composeKey(namespaceActors, "actor"), key)
				return nil, expectedErr
			},
		},
	}

	actor, err := cl.GetActor(context.Background(), "actor")
	require.Nil(t, actor)
	require.ErrorIs(t, err, expectedErr)
}

// nolint
func TestActorsReturnsScanError(t *testing.T) {
	expectedErr := errors.New("scan failure")
	cl := &cluster{
		running: atomic.NewBool(true),
		logger:  log.DiscardLogger,
		dmap: &MockDMap{
			scanFn: func(ctx context.Context, options ...olric.ScanOption) (olric.Iterator, error) {
				return nil, expectedErr
			},
		},
	}

	actors, err := cl.Actors(context.Background(), time.Second)
	require.Nil(t, actors)
	require.ErrorIs(t, err, expectedErr)
}

// nolint
func TestActorsPropagatesGetError(t *testing.T) {
	expectedErr := errors.New("actors get failure")
	cl := &cluster{
		running: atomic.NewBool(true),
		logger:  log.DiscardLogger,
		dmap: &MockDMap{
			scanFn: func(ctx context.Context, options ...olric.ScanOption) (olric.Iterator, error) {
				return &iteratorStub{keys: []string{composeKey(namespaceActors, "actor")}}, nil
			},
			getFn: func(ctx context.Context, key string) (*olric.GetResponse, error) {
				require.Equal(t, composeKey(namespaceActors, "actor"), key)
				return nil, expectedErr
			},
		},
	}

	actors, err := cl.Actors(context.Background(), time.Second)
	require.Nil(t, actors)
	require.ErrorIs(t, err, expectedErr)
}

// nolint
func TestActorsPropagatesByteError(t *testing.T) {
	cl := &cluster{
		running: atomic.NewBool(true),
		logger:  log.DiscardLogger,
		dmap: &MockDMap{
			scanFn: func(ctx context.Context, options ...olric.ScanOption) (olric.Iterator, error) {
				return &iteratorStub{keys: []string{composeKey(namespaceActors, "actor")}}, nil
			},
			getFn: func(ctx context.Context, key string) (*olric.GetResponse, error) {
				require.Equal(t, composeKey(namespaceActors, "actor"), key)
				return &olric.GetResponse{}, nil
			},
		},
	}

	actors, err := cl.Actors(context.Background(), time.Second)
	require.Nil(t, actors)
	require.ErrorIs(t, err, olric.ErrNilResponse)
}

// nolint
func TestCountActorsByHostTalliesPerHost(t *testing.T) {
	// two actors share one host, a third lives on another host; the tally must
	// group by the address's raw "host:port".
	first := address.New("a", "system", "127.0.0.1", 8080)
	second := address.New("b", "system", "127.0.0.1", 8080)
	third := address.New("c", "system", "127.0.0.2", 9002)

	encoded := make(map[string][]byte, 3)
	for _, addr := range []*address.Address{first, second, third} {
		key := composeKey(namespaceActors, addr.String())
		value, err := encode(internalpb.Actor_builder{Address: addr.String()}.Build())
		require.NoError(t, err)
		encoded[key] = value
	}

	rrKey := composeKey(namespaceActors, ActorsRoundRobinKey)
	grainKey := composeKey(namespaceGrains, "grain")

	keys := make([]string, 0, len(encoded)+2)
	for key := range encoded {
		keys = append(keys, key)
	}
	keys = append(keys, rrKey, grainKey)

	cl := &cluster{
		running: atomic.NewBool(true),
		logger:  log.DiscardLogger,
		dmap: &MockDMap{
			scanFn: func(ctx context.Context, options ...olric.ScanOption) (olric.Iterator, error) {
				return &iteratorStub{keys: keys}, nil
			},
			getFn: func(ctx context.Context, key string) (*olric.GetResponse, error) {
				value, ok := encoded[key]
				if !ok {
					// the round-robin counter and non-actor namespaces must be
					// filtered out before any Get is issued.
					t.Fatalf("unexpected Get for key %q", key)
				}
				return newGetResponseWithValue(value), nil
			},
		},
	}

	counts, err := cl.CountActorsByHost(context.Background(), time.Second)
	require.NoError(t, err)
	require.Equal(t, map[string]int{
		address.FormatHostPort("127.0.0.1", 8080): 2,
		address.FormatHostPort("127.0.0.2", 9002): 1,
	}, counts)
}

// nolint
func TestCountActorsByHostReturnsScanError(t *testing.T) {
	expectedErr := errors.New("scan failure")
	cl := &cluster{
		running: atomic.NewBool(true),
		logger:  log.DiscardLogger,
		dmap: &MockDMap{
			scanFn: func(ctx context.Context, options ...olric.ScanOption) (olric.Iterator, error) {
				return nil, expectedErr
			},
		},
	}

	counts, err := cl.CountActorsByHost(context.Background(), time.Second)
	require.Nil(t, counts)
	require.ErrorIs(t, err, expectedErr)
}

// nolint
func TestCountActorsByHostPropagatesGetError(t *testing.T) {
	expectedErr := errors.New("actors get failure")
	cl := &cluster{
		running: atomic.NewBool(true),
		logger:  log.DiscardLogger,
		dmap: &MockDMap{
			scanFn: func(ctx context.Context, options ...olric.ScanOption) (olric.Iterator, error) {
				return &iteratorStub{keys: []string{composeKey(namespaceActors, "actor")}}, nil
			},
			getFn: func(ctx context.Context, key string) (*olric.GetResponse, error) {
				require.Equal(t, composeKey(namespaceActors, "actor"), key)
				return nil, expectedErr
			},
		},
	}

	counts, err := cl.CountActorsByHost(context.Background(), time.Second)
	require.Nil(t, counts)
	require.ErrorIs(t, err, expectedErr)
}

// nolint
func TestActorsByHostReturnsOnlyMatchingHost(t *testing.T) {
	match1 := address.New("a", "system", "127.0.0.1", 8080)
	match2 := address.New("b", "system", "127.0.0.1", 8080)
	other := address.New("c", "system", "127.0.0.2", 9002)

	encoded := make(map[string][]byte, 3)
	for _, addr := range []*address.Address{match1, match2, other} {
		key := composeKey(namespaceActors, addr.String())
		value, err := encode(internalpb.Actor_builder{Address: addr.String()}.Build())
		require.NoError(t, err)
		encoded[key] = value
	}

	keys := make([]string, 0, len(encoded))
	for key := range encoded {
		keys = append(keys, key)
	}

	cl := &cluster{
		running: atomic.NewBool(true),
		logger:  log.DiscardLogger,
		dmap: &MockDMap{
			scanFn: func(ctx context.Context, options ...olric.ScanOption) (olric.Iterator, error) {
				return &iteratorStub{keys: keys}, nil
			},
			getFn: func(ctx context.Context, key string) (*olric.GetResponse, error) {
				return newGetResponseWithValue(encoded[key]), nil
			},
		},
	}

	actors, err := cl.ActorsByHost(context.Background(), "127.0.0.1", 8080, time.Second)
	require.NoError(t, err)
	require.Len(t, actors, 2)
	for _, actor := range actors {
		hostPort, ok := address.HostPortOf(actor.GetAddress())
		require.True(t, ok)
		require.Equal(t, address.FormatHostPort("127.0.0.1", 8080), hostPort)
	}
}

// nolint
func TestGrainsByHostReturnsOnlyMatchingHost(t *testing.T) {
	grains := []*internalpb.Grain{
		internalpb.Grain_builder{GrainId: internalpb.GrainId_builder{Value: "k/g1"}.Build(), Host: "127.0.0.1", Port: 8080}.Build(),
		internalpb.Grain_builder{GrainId: internalpb.GrainId_builder{Value: "k/g2"}.Build(), Host: "127.0.0.2", Port: 9002}.Build(),
		internalpb.Grain_builder{GrainId: internalpb.GrainId_builder{Value: "k/g3"}.Build(), Host: "127.0.0.1", Port: 8080}.Build(),
	}

	encoded := make(map[string][]byte, len(grains))
	for _, grain := range grains {
		key := composeKey(namespaceGrains, grain.GetGrainId().GetValue())
		value, err := encodeGrain(grain)
		require.NoError(t, err)
		encoded[key] = value
	}

	keys := make([]string, 0, len(encoded))
	for key := range encoded {
		keys = append(keys, key)
	}

	cl := &cluster{
		running: atomic.NewBool(true),
		logger:  log.DiscardLogger,
		dmap: &MockDMap{
			scanFn: func(ctx context.Context, options ...olric.ScanOption) (olric.Iterator, error) {
				return &iteratorStub{keys: keys}, nil
			},
			getFn: func(ctx context.Context, key string) (*olric.GetResponse, error) {
				return newGetResponseWithValue(encoded[key]), nil
			},
		},
	}

	result, err := cl.GrainsByHost(context.Background(), "127.0.0.1", 8080, time.Second)
	require.NoError(t, err)
	require.Len(t, result, 2)
	for _, grain := range result {
		require.Equal(t, "127.0.0.1", grain.GetHost())
		require.Equal(t, int32(8080), grain.GetPort())
	}
}

// nolint
func TestGetGrainReturnsDMapError(t *testing.T) {
	expectedErr := errors.New("get failure")
	cl := &cluster{
		running:     atomic.NewBool(true),
		logger:      log.DiscardLogger,
		readTimeout: time.Second,
		dmap: &MockDMap{
			getFn: func(ctx context.Context, key string) (*olric.GetResponse, error) {
				require.Equal(t, composeKey(namespaceGrains, "grain"), key)
				return nil, expectedErr
			},
		},
	}

	grain, err := cl.GetGrain(context.Background(), "grain")
	require.Nil(t, grain)
	require.ErrorIs(t, err, expectedErr)
}

// nolint
func TestGrainsReturnsScanError(t *testing.T) {
	expectedErr := errors.New("scan failure")
	cl := &cluster{
		running: atomic.NewBool(true),
		logger:  log.DiscardLogger,
		dmap: &MockDMap{
			scanFn: func(ctx context.Context, options ...olric.ScanOption) (olric.Iterator, error) {
				return nil, expectedErr
			},
		},
	}

	grains, err := cl.Grains(context.Background(), time.Second)
	require.Nil(t, grains)
	require.ErrorIs(t, err, expectedErr)
}

// nolint
func TestGrainsPropagatesGetError(t *testing.T) {
	expectedErr := errors.New("grains get failure")
	cl := &cluster{
		running: atomic.NewBool(true),
		logger:  log.DiscardLogger,
		dmap: &MockDMap{
			scanFn: func(ctx context.Context, options ...olric.ScanOption) (olric.Iterator, error) {
				return &iteratorStub{keys: []string{composeKey(namespaceGrains, "grain")}}, nil
			},
			getFn: func(ctx context.Context, key string) (*olric.GetResponse, error) {
				require.Equal(t, composeKey(namespaceGrains, "grain"), key)
				return nil, expectedErr
			},
		},
	}

	grains, err := cl.Grains(context.Background(), time.Second)
	require.Nil(t, grains)
	require.ErrorIs(t, err, expectedErr)
}

// nolint
func TestGrainsPropagatesByteError(t *testing.T) {
	cl := &cluster{
		running: atomic.NewBool(true),
		logger:  log.DiscardLogger,
		dmap: &MockDMap{
			scanFn: func(ctx context.Context, options ...olric.ScanOption) (olric.Iterator, error) {
				return &iteratorStub{keys: []string{composeKey(namespaceGrains, "grain")}}, nil
			},
			getFn: func(ctx context.Context, key string) (*olric.GetResponse, error) {
				require.Equal(t, composeKey(namespaceGrains, "grain"), key)
				return &olric.GetResponse{}, nil
			},
		},
	}

	grains, err := cl.Grains(context.Background(), time.Second)
	require.Nil(t, grains)
	require.ErrorIs(t, err, olric.ErrNilResponse)
}

// nolint
func TestPutJobKeyPropagatesDMapError(t *testing.T) {
	expectedErr := errors.New("put failure")
	cl := &cluster{
		running:      atomic.NewBool(true),
		logger:       log.DiscardLogger,
		writeTimeout: time.Second,
		dmap:         &MockDMap{putErr: expectedErr},
	}

	err := cl.PutJobKey(context.Background(), "job", []byte("data"))
	require.ErrorIs(t, err, expectedErr)
}

// nolint
func TestPutJobKeyStoresMetadata(t *testing.T) {
	ctx := context.Background()
	jobID := "job"
	metadata := []byte("payload")

	cl := &cluster{
		running:      atomic.NewBool(true),
		logger:       log.DiscardLogger,
		writeTimeout: time.Second,
		dmap: &MockDMap{
			putFn: func(_ context.Context, key string, value any, _ ...olric.PutOption) error {
				require.Equal(t, composeKey(namespaceJobs, jobID), key)
				require.Equal(t, metadata, value)
				return nil
			},
		},
	}

	require.NoError(t, cl.PutJobKey(ctx, jobID, metadata))
}

// nolint
func TestJobKeyReturnsDMapError(t *testing.T) {
	expectedErr := errors.New("get failure")
	cl := &cluster{
		running:     atomic.NewBool(true),
		logger:      log.DiscardLogger,
		readTimeout: time.Second,
		dmap: &MockDMap{
			getFn: func(_ context.Context, key string) (*olric.GetResponse, error) {
				require.Equal(t, composeKey(namespaceJobs, "job"), key)
				return nil, expectedErr
			},
		},
	}

	value, err := cl.JobKey(context.Background(), "job")
	require.Nil(t, value)
	require.ErrorIs(t, err, expectedErr)
}

// nolint
func TestJobKeyPropagatesByteError(t *testing.T) {
	cl := &cluster{
		running:     atomic.NewBool(true),
		logger:      log.DiscardLogger,
		readTimeout: time.Second,
		dmap: &MockDMap{
			getFn: func(_ context.Context, key string) (*olric.GetResponse, error) {
				require.Equal(t, composeKey(namespaceJobs, "job"), key)
				return &olric.GetResponse{}, nil
			},
		},
	}

	value, err := cl.JobKey(context.Background(), "job")
	require.Nil(t, value)
	require.ErrorIs(t, err, olric.ErrNilResponse)
}

// nolint
func TestJobKeyReturnsMetadata(t *testing.T) {
	metadata := []byte("payload")
	cl := &cluster{
		running:     atomic.NewBool(true),
		logger:      log.DiscardLogger,
		readTimeout: time.Second,
		dmap: &MockDMap{
			getFn: func(_ context.Context, key string) (*olric.GetResponse, error) {
				require.Equal(t, composeKey(namespaceJobs, "job"), key)
				return newGetResponseWithValue(metadata), nil
			},
		},
	}

	value, err := cl.JobKey(context.Background(), "job")
	require.NoError(t, err)
	require.Equal(t, metadata, value)
}

func TestClaimScheduleFireReturnsErrorWhenKeyEmpty(t *testing.T) {
	cl := &cluster{running: atomic.NewBool(true)}
	err := cl.ClaimScheduleFire(context.Background(), "", time.Minute)
	require.EqualError(t, err, "schedule fire key is empty")
}

func TestClaimScheduleFireReturnsErrorWhenNotRunning(t *testing.T) {
	cl := &cluster{running: atomic.NewBool(false)}
	err := cl.ClaimScheduleFire(context.Background(), "key", time.Minute)
	require.ErrorIs(t, err, ErrEngineNotRunning)
}

func TestClaimScheduleFireReturnsClaimedWhenKeyExists(t *testing.T) {
	cl := &cluster{
		running:      atomic.NewBool(true),
		dmap:         &MockDMap{putErr: olric.ErrKeyFound},
		writeTimeout: time.Second,
	}

	err := cl.ClaimScheduleFire(context.Background(), "key", time.Minute)
	require.ErrorIs(t, err, ErrScheduleFireClaimed)
}

func TestClaimScheduleFirePropagatesDMapError(t *testing.T) {
	expectedErr := errors.New("put failure")
	cl := &cluster{
		running:      atomic.NewBool(true),
		dmap:         &MockDMap{putErr: expectedErr},
		writeTimeout: time.Second,
	}

	err := cl.ClaimScheduleFire(context.Background(), "key", time.Minute)
	require.ErrorIs(t, err, expectedErr)
}

func TestClaimScheduleFireSucceeds(t *testing.T) {
	var gotOptions []olric.PutOption
	cl := &cluster{
		running:      atomic.NewBool(true),
		writeTimeout: time.Second,
		dmap: &MockDMap{
			putFn: func(_ context.Context, key string, _ any, options ...olric.PutOption) error { // nolint
				require.Equal(t, composeKey(namespaceScheduleFire, "key"), key)
				gotOptions = options
				return nil
			},
		},
	}

	err := cl.ClaimScheduleFire(context.Background(), "key", time.Minute)
	require.NoError(t, err)
	// NX (claim-once) and EX (TTL) must both be applied to the write.
	require.Len(t, gotOptions, 2)
}

// nolint
func TestDeleteJobKeyPropagatesError(t *testing.T) {
	expectedErr := errors.New("delete failure")
	cl := &cluster{
		running:      atomic.NewBool(true),
		logger:       log.DiscardLogger,
		writeTimeout: time.Second,
		dmap: &MockDMap{
			deleteFn: func(_ context.Context, keys ...string) (int, error) {
				require.Equal(t, []string{composeKey(namespaceJobs, "job")}, keys)
				return 0, expectedErr
			},
		},
	}

	require.ErrorIs(t, cl.DeleteJobKey(context.Background(), "job"), expectedErr)
}

// nolint
func TestDeleteJobKeySuccess(t *testing.T) {
	cl := &cluster{
		running:      atomic.NewBool(true),
		logger:       log.DiscardLogger,
		writeTimeout: time.Second,
		dmap: &MockDMap{
			deleteFn: func(_ context.Context, keys ...string) (int, error) {
				require.Equal(t, []string{composeKey(namespaceJobs, "job")}, keys)
				return 1, nil
			},
		},
	}

	require.NoError(t, cl.DeleteJobKey(context.Background(), "job"))
}

// nolint
func TestCreateDMapReturnsClientError(t *testing.T) {
	expectedErr := errors.New("boom")
	cl := &cluster{client: &MockClient{newDMapErr: expectedErr}}

	err := cl.createDMap()
	require.ErrorIs(t, err, expectedErr)
}

// nolint
func TestCreateSubscriptionReturnsClientError(t *testing.T) {
	expectedErr := errors.New("boom")
	cl := &cluster{
		client: &MockClient{newPubSubErr: expectedErr},
		node:   &discovery.Node{Host: "127.0.0.1", PeersPort: 4000},
	}

	err := cl.createSubscription(context.Background())
	require.ErrorIs(t, err, expectedErr)
}

// nolint
func TestHandleClusterEventInvalidEnvelope(t *testing.T) {
	cl := &cluster{}
	err := cl.handleClusterEvent("not-json")
	require.ErrorContains(t, err, "unmarshal cluster event envelope")
}

// nolint
func TestHandleClusterEventInvalidNodeJoin(t *testing.T) {
	cl := &cluster{}
	payload := `{"kind":"` + events.KindNodeJoinEvent + `","node_join":123}`

	err := cl.handleClusterEvent(payload)
	require.ErrorContains(t, err, "unmarshal node join")
}

// nolint
func TestHandleClusterEventInvalidNodeLeft(t *testing.T) {
	cl := &cluster{}
	payload := `{"kind":"` + events.KindNodeLeftEvent + `","node_left":123}`

	err := cl.handleClusterEvent(payload)
	require.ErrorContains(t, err, "unmarshal node left")
}

// nolint
func TestPeersReturnsClientError(t *testing.T) {
	expectedErr := errors.New("members failure")
	cl := &cluster{
		running: atomic.NewBool(true),
		client:  &MockClient{membersErr: expectedErr},
		logger:  log.DiscardLogger,
		node:    &discovery.Node{Host: "127.0.0.1", PeersPort: 9000},
	}

	peers, err := cl.Peers(context.Background())
	require.Nil(t, peers)
	require.ErrorIs(t, err, expectedErr)
}

// nolint
func TestIsLeaderReturnsFalseOnMembersError(t *testing.T) {
	expectedErr := errors.New("members failure")
	cl := &cluster{
		running: atomic.NewBool(true),
		client:  &MockClient{membersErr: expectedErr},
		logger:  log.DiscardLogger,
		node:    &discovery.Node{Host: "127.0.0.1", PeersPort: 9000},
	}

	isLeader := cl.IsLeader(context.Background())
	require.False(t, isLeader)
}

// nolint
func TestEventsReturnsChannel(t *testing.T) {
	ch := make(chan *Event)
	cl := &cluster{events: ch}

	go func() {
		ch <- &Event{Type: NodeJoined}
	}()

	select {
	case evt := <-cl.Events():
		require.Equal(t, NodeJoined, evt.Type)
	case <-time.After(time.Second):
		t.Fatalf("expected event from channel")
	}
}

func TestTrackNodeJoinEvent(t *testing.T) {
	t.Run("waits for a convergence that lists the node", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 4000)
		node := "127.0.0.1:5000"

		trackJoin(cl, node, testCoordinator, 1)
		require.Empty(t, cl.events)

		// converged at the generation the join was observed at: that table
		// predates the join
		converge(cl, 1, node)
		require.Empty(t, cl.events)

		// converged after the join but without the node: not the join's table
		converge(cl, 2)
		require.Empty(t, cl.events)

		converge(cl, 3, node)
		requireEmitted(t, cl, NodeJoined, node)
	})

	t.Run("ignores self", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 5000)

		trackJoin(cl, cl.node.PeersAddress(), testCoordinator, 1)
		require.Empty(t, cl.pendingJoins)
	})

	t.Run("deduplicates copies and keeps the coordinator's placement", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 6000)
		node := "127.0.0.1:7000"
		converge(cl, 1)

		trackJoin(cl, node, "127.0.0.1:7100", 9)
		trackJoin(cl, node, testCoordinator, 1)
		trackJoin(cl, node, "127.0.0.1:7200", 9)
		require.Len(t, cl.pendingJoins, 1)
		require.Equal(t, testCoordinator, cl.pendingJoins[node].source)
		require.Equal(t, uint64(1), cl.pendingJoins[node].generation)

		converge(cl, 2, node)
		requireEmitted(t, cl, NodeJoined, node)
	})

	t.Run("ignores a copy of an announced join", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 4000)
		node := "127.0.0.1:5100"
		cl.nodeJoinedEventsFilter.Add(node)

		trackJoin(cl, node, testCoordinator, 1)
		require.Empty(t, cl.pendingJoins)
	})

	t.Run("records a join while the departure of the node is pending", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 4000)
		node := "127.0.0.1:5200"
		cl.nodeJoinedEventsFilter.Add(node)

		trackLeft(cl, node, testCoordinator, 1)
		trackJoin(cl, node, testCoordinator, 2)
		require.Contains(t, cl.pendingJoins, node)
	})
}

func TestTrackNodeLeftEvent(t *testing.T) {
	t.Run("waits for a convergence without the node", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 4000)
		node := "127.0.0.1:5000"

		trackLeft(cl, node, testCoordinator, 1)
		require.Empty(t, cl.events)

		converge(cl, 1, node)
		require.Empty(t, cl.events)

		converge(cl, 2)
		requireEmitted(t, cl, NodeLeft, node)
	})

	t.Run("deduplicates copies", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 5000)
		node := "127.0.0.1:6000"

		trackLeft(cl, node, testCoordinator, 1)
		trackLeft(cl, node, "127.0.0.1:6100", 1)
		require.Len(t, cl.pendingLeaves, 1)

		converge(cl, 2)
		requireEmitted(t, cl, NodeLeft, node)
	})

	t.Run("ignores a copy of an announced departure", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 4000)
		node := "127.0.0.1:9200"
		cl.nodeLeftEventsFilter.Add(node)

		trackLeft(cl, node, testCoordinator, 1)
		require.Empty(t, cl.pendingLeaves)
	})

	t.Run("records a departure while the restart of the node is pending", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 4000)
		node := "127.0.0.1:9300"
		cl.nodeLeftEventsFilter.Add(node)

		trackJoin(cl, node, testCoordinator, 1)
		trackLeft(cl, node, testCoordinator, 2)
		require.Contains(t, cl.pendingLeaves, node)
	})
}

func TestProcessRebalanceCompleteKeepsTheNewestConvergence(t *testing.T) {
	t.Run("ignores a completion without a generation", func(t *testing.T) {
		// a coordinator running an olric that announces no convergence
		cl := newEventTestCluster("127.0.0.1", 4000)
		node := "127.0.0.1:5000"

		trackJoin(cl, node, testCoordinator, 1)
		cl.processRebalanceComplete(events.RebalanceCompleteEvent{Source: testCoordinator, Epoch: 7, Members: []string{node}})
		require.Zero(t, cl.converged.generation)
		require.Empty(t, cl.events)
	})

	t.Run("ignores stale and duplicate completions from the same coordinator", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 4000)
		node := "127.0.0.1:5000"

		trackJoin(cl, node, testCoordinator, 1)
		converge(cl, 3)
		converge(cl, 2, node)
		converge(cl, 3, node)
		require.Equal(t, uint64(3), cl.converged.generation)
		require.Empty(t, cl.converged.members)
		require.Empty(t, cl.events)

		converge(cl, 4, node)
		requireEmitted(t, cl, NodeJoined, node)
	})

	t.Run("accepts a lower generation from a new coordinator", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 4000)
		node := "127.0.0.1:5000"
		converge(cl, 5)

		trackJoin(cl, node, testCoordinator, 5)
		cl.processRebalanceComplete(events.RebalanceCompleteEvent{Source: "127.0.0.1:4200", Epoch: 1, Generation: 1, Members: []string{node}})
		require.Equal(t, "127.0.0.1:4200", cl.converged.source)
		requireEmitted(t, cl, NodeJoined, node)
	})
}

// nolint
func TestHandleClusterEventSuccessCases(t *testing.T) {
	now := time.Now().UnixNano()

	t.Run("node join", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 4000)
		node := "127.0.0.1:7000"
		payload, err := json.Marshal(events.NodeJoinEvent{
			Kind:       events.KindNodeJoinEvent,
			Source:     testCoordinator,
			NodeJoin:   node,
			Generation: 1,
			Timestamp:  now,
		})
		require.NoError(t, err)

		require.NoError(t, cl.handleClusterEvent(string(payload)))

		startPayload, err := json.Marshal(events.RebalanceStartEvent{
			Kind:       events.KindRebalanceStartEvent,
			Source:     testCoordinator,
			Epoch:      11,
			Generation: 2,
			Reason:     "node-join",
			Node:       node,
			Timestamp:  now + int64(time.Millisecond),
		})
		require.NoError(t, err)

		completePayload, err := json.Marshal(events.RebalanceCompleteEvent{
			Kind:       events.KindRebalanceCompleteEvent,
			Source:     testCoordinator,
			Epoch:      11,
			Generation: 2,
			Members:    []string{cl.node.PeersAddress(), node},
			Timestamp:  now + int64(2*time.Millisecond),
		})
		require.NoError(t, err)

		require.NoError(t, cl.handleClusterEvent(string(startPayload)))
		require.NoError(t, cl.handleClusterEvent(string(completePayload)))

		select {
		case evt := <-cl.events:
			require.Equal(t, NodeJoined, evt.Type)
			joined, ok := evt.Payload.(*NodeJoinedEvent)
			require.True(t, ok)
			require.Equal(t, node, joined.Address)
			require.Equal(t, now/int64(time.Millisecond), joined.Timestamp.UnixMilli())
		default:
			t.Fatalf("expected node join event")
		}
	})

	t.Run("node left", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 5000)
		node := "127.0.0.1:8000"
		payload, err := json.Marshal(events.NodeLeftEvent{
			Kind:       events.KindNodeLeftEvent,
			Source:     testCoordinator,
			NodeLeft:   node,
			Generation: 1,
			Timestamp:  now,
		})
		require.NoError(t, err)

		require.NoError(t, cl.handleClusterEvent(string(payload)))

		completePayload, err := json.Marshal(events.RebalanceCompleteEvent{
			Kind:       events.KindRebalanceCompleteEvent,
			Source:     testCoordinator,
			Epoch:      42,
			Generation: 2,
			Members:    []string{cl.node.PeersAddress()},
			Timestamp:  now + int64(2*time.Millisecond),
		})
		require.NoError(t, err)

		require.NoError(t, cl.handleClusterEvent(string(completePayload)))

		select {
		case evt := <-cl.events:
			require.Equal(t, NodeLeft, evt.Type)
			left, ok := evt.Payload.(*NodeLeftEvent)
			require.True(t, ok)
			require.Equal(t, node, left.Address)
			require.Equal(t, now/int64(time.Millisecond), left.Timestamp.UnixMilli())
		default:
			t.Fatalf("expected node left event")
		}
	})
}

// nolint
func TestHandleClusterEventUnknownKind(t *testing.T) {
	cl := newEventTestCluster("127.0.0.1", 4000)

	require.NoError(t, cl.handleClusterEvent(`{"kind":"noop"}`))

	select {
	case <-cl.events:
		t.Fatalf("unexpected event")
	default:
	}
}

// nolint
func TestConsumeDispatchesClusterEvents(t *testing.T) {
	cl := newEventTestCluster("127.0.0.1", 4000)
	msgs := make(chan *redis.Message, 1)
	cl.messages = msgs
	cl.consumeCtx, cl.consumeCancel = context.WithCancel(context.Background())

	done := make(chan struct{})
	cl.consumeWg.Go(func() {
		cl.consume()
		close(done)
	})

	node := "127.0.0.1:9000"
	payload, err := json.Marshal(events.NodeJoinEvent{
		Kind:       events.KindNodeJoinEvent,
		Source:     testCoordinator,
		NodeJoin:   node,
		Generation: 1,
		Timestamp:  time.Now().UnixNano(),
	})
	require.NoError(t, err)

	msgs <- &redis.Message{Channel: events.ClusterEventsChannel, Payload: string(payload)}
	startPayload, err := json.Marshal(events.RebalanceStartEvent{
		Kind:       events.KindRebalanceStartEvent,
		Source:     testCoordinator,
		Epoch:      7,
		Generation: 2,
		Reason:     "node-join",
		Node:       node,
		Timestamp:  time.Now().Add(time.Millisecond).UnixNano(),
	})
	require.NoError(t, err)
	msgs <- &redis.Message{Channel: events.ClusterEventsChannel, Payload: string(startPayload)}

	completePayload, err := json.Marshal(events.RebalanceCompleteEvent{
		Kind:       events.KindRebalanceCompleteEvent,
		Source:     testCoordinator,
		Epoch:      7,
		Generation: 2,
		Members:    []string{cl.node.PeersAddress(), node},
		Timestamp:  time.Now().Add(2 * time.Millisecond).UnixNano(),
	})
	require.NoError(t, err)
	msgs <- &redis.Message{Channel: events.ClusterEventsChannel, Payload: string(completePayload)}

	select {
	case evt := <-cl.events:
		require.Equal(t, NodeJoined, evt.Type)
	case <-time.After(time.Second):
		t.Fatalf("expected event from consume")
	}

	cl.consumeCancel()
	<-done
}

// nolint
func TestStopWaitsForConsume(t *testing.T) {
	cl := newEventTestCluster("127.0.0.1", 4000)
	cl.consumeCtx, cl.consumeCancel = context.WithCancel(context.Background())
	msgs := make(chan *redis.Message, 10)
	cl.messages = msgs
	cl.shutdownTimeout = 5 * time.Second
	// Create a mock server to avoid nil pointer in Stop()
	cl.server = &olric.Olric{}

	cl.consumeWg.Go(func() {
		cl.consume()
	})

	// Send a message to ensure consume is processing
	msgs <- &redis.Message{
		Channel: events.ClusterEventsChannel,
		Payload: `{"kind":"node-join-event","node_join":"127.0.0.1:9000","timestamp":1000000}`,
	}

	// Give it time to process
	time.Sleep(50 * time.Millisecond)

	// Test the shutdown synchronization - cancel context and wait for consume
	cl.consumeCancel()
	done := make(chan struct{})
	go func() {
		cl.consumeWg.Wait()
		close(done)
	}()

	// Should wait for consume to finish (it should exit quickly after cancel)
	start := time.Now()
	select {
	case <-done:
		duration := time.Since(start)
		require.Less(t, duration, 1*time.Second, "Should complete within reasonable time")
		// Consume should exit quickly after context cancellation
		require.Greater(t, duration, time.Microsecond, "Should have waited at least briefly")
	case <-time.After(2 * time.Second):
		t.Fatal("consume did not finish")
	}
}

// nolint
func TestStopNoPanicOnClosedChannel(t *testing.T) {
	cl := newEventTestCluster("127.0.0.1", 4000)
	cl.consumeCtx, cl.consumeCancel = context.WithCancel(context.Background())
	msgs := make(chan *redis.Message)
	cl.messages = msgs
	cl.shutdownTimeout = 5 * time.Second
	close(msgs) // Close immediately

	cl.consumeWg.Go(func() {
		cl.consume()
	})

	// Wait for consume to finish
	time.Sleep(100 * time.Millisecond)

	// Test that closing events channel after consume finishes doesn't panic
	cl.consumeCancel()
	cl.consumeWg.Wait()

	// Now safe to close events channel
	require.NotPanics(t, func() {
		cl.eventsLock.Lock()
		if cl.events != nil {
			close(cl.events)
			cl.events = nil
		}
		cl.eventsLock.Unlock()
	})
}

// nolint
func TestConsumeRespectsContextCancellation(t *testing.T) {
	cl := newEventTestCluster("127.0.0.1", 4000)
	cl.consumeCtx, cl.consumeCancel = context.WithCancel(context.Background())
	msgs := make(chan *redis.Message, 10)
	cl.messages = msgs

	cl.consumeWg.Add(1)
	done := make(chan struct{})
	go func() {
		defer cl.consumeWg.Done()
		cl.consume()
		close(done)
	}()

	// Cancel context
	cl.consumeCancel()

	// Consume should exit
	select {
	case <-done:
		// Good, consume exited
	case <-time.After(time.Second):
		t.Fatal("consume did not exit on context cancellation")
	}
}

// nolint
func TestConsumeHandlesChannelClose(t *testing.T) {
	cl := newEventTestCluster("127.0.0.1", 4000)
	cl.consumeCtx, cl.consumeCancel = context.WithCancel(context.Background())
	msgs := make(chan *redis.Message, 10)
	cl.messages = msgs

	cl.consumeWg.Add(1)
	done := make(chan struct{})
	go func() {
		defer cl.consumeWg.Done()
		cl.consume()
		close(done)
	}()

	// Send a message first
	msgs <- &redis.Message{
		Channel: events.ClusterEventsChannel,
		Payload: `{"kind":"node-join-event","node_join":"127.0.0.1:9000","timestamp":1000000}`,
	}

	// Close channel
	close(msgs)

	// Consume should exit
	select {
	case <-done:
		// Good, consume exited
	case <-time.After(time.Second):
		t.Fatal("consume did not exit when channel closed")
	}
}

// nolint
func TestStopTimeoutHandling(t *testing.T) {
	cl := newEventTestCluster("127.0.0.1", 4000)
	cl.consumeCtx, cl.consumeCancel = context.WithCancel(context.Background())
	msgs := make(chan *redis.Message)
	cl.messages = msgs
	shutdownTimeout := 100 * time.Millisecond // Short timeout

	cl.consumeWg.Go(func() {
		// Simulate slow processing - block on reading from channel
		select {
		case <-msgs:
		case <-time.After(500 * time.Millisecond):
		}
		cl.consume()
	})

	// Test timeout behavior
	ctx, cancelFn := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelFn()

	cl.consumeCancel()
	done := make(chan struct{})
	go func() {
		cl.consumeWg.Wait()
		close(done)
	}()

	start := time.Now()
	select {
	case <-done:
		t.Fatal("consume finished too quickly")
	case <-ctx.Done():
		duration := time.Since(start)
		require.GreaterOrEqual(t, duration, 100*time.Millisecond)
		require.Less(t, duration, 200*time.Millisecond) // Should timeout quickly
	}
}

// nolint
func TestSendEventLockedHandlesNilChannel(t *testing.T) {
	cl := newEventTestCluster("127.0.0.1", 4000)
	cl.events = nil

	// Should not panic
	require.NotPanics(t, func() {
		cl.sendEventLocked(&Event{Type: NodeJoined})
	})
}

// nolint
func TestPeersFiltersSelfAndParsesMeta(t *testing.T) {
	cl := &cluster{
		running: atomic.NewBool(true),
		logger:  log.DiscardLogger,
		node:    &discovery.Node{Host: "127.0.0.1", PeersPort: 4000, RemotingPort: 4001, DiscoveryPort: 3000},
	}

	other := &discovery.Node{Host: "10.0.0.1", PeersPort: 5000, RemotingPort: 7000, DiscoveryPort: 3001}
	selfMeta, err := json.Marshal(cl.node)
	require.NoError(t, err)
	otherMeta, err := json.Marshal(other)
	require.NoError(t, err)

	cl.client = &MockOlricClient{
		MockClient: &MockClient{},
		members: []olric.Member{
			{Name: cl.node.PeersAddress(), Coordinator: true, Meta: string(selfMeta)},
			{Name: other.PeersAddress(), Coordinator: false, Meta: string(otherMeta)},
		},
	}

	peers, err := cl.Peers(context.Background())
	require.NoError(t, err)
	require.Len(t, peers, 1)
	require.Equal(t, other.Host, peers[0].Host)
	require.Equal(t, other.PeersPort, peers[0].PeersPort)
	require.Equal(t, other.RemotingPort, peers[0].RemotingPort)
	require.Equal(t, other.DiscoveryPort, peers[0].DiscoveryPort)
	require.False(t, peers[0].Coordinator)
	require.Empty(t, peers[0].Roles)
}

// nolint
func TestIsLeaderReturnsTrueWhenCoordinator(t *testing.T) {
	cl := &cluster{
		running: atomic.NewBool(true),
		logger:  log.DiscardLogger,
		node:    &discovery.Node{Host: "127.0.0.1", PeersPort: 4000},
	}

	meta, err := json.Marshal(cl.node)
	require.NoError(t, err)

	cl.client = &MockOlricClient{
		MockClient: &MockClient{},
		members: []olric.Member{
			{Name: cl.node.PeersAddress(), Coordinator: true, Meta: string(meta)},
		},
	}

	require.True(t, cl.IsLeader(context.Background()))
}

func TestBuildConfigWithTLSAndDebug(t *testing.T) {
	info := &gtls.Info{
		ClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		ServerConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	cl := &cluster{
		logger:  log.DebugLogger,
		node:    &discovery.Node{Host: "127.0.0.1", PeersPort: 3322, DiscoveryPort: 3323},
		tlsInfo: info,
	}

	cfg, err := cl.buildConfig()
	require.NoError(t, err)
	require.NotNil(t, cfg.TLS)
	require.NotNil(t, cfg.Client.TLS)
	require.False(t, cfg.Client.DisableRedisLogging)
	require.EqualValues(t, oconfig.DefaultLogVerbosity, cfg.LogVerbosity)
}

func TestSetupMemberlistConfigWithTLS(t *testing.T) {
	cert, err := tls.LoadX509KeyPair("../../test/data/certs/auto.pem", "../../test/data/certs/auto.key")
	require.NoError(t, err)
	info := &gtls.Info{
		ClientConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		ServerConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
	}
	cl := &cluster{
		logger:  log.DiscardLogger,
		node:    &discovery.Node{Host: "127.0.0.1", PeersPort: 3322, DiscoveryPort: 3323},
		tlsInfo: info,
	}

	cfg := &oconfig.Config{}
	err = cl.setupMemberlistConfig(cfg)
	require.NoError(t, err)
	require.NotNil(t, cfg.MemberlistConfig)
	require.NotNil(t, cfg.MemberlistConfig.Transport)
}

func newOlricMember(t *testing.T, host string, peersPort int, coordinator bool) olric.Member {
	t.Helper()
	node := &discovery.Node{
		Name:          host,
		Host:          host,
		DiscoveryPort: 1,
		PeersPort:     peersPort,
		RemotingPort:  2,
	}
	meta, err := json.Marshal(node)
	require.NoError(t, err)
	return olric.Member{
		Name:        net.JoinHostPort(host, strconv.Itoa(peersPort)),
		Meta:        string(meta),
		Coordinator: coordinator,
	}
}

func TestCoordinatorAddress(t *testing.T) {
	t.Run("returns the coordinator peers address", func(t *testing.T) {
		leader := newOlricMember(t, "127.0.0.1", 3001, true)
		follower := newOlricMember(t, "127.0.0.1", 3002, false)
		cl := &cluster{
			running: atomic.NewBool(true),
			logger:  log.DiscardLogger,
			client:  &MockOlricClient{MockClient: &MockClient{}, members: []olric.Member{follower, leader}},
		}

		require.Equal(t, "127.0.0.1:3001", cl.coordinatorAddress(context.Background()))
	})

	t.Run("returns empty when the membership fetch fails", func(t *testing.T) {
		cl := &cluster{
			running: atomic.NewBool(true),
			logger:  log.DiscardLogger,
			client:  &MockOlricClient{MockClient: &MockClient{membersErr: errors.New("boom")}},
		}

		require.Empty(t, cl.coordinatorAddress(context.Background()))
	})

	t.Run("returns empty when no coordinator is flagged", func(t *testing.T) {
		m1 := newOlricMember(t, "127.0.0.1", 3001, false)
		m2 := newOlricMember(t, "127.0.0.1", 3002, false)
		cl := &cluster{
			running: atomic.NewBool(true),
			logger:  log.DiscardLogger,
			client:  &MockOlricClient{MockClient: &MockClient{}, members: []olric.Member{m1, m2}},
		}

		require.Empty(t, cl.coordinatorAddress(context.Background()))
	})

	t.Run("returns empty when the client is not initialised", func(t *testing.T) {
		cl := &cluster{
			running: atomic.NewBool(true),
			logger:  log.DiscardLogger,
		}

		require.Empty(t, cl.coordinatorAddress(context.Background()))
	})
}

func TestDetectLeaderChange(t *testing.T) {
	t.Run("emits LeaderChanged when the coordinator changes", func(t *testing.T) {
		leader := newOlricMember(t, "127.0.0.1", 3002, true)
		cl := &cluster{
			running:             atomic.NewBool(true),
			logger:              log.DiscardLogger,
			readTimeout:         time.Second,
			lastCoordinatorAddr: "127.0.0.1:3001",
			client:              &MockOlricClient{MockClient: &MockClient{}, members: []olric.Member{leader}},
			events:              make(chan *Event, 1),
		}

		cl.detectLeaderChangeLocked()

		require.Equal(t, "127.0.0.1:3002", cl.lastCoordinatorAddr)
		select {
		case event := <-cl.events:
			require.Equal(t, LeaderChanged, event.Type)
			changed, ok := event.Payload.(*LeaderChangedEvent)
			require.True(t, ok)
			require.Equal(t, "127.0.0.1:3002", changed.Address)
			require.False(t, changed.Timestamp.IsZero())
		default:
			t.Fatal("expected a LeaderChanged event")
		}
	})

	t.Run("does not emit when the coordinator is unchanged", func(t *testing.T) {
		leader := newOlricMember(t, "127.0.0.1", 3001, true)
		cl := &cluster{
			running:             atomic.NewBool(true),
			logger:              log.DiscardLogger,
			readTimeout:         time.Second,
			lastCoordinatorAddr: "127.0.0.1:3001",
			client:              &MockOlricClient{MockClient: &MockClient{}, members: []olric.Member{leader}},
			events:              make(chan *Event, 1),
		}

		cl.detectLeaderChangeLocked()

		require.Equal(t, "127.0.0.1:3001", cl.lastCoordinatorAddr)
		require.Empty(t, cl.events)
	})

	t.Run("preserves the baseline when the membership fetch fails", func(t *testing.T) {
		cl := &cluster{
			running:             atomic.NewBool(true),
			logger:              log.DiscardLogger,
			readTimeout:         time.Second,
			lastCoordinatorAddr: "127.0.0.1:3001",
			client:              &MockOlricClient{MockClient: &MockClient{membersErr: errors.New("boom")}},
			events:              make(chan *Event, 1),
		}

		cl.detectLeaderChangeLocked()

		require.Equal(t, "127.0.0.1:3001", cl.lastCoordinatorAddr)
		require.Empty(t, cl.events)
	})
}

// collectLeaderChanges drains a node's event stream for up to timeout, returning
// every LeaderChanged event observed in that window.
func collectLeaderChanges(t *testing.T, node Cluster, timeout time.Duration) []*LeaderChangedEvent {
	t.Helper()
	var changes []*LeaderChangedEvent
	deadline := time.After(timeout)
	for {
		select {
		case event, ok := <-node.Events():
			if !ok {
				return changes
			}
			if changed, ok := event.Payload.(*LeaderChangedEvent); ok {
				changes = append(changes, changed)
			}
		case <-deadline:
			return changes
		}
	}
}

func TestLeaderChangedEvent(t *testing.T) {
	t.Run("emits LeaderChanged when the coordinator departs", func(t *testing.T) {
		ctx := context.TODO()
		srv := startNatsServer(t)

		// node1 starts first and is therefore the sole coordinator
		node1, sd1 := startEngine(t, srv.Addr().String())
		require.NotNil(t, node1)
		pause.For(2 * time.Second)

		// node2 joins the already-formed cluster
		node2, sd2 := startEngine(t, srv.Addr().String())
		require.NotNil(t, node2)
		node2Addr := node2.(*cluster).node.PeersAddress()
		pause.For(2 * time.Second)

		require.True(t, node1.IsLeader(ctx))
		require.False(t, node2.IsLeader(ctx))

		// the initial coordinator is seeded silently, so no LeaderChanged surfaces
		// on node2 while the cluster is stable
		require.Empty(t, collectLeaderChanges(t, node2, time.Second))

		// the coordinator leaves the cluster
		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, sd1.Close())

		// node2 is promoted and emits exactly one LeaderChanged carrying its address
		changes := collectLeaderChanges(t, node2, 10*time.Second)
		require.Len(t, changes, 1)
		require.Equal(t, node2Addr, changes[0].Address)
		require.False(t, changes[0].Timestamp.IsZero())
		require.True(t, node2.IsLeader(ctx))

		require.NoError(t, node2.Stop(ctx))
		require.NoError(t, sd2.Close())
		srv.Shutdown()
	})

	t.Run("does not emit LeaderChanged when a non-coordinator departs", func(t *testing.T) {
		ctx := context.TODO()
		srv := startNatsServer(t)

		node1, sd1 := startEngine(t, srv.Addr().String())
		require.NotNil(t, node1)
		pause.For(2 * time.Second)

		node2, sd2 := startEngine(t, srv.Addr().String())
		require.NotNil(t, node2)
		pause.For(time.Second)

		// node3 is the newest node, so it is never the coordinator
		node3, sd3 := startEngine(t, srv.Addr().String())
		require.NotNil(t, node3)
		pause.For(2 * time.Second)

		require.True(t, node1.IsLeader(ctx))

		// draining node1 up to here clears the join events
		collectLeaderChanges(t, node1, time.Second)

		// a non-coordinator departure leaves the coordinator unchanged
		require.NoError(t, node3.Stop(ctx))
		require.NoError(t, sd3.Close())
		pause.For(3 * time.Second)

		require.Empty(t, collectLeaderChanges(t, node1, 2*time.Second))
		require.True(t, node1.IsLeader(ctx))

		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, node2.Stop(ctx))
		require.NoError(t, sd2.Close())
		srv.Shutdown()
	})
}

// TestLastRebalanceEvent verifies the rebalance-activity signal consumed by
// the crash-recovery gate: zero before any olric rebalance event is observed,
// and the recorded instant afterwards.
func TestLastRebalanceEvent(t *testing.T) {
	cl := new(cluster)
	assert.True(t, cl.LastRebalanceEvent().IsZero())

	now := time.Now()
	cl.lastRebalanceEventNanos.Store(now.UnixNano())
	assert.WithinDuration(t, now, cl.LastRebalanceEvent(), time.Millisecond)
}

func TestStartReturnsNilWhenAlreadyRunning(t *testing.T) {
	cl := &cluster{running: atomic.NewBool(true)}
	require.NoError(t, cl.Start(context.Background()))
}

func TestActorExistsPropagatesDMapError(t *testing.T) {
	// only a key-not-found maps to a clean false; any other read failure must
	// surface so callers do not mistake an outage for absence
	expectedErr := errors.New("get failure")
	cl := &cluster{
		running:     atomic.NewBool(true),
		logger:      log.DiscardLogger,
		readTimeout: time.Second,
		dmap: &MockDMap{
			getFn: func(ctx context.Context, key string) (*olric.GetResponse, error) { // nolint
				return nil, expectedErr
			},
		},
	}

	exists, err := cl.ActorExists(context.Background(), "actor")
	require.False(t, exists)
	require.ErrorIs(t, err, expectedErr)
}

func TestGrainExistsPropagatesDMapError(t *testing.T) {
	expectedErr := errors.New("get failure")
	cl := &cluster{
		running:     atomic.NewBool(true),
		logger:      log.DiscardLogger,
		readTimeout: time.Second,
		dmap: &MockDMap{
			getFn: func(ctx context.Context, key string) (*olric.GetResponse, error) { // nolint
				return nil, expectedErr
			},
		},
	}

	exists, err := cl.GrainExists(context.Background(), "grain")
	require.False(t, exists)
	require.ErrorIs(t, err, expectedErr)
}

func TestCountActorsByHostReturnsErrorWhenNotRunning(t *testing.T) {
	cl := &cluster{running: atomic.NewBool(false)}

	counts, err := cl.CountActorsByHost(context.Background(), time.Second)
	require.Nil(t, counts)
	require.ErrorIs(t, err, ErrEngineNotRunning)
}

func TestCountActorsByHostSkipsMalformedAddress(t *testing.T) {
	// a record whose address does not parse cannot be attributed to a host; it
	// must be skipped rather than counted under a bogus key or failing the scan
	good := address.New("a", "system", "127.0.0.1", 8080)
	goodKey := composeKey(namespaceActors, good.String())
	goodValue, err := encode(internalpb.Actor_builder{Address: good.String()}.Build())
	require.NoError(t, err)

	badKey := composeKey(namespaceActors, "malformed")
	badValue, err := encode(internalpb.Actor_builder{Address: "not-a-valid-address"}.Build())
	require.NoError(t, err)

	values := map[string][]byte{goodKey: goodValue, badKey: badValue}

	cl := &cluster{
		running: atomic.NewBool(true),
		logger:  log.DiscardLogger,
		dmap: &MockDMap{
			scanFn: func(ctx context.Context, options ...olric.ScanOption) (olric.Iterator, error) { // nolint
				return &iteratorStub{keys: []string{goodKey, badKey}}, nil
			},
			getFn: func(ctx context.Context, key string) (*olric.GetResponse, error) { // nolint
				return newGetResponseWithValue(values[key]), nil
			},
		},
	}

	counts, err := cl.CountActorsByHost(context.Background(), time.Second)
	require.NoError(t, err)
	require.Equal(t, map[string]int{address.FormatHostPort("127.0.0.1", 8080): 1}, counts)
}

func TestBuildConfigMapsLogLevels(t *testing.T) {
	// the engine log level is derived from the goakt logger level; the error
	// family and the warning level have their own mappings
	node := &discovery.Node{Host: "127.0.0.1", PeersPort: 3322, DiscoveryPort: 3323}

	errorCl := &cluster{logger: log.NewZap(log.ErrorLevel, io.Discard), node: node}
	cfg, err := errorCl.buildConfig()
	require.NoError(t, err)
	require.Equal(t, "ERROR", cfg.LogLevel)

	warnCl := &cluster{logger: log.NewZap(log.WarningLevel, io.Discard), node: node}
	cfg, err = warnCl.buildConfig()
	require.NoError(t, err)
	require.Equal(t, "WARN", cfg.LogLevel)
}

func TestSetupMemberlistConfigReturnsTLSTransportError(t *testing.T) {
	// the TLS transport binds during construction and rejects a host that is
	// not a literal IP, so a bad bind host must surface as an error
	info := &gtls.Info{
		ClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		ServerConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	cl := &cluster{
		logger:  log.DiscardLogger,
		node:    &discovery.Node{Host: "not-an-ip-literal", PeersPort: 3322, DiscoveryPort: 3323},
		tlsInfo: info,
	}

	require.Error(t, cl.setupMemberlistConfig(&oconfig.Config{}))
}

func TestBootstrapReturnsMemberlistConfigError(t *testing.T) {
	// bootstrap must wrap and return the memberlist configuration failure
	// before any engine state is created
	info := &gtls.Info{
		ClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		ServerConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	cl := &cluster{
		logger:  log.DiscardLogger,
		node:    &discovery.Node{Host: "not-an-ip-literal", PeersPort: 3322, DiscoveryPort: 3323},
		tlsInfo: info,
	}

	err := cl.bootstrap(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to configure memberlist")
}

func TestBootstrapReturnsEngineConstructionError(t *testing.T) {
	// a bind host that is neither an IP nor a resolvable name fails olric's
	// network setup, so engine construction errors after the config and
	// discovery steps succeed; the '!' keeps the name invalid to the resolver
	// itself, so no environment can resolve it
	provider := new(mocksdiscovery.Provider)
	provider.EXPECT().ID().Return("testDisco")

	ports := dynaport.Get(2)
	cl := &cluster{
		logger:            log.DiscardLogger,
		node:              &discovery.Node{Name: "invalid_host!", Host: "invalid_host!", DiscoveryPort: ports[0], PeersPort: ports[1]},
		discoveryProvider: provider,
	}

	err := cl.bootstrap(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to start cluster engine")
}

func TestConsumeToleratesHandlerErrorsAndForeignChannels(t *testing.T) {
	cl := newEventTestCluster("127.0.0.1", 4000)
	msgs := make(chan *redis.Message, 3)
	cl.messages = msgs
	cl.consumeCtx, cl.consumeCancel = context.WithCancel(context.Background())

	done := make(chan struct{})
	cl.consumeWg.Go(func() {
		cl.consume()
		close(done)
	})

	// a malformed payload is logged and must not kill the consumer, and a
	// message on a foreign channel is ignored
	msgs <- &redis.Message{Channel: events.ClusterEventsChannel, Payload: "{invalid"}
	msgs <- &redis.Message{Channel: "foreign-channel", Payload: "ignored"}

	// the consumer is still alive: a valid join event must still be tracked
	node := "127.0.0.1:9100"
	payload, err := json.Marshal(events.NodeJoinEvent{
		Kind:      events.KindNodeJoinEvent,
		NodeJoin:  node,
		Timestamp: time.Now().UnixNano(),
	})
	require.NoError(t, err)
	msgs <- &redis.Message{Channel: events.ClusterEventsChannel, Payload: string(payload)}

	require.Eventually(t, func() bool {
		cl.eventsLock.Lock()
		defer cl.eventsLock.Unlock()
		_, tracked := cl.pendingJoins[node]
		return tracked
	}, time.Second, 10*time.Millisecond)

	cl.consumeCancel()
	<-done
}

func TestHandleClusterEventInvalidRebalanceStart(t *testing.T) {
	cl := newEventTestCluster("127.0.0.1", 4000)
	payload := fmt.Sprintf(`{"kind":%q,"epoch":{}}`, events.KindRebalanceStartEvent)
	require.ErrorContains(t, cl.handleClusterEvent(payload), "unmarshal rebalance start")
}

func TestHandleClusterEventInvalidRebalanceComplete(t *testing.T) {
	cl := newEventTestCluster("127.0.0.1", 4000)
	payload := fmt.Sprintf(`{"kind":%q,"epoch":{}}`, events.KindRebalanceCompleteEvent)
	require.ErrorContains(t, cl.handleClusterEvent(payload), "unmarshal rebalance complete")
}

func TestMembershipEventTrackedAfterConvergence(t *testing.T) {
	// the convergence reflecting a change can be delivered before the change
	t.Run("coordinator copy observed before the convergence is announced right away", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 4000)
		node := "127.0.0.1:9400"
		converge(cl, 2, node)

		trackJoin(cl, node, testCoordinator, 1)
		requireEmitted(t, cl, NodeJoined, node)
	})

	t.Run("coordinator copy observed at the converged generation waits", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 4000)
		node := "127.0.0.1:9410"
		converge(cl, 2, node)

		trackJoin(cl, node, testCoordinator, 2)
		require.Empty(t, cl.events)

		converge(cl, 3, node)
		requireEmitted(t, cl, NodeJoined, node)
	})

	t.Run("copy from another node waits for the next convergence", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 4000)
		node := "127.0.0.1:9420"
		converge(cl, 2, node)

		trackJoin(cl, node, "127.0.0.1:7000", 9)
		require.Empty(t, cl.events)

		converge(cl, 3, node)
		requireEmitted(t, cl, NodeJoined, node)
	})

	t.Run("departure", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 4000)
		node := "127.0.0.1:9430"
		converge(cl, 2)

		trackLeft(cl, node, testCoordinator, 1)
		requireEmitted(t, cl, NodeLeft, node)
	})
}

func TestNodePendingAsJoinedAndDeparted(t *testing.T) {
	t.Run("departed then restarted is a member", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 4000)
		node := "127.0.0.1:9500"

		trackLeft(cl, node, testCoordinator, 1)
		trackJoin(cl, node, testCoordinator, 2)
		converge(cl, 3, node)

		require.Len(t, cl.events, 2)
		requireNextEvent(t, cl, NodeLeft, node)
		requireNextEvent(t, cl, NodeJoined, node)
	})

	t.Run("joined then departed is not a member", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 4000)
		node := "127.0.0.1:9510"

		trackJoin(cl, node, testCoordinator, 1)
		trackLeft(cl, node, testCoordinator, 2)
		converge(cl, 3)

		require.Len(t, cl.events, 2)
		requireNextEvent(t, cl, NodeJoined, node)
		requireNextEvent(t, cl, NodeLeft, node)
	})

	t.Run("convergence reflecting only the join", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 4000)
		node := "127.0.0.1:9520"

		trackJoin(cl, node, testCoordinator, 1)
		trackLeft(cl, node, testCoordinator, 2)
		converge(cl, 2, node)
		requireEmitted(t, cl, NodeJoined, node)
		require.Contains(t, cl.pendingLeaves, node)

		converge(cl, 3)
		requireEmitted(t, cl, NodeLeft, node)
	})

	t.Run("convergence reflecting only the departure of a restart", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 4000)
		node := "127.0.0.1:9530"

		trackLeft(cl, node, testCoordinator, 1)
		trackJoin(cl, node, testCoordinator, 2)
		converge(cl, 2)
		requireEmitted(t, cl, NodeLeft, node)
		require.Contains(t, cl.pendingJoins, node)

		converge(cl, 3, node)
		requireEmitted(t, cl, NodeJoined, node)
	})
}

func TestStaleDepartureOfLiveMemberIsDropped(t *testing.T) {
	// a lagging member can replay a dead notification for a node that already
	// restarted; the coordinator observed that departure and still converged
	// with the node as a member
	cl := newEventTestCluster("127.0.0.1", 4000)
	node := "127.0.0.1:9600"
	converge(cl, 1, node)

	trackLeft(cl, node, testCoordinator, 1)
	timer := cl.pendingLeaves[node].timer
	converge(cl, 2, node)
	require.Empty(t, cl.pendingLeaves)
	require.Empty(t, cl.events)
	require.False(t, timer.Stop())

	// the same departure known only from another node's copy cannot be placed
	// and is kept
	trackLeft(cl, node, "127.0.0.1:7000", 9)
	converge(cl, 3, node)
	require.Contains(t, cl.pendingLeaves, node)
	require.Empty(t, cl.events)
}

func TestRestartedNodeDepartsAgain(t *testing.T) {
	// a member that departs, restarts at the same address and departs again
	// must be announced each time
	cl := newEventTestCluster("127.0.0.1", 4000)
	node := "127.0.0.1:9700"

	trackLeft(cl, node, testCoordinator, 1)
	converge(cl, 2)
	requireEmitted(t, cl, NodeLeft, node)

	trackJoin(cl, node, testCoordinator, 2)
	converge(cl, 3, node)
	requireEmitted(t, cl, NodeJoined, node)

	trackLeft(cl, node, testCoordinator, 3)
	converge(cl, 4)
	requireEmitted(t, cl, NodeLeft, node)
}

func TestSecondDepartureWhileRestartIsPending(t *testing.T) {
	cl := newEventTestCluster("127.0.0.1", 4000)
	node := "127.0.0.1:9710"

	trackLeft(cl, node, testCoordinator, 1)
	converge(cl, 2)
	requireEmitted(t, cl, NodeLeft, node)

	// restarted, then crashed again before the table converged on the restart
	trackJoin(cl, node, testCoordinator, 2)
	trackLeft(cl, node, testCoordinator, 3)
	require.Contains(t, cl.pendingLeaves, node)

	converge(cl, 4)
	require.Len(t, cl.events, 2)
	requireNextEvent(t, cl, NodeJoined, node)
	requireNextEvent(t, cl, NodeLeft, node)
}

func TestOverdueReleasesNodeEventsInObservationOrder(t *testing.T) {
	t.Run("joined then departed", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 4000)
		node := "127.0.0.1:9800"

		trackJoin(cl, node, testCoordinator, 1)
		trackLeft(cl, node, testCoordinator, 2)
		joinTimer := cl.pendingJoins[node].timer
		leftTimer := cl.pendingLeaves[node].timer

		cl.emitOverdueNodeLeft(node)
		require.Len(t, cl.events, 2)
		requireNextEvent(t, cl, NodeJoined, node)
		requireNextEvent(t, cl, NodeLeft, node)
		require.False(t, joinTimer.Stop())
		require.False(t, leftTimer.Stop())
	})

	t.Run("departed then restarted", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 4000)
		node := "127.0.0.1:9810"

		trackLeft(cl, node, testCoordinator, 1)
		trackJoin(cl, node, testCoordinator, 2)

		cl.emitOverdueNodeJoined(node)
		require.Len(t, cl.events, 2)
		requireNextEvent(t, cl, NodeLeft, node)
		requireNextEvent(t, cl, NodeJoined, node)
	})

	t.Run("already announced is a no-op", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 4000)
		node := "127.0.0.1:9820"

		trackLeft(cl, node, testCoordinator, 1)
		converge(cl, 2)
		requireEmitted(t, cl, NodeLeft, node)

		cl.emitOverdueNodeLeft(node)
		cl.emitOverdueNodeJoined(node)
		require.Empty(t, cl.events)
	})
}

func TestPendingEventIsAnnouncedAfterTheBoundedWait(t *testing.T) {
	// with no convergence reflecting them, a departure and a join are announced
	// once the bounded wait elapses
	cl := newEventTestCluster("127.0.0.1", 4000)
	cl.pendingEmitTimeout = 20 * time.Millisecond
	leaver := "127.0.0.1:9920"
	joiner := "127.0.0.1:9921"

	trackLeft(cl, leaver, testCoordinator, 1)
	trackJoin(cl, joiner, testCoordinator, 1)

	require.Eventually(t, func() bool {
		cl.eventsLock.Lock()
		defer cl.eventsLock.Unlock()

		return len(cl.pendingLeaves) == 0 && len(cl.pendingJoins) == 0
	}, time.Second, 5*time.Millisecond)

	require.Len(t, cl.events, 2)
	announced := map[EventType]int{}
	for range 2 {
		announced[(<-cl.events).Type]++
	}

	require.Equal(t, 1, announced[NodeLeft])
	require.Equal(t, 1, announced[NodeJoined])
}

func TestPendingEventTimerIsCancelledOnAnnounce(t *testing.T) {
	// the bounded-wait timer is a safety net; once the event is announced the
	// timer must not linger for the remainder of the wait
	t.Run("node left", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 4000)
		node := "127.0.0.1:9850"

		trackLeft(cl, node, testCoordinator, 1)
		timer := cl.pendingLeaves[node].timer
		require.NotNil(t, timer)

		converge(cl, 2)
		requireEmitted(t, cl, NodeLeft, node)

		// Stop reports false for a timer that was already stopped
		require.False(t, timer.Stop())
	})

	t.Run("node joined", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 4000)
		node := "127.0.0.1:9860"

		trackJoin(cl, node, testCoordinator, 1)
		timer := cl.pendingJoins[node].timer
		require.NotNil(t, timer)

		converge(cl, 2, node)
		requireEmitted(t, cl, NodeJoined, node)
		require.False(t, timer.Stop())
	})
}

func TestCancelPendingEventsLocked(t *testing.T) {
	// stopping the engine drops every pending event and its timer
	cl := newEventTestCluster("127.0.0.1", 4000)
	joiner := "127.0.0.1:9900"
	leaver := "127.0.0.1:9901"
	converge(cl, 1, leaver)

	trackJoin(cl, joiner, testCoordinator, 1)
	trackLeft(cl, leaver, testCoordinator, 1)
	joinTimer := cl.pendingJoins[joiner].timer
	leftTimer := cl.pendingLeaves[leaver].timer

	cl.eventsLock.Lock()
	cl.cancelPendingEventsLocked()
	cl.eventsLock.Unlock()

	require.Empty(t, cl.pendingJoins)
	require.Empty(t, cl.pendingLeaves)
	require.Zero(t, cl.converged.generation)
	require.False(t, joinTimer.Stop())
	require.False(t, leftTimer.Stop())
	require.Empty(t, cl.events)
}

func TestTrackingIsIgnoredAfterStop(t *testing.T) {
	// a membership event processed after the engine closed its events channel
	// must not arm a timer that outlives the engine
	cl := newEventTestCluster("127.0.0.1", 4000)
	cl.events = nil

	trackJoin(cl, "127.0.0.1:9910", testCoordinator, 1)
	trackLeft(cl, "127.0.0.1:9911", testCoordinator, 1)
	require.Empty(t, cl.pendingJoins)
	require.Empty(t, cl.pendingLeaves)
}

func TestEmitNodeLeftLockedDeduplicates(t *testing.T) {
	cl := newEventTestCluster("127.0.0.1", 4000)
	node := "127.0.0.1:9500"
	cl.nodeLeftEventsFilter.Add(node)

	cl.emitNodeLeftLocked(node, time.Now().UnixNano())

	require.Zero(t, len(cl.events))
}

func TestEmitNodeJoinedLockedDeduplicates(t *testing.T) {
	cl := newEventTestCluster("127.0.0.1", 4000)
	node := "127.0.0.1:9600"
	cl.nodeJoinedEventsFilter.Add(node)

	cl.emitNodeJoinedLocked(node, time.Now().UnixNano())

	require.Zero(t, len(cl.events))
}

func TestStopWarnsWhenConsumeExceedsShutdownTimeout(t *testing.T) {
	// when the consume goroutines outlive the shutdown budget, Stop must warn
	// and proceed with the teardown instead of blocking forever
	ctx := context.TODO()
	nodePorts := dynaport.Get(3)
	gossipPort, clusterPort, remotingPort := nodePorts[0], nodePorts[1], nodePorts[2]
	host := "127.0.0.1"

	provider := new(mocksdiscovery.Provider)
	provider.EXPECT().ID().Return("testDisco")
	provider.EXPECT().Initialize().Return(nil)
	provider.EXPECT().Register().Return(nil)
	provider.EXPECT().Deregister().Return(nil)
	provider.EXPECT().DiscoverPeers().Return([]string{fmt.Sprintf("%s:%d", host, gossipPort)}, nil)
	provider.EXPECT().Close().Return(nil)

	hostNode := discovery.Node{
		Name:          host,
		Host:          host,
		DiscoveryPort: gossipPort,
		PeersPort:     clusterPort,
		RemotingPort:  remotingPort,
	}

	engine := New("testSystem", provider, &hostNode, WithLogger(log.DiscardLogger))
	require.NoError(t, engine.Start(ctx))

	// hold the consume wait group past the shutdown budget so Stop takes the
	// timeout branch instead of the clean wait
	cl := engine.(*cluster)
	release := make(chan struct{})
	cl.consumeWg.Go(func() { <-release })
	cl.shutdownTimeout = 100 * time.Millisecond

	start := time.Now()
	_ = engine.Stop(ctx)
	require.Less(t, time.Since(start), 5*time.Second)

	// the timeout path must still have completed the teardown behind the wait
	cl.eventsLock.Lock()
	require.Nil(t, cl.events)
	cl.eventsLock.Unlock()
	require.False(t, engine.IsRunning())

	close(release)
	cl.consumeWg.Wait()
}
