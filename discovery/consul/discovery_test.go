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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/consul"
	"github.com/travisjeffery/go-dynaport"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v3/discovery"
)

func TestDiscovery(t *testing.T) {
	agent := startConsulAgent(t)
	t.Run("With a new instance", func(t *testing.T) {
		ctx := t.Context()

		// generate the ports for the single node
		nodePorts := dynaport.Get(1)
		port := nodePorts[0]

		// create a Cluster node
		host := "127.0.0.1"
		actorSystemName := "AccountsSystem"

		endpoint, err := agent.ApiEndpoint(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, endpoint)

		config := &Config{
			Address:         endpoint,
			Timeout:         10,
			ActorSystemName: actorSystemName,
			Host:            host,
			DiscoveryPort:   port,
			Context:         ctx,
		}

		// create the instance of provider
		provider := NewDiscovery(config)
		require.NotNil(t, provider)
		// assert that provider implements the Discovery interface
		// this is a cheap test
		// assert the type of svc
		assert.IsType(t, &Discovery{}, provider)
		var p any = provider
		_, ok := p.(discovery.Provider)
		assert.True(t, ok)
	})
	t.Run("With ID assertion", func(t *testing.T) {
		ctx := t.Context()

		// generate the ports for the single node
		nodePorts := dynaport.Get(1)
		port := nodePorts[0]

		// create a Cluster node
		host := "127.0.0.1"
		actorSystemName := "AccountsSystem"

		endpoint, err := agent.ApiEndpoint(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, endpoint)

		// create the config
		config := &Config{
			Address:         endpoint,
			Timeout:         10 * time.Second,
			ActorSystemName: actorSystemName,
			Host:            host,
			DiscoveryPort:   port,
			Context:         ctx,
		}
		// create the instance of provider
		provider := NewDiscovery(config)
		require.NotNil(t, provider)
		require.NotNil(t, provider)
		assert.Equal(t, "consul", provider.ID())
	})
	t.Run("With Initialize", func(t *testing.T) {
		ctx := t.Context()

		// generate the ports for the single node
		nodePorts := dynaport.Get(1)
		port := nodePorts[0]

		// create a Cluster node
		host := "127.0.0.1"
		actorSystemName := "AccountsSystem"

		endpoint, err := agent.ApiEndpoint(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, endpoint)

		// create the config
		config := &Config{
			Address:         endpoint,
			Timeout:         10 * time.Second,
			ActorSystemName: actorSystemName,
			Host:            host,
			DiscoveryPort:   port,
			Context:         ctx,
			HealthCheck: &HealthCheck{
				Interval: 10,
				Timeout:  5,
			},
		}

		// create the instance of provider
		provider := NewDiscovery(config)
		require.NotNil(t, provider)

		// initialize
		err = provider.Initialize()
		assert.NoError(t, err)
	})
	t.Run("With Initialize: with invalid config", func(t *testing.T) {
		ctx := t.Context()

		// generate the ports for the single node
		nodePorts := dynaport.Get(1)
		port := nodePorts[0]

		// create a Cluster node
		host := "127.0.0.1"

		endpoint, err := agent.ApiEndpoint(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, endpoint)

		// create the config without the actor system name
		config := &Config{
			Address:       endpoint,
			Timeout:       10 * time.Second,
			Host:          host,
			DiscoveryPort: port,
			Context:       ctx,
		}

		// create the instance of provider
		provider := NewDiscovery(config)
		require.NotNil(t, provider)

		// initialize
		err = provider.Initialize()
		require.Error(t, err)
	})

	t.Run("With Initialize: already initialized", func(t *testing.T) {
		ctx := t.Context()
		nodePorts := dynaport.Get(1)
		port := nodePorts[0]

		// create a Cluster node
		host := "127.0.0.1"
		actorSystemName := "AccountsSystem"

		endpoint, err := agent.ApiEndpoint(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, endpoint)

		// create the config
		config := &Config{
			Address:         endpoint,
			Timeout:         10 * time.Second,
			ActorSystemName: actorSystemName,
			Host:            host,
			DiscoveryPort:   port,
			Context:         ctx,
		}

		// create the instance of provider
		provider := NewDiscovery(config)
		require.NotNil(t, provider)
		provider.initialized = atomic.NewBool(true)
		assert.Error(t, provider.Initialize())
	})
	t.Run("With Register: already registered", func(t *testing.T) {
		ctx := t.Context()

		// generate the ports for the single node
		nodePorts := dynaport.Get(1)
		port := nodePorts[0]

		// create a Cluster node
		host := "127.0.0.1"
		actorSystemName := "AccountsSystem"

		endpoint, err := agent.ApiEndpoint(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, endpoint)

		// create the config
		config := &Config{
			Address:         endpoint,
			Timeout:         10 * time.Second,
			ActorSystemName: actorSystemName,
			Host:            host,
			DiscoveryPort:   port,
			Context:         ctx,
			HealthCheck: &HealthCheck{
				Interval: 10,
				Timeout:  5,
			},
			QueryOptions: &QueryOptions{
				OnlyPassing: true,
				AllowStale:  false,           // Allow stale reads
				WaitTime:    5 * time.Second, // Maximum time to wait for changes
			},
		}

		// create the instance of provider
		provider := NewDiscovery(config)
		require.NotNil(t, provider)
		err = provider.Initialize()
		require.NoError(t, err)
		provider.registered = atomic.NewBool(true)
		err = provider.Register()
		require.Error(t, err)
		require.ErrorIs(t, err, discovery.ErrAlreadyRegistered)
	})
	t.Run("With Register: not initialized", func(t *testing.T) {
		ctx := t.Context()

		// generate the ports for the single node
		nodePorts := dynaport.Get(1)
		port := nodePorts[0]

		// create a Cluster node
		host := "127.0.0.1"
		actorSystemName := "AccountsSystem"

		endpoint, err := agent.ApiEndpoint(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, endpoint)

		// create the config
		config := &Config{
			Address:         endpoint,
			Timeout:         10 * time.Second,
			ActorSystemName: actorSystemName,
			Host:            host,
			DiscoveryPort:   port,
			Context:         ctx,
		}

		// create the instance of provider
		provider := NewDiscovery(config)
		require.NotNil(t, provider)
		err = provider.Register()
		require.Error(t, err)
		require.ErrorIs(t, err, discovery.ErrNotInitialized)
	})
	t.Run("With Deregister: when not initialized", func(t *testing.T) {
		ctx := t.Context()

		// generate the ports for the single node
		nodePorts := dynaport.Get(1)
		port := nodePorts[0]

		// create a Cluster node
		host := "127.0.0.1"
		actorSystemName := "AccountsSystem"

		endpoint, err := agent.ApiEndpoint(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, endpoint)

		// create the config
		config := &Config{
			Address:         endpoint,
			Timeout:         10 * time.Second,
			ActorSystemName: actorSystemName,
			Host:            host,
			DiscoveryPort:   port,

			Context: ctx,
		}

		// create the instance of provider
		provider := NewDiscovery(config)
		require.NotNil(t, provider)
		err = provider.Deregister()
		assert.Error(t, err)
		require.ErrorIs(t, err, discovery.ErrNotInitialized)
	})
	t.Run("With Deregister: already not registered", func(t *testing.T) {
		ctx := t.Context()

		// generate the ports for the single node
		nodePorts := dynaport.Get(1)
		port := nodePorts[0]

		// create a Cluster node
		host := "127.0.0.1"
		actorSystemName := "AccountsSystem"

		endpoint, err := agent.ApiEndpoint(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, endpoint)

		// create the config
		config := &Config{
			Address:         endpoint,
			Timeout:         10 * time.Second,
			ActorSystemName: actorSystemName,
			Host:            host,
			DiscoveryPort:   port,
			Context:         ctx,
		}

		// create the instance of provider
		provider := NewDiscovery(config)
		require.NotNil(t, provider)
		err = provider.Initialize()
		require.NoError(t, err)
		err = provider.Deregister()
		assert.Error(t, err)
		require.ErrorIs(t, err, discovery.ErrNotRegistered)
	})
	t.Run("With DiscoverPeers", func(t *testing.T) {
		ctx := t.Context()
		endpoint, err := agent.ApiEndpoint(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, endpoint)

		// create two peers
		client1 := newPeer(t, endpoint)
		client2 := newPeer(t, endpoint)

		// no discovery is allowed unless registered
		peers, err := client1.DiscoverPeers()
		require.Error(t, err)
		require.ErrorIs(t, err, discovery.ErrNotRegistered)
		require.Empty(t, peers)

		peers, err = client2.DiscoverPeers()
		require.Error(t, err)
		require.ErrorIs(t, err, discovery.ErrNotRegistered)
		require.Empty(t, peers)

		// register client 2
		require.NoError(t, client2.Register())
		peers, err = client2.DiscoverPeers()
		require.NoError(t, err)
		require.Empty(t, peers)

		// register client 1
		require.NoError(t, client1.Register())
		peers, err = client1.DiscoverPeers()
		require.NoError(t, err)
		require.NotEmpty(t, peers)
		require.Len(t, peers, 1)
		discoveredNodeAddr := client2.serviceID
		require.Equal(t, peers[0], discoveredNodeAddr)

		// discover more peers from client 2
		peers, err = client2.DiscoverPeers()
		require.NoError(t, err)
		require.NotEmpty(t, peers)
		require.Len(t, peers, 1)
		discoveredNodeAddr = client1.serviceID
		require.Equal(t, peers[0], discoveredNodeAddr)

		// de-register client 2
		require.NoError(t, client2.Deregister())
		peers, err = client2.DiscoverPeers()
		require.Error(t, err)
		require.ErrorIs(t, err, discovery.ErrNotRegistered)
		require.Empty(t, peers)

		// client-1 cannot see the deregistered client
		peers, err = client1.DiscoverPeers()
		require.NoError(t, err)
		require.Empty(t, peers)

		require.NoError(t, client1.Close())
		require.NoError(t, client2.Close())
	})
	t.Run("With DiscoverPeers: not initialized", func(t *testing.T) {
		ctx := t.Context()

		// generate the ports for the single node
		nodePorts := dynaport.Get(1)
		peersPort := nodePorts[0]

		// create a Cluster node
		host := "127.0.0.1"
		actorSystemName := "AccountsSystem"

		endpoint, err := agent.ApiEndpoint(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, endpoint)

		// create the config
		config := &Config{
			Address:         endpoint,
			Timeout:         10 * time.Second,
			ActorSystemName: actorSystemName,
			Host:            host,
			DiscoveryPort:   peersPort,
			Context:         ctx,
		}

		// create the instance of provider
		provider := NewDiscovery(config)
		peers, err := provider.DiscoverPeers()
		require.Error(t, err)
		require.Empty(t, peers)
		require.ErrorIs(t, err, discovery.ErrNotInitialized)
	})
	t.Run("With DiscoverPeers: not registered", func(t *testing.T) {
		ctx := t.Context()

		// generate the ports for the single node
		nodePorts := dynaport.Get(1)
		peersPort := nodePorts[0]

		// create a Cluster node
		host := "127.0.0.1"
		actorSystemName := "AccountsSystem"

		endpoint, err := agent.ApiEndpoint(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, endpoint)

		// create the config
		config := &Config{
			Address:         endpoint,
			Timeout:         10,
			ActorSystemName: actorSystemName,
			Host:            host,
			DiscoveryPort:   peersPort,
			Context:         ctx,
		}

		// create the instance of provider
		provider := NewDiscovery(config)
		require.NoError(t, provider.Initialize())
		peers, err := provider.DiscoverPeers()
		require.Error(t, err)
		require.Empty(t, peers)
		require.ErrorIs(t, err, discovery.ErrNotRegistered)
	})
}

