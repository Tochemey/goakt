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

package cluster

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/discovery"
	"github.com/tochemey/goakt/v2/discovery/nats"
	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/internal/lib"
	"github.com/tochemey/goakt/v2/log"
)

func TestNodes(t *testing.T) {
	ctx := context.Background()
	// start the NATS server
	srv := startNatsServer(t)

	node1, provider1 := startNode(t, "node1", srv.Addr().String())
	require.NotNil(t, node1)
	require.NotNil(t, provider1)
	node1Addr := node1.AdvertisedAddress()

	// wait for the node to start properly
	lib.Pause(2 * time.Second)

	node2, provider2 := startNode(t, "node2", srv.Addr().String())
	require.NotNil(t, node2)
	require.NotNil(t, provider2)
	node2Addr := node2.AdvertisedAddress()

	// wait for the node to start properly
	lib.Pause(2 * time.Second)

	node3, provider3 := startNode(t, "node3", srv.Addr().String())
	require.NotNil(t, node3)
	require.NotNil(t, provider3)
	node3Adrr := node3.AdvertisedAddress()

	// wait for the node to start properly
	lib.Pause(2 * time.Second)

	node1Peers, err := node1.Peers(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, node1Peers)
	require.Len(t, node1Peers, 2)
	node1PeerAddrs := make([]string, len(node1Peers))
	for i := 0; i < len(node1Peers); i++ {
		peer := node1Peers[i]
		node1PeerAddrs[i] = net.JoinHostPort(peer.Host, strconv.Itoa(peer.Port))
	}
	require.ElementsMatch(t, node1PeerAddrs, []string{node2Addr, node3Adrr})

	node2Peers, err := node2.Peers(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, node2Peers)
	require.Len(t, node2Peers, 2)
	node2PeerAddrs := make([]string, len(node2Peers))
	for i := 0; i < len(node2Peers); i++ {
		peer := node2Peers[i]
		node2PeerAddrs[i] = net.JoinHostPort(peer.Host, strconv.Itoa(peer.Port))
	}
	require.ElementsMatch(t, node2PeerAddrs, []string{node1Addr, node3Adrr})

	node3Peers, err := node3.Peers(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, node3Peers)
	require.Len(t, node3Peers, 2)
	node3PeerAddrs := make([]string, len(node3Peers))
	for i := 0; i < len(node3Peers); i++ {
		peer := node3Peers[i]
		node3PeerAddrs[i] = net.JoinHostPort(peer.Host, strconv.Itoa(peer.Port))
	}
	require.ElementsMatch(t, node3PeerAddrs, []string{node1Addr, node2Addr})

	// persist some actor on node1 and retrieve it on node3

	// create an actor and replicate in the cluster using node1
	actorName := uuid.NewString()
	actor := &internalpb.ActorRef{ActorAddress: &goaktpb.Address{Name: actorName}}
	err = node1.PutActor(ctx, actor)
	require.NoError(t, err)

	// wait for actor to propagate in the cluster
	lib.Pause(2 * time.Second)

	// fetch the actor on node3
	actual, err := node3.GetActor(ctx, actorName)
	require.NoError(t, err)
	require.NotNil(t, actual)
	assert.True(t, proto.Equal(actor, actual))

	require.NoError(t, node1.Stop(ctx))
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, node3.Stop(ctx))
	require.NoError(t, provider1.Close())
	require.NoError(t, provider2.Close())
	require.NoError(t, provider3.Close())
}

func startNode(t *testing.T, nodeName, serverAddr string) (*Node, discovery.Provider) {
	// create a context
	ctx := context.TODO()

	// generate the ports for the single startNode
	nodePorts := dynaport.Get(3)
	gossipPort := nodePorts[0]
	clusterPort := nodePorts[1]
	remotingPort := nodePorts[2]

	// create a Cluster startNode
	host := "127.0.0.1"
	// create the various config option
	applicationName := "accounts"
	actorSystemName := "testSystem"
	natsSubject := "some-subject"

	// create the config
	config := nats.Config{
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
		Birthdate:     time.Now().UnixNano(),
	}

	logger := log.DefaultLogger

	// create the instance of provider
	provider := nats.NewDiscovery(&config, &hostNode, nats.WithLogger(logger))

	// create the cluster node
	node := NewNode(nodeName, provider, &hostNode, WithNodeLogger(logger))

	// start the node
	require.NoError(t, node.Start(ctx))

	return node, provider
}
