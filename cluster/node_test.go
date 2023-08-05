package cluster

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
)

func TestGetHostNode(t *testing.T) {
	t.Run("With host node env vars set", func(t *testing.T) {
		// generate the ports for the single node
		nodePorts := dynaport.Get(2)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		host := "127.0.0.1"
		setEnvs("testNode", host, gossipPort, clusterPort)
		node, err := getHostNode()
		require.NoError(t, err)
		require.NotNil(t, node)
		clusterAddr := node.ClusterAddress()
		gossipAddr := node.GossipAddress()
		assert.Equal(t, fmt.Sprintf("%s:%d", host, clusterPort), clusterAddr)
		assert.Equal(t, fmt.Sprintf("%s:%d", host, gossipPort), gossipAddr)
		unsetEnvs()
	})
	t.Run("With host node env vars not set", func(t *testing.T) {
		node, err := getHostNode()
		require.Error(t, err)
		require.Nil(t, node)
	})
}
