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
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/discovery"
	internalpb "github.com/tochemey/goakt/internal/v1"
	testkit "github.com/tochemey/goakt/testkit/discovery"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/protobuf/proto"
)

func TestCluster(t *testing.T) {
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

		// create a Node node
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

		// start the Node node
		err = cluster.Start(ctx)
		require.NoError(t, err)

		hostNodeAddr := cluster.NodeHost()
		assert.Equal(t, host, hostNodeAddr)

		//  shutdown the Node node
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()

		// stop the node
		require.NoError(t, cluster.Stop(ctx))
		provider.AssertExpectations(t)
	})
	t.Run("With PutActor and GetActor", func(t *testing.T) {
		// create the context
		ctx := context.TODO()

		// generate the ports for the single node
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

		// create a Node node
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

		// start the Node node
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
		//  shutdown the Node node
		time.Sleep(time.Second)

		// stop the node
		require.NoError(t, cluster.Stop(ctx))
		provider.AssertExpectations(t)
	})
	t.Run("With SetKey and KeyExists", func(t *testing.T) {
		// create the context
		ctx := context.TODO()

		// generate the ports for the single node
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

		// create a Node node
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

		// start the Node node
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

		//  shutdown the Node node
		time.Sleep(time.Second)

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

		// create a Node node
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

		// start the Node node
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
}
