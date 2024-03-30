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

package nats

import (
	"os"
	"strconv"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/discovery"
	"github.com/tochemey/goakt/log"
)

func startNatsServer(t *testing.T) *natsserver.Server {
	t.Helper()
	serv, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1",
		Port: -1,
	})

	require.NoError(t, err)

	ready := make(chan bool)
	go func() {
		ready <- true
		serv.Start()
	}()
	<-ready

	if !serv.ReadyForConnections(2 * time.Second) {
		t.Fatalf("nats-io server failed to start")
	}

	return serv
}

func newPeer(t *testing.T, serverAddr string) *Discovery {
	// generate the ports for the single node
	nodePorts := dynaport.Get(3)
	gossipPort := nodePorts[0]
	clusterPort := nodePorts[1]
	remotingPort := nodePorts[2]

	// create a Cluster node
	host := "localhost"
	// set the environments
	require.NoError(t, os.Setenv("GOSSIP_PORT", strconv.Itoa(gossipPort)))
	require.NoError(t, os.Setenv("CLUSTER_PORT", strconv.Itoa(clusterPort)))
	require.NoError(t, os.Setenv("REMOTING_PORT", strconv.Itoa(remotingPort)))
	require.NoError(t, os.Setenv("NODE_NAME", "testNode"))
	require.NoError(t, os.Setenv("NODE_IP", host))

	// create the various config option
	applicationName := "accounts"
	actorSystemName := "AccountsSystem"
	natsSubject := "some-subject"
	// create the instance of provider
	provider := NewDiscovery()

	// create the config
	config := discovery.Config{
		ApplicationName: applicationName,
		ActorSystemName: actorSystemName,
		NatsServer:      serverAddr,
		NatsSubject:     natsSubject,
	}

	// set config
	err := provider.SetConfig(config)
	require.NoError(t, err)

	// initialize
	err = provider.Initialize()
	require.NoError(t, err)
	// clear the env var
	require.NoError(t, os.Unsetenv("GOSSIP_PORT"))
	require.NoError(t, os.Unsetenv("CLUSTER_PORT"))
	require.NoError(t, os.Unsetenv("REMOTING_PORT"))
	require.NoError(t, os.Unsetenv("NODE_NAME"))
	require.NoError(t, os.Unsetenv("NODE_IP"))
	// return the provider
	return provider
}

