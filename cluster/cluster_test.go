package cluster

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/discovery"
	goaktpb "github.com/tochemey/goakt/internal/goakt/v1"
	mocksdiscovery "github.com/tochemey/goakt/mocks/discovery"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/protobuf/proto"
)

func TestCluster(t *testing.T) {
	t.Run("With Start and Stop", func(t *testing.T) {
		// create the context
		ctx := context.TODO()

		// generate the ports for the single node
		nodePorts := dynaport.Get(2)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]

		// define discovered addresses
		addrs := []string{
			fmt.Sprintf("127.0.0.1:%d", gossipPort),
		}

		// mock the discovery provider
		provider := new(mocksdiscovery.Provider)
		config := discovery.NewConfig()
		provider.
			On("ID").Return("testDisco").
			On("Initialize").Return(nil).
			On("Register").Return(nil).
			On("Deregister").Return(nil).
			On("SetConfig", config).Return(nil).
			On("DiscoverPeers").Return(addrs, nil)

		// create the service discovery
		serviceDiscovery := discovery.NewServiceDiscovery(provider, config)

		// create a cluster node
		host := "127.0.0.1"
		setEnvs("testNode", host, gossipPort, clusterPort)
		node, err := New("test", serviceDiscovery)
		require.NotNil(t, node)
		require.NoError(t, err)
		// clear the env vars
		unsetEnvs()

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
		nodePorts := dynaport.Get(2)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]

		// define discovered addresses
		addrs := []string{
			fmt.Sprintf("127.0.0.1:%d", gossipPort),
		}

		// mock the discovery provider
		provider := new(mocksdiscovery.Provider)
		config := discovery.NewConfig()
		provider.
			On("ID").Return("testDisco").
			On("Initialize").Return(nil).
			On("Register").Return(nil).
			On("Deregister").Return(nil).
			On("SetConfig", config).Return(nil).
			On("DiscoverPeers").Return(addrs, nil)

		// create the service discovery
		serviceDiscovery := discovery.NewServiceDiscovery(provider, config)

		// create a cluster node
		host := "127.0.0.1"
		setEnvs("testNode", host, gossipPort, clusterPort)
		node, err := New("test", serviceDiscovery)
		require.NotNil(t, node)
		require.NoError(t, err)
		// clear the env vars
		unsetEnvs()

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

func setEnvs(name, host string, gossipPort, clusterPort int) {
	_ = os.Setenv("GOSSIP_PORT", strconv.Itoa(gossipPort))
	_ = os.Setenv("CLUSTER_PORT", strconv.Itoa(clusterPort))
	_ = os.Setenv("POD_NAME", name)
	_ = os.Setenv("POD_IP", host)
}

func unsetEnvs() {
	_ = os.Unsetenv("GOSSIP_PORT")
	_ = os.Unsetenv("CLUSTER_PORT")
	_ = os.Unsetenv("POD_NAME")
	_ = os.Unsetenv("POD_IP")
}
