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
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"

	"github.com/tochemey/goakt/v4/discovery"
)

func TestNewDiscovery(t *testing.T) {
	cfg := &Config{
		ClusterName: "my-app",
		SelfAddress: "127.0.0.1:7946",
	}
	d := NewDiscovery(cfg)
	require.NotNil(t, d)
	assert.IsType(t, &Discovery{}, d)
	var p interface{} = d
	_, ok := p.(discovery.Provider)
	assert.True(t, ok)
}

func TestDiscovery_ID(t *testing.T) {
	d := NewDiscovery(&Config{ClusterName: "x", SelfAddress: "127.0.0.1:7946"})
	assert.Equal(t, discovery.ProviderSelfManaged, d.ID())
}

func TestDiscovery_Initialize(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		d := NewDiscovery(nil)
		err := d.Initialize()
		require.Error(t, err)
		assert.ErrorIs(t, err, discovery.ErrInvalidConfig)
	})
	t.Run("valid config", func(t *testing.T) {
		d := NewDiscovery(&Config{ClusterName: "my-app", SelfAddress: "127.0.0.1:7946"})
		err := d.Initialize()
		require.NoError(t, err)
	})
	t.Run("already initialized", func(t *testing.T) {
		d := NewDiscovery(&Config{ClusterName: "my-app", SelfAddress: "127.0.0.1:7946"})
		require.NoError(t, d.Initialize())
		err := d.Initialize()
		require.Error(t, err)
		assert.ErrorIs(t, err, discovery.ErrAlreadyInitialized)
	})
}

func TestDiscovery_Register(t *testing.T) {
	ports := dynaport.Get(1)
	cfg := &Config{
		ClusterName:       "my-app",
		SelfAddress:       net.JoinHostPort("127.0.0.1", "7946"),
		BroadcastPort:     ports[0],
		BroadcastInterval: 100 * time.Millisecond,
		BroadcastAddress:  net.IPv4(127, 0, 0, 1),
	}
	t.Run("before initialize", func(t *testing.T) {
		d := NewDiscovery(cfg)
		err := d.Register()
		require.Error(t, err)
		assert.ErrorIs(t, err, discovery.ErrNotInitialized)
	})
	t.Run("valid flow", func(t *testing.T) {
		d := NewDiscovery(cfg)
		require.NoError(t, d.Initialize())
		err := d.Register()
		require.NoError(t, err)
		require.NoError(t, d.Deregister())
	})
	t.Run("already registered", func(t *testing.T) {
		d := NewDiscovery(cfg)
		require.NoError(t, d.Initialize())
		require.NoError(t, d.Register())
		defer d.Deregister()
		err := d.Register()
		require.Error(t, err)
		assert.ErrorIs(t, err, discovery.ErrAlreadyRegistered)
	})
	t.Run("start fails", func(t *testing.T) {
		// On darwin and linux, SO_REUSEPORT allows multiple listeners on the same port.
		if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
			t.Skip("SO_REUSEPORT allows multiple bindings on darwin/linux")
		}
		d1 := NewDiscovery(cfg)
		require.NoError(t, d1.Initialize())
		require.NoError(t, d1.Register())
		defer d1.Deregister()

		d2 := NewDiscovery(cfg)
		require.NoError(t, d2.Initialize())
		err := d2.Register()
		require.Error(t, err)
	})
}

func TestDiscovery_Deregister(t *testing.T) {
	ports := dynaport.Get(1)
	cfg := &Config{
		ClusterName:       "my-app",
		SelfAddress:       "127.0.0.1:7946",
		BroadcastPort:     ports[0],
		BroadcastInterval: 100 * time.Millisecond,
		BroadcastAddress:  net.IPv4(127, 0, 0, 1),
	}
	t.Run("before register", func(t *testing.T) {
		d := NewDiscovery(cfg)
		require.NoError(t, d.Initialize())
		err := d.Deregister()
		require.Error(t, err)
		assert.ErrorIs(t, err, discovery.ErrNotRegistered)
	})
	t.Run("valid flow", func(t *testing.T) {
		d := NewDiscovery(cfg)
		require.NoError(t, d.Initialize())
		require.NoError(t, d.Register())
		err := d.Deregister()
		require.NoError(t, err)
	})
}

func TestDiscovery_DiscoverPeers(t *testing.T) {
	ports := dynaport.Get(1)
	cfg := &Config{
		ClusterName:       "my-app",
		SelfAddress:       "127.0.0.1:7946",
		BroadcastPort:     ports[0],
		BroadcastInterval: 100 * time.Millisecond,
		BroadcastAddress:  net.IPv4(127, 0, 0, 1),
	}
	t.Run("not registered", func(t *testing.T) {
		d := NewDiscovery(cfg)
		require.NoError(t, d.Initialize())
		peers, err := d.DiscoverPeers()
		require.Error(t, err)
		assert.Nil(t, peers)
		assert.ErrorIs(t, err, discovery.ErrNotRegistered)
	})
	t.Run("registered returns peers", func(t *testing.T) {
		d := NewDiscovery(cfg)
		require.NoError(t, d.Initialize())
		require.NoError(t, d.Register())
		defer d.Deregister()

		conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: ports[0]})
		require.NoError(t, err)
		pkt := []byte("goakt-v1|my-app|192.168.1.10:7950")
		_, _ = conn.Write(pkt)
		conn.Close()

		time.Sleep(50 * time.Millisecond)
		peers, err := d.DiscoverPeers()
		require.NoError(t, err)
		require.Len(t, peers, 1)
		assert.Equal(t, "192.168.1.10:7950", peers[0])
	})
	t.Run("registered alone returns empty", func(t *testing.T) {
		d := NewDiscovery(cfg)
		require.NoError(t, d.Initialize())
		require.NoError(t, d.Register())
		defer d.Deregister()
		peers, err := d.DiscoverPeers()
		require.NoError(t, err)
		assert.Empty(t, peers)
	})
}

func TestDiscovery_Close(t *testing.T) {
	ports := dynaport.Get(1)
	cfg := &Config{
		ClusterName:       "my-app",
		SelfAddress:       "127.0.0.1:7946",
		BroadcastPort:     ports[0],
		BroadcastInterval: 100 * time.Millisecond,
		BroadcastAddress:  net.IPv4(127, 0, 0, 1),
	}
	d := NewDiscovery(cfg)
	require.NoError(t, d.Initialize())
	require.NoError(t, d.Register())
	err := d.Close()
	require.NoError(t, err)
	_, err = d.DiscoverPeers()
	require.Error(t, err)
	assert.ErrorIs(t, err, discovery.ErrNotRegistered)
	err = d.Close()
	require.NoError(t, err)
}
