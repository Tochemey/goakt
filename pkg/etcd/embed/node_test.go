package embed

import (
	"os"
	"testing"

	"github.com/coreos/etcd/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/log"
)

func TestEmbed(t *testing.T) {
	t.Run("With successful start", func(t *testing.T) {
		assert.NoError(t, os.RemoveAll("test"))
		// create the various URLs and cluster name
		clientURLs := types.MustNewURLs([]string{"http://0.0.0.0:2379"})
		peerURLs := types.MustNewURLs([]string{"http://0.0.0.0:2380"})
		endpoints := clientURLs
		clusterName := "test"
		datadir := "test"

		// create an instance of the config
		config := NewConfig(clusterName, clientURLs, peerURLs, endpoints, WithDataDir(datadir))
		// create an instance of embed
		embed := NewNode(config)

		// start the embed server
		require.NoError(t, embed.Start())

		// stop the embed server
		assert.NoError(t, embed.Stop())
		assert.NoError(t, os.RemoveAll("test"))
	})
	t.Run("With successful start with initial URL", func(t *testing.T) {
		assert.NoError(t, os.RemoveAll("test"))
		// create the various URLs and cluster name
		clientURLs := types.MustNewURLs([]string{"http://0.0.0.0:2379"})
		peerURLs := types.MustNewURLs([]string{"http://0.0.0.0:2380"})
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
			WithInitialCluster("test=http://0.0.0.0:2380"),
			WithLogger(logger))
		// create an instance of embed
		embed := NewNode(config)

		// start the embed server
		require.NoError(t, embed.Start())

		// stop the embed server
		assert.NoError(t, embed.Stop())
		assert.NoError(t, os.RemoveAll("test"))
	})
}
