package discovery

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNode(t *testing.T) {
	t.Run("With valid node", func(t *testing.T) {
		node := &Node{
			Name:      "node-1",
			Host:      "localhost",
			StartTime: time.Now().Add(time.Second).UnixMilli(),
			Ports: map[string]int32{
				"discovery-port": 1111,
				"peers-port":     1112,
			},
			IsRunning: true,
		}
		assert.True(t, node.IsValid())
	})
	t.Run("With invalid node: invalid discovery-port name", func(t *testing.T) {
		node := &Node{
			Name:      "node-1",
			Host:      "localhost",
			StartTime: time.Now().Add(time.Second).UnixMilli(),
			Ports: map[string]int32{
				"clients-port": 1111, // invalid key
				"peers-port":   1112,
			},
			IsRunning: true,
		}
		assert.False(t, node.IsValid())
	})
	t.Run("With invalid node: invalid peers-port name", func(t *testing.T) {
		node := &Node{
			Name:      "node-1",
			Host:      "localhost",
			StartTime: time.Now().Add(time.Second).UnixMilli(),
			Ports: map[string]int32{
				"discovery-port": 1111,
				"peers-ports":    1112, // invalid key
			},
			IsRunning: true,
		}
		assert.False(t, node.IsValid())
	})
	t.Run("With invalid node: node name not set", func(t *testing.T) {
		node := &Node{
			Host:      "localhost",
			StartTime: time.Now().Add(time.Second).UnixMilli(),
			Ports: map[string]int32{
				"discovery-port": 1111,
				"peers-ports":    1112, // invalid key
			},
			IsRunning: true,
		}
		assert.False(t, node.IsValid())
	})
	t.Run("With invalid node: node hos not set", func(t *testing.T) {
		node := &Node{
			Name:      "node-1",
			StartTime: time.Now().Add(time.Second).UnixMilli(),
			Ports: map[string]int32{
				"discovery-port": 1111,
				"peers-ports":    1112, // invalid key
			},
			IsRunning: true,
		}
		assert.False(t, node.IsValid())
	})
	t.Run("With Addresses", func(t *testing.T) {
		node := &Node{
			Name:      "node-1",
			Host:      "localhost",
			StartTime: time.Now().Add(time.Second).UnixMilli(),
			Ports: map[string]int32{
				"discovery-port": 1111,
				"peers-port":     1112,
			},
			IsRunning: true,
		}

		assert.Equal(t, "localhost:1112", node.PeersAddress())
		assert.Equal(t, "localhost:1111", node.DiscoveryAddress())
	})
	t.Run("With Peers Port", func(t *testing.T) {
		node := &Node{
			Name:      "node-1",
			Host:      "localhost",
			StartTime: time.Now().Add(time.Second).UnixMilli(),
			Ports: map[string]int32{
				"discovery-port": 1111,
				"peers-port":     1112,
			},
			IsRunning: true,
		}
		port := node.PeersPort()
		assert.EqualValues(t, 1112, port)
	})
	t.Run("With Discovery Port", func(t *testing.T) {
		node := &Node{
			Name:      "node-1",
			Host:      "localhost",
			StartTime: time.Now().Add(time.Second).UnixMilli(),
			Ports: map[string]int32{
				"discovery-port": 1111,
				"peers-port":     1112,
			},
			IsRunning: true,
		}
		port := node.DiscoveryPort()
		assert.EqualValues(t, 1111, port)
	})
}
