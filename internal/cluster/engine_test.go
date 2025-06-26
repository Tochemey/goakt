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

package cluster

import (
	"context"
	"fmt"
	"net"
	"strconv"
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
	"github.com/tochemey/goakt/v3/discovery/nats"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/util"
	"github.com/tochemey/goakt/v3/log"
	testkit "github.com/tochemey/goakt/v3/mocks/discovery"
)

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
		provider := new(testkit.Provider)

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
		cluster, err := NewEngine("test", provider, &hostNode, WithLogger(logger))
		require.NotNil(t, cluster)
		require.NoError(t, err)

		// start the Node node
		err = cluster.Start(ctx)
		require.NoError(t, err)

		hostNodeAddr := cluster.node.Host
		assert.Equal(t, host, hostNodeAddr)

		//  shutdown the Node node
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()

		// stop the node
		require.NoError(t, cluster.Stop(ctx))
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
		provider := new(testkit.Provider)

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

		cluster, err := NewEngine("test", provider, &hostNode, WithLogger(log.DiscardLogger))
		require.NotNil(t, cluster)
		require.NoError(t, err)

		// start the Node
		err = cluster.Start(ctx)
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
		util.Pause(time.Second)

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
		provider := new(testkit.Provider)

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
		cluster, err := NewEngine("test", provider, &hostNode, WithLogger(logger))
		require.NotNil(t, cluster)
		require.NoError(t, err)

		// start the Node
		err = cluster.Start(ctx)
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

		// let us remove the actor
		err = cluster.RemoveActor(ctx, actorName)
		require.NoError(t, err)

		actual, err = cluster.GetActor(ctx, actorName)
		require.Nil(t, actual)
		assert.EqualError(t, err, ErrActorNotFound.Error())

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
		provider := new(testkit.Provider)

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
		cluster, err := NewEngine("test", provider, &hostNode, WithLogger(logger))
		require.NotNil(t, cluster)
		require.NoError(t, err)

		err = cluster.PutActor(ctx, new(internalpb.Actor))
		require.Error(t, err)
		require.EqualError(t, err, ErrEngineNotRunning.Error())

		_, err = cluster.GetActor(ctx, "actorName")
		require.Error(t, err)
		require.EqualError(t, err, ErrEngineNotRunning.Error())

		_, err = cluster.GetState(ctx, "peerAddress")
		require.Error(t, err)
		require.EqualError(t, err, ErrEngineNotRunning.Error())

		_, err = cluster.Peers(ctx)
		require.Error(t, err)
		require.EqualError(t, err, ErrEngineNotRunning.Error())

		err = cluster.RemoveActor(ctx, "actorName")
		require.Error(t, err)
		require.EqualError(t, err, ErrEngineNotRunning.Error())

		partition := cluster.GetPartition("actorName")
		require.NotZero(t, partition)
		require.EqualValues(t, -1, partition)

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
		provider := new(testkit.Provider)

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

		cluster, err := NewEngine("test", provider, &hostNode, WithLogger(log.DiscardLogger))
		require.NotNil(t, cluster)
		require.NoError(t, err)

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
		util.Pause(time.Second)

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
		provider := new(testkit.Provider)

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

		cluster, err := NewEngine("test", provider, &hostNode, WithLogger(log.DiscardLogger))
		require.NotNil(t, cluster)
		require.NoError(t, err)

		// start the Node
		err = cluster.Start(ctx)
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

		// fetch the grain
		actual, err := cluster.GetGrain(ctx, identity)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.True(t, proto.Equal(grain, actual))

		//  fetch non-existing actor
		fakeActorName := "fake"
		actual, err = cluster.GetGrain(ctx, fakeActorName)
		require.Nil(t, actual)
		require.ErrorIs(t, err, ErrGrainNotFound)

		//  shutdown the Node
		util.Pause(time.Second)

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
		provider := new(testkit.Provider)

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
		cluster, err := NewEngine("test", provider, &hostNode, WithLogger(logger))
		require.NotNil(t, cluster)
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
		provider := new(testkit.Provider)

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

		cluster, err := NewEngine("test", provider, &hostNode, WithLogger(log.DiscardLogger))
		require.NotNil(t, cluster)
		require.NoError(t, err)

		// start the Node
		err = cluster.Start(ctx)
		require.NoError(t, err)

		// create an grain
		identity := "grainKind/grainName"

		// replicate the grain in the Node
		err = cluster.grainsMap.Put(ctx, identity, []byte("invalid grain data"))
		require.NoError(t, err)

		// fetch the grain
		actual, err := cluster.GetGrain(ctx, identity)
		require.Error(t, err)
		require.Nil(t, actual)

		// stop the node
		require.NoError(t, cluster.Stop(ctx))
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
		provider := new(testkit.Provider)

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

		cluster, err := NewEngine("test", provider, &hostNode, WithLogger(log.DiscardLogger))
		require.NotNil(t, cluster)
		require.NoError(t, err)

		// start the Node
		err = cluster.Start(ctx)
		require.NoError(t, err)

		actorName := "actorName"
		err = cluster.actorsMap.Put(ctx, actorName, []byte("invalid grain data"))
		require.NoError(t, err)

		actual, err := cluster.GetActor(ctx, actorName)
		require.Error(t, err)
		require.Nil(t, actual)

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
		node1, sd1 := startEngine(t, "node1", srv.Addr().String())
		require.NotNil(t, node1)

		// wait for the node to start properly
		util.Pause(2 * time.Second)

		// create a cluster node2
		node2, sd2 := startEngine(t, "node2", srv.Addr().String())
		require.NotNil(t, node2)
		node2Addr := node2.node.PeersAddress()

		// wait for the node to start properly
		util.Pause(time.Second)

		// create a cluster node3
		node3, sd3 := startEngine(t, "node3", srv.Addr().String())
		require.NotNil(t, node3)
		require.NotNil(t, sd3)

		// wait for the node to start properly
		util.Pause(time.Second)

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
		util.Pause(time.Second)

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
		util.Pause(time.Second)

		identity := "grainKind/grainName"
		grain := &internalpb.Grain{
			GrainId: &internalpb.GrainId{
				Kind:  "grainKind",
				Name:  "grainName",
				Value: identity,
			},
			Host: node2.node.Host,
			Port: int32(node2.node.RemotingPort),
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
		util.Pause(time.Second)

		actors, err := node1.Actors(ctx, time.Second)
		require.NoError(t, err)
		require.Len(t, actors, 2)

		actors, err = node3.Actors(ctx, time.Second)
		require.NoError(t, err)
		require.Len(t, actors, 2)

		actors, err = node2.Actors(ctx, time.Second)
		require.NoError(t, err)
		require.Len(t, actors, 2)

		// stop the second node
		require.NoError(t, node2.Stop(ctx))
		// wait for the event to propagate properly
		util.Pause(time.Second)

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
		conf := autotls.Config{AutoTLS: true}
		require.NoError(t, autotls.Setup(&conf))

		// start the NATS server
		srv := startNatsServer(t)

		// create a cluster node1
		node1, sd1 := startEngineWithTLS(t, "node1", srv.Addr().String(), conf)
		require.NotNil(t, node1)

		// wait for the node to start properly
		util.Pause(2 * time.Second)

		// create a cluster node2
		node2, sd2 := startEngineWithTLS(t, "node2", srv.Addr().String(), conf)
		require.NotNil(t, node2)
		node2Addr := node2.node.PeersAddress()

		// wait for the node to start properly
		util.Pause(time.Second)

		// create a cluster node3
		node3, sd3 := startEngineWithTLS(t, "node3", srv.Addr().String(), conf)
		require.NotNil(t, node3)
		require.NotNil(t, sd3)

		// wait for the node to start properly
		util.Pause(time.Second)

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
		util.Pause(time.Second)

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
			Host: node2.node.Host,
			Port: int32(node2.node.RemotingPort),
		}

		// replicate the grain in the Node
		err = node2.PutGrain(ctx, grain)
		require.NoError(t, err)

		// wait for some time
		util.Pause(time.Second)

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
		util.Pause(time.Second)

		actors, err := node1.Actors(ctx, time.Second)
		require.NoError(t, err)
		require.Len(t, actors, 2)

		actors, err = node3.Actors(ctx, time.Second)
		require.NoError(t, err)
		require.Len(t, actors, 2)

		actors, err = node2.Actors(ctx, time.Second)
		require.NoError(t, err)
		require.Len(t, actors, 2)

		// stop the second node
		require.NoError(t, node2.Stop(ctx))
		// wait for the event to propagate properly
		util.Pause(time.Second)

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

