package cluster

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/tochemey/goakt/discovery"
	"github.com/tochemey/goakt/log"
	mocksdiscovery "github.com/tochemey/goakt/mocks/discovery"
	"github.com/tochemey/goakt/pkg/etcd/host"
)

func TestCluster(t *testing.T) {
	t.Run("With successful start and stop with single node", func(t *testing.T) {
		assert.NoError(t, os.RemoveAll("test"))
		ctx := context.TODO()
		addrs, err := host.Addresses()
		assert.NoError(t, err)
		// create the mock discovery provider
		nodes := []*discovery.Node{
			{
				Name:      "node1",
				Host:      addrs[0],
				StartTime: time.Now().UnixMilli(),
				Ports: map[string]int32{
					"clients-port": 6379,
					"peers-port":   6380,
				},
				IsRunning: true,
			},
		}
		disco := new(mocksdiscovery.Discovery)
		disco.On("ID").Return("mockdisco")
		disco.On("Start", ctx, mock.Anything).Return(nil)
		disco.On("Stop").Return(nil)
		disco.On("Nodes", mock.Anything).Return(nodes, nil)
		disco.On("Watch", mock.Anything).Return(nil, nil)

		// create an instance of cluster
		cl := New(disco, log.DefaultLogger,
			WithDataDir("test"))

		// start the cluster
		assert.NoError(t, cl.Start(ctx))
		// wait for the cluster to start
		time.Sleep(2 * time.Second)
		assert.NoError(t, cl.Stop())
		assert.NoError(t, os.RemoveAll("test"))
	})
}
