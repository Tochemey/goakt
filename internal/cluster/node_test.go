/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/discovery"
	"github.com/tochemey/goakt/discovery/nats"
	"github.com/tochemey/goakt/goaktpb"
	"github.com/tochemey/goakt/internal/internalpb"
	"github.com/tochemey/goakt/log"
	testkit "github.com/tochemey/goakt/mocks/discovery"
)

func TestSingleNode(t *testing.T) {
	t.Run("With Start and Shutdown", func(t *testing.T) {
		// create the context
		ctx := context.TODO()

		// generate the ports for the single startNode
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		// define discovered addresses
		addrs := []string{
			fmt.Sprintf("localhost:%d", gossipPort),
		}

		// mock the discovery provider
		provider := new(testkit.Provider)
		config := discovery.NewConfig()

		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().SetConfig(config).Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		// create the service discovery
		serviceDiscovery := discovery.NewServiceDiscovery(provider, config)

		// create a Node startNode
		host := "localhost"

		// set the environments
		t.Setenv("GOSSIP_PORT", strconv.Itoa(gossipPort))
		t.Setenv("CLUSTER_PORT", strconv.Itoa(clusterPort))
		t.Setenv("REMOTING_PORT", strconv.Itoa(remotingPort))
		t.Setenv("NODE_NAME", "testNode")
		t.Setenv("NODE_IP", host)

		logger := log.New(log.ErrorLevel, os.Stdout)
		cluster, err := NewNode("test", serviceDiscovery, WithLogger(logger))
		require.NotNil(t, cluster)
		require.NoError(t, err)

		// start the Node startNode
		err = cluster.Start(ctx)
		require.NoError(t, err)

		hostNodeAddr := cluster.NodeHost()
		assert.Equal(t, host, hostNodeAddr)

		//  shutdown the Node startNode
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()

		// stop the startNode
		require.NoError(t, cluster.Stop(ctx))
		provider.AssertExpectations(t)
	})
	t.Run("With PutActor and GetActor", func(t *testing.T) {
		// create the context
		ctx := context.TODO()

		// generate the ports for the single startNode
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		// define discovered addresses
		addrs := []string{
			fmt.Sprintf("localhost:%d", gossipPort),
		}

		// mock the discovery provider
		provider := new(testkit.Provider)
		config := discovery.NewConfig()

		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().SetConfig(config).Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		// create the service discovery
		serviceDiscovery := discovery.NewServiceDiscovery(provider, config)

		// create a Node startNode
		host := "localhost"
		// set the environments
		t.Setenv("GOSSIP_PORT", strconv.Itoa(gossipPort))
		t.Setenv("CLUSTER_PORT", strconv.Itoa(clusterPort))
		t.Setenv("REMOTING_PORT", strconv.Itoa(remotingPort))
		t.Setenv("NODE_NAME", "testNode")
		t.Setenv("NODE_IP", host)

		cluster, err := NewNode("test", serviceDiscovery)
		require.NotNil(t, cluster)
		require.NoError(t, err)

		// start the Node startNode
		err = cluster.Start(ctx)
		require.NoError(t, err)

		// create an actor
		actorName := uuid.NewString()
		actor := &internalpb.WireActor{ActorName: actorName}

		// replicate the actor in the Node
		err = cluster.PutActor(ctx, actor)
		require.NoError(t, err)

		// fetch the actor
		actual, err := cluster.GetActor(ctx, actorName)
		require.NoError(t, err)
		require.NotNil(t, actual)

		assert.True(t, proto.Equal(actor, actual))

		//  fetch non-existing actor
		fakeActorName := "fake"
		actual, err = cluster.GetActor(ctx, fakeActorName)
		require.Nil(t, actual)
		assert.EqualError(t, err, ErrActorNotFound.Error())
		//  shutdown the Node startNode
		time.Sleep(time.Second)

		// stop the startNode
		require.NoError(t, cluster.Stop(ctx))
		provider.AssertExpectations(t)
	})
	t.Run("With SetKey and KeyExists", func(t *testing.T) {
		// create the context
		ctx := context.TODO()

		// generate the ports for the single startNode
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		// define discovered addresses
		addrs := []string{
			fmt.Sprintf("localhost:%d", gossipPort),
		}

		// mock the discovery provider
		provider := new(testkit.Provider)
		config := discovery.NewConfig()

		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().SetConfig(config).Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		// create the service discovery
		serviceDiscovery := discovery.NewServiceDiscovery(provider, config)

		// create a Node startNode
		host := "localhost"
		// set the environments
		t.Setenv("GOSSIP_PORT", strconv.Itoa(gossipPort))
		t.Setenv("CLUSTER_PORT", strconv.Itoa(clusterPort))
		t.Setenv("REMOTING_PORT", strconv.Itoa(remotingPort))
		t.Setenv("NODE_NAME", "testNode")
		t.Setenv("NODE_IP", host)

		cluster, err := NewNode("test", serviceDiscovery)
		require.NotNil(t, cluster)
		require.NoError(t, err)

		// start the Node startNode
		err = cluster.Start(ctx)
		require.NoError(t, err)

		// set key
		key := "my-key"
		require.NoError(t, cluster.SetKey(ctx, key))

		isSet, err := cluster.KeyExists(ctx, key)
		require.NoError(t, err)
		assert.True(t, isSet)

		// check the key existence
		isSet, err = cluster.KeyExists(ctx, "fake")
		require.NoError(t, err)
		assert.False(t, isSet)

		//  shutdown the Node startNode
		time.Sleep(time.Second)

		// stop the startNode
		require.NoError(t, cluster.Stop(ctx))
		provider.AssertExpectations(t)
	})
	t.Run("With RemoveActor", func(t *testing.T) {
		// create the context
		ctx := context.TODO()

		// generate the ports for the single startNode
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		// define discovered addresses
		addrs := []string{
			fmt.Sprintf("localhost:%d", gossipPort),
		}

		// mock the discovery provider
		provider := new(testkit.Provider)
		config := discovery.NewConfig()

		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().SetConfig(config).Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		// create the service discovery
		serviceDiscovery := discovery.NewServiceDiscovery(provider, config)

		// create a Node startNode
		host := "localhost"
		// set the environments
		t.Setenv("GOSSIP_PORT", strconv.Itoa(gossipPort))
		t.Setenv("CLUSTER_PORT", strconv.Itoa(clusterPort))
		t.Setenv("REMOTING_PORT", strconv.Itoa(remotingPort))
		t.Setenv("NODE_NAME", "testNode")
		t.Setenv("NODE_IP", host)

		logger := log.New(log.WarningLevel, os.Stdout)
		cluster, err := NewNode("test", serviceDiscovery, WithLogger(logger))
		require.NotNil(t, cluster)
		require.NoError(t, err)

		// start the Node startNode
		err = cluster.Start(ctx)
		require.NoError(t, err)

		// create an actor
		actorName := uuid.NewString()
		actor := &internalpb.WireActor{ActorName: actorName}

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
	t.Run("With host node env vars not set", func(t *testing.T) {
		// generate the ports for the single startNode
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		remotingPort := nodePorts[2]

		// define discovered addresses
		addrs := []string{
			fmt.Sprintf("localhost:%d", gossipPort),
		}

		// mock the discovery provider
		provider := new(testkit.Provider)
		config := discovery.NewConfig()

		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().SetConfig(config).Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)
		provider.EXPECT().Close().Return(nil)

		// create the service discovery
		serviceDiscovery := discovery.NewServiceDiscovery(provider, config)

		// set the environments
		t.Setenv("GOSSIP_PORT", strconv.Itoa(gossipPort))
		t.Setenv("REMOTING_PORT", strconv.Itoa(remotingPort))

		cluster, err := NewNode("test", serviceDiscovery)
		require.Nil(t, cluster)
		require.Error(t, err)
	})
}

func TestMultipleNodes(t *testing.T) {
	// start the NATS server
	srv := startNatsServer(t)

	// create a cluster node1
	node1, sd1 := startNode(t, "node1", srv.Addr().String())
	require.NotNil(t, node1)

	// wait for the node to start properly
	time.Sleep(2 * time.Second)

	// create a cluster node1
	node2, sd2 := startNode(t, "node2", srv.Addr().String())
	require.NotNil(t, node2)
	node2Addr := node2.AdvertisedAddress()

	// wait for the node to start properly
	time.Sleep(time.Second)

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
	require.Len(t, events, 1)
	event := events[0]
	msg, err := event.Type.UnmarshalNew()
	require.NoError(t, err)
	nodeJoined, ok := msg.(*goaktpb.NodeJoined)
	require.True(t, ok)
	require.NotNil(t, nodeJoined)
	require.Equal(t, node2Addr, nodeJoined.GetAddress())

	// wait for some time
	time.Sleep(time.Second)

	// stop the second node
	require.NoError(t, node2.Stop(context.TODO()))
	// wait for the event to propagate properly
	time.Sleep(time.Second)

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
	msg, err = event.Type.UnmarshalNew()
	require.NoError(t, err)
	nodeLeft, ok := msg.(*goaktpb.NodeLeft)
	require.True(t, ok)
	require.NotNil(t, nodeLeft)
	require.Equal(t, node2Addr, nodeLeft.GetAddress())

	t.Cleanup(func() {
		require.NoError(t, node1.Stop(context.TODO()))
		require.NoError(t, sd1.Close())
		require.NoError(t, sd2.Close())
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

func startNode(t *testing.T, nodeName, serverAddr string) (*Node, discovery.Provider) {
	// create a context
	ctx := context.TODO()

	// generate the ports for the single startNode
	nodePorts := dynaport.Get(3)
	gossipPort := nodePorts[0]
	clusterPort := nodePorts[1]
	remotingPort := nodePorts[2]

	// create a Cluster startNode
	host := "127.0.0.1"
	// set the environments
	require.NoError(t, os.Setenv("GOSSIP_PORT", strconv.Itoa(gossipPort)))
	require.NoError(t, os.Setenv("CLUSTER_PORT", strconv.Itoa(clusterPort)))
	require.NoError(t, os.Setenv("REMOTING_PORT", strconv.Itoa(remotingPort)))
	require.NoError(t, os.Setenv("NODE_NAME", nodeName))
	require.NoError(t, os.Setenv("NODE_IP", host))

	// create the various config option
	applicationName := "accounts"
	actorSystemName := "testSystem"
	natsSubject := "some-subject"
	// create the instance of provider
	provider := nats.NewDiscovery()

	// create the config
	config := discovery.Config{
		nats.ApplicationName: applicationName,
		nats.ActorSystemName: actorSystemName,
		nats.NatsServer:      serverAddr,
		nats.NatsSubject:     natsSubject,
	}

	// create the startNode
	node, err := NewNode(nodeName, discovery.NewServiceDiscovery(provider, config))
	require.NoError(t, err)
	require.NotNil(t, node)

	// start the node
	require.NoError(t, node.Start(ctx))

	// clear the env var
	require.NoError(t, os.Unsetenv("GOSSIP_PORT"))
	require.NoError(t, os.Unsetenv("CLUSTER_PORT"))
	require.NoError(t, os.Unsetenv("REMOTING_PORT"))
	require.NoError(t, os.Unsetenv("NODE_NAME"))
	require.NoError(t, os.Unsetenv("NODE_IP"))

	// return the cluster startNode
	return node, provider
}
