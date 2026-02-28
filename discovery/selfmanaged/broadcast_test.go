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
	"fmt"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
)

func TestBroadcast_encodePacket(t *testing.T) {
	cfg := &Config{
		ClusterName: "test-cluster",
		SelfAddress: "192.168.1.10:7946",
	}
	b := newBroadcast(cfg)
	pkt := b.encodePacket()
	s := string(pkt)
	parts := strings.Split(s, protocolSep)
	require.Len(t, parts, 3)
	assert.Equal(t, protocolVersion, parts[0])
	assert.Equal(t, "test-cluster", parts[1])
	assert.Equal(t, "192.168.1.10:7946", parts[2])
}

func TestBroadcast_handlePacket(t *testing.T) {
	cfg := &Config{
		ClusterName: "test-cluster",
		SelfAddress: "127.0.0.1:7946",
	}
	b := newBroadcast(cfg)

	t.Run("valid packet", func(t *testing.T) {
		pkt := []byte(fmt.Sprintf("%s|%s|192.168.1.20:7947", protocolVersion, cfg.ClusterName))
		b.handlePacket(pkt)
		peers := b.getPeers(cfg.SelfAddress)
		require.Len(t, peers, 1)
		assert.Equal(t, "192.168.1.20:7947", peers[0])
	})
	t.Run("wrong protocol version", func(t *testing.T) {
		pkt := []byte("goakt-v2|test-cluster|192.168.1.21:7947")
		b.handlePacket(pkt)
		peers := b.getPeers(cfg.SelfAddress)
		assert.Len(t, peers, 1, "should not add peer from wrong protocol")
	})
	t.Run("wrong cluster name", func(t *testing.T) {
		pkt := []byte(fmt.Sprintf("%s|other-cluster|192.168.1.22:7947", protocolVersion))
		b.handlePacket(pkt)
		peers := b.getPeers(cfg.SelfAddress)
		assert.Len(t, peers, 1, "should not add peer from different cluster")
	})
	t.Run("invalid address format", func(t *testing.T) {
		pkt := []byte(fmt.Sprintf("%s|%s|invalid", protocolVersion, cfg.ClusterName))
		b.handlePacket(pkt)
		peers := b.getPeers(cfg.SelfAddress)
		assert.Len(t, peers, 1)
	})
	t.Run("invalid port in address", func(t *testing.T) {
		pkt := []byte(fmt.Sprintf("%s|%s|192.168.1.20:notaport", protocolVersion, cfg.ClusterName))
		b.handlePacket(pkt)
		peers := b.getPeers(cfg.SelfAddress)
		assert.Len(t, peers, 1)
	})
	t.Run("empty address", func(t *testing.T) {
		pkt := []byte(fmt.Sprintf("%s|%s|   ", protocolVersion, cfg.ClusterName))
		b.handlePacket(pkt)
		peers := b.getPeers(cfg.SelfAddress)
		assert.Len(t, peers, 1)
	})
	t.Run("empty host in address", func(t *testing.T) {
		pkt := []byte(fmt.Sprintf("%s|%s|:7946", protocolVersion, cfg.ClusterName))
		b.handlePacket(pkt)
		peers := b.getPeers(cfg.SelfAddress)
		assert.Len(t, peers, 1)
	})
	t.Run("malformed packet", func(t *testing.T) {
		b.handlePacket([]byte("short"))
		b.handlePacket([]byte("only|two"))
		peers := b.getPeers(cfg.SelfAddress)
		assert.Len(t, peers, 1)
	})
}

func TestBroadcast_getPeers_excludesSelf(t *testing.T) {
	cfg := &Config{
		ClusterName: "test-cluster",
		SelfAddress: "127.0.0.1:7946",
	}
	b := newBroadcast(cfg)
	b.handlePacket([]byte(fmt.Sprintf("%s|%s|127.0.0.1:7946", protocolVersion, cfg.ClusterName)))
	b.handlePacket([]byte(fmt.Sprintf("%s|%s|192.168.1.20:7947", protocolVersion, cfg.ClusterName)))
	peers := b.getPeers(cfg.SelfAddress)
	require.Len(t, peers, 1)
	assert.Equal(t, "192.168.1.20:7947", peers[0])
}

