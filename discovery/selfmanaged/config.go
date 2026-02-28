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
	"time"

	"github.com/tochemey/goakt/v4/discovery"
	"github.com/tochemey/goakt/v4/internal/validation"
)

const (
	defaultBroadcastPort     = 7947
	defaultBroadcastInterval = 5 * time.Second
	peerExpiryMultiplier     = 3
)

// Config holds the configuration for the self-managed discovery provider.
// No peer addresses or seeds are required; nodes discover each other via
// UDP broadcast on the LAN.
type Config struct {
	// ClusterName identifies the cluster. Only nodes with the same name
	// discover each other. Prevents different clusters on the same LAN
	// from merging.
	ClusterName string
	// SelfAddress is this node's address (host:discoveryPort) to advertise.
	// Must be in the format expected by Memberlist (host:port).
	SelfAddress string
	// BroadcastPort is the UDP port used for discovery announcements.
	// Defaults to 7947 if zero.
	BroadcastPort int
	// BroadcastInterval is how often this node announces its presence.
	// Defaults to 5s if zero. Peers not seen within 3× this interval
	// are evicted from the cache.
	BroadcastInterval time.Duration
	// BroadcastAddress is the target IP for announcements. Defaults to
	// 255.255.255.255. Set to 127.0.0.1 for loopback-only (e.g. tests).
	// Loopback behavior: Linux uses 127.255.255.255; macOS uses multicast 224.0.0.1;
	// Windows binds to 127.0.0.1 and sends to 127.255.255.255.
	BroadcastAddress net.IP
}

// Validate checks that the configuration is valid.
func (c *Config) Validate() error {
	if c == nil {
		return discovery.ErrInvalidConfig
	}
	return validation.New(validation.FailFast()).
		AddValidator(validation.NewEmptyStringValidator("ClusterName", c.ClusterName)).
		AddValidator(validation.NewEmptyStringValidator("SelfAddress", c.SelfAddress)).
		AddValidator(validation.NewTCPAddressValidator(c.SelfAddress)).
		Validate()
}

// broadcastPort returns the effective broadcast port, applying defaults.
func (c *Config) broadcastPort() int {
	if c.BroadcastPort > 0 {
		return c.BroadcastPort
	}
	return defaultBroadcastPort
}

// broadcastInterval returns the effective broadcast interval, applying defaults.
func (c *Config) broadcastInterval() time.Duration {
	if c.BroadcastInterval > 0 {
		return c.BroadcastInterval
	}
	return defaultBroadcastInterval
}

// peerExpiry returns the duration after which a peer is considered dead.
func (c *Config) peerExpiry() time.Duration {
	return peerExpiryMultiplier * c.broadcastInterval()
}

// broadcastIP returns the IP to use for broadcast. Defaults to 255.255.255.255.
// When BroadcastAddress is in 127.0.0.0/8 (loopback), returns 127.255.255.255
// so multiple receivers on the same host can receive (works on Linux; macOS may vary).
func (c *Config) broadcastIP() net.IP {
	if len(c.BroadcastAddress) >= 4 && c.BroadcastAddress[0] == 127 {
		return net.IPv4(127, 255, 255, 255)
	}
	if len(c.BroadcastAddress) > 0 {
		return c.BroadcastAddress
	}
	return net.IPv4(255, 255, 255, 255)
}

// isLoopbackBroadcast returns true when using loopback (127.x.x.x) for discovery.
// In this mode, we use 127.255.255.255 and SO_BROADCAST so multiple receivers get packets.
func (c *Config) isLoopbackBroadcast() bool {
	return len(c.BroadcastAddress) >= 4 && c.BroadcastAddress[0] == 127
}

// broadcastBindAddr returns the address to bind the receiver. For loopback mode,
// Linux uses 127.255.255.255; Windows cannot bind to that, so uses 127.0.0.1
// (Windows delivers broadcasts to sockets bound to 127.0.0.1).
func (c *Config) broadcastBindAddr(port int) *net.UDPAddr {
	if c.isLoopbackBroadcast() {
		if runtime.GOOS == "windows" {
			return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}
		}
		return &net.UDPAddr{IP: net.IPv4(127, 255, 255, 255), Port: port}
	}
	return &net.UDPAddr{IP: net.IPv4zero, Port: port}
}