func newPeer(t *testing.T, agentEndpoint string) *Discovery {
	t.Helper()
	// generate the ports for the single node
	nodePorts := dynaport.Get(1)
	port := nodePorts[0]

	// create a Cluster node
	host := "127.0.0.1"

	// create the config
	config := &Config{
		Address:         agentEndpoint,
		Timeout:         10 * time.Second,
		ActorSystemName: "test-system",
		Host:            host,
		DiscoveryPort:   port,
		Context:         t.Context(),
		HealthCheck: &HealthCheck{
			Interval: 10 * time.Second,
			Timeout:  5 * time.Second,
		},
		QueryOptions: &QueryOptions{
			OnlyPassing: false,
			AllowStale:  false,
			WaitTime:    5 * time.Second,
		},
	}

	// create the instance of provider
	provider := NewDiscovery(config)

	err := provider.Initialize()
	require.NoError(t, err)
	// return the provider
	return provider
}

func startConsulAgent(t *testing.T) *consul.ConsulContainer {
	t.Helper()
	consulContainer, err := consul.Run(t.Context(), "hashicorp/consul:1.15")
	require.NoError(t, err)
	t.Cleanup(func() {
		err := consulContainer.Terminate(context.Background())
		require.NoError(t, err)
	})
	return consulContainer
}
