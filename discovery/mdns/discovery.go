/*
 * MIT License
 *
 * Copyright (c) 2022-2023 Tochemey
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
	"github.com/tochemey/goakt/discovery"
	"go.uber.org/atomic"
)

const (
	ServiceName = "name"
	Service     = "service"
	Domain      = "domain"
	Port        = "port"
	IPv6        = "ipv6"
)

// discoConfig represents the mDNS provider discoConfig
type discoConfig struct {
	// Provider specifies the provider name
	Provider string
	// Service specifies the service name
	ServiceName string
	// Service specifies the service type
	Service string
	// Specifies the service domain
	Domain string
	// Port specifies the port the service is listening to
	Port int
	// IPv6 states whether to fetch ipv6 address instead of ipv4
	IPv6 *bool
}

// Discovery defines the mDNS discovery provider
type Discovery struct {
	option *discoConfig
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
func NewDiscovery() *Discovery {
	// create an instance of
	d := &Discovery{
		mu:          sync.Mutex{},
		stopChan:    make(chan struct{}, 1),
		initialized: atomic.NewBool(false),
		option:      &discoConfig{},
	}

	return d
}

// ID returns the discovery provider identifier
func (d *Discovery) ID() string {
	return "mdns"
}

// Initialize the discovery provider
func (d *Discovery) Initialize() error {
	// acquire the lock
	d.mu.Lock()
	// release the lock
	defer d.mu.Unlock()
	// first check whether the discovery provider is running
	if d.initialized.Load() {
		return discovery.ErrAlreadyInitialized
	}

	// check the options
	if d.option.Provider == "" {
		d.option.Provider = d.ID()
	}

	return nil
}

// Register registers this node to a service discovery directory.
func (d *Discovery) Register() error {
	// acquire the lock
	d.mu.Lock()
	// release the lock
	defer d.mu.Unlock()

	// first check whether the discovery provider has started
	// avoid to re-register the discovery
	if d.initialized.Load() {
		return discovery.ErrAlreadyRegistered
	}

	// initialize the resolver
	res, err := zeroconf.NewResolver(nil)
	// handle the error
	if err != nil {
		return errors.Wrap(err, "failed to instantiate the mDNS discovery provider")
	}
	// set the resolver
	d.resolver = res

	// register the service
	srv, err := zeroconf.Register(d.option.ServiceName, d.option.Service, d.option.Domain, d.option.Port, []string{"txtv=0", "lo=1", "la=2"}, nil)
	// handle the error
	if err != nil {
		return err
	}

	// set the server
	d.server = srv

	// set initialized
	d.initialized = atomic.NewBool(true)
	return nil
}

// Deregister removes this node from a service discovery directory.
func (d *Discovery) Deregister() error {
	// acquire the lock
	d.mu.Lock()
	// release the lock
	defer d.mu.Unlock()

	// first check whether the discovery provider has started
	if !d.initialized.Load() {
		return discovery.ErrNotInitialized
	}

	// set the initialized to false
	d.initialized = atomic.NewBool(false)

	// shutdown the registered service
	if d.server != nil {
		d.server.Shutdown()
	}

	// stop the watchers
	close(d.stopChan)
	// return
	return nil
}

// Close closes the provider
func (d *Discovery) Close() error {
	return nil
}

// SetConfig registers the underlying discovery configuration
func (d *Discovery) SetConfig(config discovery.Config) error {
	// acquire the lock
	d.mu.Lock()
	// release the lock
	defer d.mu.Unlock()

	// first check whether the discovery provider is running
	if d.initialized.Load() {
		return discovery.ErrAlreadyInitialized
	}

	var err error
	// validate the meta
	// let us make sure we have the required options set

	// assert the presence of service instance
	if _, ok := config[ServiceName]; !ok {
		return errors.New("mDNS service name is not provided")
	}

	// assert the presence of the service type
	if _, ok := config[Service]; !ok {
		return errors.New("mDNS service type is not provided")
	}

	// assert the presence of the listening port
	if _, ok := config[Port]; !ok {
		return errors.New("mDNS listening port is not provided")
	}

	// assert the service domain
	if _, ok := config[Domain]; !ok {
		return errors.New("mDNS domain is not provided")
	}

	// assert the ipv6 domain
	if _, ok := config[IPv6]; !ok {
		return errors.New("mDNS ipv6 option is not provided")
	}

	// set the options
	if err = d.setOptions(config); err != nil {
		return errors.Wrap(err, "failed to instantiate the mDNS discovery provider")
	}
	return nil
}

// DiscoverPeers returns a list of known nodes.
func (d *Discovery) DiscoverPeers() ([]string, error) {
	// first check whether the discovery provider is running
	if !d.initialized.Load() {
		return nil, discovery.ErrNotInitialized
	}
	// init entries channel
	entries := make(chan *zeroconf.ServiceEntry, 100)

	// create a context to browse the services for 5 seconds
	// TODO: make the timeout configurable
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// let us browse the services
	if err := d.resolver.Browse(ctx, d.option.Service, d.option.Domain, entries); err != nil {
		return nil, err
	}
	<-ctx.Done()

	// set ipv6 filter
	v6 := false
	if d.option.IPv6 != nil {
		v6 = *d.option.IPv6
	}

	// define the addresses list
	addresses := goset.NewSet[string]()
	for entry := range entries {
		// validate the entry
		if !d.validateEntry(entry) {
			continue
		}
		// lookup for v6 address
		if v6 {
			// iterate the list of ports
			for _, addr := range entry.AddrIPv6 {
				addresses.Add(net.JoinHostPort(addr.String(), strconv.Itoa(entry.Port)))
			}
		}

		// iterate the list of ports
		for _, addr := range entry.AddrIPv4 {
			addresses.Add(net.JoinHostPort(addr.String(), strconv.Itoa(entry.Port)))
		}
	}
	return addresses.ToSlice(), nil
}

// setOptions sets the kubernetes discoConfig
func (d *Discovery) setOptions(config discovery.Config) (err error) {
	// create an instance of Option
	option := new(discoConfig)
	// extract the service name
	option.ServiceName, err = config.GetString(ServiceName)
	// handle the error in case the service instance value is not properly set
	if err != nil {
		return err
	}
	// extract the service name
	option.Service, err = config.GetString(Service)
	// handle the error in case the service type value is not properly set
	if err != nil {
		return err
	}
	// extract the service domain
	option.Domain, err = config.GetString(Domain)
	// handle the error when the domain is not properly set
	if err != nil {
		return err
	}
	// extract the port the service is listening to
	option.Port, err = config.GetInt(Port)
	// handle the error when the port is not properly set
	if err != nil {
		return err
	}

	// extract the type of ip address to lookup
	option.IPv6, err = config.GetBool(IPv6)
	// handle the error
	if err != nil {
		return err
	}

	// in case none of the above extraction fails then set the option
	d.option = option
	return nil
}

// validateEntry validates the mDNS discovered entry
func (d *Discovery) validateEntry(entry *zeroconf.ServiceEntry) bool {
	return entry.Port == d.option.Port &&
		entry.Service == d.option.Service &&
		entry.Domain == d.option.Domain &&
		entry.Instance == d.option.ServiceName
}
