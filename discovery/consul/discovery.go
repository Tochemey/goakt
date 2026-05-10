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

package consul

import (
	"fmt"
	"net"
	"strconv"
	"sync"

	"github.com/hashicorp/consul/api"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v4/discovery"
	"github.com/tochemey/goakt/v4/internal/locker"
)

// Discovery represents the Consul discovery provider.
type Discovery struct {
	_           locker.NoCopy
	client      *api.Client
	config      *Config
	initialized *atomic.Bool
	registered  *atomic.Bool
	mu          *sync.RWMutex
	serviceID   string
}

var _ discovery.Provider = (*Discovery)(nil)

// NewDiscovery creates a new instance of the Consul discovery provider.
// It initializes the provider with the given configuration and sets the initial state.
// It returns a pointer to the Discovery instance.
func NewDiscovery(config *Config) *Discovery {
	// shallow-copy the user's Config so Sanitize doesn't mutate the caller's pointer
	cfg := *config
	return &Discovery{
		config:      &cfg,
		initialized: atomic.NewBool(false),
		registered:  atomic.NewBool(false),
		mu:          &sync.RWMutex{},
		serviceID:   net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.DiscoveryPort)),
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

	// Self() probes the agent. The Consul Go client uses its own HTTP timeout
	// so we don't wrap a context here — the API doesn't accept one for this call.
	if _, err = client.Agent().Self(); err != nil {
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

	if !x.initialized.Load() {
		return discovery.ErrNotInitialized
	}
	if !x.registered.Load() {
		return discovery.ErrNotRegistered
	}
	return x.deregisterLocked()
}

// deregisterLocked removes the service from Consul.
// Caller must hold x.mu in write mode and must have verified registered==true.
func (x *Discovery) deregisterLocked() error {
	defer x.registered.Store(false)
	return x.client.Agent().ServiceDeregister(x.serviceID)
}

// DiscoverPeers retrieves the list of peers registered in Consul.
// It returns a slice of strings containing the addresses of the peers.
func (x *Discovery) DiscoverPeers() ([]string, error) {
	x.mu.RLock()
	if !x.initialized.Load() {
		x.mu.RUnlock()
		return nil, discovery.ErrNotInitialized
	}
	if !x.registered.Load() {
		x.mu.RUnlock()
		return nil, discovery.ErrNotRegistered
	}
	client := x.client
	x.mu.RUnlock()

	queryOpts := &api.QueryOptions{
		AllowStale: x.config.QueryOptions.AllowStale,
		Datacenter: x.config.QueryOptions.Datacenter,
		Near:       x.config.QueryOptions.Near,
	}
	if x.config.QueryOptions.WaitTime > 0 {
		queryOpts.WaitTime = x.config.QueryOptions.WaitTime
	}

	services, _, err := client.Health().Service(
		x.config.ActorSystemName,
		x.config.ActorSystemName,
		x.config.QueryOptions.OnlyPassing,
		queryOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to discover services: %w", err)
	}

	// Consul service IDs are unique by host:port, so no set dedup is needed.
	peers := make([]string, 0, len(services))
	for _, service := range services {
		if service == nil || service.Service == nil || service.Node == nil {
			continue
		}
		if service.Service.ID == x.serviceID {
			continue
		}

		address := service.Service.Address
		if address == "" {
			address = service.Node.Address
		}
		if service.Service.Port > 0 {
			peers = append(peers, net.JoinHostPort(address, strconv.Itoa(service.Service.Port)))
		} else {
			peers = append(peers, address)
		}
	}
	return peers, nil
}

// Close cleans up the discovery provider.
// If still registered, the service is deregistered from Consul on a best-effort basis;
// the error is dropped because Close has no meaningful recovery.
func (x *Discovery) Close() error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if x.registered.Load() {
		_ = x.deregisterLocked()
	}
	x.initialized.Store(false)
	x.client = nil
	return nil
}
