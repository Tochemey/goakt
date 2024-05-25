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

package mdns

import (
	"context"
	"net"
	"strconv"
	"sync"
	"time"

	goset "github.com/deckarep/golang-set/v2"
	"github.com/grandcat/zeroconf"
	"github.com/pkg/errors"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v2/discovery"
)

// Discovery defines the mDNS discovery provider
type Discovery struct {
	config *Config
	mu     sync.Mutex

	stopChan chan struct{}

	initialized *atomic.Bool

	// resolver is used to browse for service discovery
	resolver *zeroconf.Resolver

	server *zeroconf.Server
}

// enforce compilation error
var _ discovery.Provider = &Discovery{}

// NewDiscovery returns an instance of the mDNS discovery provider
func NewDiscovery(config *Config) *Discovery {
	d := &Discovery{
		mu:          sync.Mutex{},
		stopChan:    make(chan struct{}, 1),
		initialized: atomic.NewBool(false),
		config:      config,
	}

	return d
}

// ID returns the discovery provider identifier
func (d *Discovery) ID() string {
	return "mdns"
}

// Initialize the discovery provider
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

	res, err := zeroconf.NewResolver(nil)
	if err != nil {
		return errors.Wrap(err, "failed to instantiate the mDNS discovery provider")
	}

	d.resolver = res

	srv, err := zeroconf.Register(d.config.ServiceName, d.config.Service, d.config.Domain, d.config.Port, []string{"txtv=0", "lo=1", "la=2"}, nil)
	if err != nil {
		return err
	}

	d.server = srv

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

	if d.server != nil {
		d.server.Shutdown()
	}

	close(d.stopChan)
	return nil
}

// Close closes the provider
func (d *Discovery) Close() error {
	return nil
}

// DiscoverPeers returns a list of known nodes.
func (d *Discovery) DiscoverPeers() ([]string, error) {
	if !d.initialized.Load() {
		return nil, discovery.ErrNotInitialized
	}
	entries := make(chan *zeroconf.ServiceEntry, 100)

	// create a context to browse the services for 5 seconds
	// TODO: make the timeout configurable
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := d.resolver.Browse(ctx, d.config.Service, d.config.Domain, entries); err != nil {
		return nil, err
	}
	<-ctx.Done()

	v6 := false
	if d.config.IPv6 != nil {
		v6 = *d.config.IPv6
	}

	addresses := goset.NewSet[string]()
	for entry := range entries {
		if !d.validateEntry(entry) {
			continue
		}

		if v6 {
			for _, addr := range entry.AddrIPv6 {
				addresses.Add(net.JoinHostPort(addr.String(), strconv.Itoa(entry.Port)))
			}
		}

		for _, addr := range entry.AddrIPv4 {
			addresses.Add(net.JoinHostPort(addr.String(), strconv.Itoa(entry.Port)))
		}
	}
	return addresses.ToSlice(), nil
}

// validateEntry validates the mDNS discovered entry
func (d *Discovery) validateEntry(entry *zeroconf.ServiceEntry) bool {
	return entry.Port == d.config.Port &&
		entry.Service == d.config.Service &&
		entry.Domain == d.config.Domain &&
		entry.Instance == d.config.ServiceName
}
