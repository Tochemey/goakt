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
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/tochemey/olric/events"
	"github.com/travisjeffery/go-dynaport"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/discovery"
	"github.com/tochemey/goakt/v3/discovery/nats"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/pause"
	"github.com/tochemey/goakt/v3/log"
	mocksdiscovery "github.com/tochemey/goakt/v3/mocks/discovery"
	gtls "github.com/tochemey/goakt/v3/tls"
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
		actor := &internalpb.Actor{Address: &goaktpb.Address{Name: actorName}}

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
		actor := &internalpb.Actor{Address: &goaktpb.Address{Name: actorName}}
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
	t.Run("With Put/RemoveKind and LookupKind", func(t *testing.T) {
		// create the context
		ctx := context.TODO()

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

		provider.EXPECT().ID().Return("id")
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

		var err error
		cluster := New("test", provider, &hostNode, WithLogger(log.DiscardLogger))
		require.NotNil(t, cluster)

		// start the Node
		err = cluster.Start(ctx)
		require.NoError(t, err)

		// create an actor
		actorName := uuid.NewString()
		actorKind := "kind"
		actor := &internalpb.Actor{
			Address:     &goaktpb.Address{Name: actorName},
			Type:        actorKind,
			IsSingleton: true,
		}

		// replicate the actor in the Node
		err = cluster.PutActor(ctx, actor)
		require.NoError(t, err)

		err = cluster.PutKind(ctx, actorKind)
		require.NoError(t, err)

		actual, err := cluster.LookupKind(ctx, actorKind)
		require.NoError(t, err)
		require.NotEmpty(t, actual)

		// remove the kind
		err = cluster.RemoveKind(ctx, actorKind)
		require.NoError(t, err)

		// check the kind existence
		actual, err = cluster.LookupKind(ctx, actorKind)
		require.NoError(t, err)
		require.Empty(t, actual)

		//  shutdown the Node
		pause.For(time.Second)

		// stop the node
		require.NoError(t, cluster.Stop(ctx))
		provider.AssertExpectations(t)
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
		grain := &internalpb.Grain{
			GrainId: &internalpb.GrainId{
				Kind:  "grainKind",
				Name:  "grainName",
				Value: identity,
			},
			Host: host,
			Port: int32(remotingPort),
		}

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
		grain := &internalpb.Grain{
			GrainId: &internalpb.GrainId{
				Kind:  "grainKind",
				Name:  "grainName",
				Value: identity,
			},
			Host: host,
			Port: int32(remotingPort),
		}

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
		grain := &internalpb.Grain{
			GrainId: &internalpb.GrainId{
				Kind:  "grainKind",
				Name:  "grainName",
				Value: identity,
			},
			Host: host,
			Port: int32(remotingPort),
		}

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

		// wait for some time
		pause.For(time.Second)

		// create some actors
		actorName := uuid.NewString()
		actor := &internalpb.Actor{
			Address: &goaktpb.Address{
				Name: actorName,
			},
			Type: "actorKind",
		}

		// put an actor
		err = node2.PutActor(ctx, actor)
		require.NoError(t, err)

		// wait for some time
		pause.For(time.Second)

		identity := "grainKind/grainName"
		grain := &internalpb.Grain{
			GrainId: &internalpb.GrainId{
				Kind:  "grainKind",
				Name:  "grainName",
				Value: identity,
			},
			Host: node2.(*cluster).node.Host,
			Port: int32(node2.(*cluster).node.RemotingPort),
		}

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
		actor2 := &internalpb.Actor{
			Address: &goaktpb.Address{
				Name: actorName2,
			},
			Type: "actorKind",
		}
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
		msg, err = event.Payload.UnmarshalNew()
		require.NoError(t, err)
		nodeLeft, ok := msg.(*goaktpb.NodeLeft)
		require.True(t, ok)
		require.NotNil(t, nodeLeft)
		require.Equal(t, node2Addr, nodeLeft.GetAddress())

		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd3.Close())
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

		// wait for some time
		pause.For(time.Second)

		// create some actors
		actorName := uuid.NewString()
		actor := &internalpb.Actor{
			Address: &goaktpb.Address{
				Name: actorName,
			},
			Type: "actorKind",
		}

		// put an actor
		err = node2.PutActor(ctx, actor)
		require.NoError(t, err)

		identity := "grainKind/grainName"
		grain := &internalpb.Grain{
			GrainId: &internalpb.GrainId{
				Kind:  "grainKind",
				Name:  "grainName",
				Value: identity,
			},
			Host: node2.(*cluster).node.Host,
			Port: int32(node2.(*cluster).node.RemotingPort),
		}

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
		actor2 := &internalpb.Actor{
			Address: &goaktpb.Address{
				Name: actorName2,
			},
			Type: "actorKind",
		}
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
		msg, err = event.Payload.UnmarshalNew()
		require.NoError(t, err)
		nodeLeft, ok := msg.(*goaktpb.NodeLeft)
		require.True(t, ok)
		require.NotNil(t, nodeLeft)
		require.Equal(t, node2Addr, nodeLeft.GetAddress())

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

func startEngine(t *testing.T, serverAddr string) (Cluster, discovery.Provider) {
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
	engine := New(actorSystemName, provider, &hostNode, WithLogger(log.DiscardLogger))
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

	grain := &internalpb.Grain{GrainId: &internalpb.GrainId{Value: ""}}
	err := cl.PutGrain(context.Background(), grain)
	require.EqualError(t, err, "grain id value is empty")
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

func TestGetActorReturnsDMapError(t *testing.T) {
	expectedErr := errors.New("get failure")
	cl := &cluster{
		running:     atomic.NewBool(true),
		logger:      log.DiscardLogger,
		readTimeout: time.Second,
		dmap: &MockDMap{
			getFn: func(ctx context.Context, key string) (*olric.GetResponse, error) {
				require.Equal(t, composeKey(namespaceActors, "actor"), key)
				return nil, expectedErr
			},
		},
	}

	actor, err := cl.GetActor(context.Background(), "actor")
	require.Nil(t, actor)
	require.ErrorIs(t, err, expectedErr)
}

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

func TestCreateDMapReturnsClientError(t *testing.T) {
	expectedErr := errors.New("boom")
	cl := &cluster{client: &MockClient{newDMapErr: expectedErr}}

	err := cl.createDMap()
	require.ErrorIs(t, err, expectedErr)
}

func TestCreateSubscriptionReturnsClientError(t *testing.T) {
	expectedErr := errors.New("boom")
	cl := &cluster{
		client: &MockClient{newPubSubErr: expectedErr},
		node:   &discovery.Node{Host: "127.0.0.1", PeersPort: 4000},
	}

	err := cl.createSubscription(context.Background())
	require.ErrorIs(t, err, expectedErr)
}

func TestHandleClusterEventInvalidEnvelope(t *testing.T) {
	cl := &cluster{}
	err := cl.handleClusterEvent("not-json")
	require.ErrorContains(t, err, "unmarshal cluster event envelope")
}

func TestHandleClusterEventInvalidNodeJoin(t *testing.T) {
	cl := &cluster{}
	payload := `{"kind":"` + events.KindNodeJoinEvent + `","node_join":123}`

	err := cl.handleClusterEvent(payload)
	require.ErrorContains(t, err, "unmarshal node join")
}

func TestHandleClusterEventInvalidNodeLeft(t *testing.T) {
	cl := &cluster{}
	payload := `{"kind":"` + events.KindNodeLeftEvent + `","node_left":123}`

	err := cl.handleClusterEvent(payload)
	require.ErrorContains(t, err, "unmarshal node left")
}

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

func TestProcessNodeJoin(t *testing.T) {
	now := time.Now().UnixNano()

	t.Run("emits event", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 4000)
		ev := events.NodeJoinEvent{NodeJoin: "127.0.0.1:5000", Timestamp: now}

		cl.processNodeJoin(ev)

		select {
		case evt := <-cl.events:
			require.Equal(t, NodeJoined, evt.Type)
			msg, err := evt.Payload.UnmarshalNew()
			require.NoError(t, err)
			joined, ok := msg.(*goaktpb.NodeJoined)
			require.True(t, ok)
			require.Equal(t, ev.NodeJoin, joined.GetAddress())
			require.Equal(t, ev.Timestamp/int64(time.Millisecond), joined.GetTimestamp().AsTime().UnixMilli())
		default:
			t.Fatalf("expected event")
		}
	})

	t.Run("ignores self", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 5000)
		ev := events.NodeJoinEvent{NodeJoin: cl.node.PeersAddress(), Timestamp: now}

		cl.processNodeJoin(ev)

		select {
		case <-cl.events:
			t.Fatalf("unexpected event")
		default:
		}
	})

	t.Run("deduplicates", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 6000)
		ev := events.NodeJoinEvent{NodeJoin: "127.0.0.1:7000", Timestamp: now}

		cl.processNodeJoin(ev)
		<-cl.events
		cl.processNodeJoin(ev)

		select {
		case <-cl.events:
			t.Fatalf("expected no duplicate event")
		default:
		}
	})
}

