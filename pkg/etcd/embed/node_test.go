package embed

import (
	"fmt"
	"os"
	"testing"

	"github.com/coreos/etcd/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/log"
	"github.com/travisjeffery/go-dynaport"
)

func TestEmbed(t *testing.T) {
	t.Run("With successful start and stop", func(t *testing.T) {
		assert.NoError(t, os.RemoveAll("test"))

		// let us generate two ports
		ports := dynaport.Get(2)
		clientsPort := ports[0]
		peersPort := ports[1]

		// create the various URLs and cluster name
		clientURLs := types.MustNewURLs([]string{fmt.Sprintf("http://0.0.0.0:%d", clientsPort)})
		peerURLs := types.MustNewURLs([]string{fmt.Sprintf("http://0.0.0.0:%d", peersPort)})
		endpoints := clientURLs
		clusterName := "test"
		datadir := "test"

		// create an instance of the config
		config := NewConfig(clusterName, clientURLs, peerURLs, endpoints, WithDataDir(datadir))
		// create an instance of node
		node := NewNode(config)

		// start the node server
		require.NoError(t, node.Start())
		assert.NotEmpty(t, node.ID())
		members, err := node.Members()
		require.NoError(t, err)
		assert.NotEmpty(t, members)
		assert.Len(t, members, 1)
		assert.NotNil(t, node.Session())
		assert.NotNil(t, node.Client())

		// assertions single node cluster
		curls := node.ClientURLs().StringSlice()
		require.ElementsMatch(t, clientURLs.StringSlice(), curls)
		require.True(t, node.IsLeader())
		assert.NotEmpty(t, node.LeaderID())

		// stop the node server
		assert.NoError(t, node.Stop())
		assert.NoError(t, os.RemoveAll("test"))
	})
	t.Run("With successful start and stop with initial URL without joining an existing cluster", func(t *testing.T) {
		assert.NoError(t, os.RemoveAll("test"))
		// let us generate two ports
		ports := dynaport.Get(2)
		clientsPort := ports[0]
		peersPort := ports[1]

		// create the various URLs and cluster name
		clientURLs := types.MustNewURLs([]string{fmt.Sprintf("http://0.0.0.0:%d", clientsPort)})
		peerURLs := types.MustNewURLs([]string{fmt.Sprintf("http://0.0.0.0:%d", peersPort)})

		endpoints := clientURLs
		clusterName := "test"
		datadir := "test"
		// create a logger
		logger := log.New(log.DebugLevel, os.Stdout)

		// create an instance of the config
		config := NewConfig(clusterName,
			clientURLs,
			peerURLs,
			endpoints,
			WithDataDir(datadir),
			WithInitialCluster(fmt.Sprintf("test=http://0.0.0.0:%d", peersPort)),
			WithLogger(logger))
		// create an instance of node
		node := NewNode(config)

		// start the node server
		require.NoError(t, node.Start())
		assert.NotEmpty(t, node.ID())
		members, err := node.Members()
		require.NoError(t, err)
		assert.NotEmpty(t, members)
		assert.Len(t, members, 1)
		assert.NotNil(t, node.Session())
		assert.NotNil(t, node.Client())

		// assertions single node cluster
		curls := node.ClientURLs().StringSlice()
		require.ElementsMatch(t, clientURLs.StringSlice(), curls)
		require.True(t, node.IsLeader())
		assert.NotEmpty(t, node.LeaderID())

		// stop the node server
		assert.NoError(t, node.Stop())
		assert.NoError(t, os.RemoveAll("test"))
	})
}
