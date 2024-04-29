/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package dnssd

import (
	"context"
	"net"
	"sync"

	goset "github.com/deckarep/golang-set/v2"
	"github.com/pkg/errors"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/discovery"
)

const (
	DomainName = "domain-name"
	IPv6       = "ipv6"
)

// Discovery represents the DNS service discovery
// IP addresses are looked up by querying the default
// DNS resolver for A and AAAA records associated with the DNS name.
type Discovery struct {
	mu     sync.Mutex
	config *Config

	// states whether the actor system has started or not
	initialized *atomic.Bool
}

// enforce compilation error
var _ discovery.Provider = &Discovery{}

// NewDiscovery returns an instance of the DNS discovery provider
func NewDiscovery(config *Config) *Discovery {
	return &Discovery{
		mu:          sync.Mutex{},
		config:      config,
		initialized: atomic.NewBool(false),
	}
}

// ID returns the discovery provider id
func (d *Discovery) ID() string {
	return "dns-sd"
}

// Initialize initializes the plugin: registers some internal data structures, clients etc.
func (d *Discovery) Initialize() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.initialized.Load() {
		return discovery.ErrAlreadyInitialized
	}

	return d.config.Validate()
}

// Register registers this node to a service discovery directory.
func (d *Discovery) Register() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.initialized.Load() {
		return discovery.ErrAlreadyRegistered
	}

	d.initialized = atomic.NewBool(true)
	return nil
}

// Deregister removes this node from a service discovery directory.
func (d *Discovery) Deregister() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.initialized.Load() {
		return discovery.ErrNotInitialized
	}
	d.initialized = atomic.NewBool(false)
	return nil
}

// DiscoverPeers returns a list of known nodes.
func (d *Discovery) DiscoverPeers() ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.initialized.Load() {
		return nil, discovery.ErrNotInitialized
	}

	ctx := context.Background()

	// set ipv6 filter
	v6 := false
	if d.config.IPv6 != nil {
		v6 = *d.config.IPv6
	}

	peers := goset.NewSet[string]()
	var err error

	// only extract ipv6
	if v6 {
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip6", d.config.DomainName)
		if err != nil {
			return nil, err
		}

		for _, ip := range ips {
			if !peers.Add(ip.String()) {
				// return an error when fail to add to the list
				return nil, errors.New("failed to retrieve addresses")
			}
		}

		return peers.ToSlice(), nil
	}

	// lookup the addresses based upon the dns name
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, d.config.DomainName)
	if err != nil {
		return nil, err
	}

	for _, addr := range addrs {
		if !peers.Add(addr.IP.String()) {
			// return an error when fail to add to the list
			return nil, errors.New("failed to retrieve addresses")
		}
	}

	return peers.ToSlice(), nil
}

// Close closes the provider
func (d *Discovery) Close() error {
	return nil
}
