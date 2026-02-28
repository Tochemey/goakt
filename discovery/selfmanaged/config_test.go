// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package selfmanaged

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/discovery"
)

func TestConfig_Validate(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		var c *Config
		err := c.Validate()
		require.Error(t, err)
		assert.ErrorIs(t, err, discovery.ErrInvalidConfig)
	})
	t.Run("empty ClusterName", func(t *testing.T) {
		c := &Config{
			ClusterName: "",
			SelfAddress: "127.0.0.1:7946",
		}
		err := c.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ClusterName")
	})
	t.Run("empty SelfAddress", func(t *testing.T) {
		c := &Config{
			ClusterName: "my-app",
			SelfAddress: "",
		}
		err := c.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "SelfAddress")
	})
	t.Run("invalid SelfAddress format", func(t *testing.T) {
		c := &Config{
			ClusterName: "my-app",
			SelfAddress: "not-a-valid-address",
		}
		err := c.Validate()
		require.Error(t, err)
	})
	t.Run("invalid SelfAddress port", func(t *testing.T) {
		c := &Config{
			ClusterName: "my-app",
			SelfAddress: "127.0.0.1:99999",
		}
		err := c.Validate()
		require.Error(t, err)
	})
	t.Run("valid config", func(t *testing.T) {
		c := &Config{
			ClusterName: "my-app",
			SelfAddress: "127.0.0.1:7946",
		}
		err := c.Validate()
		require.NoError(t, err)
	})
}

func TestConfig_defaults(t *testing.T) {
	t.Run("broadcastPort default", func(t *testing.T) {
		c := &Config{BroadcastPort: 0}
		assert.Equal(t, defaultBroadcastPort, c.broadcastPort())
	})
	t.Run("broadcastPort explicit", func(t *testing.T) {
		c := &Config{BroadcastPort: 9000}
		assert.Equal(t, 9000, c.broadcastPort())
	})
	t.Run("broadcastInterval default", func(t *testing.T) {
		c := &Config{BroadcastInterval: 0}
		assert.Equal(t, defaultBroadcastInterval, c.broadcastInterval())
	})
	t.Run("broadcastInterval explicit", func(t *testing.T) {
		c := &Config{BroadcastInterval: 2 * time.Second}
		assert.Equal(t, 2*time.Second, c.broadcastInterval())
	})
	t.Run("peerExpiry", func(t *testing.T) {
		c := &Config{BroadcastInterval: time.Second}
		assert.Equal(t, 3*time.Second, c.peerExpiry())
	})
	t.Run("broadcastIP default", func(t *testing.T) {
		c := &Config{}
		assert.True(t, c.broadcastIP().Equal(net.IPv4(255, 255, 255, 255)))
	})
	t.Run("broadcastIP explicit", func(t *testing.T) {
		c := &Config{BroadcastAddress: net.IPv4(127, 0, 0, 1)}
		assert.True(t, c.broadcastIP().Equal(net.IPv4(127, 0, 0, 1)))
	})
}
