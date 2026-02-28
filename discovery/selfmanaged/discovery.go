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
	"sync"

	"github.com/tochemey/goakt/v4/discovery"
	"github.com/tochemey/goakt/v4/internal/locker"
)

// Discovery implements discovery.Provider using UDP broadcast for peer discovery.
// No external services or peer configuration is required; nodes on the same LAN
// discover each other automatically.
type Discovery struct {
	_    locker.NoCopy
	cfg  *Config
	bc   *broadcast
	mu   sync.Mutex
	reg  bool
	init bool
}

var _ discovery.Provider = (*Discovery)(nil)

// NewDiscovery creates a self-managed discovery provider. The config must include
// ClusterName and SelfAddress; BroadcastPort and BroadcastInterval use defaults
// when zero.
func NewDiscovery(config *Config) *Discovery {
	return &Discovery{cfg: config}
}

// ID returns the provider identifier.
func (d *Discovery) ID() string {
	return discovery.ProviderSelfManaged
}

// Initialize validates the configuration. Must be called before Register.
func (d *Discovery) Initialize() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.init {
		return discovery.ErrAlreadyInitialized
	}
	if err := d.cfg.Validate(); err != nil {
		return err
	}
	d.init = true
	return nil
}

// Register starts the broadcast and listen loops. This node will announce itself
// and discover peers on the same subnet. Must be called after Initialize.
func (d *Discovery) Register() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.init {
		return discovery.ErrNotInitialized
	}
	if d.reg {
		return discovery.ErrAlreadyRegistered
	}
	d.bc = newBroadcast(d.cfg)
	if err := d.bc.start(); err != nil {
		return err
	}
	d.reg = true
	return nil
}

// Deregister stops the broadcast and listen loops.
func (d *Discovery) Deregister() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.reg {
		return discovery.ErrNotRegistered
	}
	d.bc.stop()
	d.bc = nil
	d.reg = false
	return nil
}

// DiscoverPeers returns addresses of peers discovered via broadcast, excluding
// this node. Addresses are in host:discoveryPort format for Memberlist.
func (d *Discovery) DiscoverPeers() ([]string, error) {
	d.mu.Lock()
	if !d.reg || d.bc == nil {
		d.mu.Unlock()
		return nil, discovery.ErrNotRegistered
	}
	bc := d.bc
	selfAddr := d.cfg.SelfAddress
	d.mu.Unlock()
	return bc.getPeers(selfAddr), nil
}

// Close releases resources. Safe to call multiple times.
func (d *Discovery) Close() error {
	_ = d.Deregister()
	return nil
}
