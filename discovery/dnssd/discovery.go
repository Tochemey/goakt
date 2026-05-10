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

package dnssd

import (
	"context"
	"net"
	"sync"
	"time"

	goset "github.com/deckarep/golang-set/v2"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v4/discovery"
	"github.com/tochemey/goakt/v4/internal/locker"
)

const lookupTimeout = 30 * time.Second

// Discovery represents the DNS service discovery
// IP addresses are looked up by querying the default
// DNS resolver for A and AAAA records associated with the DNS name.
type Discovery struct {
	_      locker.NoCopy
	mu     sync.Mutex
	config *Config

	initialized *atomic.Bool
	registered  *atomic.Bool
}

// enforce compilation error
var _ discovery.Provider = &Discovery{}

// NewDiscovery returns an instance of the DNS discovery provider
func NewDiscovery(config *Config) *Discovery {
	return &Discovery{
		mu:          sync.Mutex{},
		config:      config,
		initialized: atomic.NewBool(false),
		registered:  atomic.NewBool(false),
	}
}

// ID returns the discovery provider id
func (d *Discovery) ID() string {
	return discovery.ProviderDNS
}

// Initialize validates the provider configuration and marks the provider as ready
// for Register / DiscoverPeers.
func (d *Discovery) Initialize() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.initialized.Load() {
		return discovery.ErrAlreadyInitialized
	}
	if err := d.config.Validate(); err != nil {
		return err
	}
	d.initialized.Store(true)
	return nil
}

// Register marks this node as participating in discovery. DNS-SD has no remote
// registry to announce to, so this is purely local state.
func (d *Discovery) Register() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.initialized.Load() {
		return discovery.ErrNotInitialized
	}
	if d.registered.Load() {
		return discovery.ErrAlreadyRegistered
	}
	d.registered.Store(true)
	return nil
}

// Deregister removes this node from a service discovery directory.
func (d *Discovery) Deregister() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.initialized.Load() {
		return discovery.ErrNotInitialized
	}
	if !d.registered.Load() {
		return discovery.ErrNotRegistered
	}
	d.registered.Store(false)
	return nil
}

// DiscoverPeers returns a list of known nodes.
func (d *Discovery) DiscoverPeers() ([]string, error) {
	d.mu.Lock()
	if !d.initialized.Load() {
		d.mu.Unlock()
		return nil, discovery.ErrNotInitialized
	}
	if !d.registered.Load() {
		d.mu.Unlock()
		return nil, discovery.ErrNotRegistered
	}
	domain := d.config.DomainName
	v6 := d.config.IPv6 != nil && *d.config.IPv6
	d.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), lookupTimeout)
	defer cancel()

	resolver := &net.Resolver{PreferGo: true}

	if v6 {
		ips, err := resolver.LookupIP(ctx, "ip6", domain)
		if err != nil {
			return nil, err
		}
		ipList := make([]string, len(ips))
		for i, ip := range ips {
			ipList[i] = ip.String()
		}
		return goset.NewSet(ipList...).ToSlice(), nil
	}

	addrs, err := resolver.LookupIPAddr(ctx, domain)
	if err != nil {
		return nil, err
	}
	ipList := make([]string, len(addrs))
	for i, addr := range addrs {
		ipList[i] = addr.IP.String()
	}
	return goset.NewSet(ipList...).ToSlice(), nil
}

// Close closes the provider
func (d *Discovery) Close() error {
	return nil
}
