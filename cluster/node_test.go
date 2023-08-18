package cluster

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
)

func TestGetHostNode(t *testing.T) {
	t.Run("With host node env vars set", func(t *testing.T) {
		// generate the ports for the single node
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]
		host := "127.0.0.1"

		t.Setenv("GOSSIP_PORT", strconv.Itoa(gossipPort))
		t.Setenv("CLUSTER_PORT", strconv.Itoa(clusterPort))
		t.Setenv("REMOTING_PORT", strconv.Itoa(remotingPort))
		t.Setenv("NODE_NAME", "testNode")
		t.Setenv("NODE_IP", host)

		node, err := getHostNode()
		require.NoError(t, err)
		require.NotNil(t, node)
		clusterAddr := node.ClusterAddress()
		gossipAddr := node.GossipAddress()
		assert.Equal(t, fmt.Sprintf("%s:%d", host, clusterPort), clusterAddr)
		assert.Equal(t, fmt.Sprintf("%s:%d", host, gossipPort), gossipAddr)
	})
	t.Run("With host node env vars not set", func(t *testing.T) {
		node, err := getHostNode()
		require.Error(t, err)
		require.Nil(t, node)
	})
}
