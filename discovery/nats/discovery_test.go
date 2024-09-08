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
	"fmt"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v2/discovery"
	"github.com/tochemey/goakt/v2/log"
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
	host := "127.0.0.1"
	// create the various config option
	applicationName := "accounts"
	actorSystemName := "AccountsSystem"
	natsSubject := "some-subject"

	// create the config
	config := &Config{
		ApplicationName: applicationName,
		ActorSystemName: actorSystemName,
		NatsServer:      fmt.Sprintf("nats://%s", serverAddr),
		NatsSubject:     natsSubject,
	}

	hostNode := discovery.Node{
		Name:          host,
		Host:          host,
		DiscoveryPort: gossipPort,
		PeersPort:     clusterPort,
		RemotingPort:  remotingPort,
	}

	// create the instance of provider
	provider := NewDiscovery(config, &hostNode, WithLogger(log.DiscardLogger))

	// initialize
	err := provider.Initialize()
	require.NoError(t, err)
	// return the provider
	return provider
}

func TestDiscovery(t *testing.T) {
	t.Run("With a new instance", func(t *testing.T) {
		// start the NATS server
		srv := startNatsServer(t)

		// generate the ports for the single node
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		// create a Cluster node
		host := "127.0.0.1"
		// create the various config option
		applicationName := "accounts"
		actorSystemName := "AccountsSystem"
		natsSubject := "some-subject"

		// create the config
		config := &Config{
			ApplicationName: applicationName,
			ActorSystemName: actorSystemName,
			NatsServer:      fmt.Sprintf("nats://%s", srv.Addr().String()),
			NatsSubject:     natsSubject,
		}

		hostNode := discovery.Node{
			Name:          host,
			Host:          host,
			DiscoveryPort: gossipPort,
			PeersPort:     clusterPort,
			RemotingPort:  remotingPort,
		}

		// create the instance of provider
		provider := NewDiscovery(config, &hostNode, WithLogger(log.DiscardLogger))
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
		// start the NATS server
		srv := startNatsServer(t)

		// generate the ports for the single node
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		// create a Cluster node
		host := "127.0.0.1"
		// create the various config option
		applicationName := "accounts"
		actorSystemName := "AccountsSystem"
		natsSubject := "some-subject"

		// create the config
		config := &Config{
			ApplicationName: applicationName,
			ActorSystemName: actorSystemName,
			NatsServer:      fmt.Sprintf("nats://%s", srv.Addr().String()),
			NatsSubject:     natsSubject,
		}

		hostNode := discovery.Node{
			Name:          host,
			Host:          host,
			DiscoveryPort: gossipPort,
			PeersPort:     clusterPort,
			RemotingPort:  remotingPort,
		}
		// create the instance of provider
		provider := NewDiscovery(config, &hostNode, WithLogger(log.DiscardLogger))
		require.NotNil(t, provider)
		assert.Equal(t, "nats", provider.ID())
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
		host := "127.0.0.1"

		// create the various config option
		natsServer := srv.Addr().String()
		applicationName := "accounts"
		actorSystemName := "AccountsSystem"
		natsSubject := "some-subject"

		// create the config
		config := &Config{
			ApplicationName: applicationName,
			ActorSystemName: actorSystemName,
			NatsServer:      fmt.Sprintf("nats://%s", natsServer),
			NatsSubject:     natsSubject,
		}

		hostNode := discovery.Node{
			Name:          host,
			Host:          host,
			DiscoveryPort: gossipPort,
			PeersPort:     clusterPort,
			RemotingPort:  remotingPort,
		}

		// create the instance of provider
		provider := NewDiscovery(config, &hostNode, WithLogger(log.DiscardLogger))

		// initialize
		err := provider.Initialize()
		assert.NoError(t, err)

		// stop the NATS server
		t.Cleanup(srv.Shutdown)
	})
	t.Run("With Initialize: already initialized", func(t *testing.T) {
		// start the NATS server
		srv := startNatsServer(t)

		// generate the ports for the single node
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		// create a Cluster node
		host := "127.0.0.1"

		// create the various config option
		natsServer := srv.Addr().String()
		applicationName := "accounts"
		actorSystemName := "AccountsSystem"
		natsSubject := "some-subject"

		// create the config
		config := &Config{
			ApplicationName: applicationName,
			ActorSystemName: actorSystemName,
			NatsServer:      fmt.Sprintf("nats://%s", natsServer),
			NatsSubject:     natsSubject,
		}

		hostNode := discovery.Node{
			Name:          host,
			Host:          host,
			DiscoveryPort: gossipPort,
			PeersPort:     clusterPort,
			RemotingPort:  remotingPort,
		}

		// create the instance of provider
		provider := NewDiscovery(config, &hostNode, WithLogger(log.DiscardLogger))
		provider.initialized = atomic.NewBool(true)
		assert.Error(t, provider.Initialize())
	})
	t.Run("With Register: already registered", func(t *testing.T) {
		// start the NATS server
		srv := startNatsServer(t)

		// generate the ports for the single node
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		// create a Cluster node
		host := "127.0.0.1"

		// create the various config option
		natsServer := srv.Addr().String()
		applicationName := "accounts"
		actorSystemName := "AccountsSystem"
		natsSubject := "some-subject"

		// create the config
		config := &Config{
			ApplicationName: applicationName,
			ActorSystemName: actorSystemName,
			NatsServer:      fmt.Sprintf("nats://%s", natsServer),
			NatsSubject:     natsSubject,
		}

		hostNode := discovery.Node{
			Name:          host,
			Host:          host,
			DiscoveryPort: gossipPort,
			PeersPort:     clusterPort,
			RemotingPort:  remotingPort,
		}

		// create the instance of provider
		provider := NewDiscovery(config, &hostNode, WithLogger(log.DiscardLogger))
		provider.registered = atomic.NewBool(true)
		err := provider.Register()
		assert.Error(t, err)
		assert.EqualError(t, err, discovery.ErrAlreadyRegistered.Error())
	})
	t.Run("With Deregister: already not registered", func(t *testing.T) {
		// start the NATS server
		srv := startNatsServer(t)

		// generate the ports for the single node
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		// create a Cluster node
		host := "127.0.0.1"

		// create the various config option
		natsServer := srv.Addr().String()
		applicationName := "accounts"
		actorSystemName := "AccountsSystem"
		natsSubject := "some-subject"

		// create the config
		config := &Config{
			ApplicationName: applicationName,
			ActorSystemName: actorSystemName,
			NatsServer:      fmt.Sprintf("nats://%s", natsServer),
			NatsSubject:     natsSubject,
		}

		hostNode := discovery.Node{
			Name:          host,
			Host:          host,
			DiscoveryPort: gossipPort,
			PeersPort:     clusterPort,
			RemotingPort:  remotingPort,
		}

		// create the instance of provider
		provider := NewDiscovery(config, &hostNode, WithLogger(log.DiscardLogger))
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
		discoveredNodeAddr := client2.hostNode.DiscoveryAddress()
		require.Equal(t, peers[0], discoveredNodeAddr)

		// discover more peers from client 2
		peers, err = client2.DiscoverPeers()
		require.NoError(t, err)
		require.NotEmpty(t, peers)
		require.Len(t, peers, 1)
		discoveredNodeAddr = client1.hostNode.DiscoveryAddress()
		require.Equal(t, peers[0], discoveredNodeAddr)

		// de-register client 2 but it can see client1
		require.NoError(t, client2.Deregister())
		peers, err = client2.DiscoverPeers()
		require.NoError(t, err)
		require.NotEmpty(t, peers)
		discoveredNodeAddr = client1.hostNode.DiscoveryAddress()
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
		// start the NATS server
		srv := startNatsServer(t)

		// generate the ports for the single node
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		// create a Cluster node
		host := "127.0.0.1"

		// create the various config option
		natsServer := srv.Addr().String()
		applicationName := "accounts"
		actorSystemName := "AccountsSystem"
		natsSubject := "some-subject"

		// create the config
		config := &Config{
			ApplicationName: applicationName,
			ActorSystemName: actorSystemName,
			NatsServer:      fmt.Sprintf("nats://%s", natsServer),
			NatsSubject:     natsSubject,
		}

		hostNode := discovery.Node{
			Name:          host,
			Host:          host,
			DiscoveryPort: gossipPort,
			PeersPort:     clusterPort,
			RemotingPort:  remotingPort,
		}

		provider := NewDiscovery(config, &hostNode, WithLogger(log.DiscardLogger))
		peers, err := provider.DiscoverPeers()
		assert.Error(t, err)
		assert.Empty(t, peers)
		assert.EqualError(t, err, discovery.ErrNotInitialized.Error())
	})
	t.Run("With Initialize: invalid config", func(t *testing.T) {
		config := &Config{}
		provider := NewDiscovery(config, nil, WithLogger(log.DiscardLogger))

		// initialize
		err := provider.Initialize()
		assert.Error(t, err)
	})
}