func TestProcessNodeLeft(t *testing.T) {
	now := time.Now().UnixNano()

	t.Run("emits event", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 4000)
		ev := events.NodeLeftEvent{NodeLeft: "127.0.0.1:5000", Timestamp: now}

		cl.processNodeLeft(ev)

		select {
		case evt := <-cl.events:
			require.Equal(t, NodeLeft, evt.Type)
			msg, err := evt.Payload.UnmarshalNew()
			require.NoError(t, err)
			left, ok := msg.(*goaktpb.NodeLeft)
			require.True(t, ok)
			require.Equal(t, ev.NodeLeft, left.GetAddress())
			require.Equal(t, ev.Timestamp/int64(time.Millisecond), left.GetTimestamp().AsTime().UnixMilli())
		default:
			t.Fatalf("expected event")
		}
	})

	t.Run("deduplicates", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 5000)
		ev := events.NodeLeftEvent{NodeLeft: "127.0.0.1:6000", Timestamp: now}

		cl.processNodeLeft(ev)
		<-cl.events
		cl.processNodeLeft(ev)

		select {
		case <-cl.events:
			t.Fatalf("expected no duplicate event")
		default:
		}
	})
}

func TestHandleClusterEventSuccessCases(t *testing.T) {
	now := time.Now().UnixNano()

	t.Run("node join", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 4000)
		payload, err := json.Marshal(events.NodeJoinEvent{
			Kind:      events.KindNodeJoinEvent,
			NodeJoin:  "127.0.0.1:7000",
			Timestamp: now,
		})
		require.NoError(t, err)

		require.NoError(t, cl.handleClusterEvent(string(payload)))

		select {
		case evt := <-cl.events:
			require.Equal(t, NodeJoined, evt.Type)
		default:
			t.Fatalf("expected node join event")
		}
	})

	t.Run("node left", func(t *testing.T) {
		cl := newEventTestCluster("127.0.0.1", 5000)
		payload, err := json.Marshal(events.NodeLeftEvent{
			Kind:      events.KindNodeLeftEvent,
			NodeLeft:  "127.0.0.1:8000",
			Timestamp: now,
		})
		require.NoError(t, err)

		require.NoError(t, cl.handleClusterEvent(string(payload)))

		select {
		case evt := <-cl.events:
			require.Equal(t, NodeLeft, evt.Type)
		default:
			t.Fatalf("expected node left event")
		}
	})
}

