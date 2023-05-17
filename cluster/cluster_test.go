package cluster

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/discovery"
	"github.com/tochemey/goakt/log"
	mocks "github.com/tochemey/goakt/mocks/discovery"
)

func TestCluster(t *testing.T) {
	t.Run("With successful start and stop with a single node discovered", func(t *testing.T) {
		assert.NoError(t, os.RemoveAll("test"))
		// create a go context
		ctx := context.TODO()
		// create a logger
		logger := log.New(log.DebugLevel, os.Stdout)
		// define discovery nodes
		discoNodes := []*discovery.Node{
			{
				Name:      "test",
				Host:      "localhost",
				StartTime: time.Now().UnixMicro(),
				Ports: map[string]int32{
					"clients-port": 3379,
					"peers-port":   3380,
				},
			},
		}
		// mock the discovery provider
		disco := new(mocks.Discovery)
		disco.On("ID").Return("testDisco")
		disco.On("Nodes", ctx).Return(discoNodes, nil)

		// create an instance of the cluster
		cl := New(disco, logger, WithDataDir("test"))
		// start the cluster
		require.NoError(t, cl.Start(ctx))
		// stop the cluster after some time
		require.NoError(t, cl.Stop())
		assert.NoError(t, os.RemoveAll("test"))
	})
	t.Run("With successful start and stop with two nodes discovered", func(t *testing.T) {
		assert.NoError(t, os.RemoveAll("test"))
		// create a go context
		ctx := context.TODO()
		// create a logger
		logger := log.New(log.DebugLevel, os.Stdout)
		// define discovery nodes
		discoNodes := []*discovery.Node{
			{
				Name:      "node1",
				Host:      "localhost",
				StartTime: time.Now().UnixMicro(),
				Ports: map[string]int32{
					"clients-port": 3379,
					"peers-port":   3380,
				},
			},
			{
				Name:      "node2",
				Host:      "localhost",
				StartTime: time.Now().UnixMicro(),
				Ports: map[string]int32{
					"clients-port": 3381,
					"peers-port":   3382,
				},
			},
		}
		// mock the discovery provider
		disco := new(mocks.Discovery)
		disco.On("ID").Return("testDisco")
		disco.On("Nodes", ctx).Return(discoNodes, nil)

		// create an instance of the cluster
		cl := New(disco, logger, WithDataDir("test"))
		// start the cluster
		require.NoError(t, cl.Start(ctx))
		// stop the cluster after some time
		require.NoError(t, cl.Stop())
		assert.NoError(t, os.RemoveAll("test"))
	})
	t.Run("With successful start and stop with more than two nodes discovered", func(t *testing.T) {
		assert.NoError(t, os.RemoveAll("test"))
		// create a go context
		ctx := context.TODO()
		// create a logger
		logger := log.New(log.DebugLevel, os.Stdout)
		// define discovery nodes
		discoNodes := []*discovery.Node{
			{
				Name:      "node1",
				Host:      "localhost",
				StartTime: time.Now().UnixMicro(),
				Ports: map[string]int32{
					"clients-port": 3379,
					"peers-port":   3380,
				},
			},
			{
				Name:      "node2",
				Host:      "localhost",
				StartTime: time.Now().UnixMicro(),
				Ports: map[string]int32{
					"clients-port": 3381,
					"peers-port":   3382,
				},
			},
			{
				Name:      "node3",
				Host:      "localhost",
				StartTime: time.Now().UnixMicro(),
				Ports: map[string]int32{
					"clients-port": 3383,
					"peers-port":   3384,
				},
			},
			{
				Name:      "node4",
				Host:      "localhost",
				StartTime: time.Now().UnixMicro(),
				Ports: map[string]int32{
					"clients-port": 3385,
					"peers-port":   3386,
				},
			},
		}
		// mock the discovery provider
		disco := new(mocks.Discovery)
		disco.On("ID").Return("testDisco")
		disco.On("Nodes", ctx).Return(discoNodes, nil)

		// create an instance of the cluster
		cl := New(disco, logger, WithDataDir("test"))
		// start the cluster
		require.NoError(t, cl.Start(ctx))
		// stop the cluster after some time
		require.NoError(t, cl.Stop())
		assert.NoError(t, os.RemoveAll("test"))
	})
}
