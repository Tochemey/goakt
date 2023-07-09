package static

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/discovery"
	"github.com/tochemey/goakt/log"
)

func TestStaticProvider(t *testing.T) {
	t.Run("With new instance", func(t *testing.T) {
		// create a logger
		logger := log.DefaultLogger
		// define the nodes
		var nodes []*discovery.Node
		// create the instance of provider
		provider := NewDiscovery(nodes, logger)
		require.NotNil(t, provider)
		// assert that provider implements the Discovery interface
		// this is a cheap test
		// assert the type of svc
		assert.IsType(t, &Discovery{}, provider)
		var p interface{} = provider
		_, ok := p.(discovery.Discovery)
		assert.True(t, ok)
	})
	t.Run("With ID assertion", func(t *testing.T) {
		// cheap test
		// create a logger
		logger := log.DefaultLogger
		// define the nodes
		var nodes []*discovery.Node
		// create the instance of provider
		provider := NewDiscovery(nodes, logger)
		require.NotNil(t, provider)
		assert.Equal(t, "static", provider.ID())
	})
	t.Run("With Start", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// create a logger
		logger := log.DefaultLogger
		// define the nodes
		nodes := []*discovery.Node{
			{
				Name:      "node-1",
				Host:      "localhost",
				StartTime: time.Now().Add(time.Second).UnixMilli(),
				Ports: map[string]int32{
					"clients-port": 1111,
					"peers-port":   1112,
				},
				IsRunning: true,
			},
		}
		// create the instance of provider
		provider := NewDiscovery(nodes, logger)
		require.NotNil(t, provider)
		assert.NoError(t, provider.Start(ctx, discovery.NewMeta()))
		assert.NoError(t, provider.Stop())
	})
	t.Run("With failed Start", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// create a logger
		logger := log.DefaultLogger
		// define the nodes
		nodes := []*discovery.Node{
			{
				Name:      "node-1",
				Host:      "localhost",
				StartTime: time.Now().Add(time.Second).UnixMilli(),
				Ports: map[string]int32{
					"clients-port": 1111,
					"peers-port":   1112,
				},
				IsRunning: false,
			},
		}
		// create the instance of provider
		provider := NewDiscovery(nodes, logger)
		require.NotNil(t, provider)
		assert.Error(t, provider.Start(ctx, discovery.NewMeta()))
	})
	t.Run("With Nodes", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// create a logger
		logger := log.DefaultLogger
		// define the nodes
		nodes := []*discovery.Node{
			{
				Name:      "node-1",
				Host:      "localhost",
				StartTime: time.Now().Add(time.Second).UnixMilli(),
				Ports: map[string]int32{
					"clients-port": 1111,
					"peers-port":   1112,
				},
				IsRunning: true,
			},
		}
		// create the instance of provider
		provider := NewDiscovery(nodes, logger)
		require.NotNil(t, provider)
		assert.NoError(t, provider.Start(ctx, discovery.NewMeta()))

		actual, err := provider.Nodes(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, actual)
		require.Len(t, actual, 1)

		assert.NoError(t, provider.Stop())
	})
	t.Run("With Watch nodes", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// create a logger
		logger := log.DefaultLogger
		// define the nodes
		nodes := []*discovery.Node{
			{
				Name:      "node-1",
				Host:      "localhost",
				StartTime: time.Now().Add(time.Second).UnixMilli(),
				Ports: map[string]int32{
					"clients-port": 1111,
					"peers-port":   1112,
				},
				IsRunning: true,
			},
		}
		// create the instance of provider
		provider := NewDiscovery(nodes, logger)
		require.NotNil(t, provider)
		assert.NoError(t, provider.Start(ctx, discovery.NewMeta()))

		watchChan, err := provider.Watch(ctx)
		assert.NoError(t, err)
		assert.NotNil(t, watchChan)
		assert.Empty(t, watchChan)

		assert.NoError(t, provider.Stop())
	})
	t.Run("With Earliest node", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// create a logger
		logger := log.DefaultLogger
		ts := time.Now()
		// define the nodes
		nodes := []*discovery.Node{
			{
				Name:      "node-1",
				Host:      "localhost",
				StartTime: ts.AddDate(0, 0, -1).UnixMilli(),
				Ports: map[string]int32{
					"clients-port": 1111,
					"peers-port":   1112,
				},
				IsRunning: true,
			},
			{
				Name:      "node-2",
				Host:      "localhost",
				StartTime: ts.Add(time.Second).UnixMilli(),
				Ports: map[string]int32{
					"clients-port": 1113,
					"peers-port":   1114,
				},
				IsRunning: true,
			},
		}
		// create the instance of provider
		provider := NewDiscovery(nodes, logger)
		require.NotNil(t, provider)
		assert.NoError(t, provider.Start(ctx, discovery.NewMeta()))

		actual, err := provider.EarliestNode(ctx)
		require.NoError(t, err)
		require.NotNil(t, actual)

		expected := &discovery.Node{
			Name:      "node-1",
			Host:      "localhost",
			StartTime: ts.AddDate(0, 0, -1).UnixMilli(),
			Ports: map[string]int32{
				"clients-port": 1111,
				"peers-port":   1112,
			},
			IsRunning: true,
		}
		assert.True(t, cmp.Equal(expected, actual))
		assert.NoError(t, provider.Stop())
	})
}