func TestBroadcast_getPeers_excludesExpired(t *testing.T) {
	cfg := &Config{
		ClusterName:       "test-cluster",
		SelfAddress:       "127.0.0.1:7946",
		BroadcastInterval: 100 * time.Millisecond,
	}
	b := newBroadcast(cfg)
	b.handlePacket([]byte(fmt.Sprintf("%s|%s|192.168.1.20:7947", protocolVersion, cfg.ClusterName)))
	peers := b.getPeers(cfg.SelfAddress)
	require.Len(t, peers, 1)
	time.Sleep(cfg.peerExpiry() + 50*time.Millisecond)
	peers = b.getPeers(cfg.SelfAddress)
	assert.Empty(t, peers)
}

func TestBroadcast_startStop(t *testing.T) {
	ports := dynaport.Get(1)
	cfg := &Config{
		ClusterName:       "test-cluster",
		SelfAddress:       net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", ports[0])),
		BroadcastPort:     ports[0],
		BroadcastInterval: 100 * time.Millisecond,
		BroadcastAddress:  net.IPv4(127, 0, 0, 1),
	}
	b := newBroadcast(cfg)
	require.NoError(t, b.start())
	time.Sleep(150 * time.Millisecond)
	b.stop()
}

func TestBroadcast_start_portInUse(t *testing.T) {
	// On darwin and linux, SO_REUSEPORT allows multiple listeners on the same port,
	// so this test would not get an error. Skip on those platforms.
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		t.Skip("SO_REUSEPORT allows multiple bindings on darwin/linux")
	}
	ports := dynaport.Get(1)
	cfg := &Config{
		ClusterName:      "test-cluster",
		SelfAddress:      "127.0.0.1:7946",
		BroadcastPort:    ports[0],
		BroadcastAddress: net.IPv4(127, 0, 0, 1),
	}
	b1 := newBroadcast(cfg)
	require.NoError(t, b1.start())
	defer b1.stop()

	b2 := newBroadcast(cfg)
	err := b2.start()
	require.Error(t, err)
}

func TestBroadcast_stopIdempotent(t *testing.T) {
	ports := dynaport.Get(1)
	cfg := &Config{
		ClusterName:      "test-cluster",
		SelfAddress:      "127.0.0.1:7946",
		BroadcastPort:    ports[0],
		BroadcastAddress: net.IPv4(127, 0, 0, 1),
	}
	b := newBroadcast(cfg)
	require.NoError(t, b.start())
	b.stop()
	b.stop()
	b.stop()
}

func TestIsTimeout(t *testing.T) {
	assert.False(t, isTimeout(net.UnknownNetworkError("unknown")))
}

func TestBroadcast_receivesPacket(t *testing.T) {
	ports := dynaport.Get(1)
	broadcastPort := ports[0]
	cfg := &Config{
		ClusterName:       "test-cluster",
		SelfAddress:       "127.0.0.1:7946",
		BroadcastPort:     broadcastPort,
		BroadcastInterval: 100 * time.Millisecond,
		BroadcastAddress:  net.IPv4(127, 0, 0, 1),
	}
	b := newBroadcast(cfg)
	require.NoError(t, b.start())
	defer b.stop()

	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: broadcastPort})
	require.NoError(t, err)
	defer conn.Close()

	pkt := []byte(fmt.Sprintf("%s|%s|192.168.1.99:7950", protocolVersion, cfg.ClusterName))
	_, err = conn.Write(pkt)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	peers := b.getPeers(cfg.SelfAddress)
	require.Len(t, peers, 1)
	assert.Equal(t, "192.168.1.99:7950", peers[0])
}