func TestHandleClusterEventUnknownKind(t *testing.T) {
	cl := newEventTestCluster("127.0.0.1", 4000)

	require.NoError(t, cl.handleClusterEvent(`{"kind":"noop"}`))

	select {
	case <-cl.events:
		t.Fatalf("unexpected event")
	default:
	}
}

func TestConsumeDispatchesClusterEvents(t *testing.T) {
	cl := newEventTestCluster("127.0.0.1", 4000)
	msgs := make(chan *redis.Message, 1)
	cl.messages = msgs

	done := make(chan struct{})
	go func() {
		cl.consume()
		close(done)
	}()

	payload, err := json.Marshal(events.NodeJoinEvent{
		Kind:      events.KindNodeJoinEvent,
		NodeJoin:  "127.0.0.1:9000",
		Timestamp: time.Now().UnixNano(),
	})
	require.NoError(t, err)

	msgs <- &redis.Message{Channel: events.ClusterEventsChannel, Payload: string(payload)}

	select {
	case evt := <-cl.events:
		require.Equal(t, NodeJoined, evt.Type)
	case <-time.After(time.Second):
		t.Fatalf("expected event from consume")
	}

	close(msgs)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("consume did not exit")
	}
}

func TestPeersFiltersSelfAndParsesMeta(t *testing.T) {
	cl := &cluster{
		running: atomic.NewBool(true),
		logger:  log.DiscardLogger,
		node:    &discovery.Node{Host: "127.0.0.1", PeersPort: 4000},
	}

	other := &discovery.Node{Host: "10.0.0.1", PeersPort: 5000, RemotingPort: 7000}
	selfMeta, err := json.Marshal(cl.node)
	require.NoError(t, err)
	otherMeta, err := json.Marshal(other)
	require.NoError(t, err)

	cl.client = &fakeClient{
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
	require.False(t, peers[0].Coordinator)
}

func TestIsLeaderReturnsTrueWhenCoordinator(t *testing.T) {
	cl := &cluster{
		running: atomic.NewBool(true),
		logger:  log.DiscardLogger,
		node:    &discovery.Node{Host: "127.0.0.1", PeersPort: 4000},
	}

	meta, err := json.Marshal(cl.node)
	require.NoError(t, err)

	cl.client = &fakeClient{
		MockClient: &MockClient{},
		members: []olric.Member{
			{Name: cl.node.PeersAddress(), Coordinator: true, Meta: string(meta)},
		},
	}

	require.True(t, cl.IsLeader(context.Background()))
}
