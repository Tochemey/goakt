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

package etcd

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	testcontainer "github.com/testcontainers/testcontainers-go/modules/etcd"
	"github.com/travisjeffery/go-dynaport"

	"github.com/tochemey/goakt/v3/discovery"
)

func TestDiscovery(t *testing.T) {
	cluster := startEtcdCluster(t)
	t.Run("With a new instance", func(t *testing.T) {
		ctx := t.Context()

		// generate the ports for the single node
		nodePorts := dynaport.Get(1)
		port := nodePorts[0]

		// create a Cluster node
		host := "127.0.0.1"
		actorSystemName := "AccountsSystem"

		endpoints, err := cluster.ClientEndpoints(ctx)
		require.NoError(t, err)

		config := &Config{
			Endpoints:       endpoints,
			Timeout:         10 * time.Second,
			ActorSystemName: actorSystemName,
			Host:            host,
			DiscoveryPort:   port,
			Context:         ctx,
			TTL:             60,
			DialTimeout:     5 * time.Second,
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

		endpoints, err := cluster.ClientEndpoints(ctx)
		require.NoError(t, err)

		config := &Config{
			Endpoints:       endpoints,
			Timeout:         10 * time.Second,
			ActorSystemName: actorSystemName,
			Host:            host,
			DiscoveryPort:   port,
			Context:         ctx,
			TTL:             60,
			DialTimeout:     5 * time.Second,
		}

		// create the instance of provider
		provider := NewDiscovery(config)
		require.NotNil(t, provider)
		require.NotNil(t, provider)
		require.Equal(t, "etcd", provider.ID())
	})
	t.Run("With Initialize", func(t *testing.T) {
		ctx := t.Context()

		// generate the ports for the single node
		nodePorts := dynaport.Get(1)
		port := nodePorts[0]

		// create a Cluster node
		host := "127.0.0.1"
		actorSystemName := "AccountsSystem"

		endpoints, err := cluster.ClientEndpoints(ctx)
		require.NoError(t, err)

		config := &Config{
			Endpoints:       endpoints,
			Timeout:         10 * time.Second,
			ActorSystemName: actorSystemName,
			Host:            host,
			DiscoveryPort:   port,
			Context:         ctx,
			TTL:             60,
			DialTimeout:     5 * time.Second,
		}

		// create the instance of provider
		provider := NewDiscovery(config)
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

		endpoints, err := cluster.ClientEndpoints(ctx)
		require.NoError(t, err)

		config := &Config{
			Endpoints:     endpoints,
			Timeout:       10 * time.Second,
			Host:          host,
			DiscoveryPort: port,
			Context:       ctx,
			TTL:           60,
			DialTimeout:   5 * time.Second,
		}

		// create the instance of provider
		provider := NewDiscovery(config)
		// initialize
		err = provider.Initialize()
		require.Error(t, err)
	})
	t.Run("With Initialize: already initialized", func(t *testing.T) {
		ctx := t.Context()

		// generate the ports for the single node
		nodePorts := dynaport.Get(1)
		port := nodePorts[0]

		// create a Cluster node
		host := "127.0.0.1"
		actorSystemName := "AccountsSystem"

		endpoints, err := cluster.ClientEndpoints(ctx)
		require.NoError(t, err)

		config := &Config{
			Endpoints:       endpoints,
			Timeout:         10 * time.Second,
			ActorSystemName: actorSystemName,
			Host:            host,
			DiscoveryPort:   port,
			Context:         ctx,
			TTL:             60,
			DialTimeout:     5 * time.Second,
		}

		// create the instance of provider
		provider := NewDiscovery(config)
		err = provider.Initialize()
		require.NoError(t, err)
		// initialize again
		err = provider.Initialize()
		require.Error(t, err)
		require.ErrorIs(t, err, discovery.ErrAlreadyInitialized)
	})
	t.Run("With Register: already registered", func(t *testing.T) {
		ctx := t.Context()

		// generate the ports for the single node
		nodePorts := dynaport.Get(1)
		port := nodePorts[0]

		// create a Cluster node
		host := "127.0.0.1"
		actorSystemName := "AccountsSystem"

		endpoints, err := cluster.ClientEndpoints(ctx)
		require.NoError(t, err)

		config := &Config{
			Endpoints:       endpoints,
			Timeout:         10 * time.Second,
			ActorSystemName: actorSystemName,
			Host:            host,
			DiscoveryPort:   port,
			Context:         ctx,
			TTL:             60,
			DialTimeout:     5 * time.Second,
		}

		// create the instance of provider
		provider := NewDiscovery(config)
		err = provider.Initialize()
		require.NoError(t, err)
		err = provider.Register()
		require.NoError(t, err)
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

		endpoints, err := cluster.ClientEndpoints(ctx)
		require.NoError(t, err)

		config := &Config{
			Endpoints:       endpoints,
			Timeout:         10 * time.Second,
			ActorSystemName: actorSystemName,
			Host:            host,
			DiscoveryPort:   port,
			Context:         ctx,
			TTL:             60,
			DialTimeout:     5 * time.Second,
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

		endpoints, err := cluster.ClientEndpoints(ctx)
		require.NoError(t, err)

		config := &Config{
			Endpoints:       endpoints,
			Timeout:         10 * time.Second,
			ActorSystemName: actorSystemName,
			Host:            host,
			DiscoveryPort:   port,
			Context:         ctx,
			TTL:             60,
			DialTimeout:     5 * time.Second,
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

		endpoints, err := cluster.ClientEndpoints(ctx)
		require.NoError(t, err)

		config := &Config{
			Endpoints:       endpoints,
			Timeout:         10 * time.Second,
			ActorSystemName: actorSystemName,
			Host:            host,
			DiscoveryPort:   port,
			Context:         ctx,
			TTL:             60,
			DialTimeout:     5 * time.Second,
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
	t.Run("With DiscoverPeers: not initialized", func(t *testing.T) {
		ctx := t.Context()

		// generate the ports for the single node
		nodePorts := dynaport.Get(1)
		port := nodePorts[0]

		// create a Cluster node
		host := "127.0.0.1"
		actorSystemName := "AccountsSystem"

		endpoints, err := cluster.ClientEndpoints(ctx)
		require.NoError(t, err)

		config := &Config{
			Endpoints:       endpoints,
			Timeout:         10 * time.Second,
			ActorSystemName: actorSystemName,
			Host:            host,
			DiscoveryPort:   port,
			Context:         ctx,
			TTL:             60,
			DialTimeout:     5 * time.Second,
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
		port := nodePorts[0]

		// create a Cluster node
		host := "127.0.0.1"
		actorSystemName := "AccountsSystem"

		endpoints, err := cluster.ClientEndpoints(ctx)
		require.NoError(t, err)

		config := &Config{
			Endpoints:       endpoints,
			Timeout:         10 * time.Second,
			ActorSystemName: actorSystemName,
			Host:            host,
			DiscoveryPort:   port,
			Context:         ctx,
			TTL:             60,
			DialTimeout:     5 * time.Second,
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

func TestDiscoverPeers(t *testing.T) {
	t.Run("With DiscoverPeers", func(t *testing.T) {
		cluster := startEtcdCluster(t)
		ctx := t.Context()
		actorSystemName := "AccountsSystem"
		endpoints, err := cluster.ClientEndpoints(ctx)
		require.NoError(t, err)

		// create two peers
		client1 := newPeer(t, actorSystemName, endpoints)
		client2 := newPeer(t, actorSystemName, endpoints)

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
		discoveredNodeAddr := client2.key
		require.Equal(t, peers[0], discoveredNodeAddr)

		// discover more peers from client 2
		peers, err = client2.DiscoverPeers()
		require.NoError(t, err)
		require.NotEmpty(t, peers)
		require.Len(t, peers, 1)
		discoveredNodeAddr = client1.key
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
}

func startEtcdCluster(t *testing.T) *testcontainer.EtcdContainer {
	t.Helper()
	etcdContainer, err := testcontainer.Run(
		t.Context(),
		"gcr.io/etcd-development/etcd:v3.5.14",
		testcontainer.WithNodes("etcd-1", "etcd-2", "etcd-3"),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		err := testcontainers.TerminateContainer(etcdContainer)
		require.NoError(t, err)
	})
	return etcdContainer
}

func newPeer(t *testing.T, actorSystemName string, endpoints []string) *Discovery {
	t.Helper()
	// generate the ports for the single node
	nodePorts := dynaport.Get(1)
	port := nodePorts[0]

	// create a Cluster node
	host := "127.0.0.1"

	// create the config
	config := &Config{
		Endpoints:       endpoints,
		Timeout:         10 * time.Second,
		ActorSystemName: actorSystemName,
		Host:            host,
		DiscoveryPort:   port,
		Context:         t.Context(),
		TTL:             60,
		DialTimeout:     5 * time.Second,
	}

	// create the instance of provider
	provider := NewDiscovery(config)

	err := provider.Initialize()
	require.NoError(t, err)
	// return the provider
	return provider
}