func startEngine(t *testing.T, nodeName, serverAddr string) (*Engine, discovery.Provider) {
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
	applicationName := "accounts"
	actorSystemName := "testSystem"
	natsSubject := "some-subject"

	// create the config
	config := nats.Config{
		ApplicationName: applicationName,
		ActorSystemName: actorSystemName,
		NatsServer:      fmt.Sprintf("nats://%s", serverAddr),
		NatsSubject:     natsSubject,
		Host:            host,
		DiscoveryPort:   gossipPort,
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
	engine, err := NewEngine(nodeName, provider, &hostNode, WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, engine)

	// start the node
	require.NoError(t, engine.Start(ctx))

	// return the cluster node
	return engine, provider
}

func startEngineWithTLS(t *testing.T, nodeName, serverAddr string, conf autotls.Config) (*Engine, discovery.Provider) {
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
	applicationName := "accounts"
	actorSystemName := "testSystem"
	natsSubject := "some-subject"

	// create the config
	config := nats.Config{
		ApplicationName: applicationName,
		ActorSystemName: actorSystemName,
		NatsServer:      fmt.Sprintf("nats://%s", serverAddr),
		NatsSubject:     natsSubject,
		Host:            host,
		DiscoveryPort:   gossipPort,
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
	engine, err := NewEngine(nodeName, provider, &hostNode,
		WithTLS(conf.ServerTLS, conf.ClientTLS),
		WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, engine)

	// start the node
	require.NoError(t, engine.Start(ctx))

	// return the cluster node
	return engine, provider
}