func TestDiscovery(t *testing.T) {
	t.Run("With a new instance", func(t *testing.T) {
		// create the instance of provider
		provider := NewDiscovery()
		require.NotNil(t, provider)
		// assert that provider implements the Discovery interface
		// this is a cheap test
		// assert the type of svc
		assert.IsType(t, &Discovery{}, provider)
		var p interface{} = provider
		_, ok := p.(discovery.Provider)
		assert.True(t, ok)
	})
	t.Run("With ID assertion", func(t *testing.T) {
		// cheap test
		// create the instance of provider
		provider := NewDiscovery()
		require.NotNil(t, provider)
		assert.Equal(t, "nats", provider.ID())
	})
	t.Run("With SetConfig", func(t *testing.T) {
		// create the various config option
		natsServer := "nats://127.0.0.1:2322"
		applicationName := "accounts"
		actorSystemName := "AccountsSystem"
		natsSubject := "some-subject"
		// create the instance of provider
		provider := NewDiscovery()
		// create the config
		config := discovery.Config{
			ApplicationName: applicationName,
			ActorSystemName: actorSystemName,
			NatsServer:      natsServer,
			NatsSubject:     natsSubject,
		}

		// set config
		assert.NoError(t, provider.SetConfig(config))
	})
	t.Run("With SetConfig: already initialized", func(t *testing.T) {
		// start the NATS server
		srv := startNatsServer(t)
		// create the various config option
		natsServer := srv.Addr().String()
		applicationName := "accounts"
		actorSystemName := "AccountsSystem"
		natsSubject := "some-subject"
		// create the instance of provider
		provider := NewDiscovery()
		provider.initialized = atomic.NewBool(true)
		// create the config
		config := discovery.Config{
			ApplicationName: applicationName,
			ActorSystemName: actorSystemName,
			NatsServer:      natsServer,
			NatsSubject:     natsSubject,
		}

		// set config
		err := provider.SetConfig(config)
		assert.Error(t, err)
		assert.EqualError(t, err, discovery.ErrAlreadyInitialized.Error())
		// stop the NATS server
		t.Cleanup(srv.Shutdown)
	})
	t.Run("With SetConfig: nats server not set", func(t *testing.T) {
		applicationName := "accounts"
		actorSystemName := "AccountsSystem"
		natsSubject := "some-subject"
		// create the instance of provider
		provider := NewDiscovery()
		// create the config
		config := discovery.Config{
			ApplicationName: applicationName,
			ActorSystemName: actorSystemName,
			NatsSubject:     natsSubject,
		}

		// set config
		assert.Error(t, provider.SetConfig(config))
	})
	t.Run("With SetConfig: nats subject not set", func(t *testing.T) {
		// create the various config option
		natsServer := "nats://127.0.0.1:2322"
		applicationName := "accounts"
		actorSystemName := "AccountsSystem"
		// create the instance of provider
		provider := NewDiscovery()
		// create the config
		config := discovery.Config{
			ApplicationName: applicationName,
			ActorSystemName: actorSystemName,
			NatsServer:      natsServer,
		}

		// set config
		assert.Error(t, provider.SetConfig(config))
	})
	t.Run("With SetConfig: actor system not set", func(t *testing.T) {
		// create the various config option
		natsServer := "nats://127.0.0.1:2322"
		applicationName := "accounts"
		natsSubject := "some-subject"
		// create the instance of provider
		provider := NewDiscovery()
		// create the config
		config := discovery.Config{
			ApplicationName: applicationName,
			NatsServer:      natsServer,
			NatsSubject:     natsSubject,
		}

		// set config
		assert.Error(t, provider.SetConfig(config))
	})
	t.Run("With SetConfig: application name not set", func(t *testing.T) {
		// create the various config option
		natsServer := "nats://127.0.0.1:2322"
		actorSystemName := "AccountsSystem"
		natsSubject := "some-subject"
		// create the instance of provider
		provider := NewDiscovery()
		// create the config
		config := discovery.Config{
			ActorSystemName: actorSystemName,
			NatsServer:      natsServer,
			NatsSubject:     natsSubject,
		}

		// set config
		assert.Error(t, provider.SetConfig(config))
	})
	t.Run("With Initialize", func(t *testing.T) {
		// start the NATS server
		srv := startNatsServer(t)

		// generate the ports for the single node
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		// create a Cluster node
		host := "localhost"
		// set the environments
		t.Setenv("GOSSIP_PORT", strconv.Itoa(gossipPort))
		t.Setenv("CLUSTER_PORT", strconv.Itoa(clusterPort))
		t.Setenv("REMOTING_PORT", strconv.Itoa(remotingPort))
		t.Setenv("NODE_NAME", "testNode")
		t.Setenv("NODE_IP", host)

		// create the various config option
		natsServer := srv.Addr().String()
		applicationName := "accounts"
		actorSystemName := "AccountsSystem"
		natsSubject := "some-subject"
		// create the instance of provider
		provider := NewDiscovery(WithLogger(log.DiscardLogger))

		// create the config
		config := discovery.Config{
			ApplicationName: applicationName,
			ActorSystemName: actorSystemName,
			NatsServer:      natsServer,
			NatsSubject:     natsSubject,
		}

		// set config
		err := provider.SetConfig(config)
		require.NoError(t, err)

		// initialize
		err = provider.Initialize()
		assert.NoError(t, err)

		// stop the NATS server
		t.Cleanup(srv.Shutdown)
	})
	t.Run("With Initialize: already initialized", func(t *testing.T) {
		// create the instance of provider
		provider := NewDiscovery()
		provider.initialized = atomic.NewBool(true)
		assert.Error(t, provider.Initialize())
	})
	t.Run("With Initialize: with host config not set", func(t *testing.T) {
		// create the instance of provider
		provider := NewDiscovery()
		assert.Error(t, provider.Initialize())
	})
	t.Run("With Register: already registered", func(t *testing.T) {
		// create the instance of provider
		provider := NewDiscovery()
		provider.registered = atomic.NewBool(true)
		err := provider.Register()
		assert.Error(t, err)
		assert.EqualError(t, err, discovery.ErrAlreadyRegistered.Error())
	})
	t.Run("With Deregister: already not registered", func(t *testing.T) {
		// create the instance of provider
		provider := NewDiscovery()
		err := provider.Deregister()
		assert.Error(t, err)
		assert.EqualError(t, err, discovery.ErrNotRegistered.Error())
	})
	t.Run("With DiscoverPeers", func(t *testing.T) {
		// start the NATS server
		srv := startNatsServer(t)
		// create two peers
		client1 := newPeer(t, srv.Addr().String())
		client2 := newPeer(t, srv.Addr().String())

		// no discovery is allowed unless registered
		peers, err := client1.DiscoverPeers()
		require.Error(t, err)
		assert.EqualError(t, err, discovery.ErrNotRegistered.Error())
		require.Empty(t, peers)

		peers, err = client2.DiscoverPeers()
		require.Error(t, err)
		assert.EqualError(t, err, discovery.ErrNotRegistered.Error())
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
		discoveredNodeAddr := client2.hostNode.GossipAddress()
		require.Equal(t, peers[0], discoveredNodeAddr)

		// discover more peers from client 2
		peers, err = client2.DiscoverPeers()
		require.NoError(t, err)
		require.NotEmpty(t, peers)
		require.Len(t, peers, 1)
		discoveredNodeAddr = client1.hostNode.GossipAddress()
		require.Equal(t, peers[0], discoveredNodeAddr)

		// de-register client 2 but it can see client1
		require.NoError(t, client2.Deregister())
		peers, err = client2.DiscoverPeers()
		require.NoError(t, err)
		require.NotEmpty(t, peers)
		discoveredNodeAddr = client1.hostNode.GossipAddress()
		require.Equal(t, peers[0], discoveredNodeAddr)

		// client-1 cannot see the deregistered client
		peers, err = client1.DiscoverPeers()
		require.NoError(t, err)
		require.Empty(t, peers)

		require.NoError(t, client1.Close())
		require.NoError(t, client2.Close())

		// stop the NATS server
		t.Cleanup(srv.Shutdown)
	})
	t.Run("With DiscoverPeers: not initialized", func(t *testing.T) {
		provider := NewDiscovery()
		peers, err := provider.DiscoverPeers()
		assert.Error(t, err)
		assert.Empty(t, peers)
		assert.EqualError(t, err, discovery.ErrNotInitialized.Error())
	})
}
