/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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

package consul

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"

	goset "github.com/deckarep/golang-set/v2"
	"github.com/hashicorp/consul/api"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v3/discovery"
	"github.com/tochemey/goakt/v3/internal/locker"
)

// Discovery represents the Consul discovery provider.
type Discovery struct {
	_           locker.NoCopy
	client      *api.Client
	config      *Config
	initialized *atomic.Bool
	registered  *atomic.Bool
	mu          *sync.RWMutex
	ctx         context.Context
	serviceID   string
}

var _ discovery.Provider = (*Discovery)(nil)

// NewDiscovery creates a new instance of the Consul discovery provider.
// It initializes the provider with the given configuration and sets the initial state.
// It returns a pointer to the Discovery instance.
func NewDiscovery(config *Config) *Discovery {
	if config == nil {
		config = new(Config)
	}

	return &Discovery{
		config:      config,
		initialized: atomic.NewBool(false),
		registered:  atomic.NewBool(false),
		mu:          &sync.RWMutex{},
		ctx:         config.Context,
		serviceID:   net.JoinHostPort(config.Host, strconv.Itoa(config.DiscoveryPort)),
	}
}

// ID returns the discovery provider id
func (x *Discovery) ID() string {
	return discovery.ProviderConsul
}

// Initialize initializes the consul discovery provider
// It creates a new consul client and checks if the connection is valid.
func (x *Discovery) Initialize() error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if x.initialized.Load() {
		return discovery.ErrAlreadyInitialized
	}

	x.config.Sanitize()
	if err := x.config.Validate(); err != nil {
		return fmt.Errorf("consul discovery config is invalid: %w", err)
	}

	consulConfig := api.DefaultConfig()
	consulConfig.Address = x.config.Address
	consulConfig.Datacenter = x.config.Datacenter
	consulConfig.Token = x.config.Token

	client, err := api.NewClient(consulConfig)
	if err != nil {
		return fmt.Errorf("failed to create consul client: %w", err)
	}

	_, cancel := context.WithTimeout(x.ctx, x.config.Timeout)
	defer cancel()

	_, err = client.Agent().Self()
	if err != nil {
		return fmt.Errorf("failed to connect to consul: %w", err)
	}

	x.client = client
	x.initialized.Store(true)

	return nil
}

// Register registers this node with Consul.
func (x *Discovery) Register() error {
	x.mu.Lock()
	defer x.mu.Unlock()

	// TODO: maybe remove this check
	if !x.initialized.Load() {
		return discovery.ErrNotInitialized
	}

	if x.registered.Load() {
		return discovery.ErrAlreadyRegistered
	}

	service := &api.AgentServiceRegistration{
		ID:      x.serviceID,
		Name:    x.config.ActorSystemName,
		Port:    x.config.DiscoveryPort,
		Address: x.config.Host,
		Tags:    []string{x.config.ActorSystemName},
	}

	if x.config.HealthCheck != nil {
		check := &api.AgentServiceCheck{
			Interval: x.config.HealthCheck.Interval.String(),
			Timeout:  x.config.HealthCheck.Timeout.String(),
			TCP:      net.JoinHostPort(x.config.Host, strconv.Itoa(x.config.DiscoveryPort)),
		}

		service.Check = check
	}

	// Register service
	err := x.client.Agent().ServiceRegister(service)
	if err != nil {
		return err
	}

	x.registered.Store(true)
	return nil
}

// Deregister removes this node from the Consul service discovery.
func (x *Discovery) Deregister() error {
	x.mu.Lock()
	defer x.mu.Unlock()

	// TODO: maybe remove this check
	if !x.initialized.Load() {
		return discovery.ErrNotInitialized
	}

	if !x.registered.Load() {
		return discovery.ErrNotRegistered
	}

	err := x.client.Agent().ServiceDeregister(x.serviceID)
	if err != nil {
		return err
	}

	x.registered.Store(false)
	return nil
}

// DiscoverPeers retrieves the list of peers registered in Consul.
// It returns a slice of strings containing the addresses of the peers.
func (x *Discovery) DiscoverPeers() ([]string, error) {
	x.mu.RLock()
	defer x.mu.RUnlock()

	// TODO: maybe remove this check
	if !x.initialized.Load() {
		return nil, discovery.ErrNotInitialized
	}

	if !x.registered.Load() {
		return nil, discovery.ErrNotRegistered
	}

	// Build query options
	queryOpts := &api.QueryOptions{
		AllowStale: x.config.QueryOptions.AllowStale,
		Datacenter: x.config.QueryOptions.Datacenter,
		Near:       x.config.QueryOptions.Near,
	}

	if x.config.QueryOptions.WaitTime > 0 {
		queryOpts.WaitTime = x.config.QueryOptions.WaitTime
	}

	var services []*api.ServiceEntry
	var err error

	// Query services - use health endpoint to get full service info
	services, _, err = x.client.Health().Service(
		x.config.ActorSystemName,
		x.config.ActorSystemName,
		x.config.QueryOptions.OnlyPassing,
		queryOpts)

	if err != nil {
		return nil, fmt.Errorf("failed to discover services: %w", err)
	}

	peers := goset.NewSet[string]()
	for _, service := range services {
		if service == nil || service.Service == nil || service.Node == nil {
			continue
		}

		// Skip self if registered
		if service.Service.ID == x.serviceID {
			continue
		}

		address := service.Service.Address
		if address == "" {
			address = service.Node.Address
		}

		if service.Service.Port > 0 {
			peers.Add(net.JoinHostPort(address, strconv.Itoa(service.Service.Port)))
		} else {
			peers.Add(address)
		}
	}

	return peers.ToSlice(), nil
}

// Close cleans up the discovery provider.
func (x *Discovery) Close() error {
	x.initialized.Store(false)
	x.registered.Store(false)

	x.mu.Lock()
	x.client = nil
	x.mu.Unlock()
	return nil
}
