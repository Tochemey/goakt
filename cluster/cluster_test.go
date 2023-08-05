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
	mocksdiscovery "github.com/tochemey/goakt/goaktmocks/discovery"
	goaktpb "github.com/tochemey/goakt/internal/goakt/v1"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/protobuf/proto"
)

func TestCluster(t *testing.T) {
	t.Run("With Start and Stop", func(t *testing.T) {
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
		config := discovery.NewConfig()

		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().SetConfig(config).Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)

		// create the service discovery
		serviceDiscovery := discovery.NewServiceDiscovery(provider, config)

		// create a cluster node
		host := "127.0.0.1"

		// set the environments
		t.Setenv("GOSSIP_PORT", strconv.Itoa(gossipPort))
		t.Setenv("CLUSTER_PORT", strconv.Itoa(clusterPort))
		t.Setenv("REMOTING_PORT", strconv.Itoa(remotingPort))
		t.Setenv("POD_NAME", "testNode")
		t.Setenv("POD_IP", host)

		node, err := New("test", serviceDiscovery)
		require.NotNil(t, node)
		require.NoError(t, err)

		// start the cluster node
		err = node.Start(ctx)
		require.NoError(t, err)

		hostNodeAddr := node.NodeHost()
		assert.Equal(t, host, hostNodeAddr)

		//  shutdown the cluster node
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()

		// stop the node
		require.NoError(t, node.Stop(ctx))
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
			fmt.Sprintf("127.0.0.1:%d", gossipPort),
		}

		// mock the discovery provider
		provider := new(mocksdiscovery.Provider)
		config := discovery.NewConfig()

		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().SetConfig(config).Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)

		// create the service discovery
		serviceDiscovery := discovery.NewServiceDiscovery(provider, config)

		// create a cluster node
		host := "127.0.0.1"
		// set the environments
		t.Setenv("GOSSIP_PORT", strconv.Itoa(gossipPort))
		t.Setenv("CLUSTER_PORT", strconv.Itoa(clusterPort))
		t.Setenv("REMOTING_PORT", strconv.Itoa(remotingPort))
		t.Setenv("POD_NAME", "testNode")
		t.Setenv("POD_IP", host)

		node, err := New("test", serviceDiscovery)
		require.NotNil(t, node)
		require.NoError(t, err)

		// start the cluster node
		err = node.Start(ctx)
		require.NoError(t, err)

		// create an actor
		actorName := uuid.NewString()
		actor := &goaktpb.WireActor{ActorName: actorName}

		// replicate the actor in the cluster
		err = node.PutActor(ctx, actor)
		require.NoError(t, err)

		// fetch the actor
		actual, err := node.GetActor(ctx, actorName)
		require.NoError(t, err)
		require.NotNil(t, actual)

		assert.True(t, proto.Equal(actor, actual))

		//  fetch non-existing actor
		fakeActorName := "fake"
		actual, err = node.GetActor(ctx, fakeActorName)
		require.Nil(t, actual)
		assert.EqualError(t, err, ErrActorNotFound.Error())
		//  shutdown the cluster node
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()

		// stop the node
		require.NoError(t, node.Stop(ctx))
		provider.AssertExpectations(t)
	})
}
