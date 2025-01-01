/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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

package members

import (
	"context"
	"fmt"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
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

func TestEvents(t *testing.T) {
	ctx := context.Background()

	// start the NATS server
	srv := startNatsServer(t)

	// create a cluster node1
	node1, sd1 := startServer(t, "node1", srv.Addr().String())
	require.NotNil(t, node1)

	// create a cluster node2
	node2, sd2 := startServer(t, "node2", srv.Addr().String())
	require.NotNil(t, node2)

	// assert the node joined cluster event
	var events []*Event

	// define an events reader loop and read events for some time
L:
	for {
		select {
		case event, ok := <-node1.Events():
			if ok {
				events = append(events, event)
			}
		case <-time.After(time.Second):
			break L
		}
	}

	require.NotEmpty(t, events)
	require.Len(t, events, 1)
	event := events[0]
	require.NotNil(t, event)
	require.True(t, event.Type == NodeJoined)
	actualAddr := event.Member.PeerAddress()
	require.Equal(t, node2.Address(), actualAddr)
	peers, err := node1.Peers()
	require.NoError(t, err)
	require.Len(t, peers, 1)
	require.Equal(t, node2.Address(), peers[0].PeerAddress())
	me := node1.Whoami()
	require.NotNil(t, me)
	require.Equal(t, node1.Address(), me.PeerAddress())
	leader, err := node2.Leader()
	require.NoError(t, err)
	require.NotNil(t, leader)
	require.Equal(t, node1.Address(), leader.PeerAddress())

	// wait for some time
	lib.Pause(time.Second)

	// stop the second node
	require.NoError(t, node2.Stop(ctx))
	// wait for the event to propagate properly
	lib.Pause(time.Second)

	// reset the slice
	events = []*Event{}

	// define an events reader loop and read events for some time
L2:
	for {
		select {
		case event, ok := <-node1.Events():
			if ok {
				events = append(events, event)
			}
		case <-time.After(time.Second):
			break L2
		}
	}

	require.NotEmpty(t, events)
	require.Len(t, events, 1)
	event = events[0]
	require.NotNil(t, event)
	require.True(t, event.Type == NodeLeft)
	actualAddr = event.Member.PeerAddress()
	require.Equal(t, node2.Address(), actualAddr)

	require.NoError(t, node1.Stop(ctx))
	require.NoError(t, sd1.Close())
	require.NoError(t, sd2.Close())
	srv.Shutdown()
}

func TestMembersSingleNode(t *testing.T) {
	ctx := context.Background()

	// start the NATS server
	srv := startNatsServer(t)

	// create a cluster node1
	node, sd := startServer(t, "node1", srv.Addr().String())
	require.NotNil(t, node)
	require.NotNil(t, sd)

	// create an actor on node2
	actorName := "node2-actor1"
	actorKind := "actorKind"
	actor := &internalpb.ActorRef{
		ActorAddress: &goaktpb.Address{Name: actorName},
		ActorType:    actorKind,
	}
	err := node.PutActor(actor)
	require.NoError(t, err)

	// attempt to create the same actor from any node will fail
	anotherActorWithSameName := &internalpb.ActorRef{
		ActorAddress: &goaktpb.Address{Name: actorName},
		ActorType:    "anotherKind",
	}
	err = node.PutActor(anotherActorWithSameName)
	require.Error(t, err)
	require.EqualError(t, err, ErrActorAlreadyExists.Error())

	actual, err := node.GetActor(actorName)
	require.NoError(t, err)
	require.NotNil(t, actual)
	require.True(t, proto.Equal(actor, actual))

	key := "jobKey"
	err = node.SetSchedulerJobKey(key)
	require.NoError(t, err)

	exist := node.SchedulerJobKeyExists(key)
	require.True(t, exist)

	err = node.SetSchedulerJobKey(key)
	require.Error(t, err)
	require.EqualError(t, err, ErrKeyAlreadyExists.Error())

	exist = node.SchedulerJobKeyExists(key)
	require.True(t, exist)

	require.NoError(t, node.Stop(ctx))
	require.NoError(t, sd.Close())
	srv.Shutdown()
}

func TestMembers(t *testing.T) {
	ctx := context.Background()

	// start the NATS server
	srv := startNatsServer(t)

	// create a cluster node1
	node1, sd1 := startServer(t, "node1", srv.Addr().String())
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create a cluster node2
	node2, sd2 := startServer(t, "node2", srv.Addr().String())
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create a cluster node3
	node3, sd3 := startServer(t, "node3", srv.Addr().String())
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	// create an actor on node2
	actorName := "node2-actor1"
	actorKind := "actorKind"
	actor := &internalpb.ActorRef{
		ActorAddress: &goaktpb.Address{Name: actorName},
		ActorType:    actorKind,
	}
	err := node2.PutActor(actor)
	require.NoError(t, err)

	// wait for replication
	lib.Pause(2 * time.Second)

	// attempt to create the same actor from any node will fail
	anotherActorWithSameName := &internalpb.ActorRef{
		ActorAddress: &goaktpb.Address{Name: actorName},
		ActorType:    "anotherKind",
	}
	err = node3.PutActor(anotherActorWithSameName)
	require.Error(t, err)
	require.EqualError(t, err, ErrActorAlreadyExists.Error())

	// get the actor from node1 and node3
	actual, err := node1.GetActor(actorName)
	require.NoError(t, err)
	require.NotNil(t, actual)
	require.True(t, proto.Equal(actor, actual))

	actual, err = node3.GetActor(actorName)
	require.NoError(t, err)
	require.NotNil(t, actual)
	require.True(t, proto.Equal(actor, actual))

	// replicate some job keys
	key := "jobKey"
	err = node1.SetSchedulerJobKey(key)
	require.NoError(t, err)

	// wait for replication
	lib.Pause(2 * time.Second)

	// check on the other nodes that the key exist
	exist := node2.SchedulerJobKeyExists(key)
	require.True(t, exist)

	err = node2.SetSchedulerJobKey(key)
	require.Error(t, err)
	require.EqualError(t, err, ErrKeyAlreadyExists.Error())

	exist = node3.SchedulerJobKeyExists(key)
	require.True(t, exist)

	// remove the actor created on node2 using node1
	err = node1.DeleteActor(ctx, actorName)
	require.NoError(t, err)

	// wait for replication
	lib.Pause(time.Second)
	_, err = node3.GetActor(actorName)
	require.Error(t, err)
	require.EqualError(t, err, ErrActorNotFound.Error())

	require.NoError(t, node1.Stop(ctx))
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, node3.Stop(ctx))
	require.NoError(t, sd1.Close())
	require.NoError(t, sd2.Close())
	require.NoError(t, sd3.Close())
	srv.Shutdown()
}

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

func startServer(t *testing.T, nodeName, serverAddr string) (*Server, discovery.Provider) {
	// create a context
	ctx := context.TODO()

	logger := log.DefaultLogger

	// generate the ports for the single startNode
	nodePorts := dynaport.Get(3)
	discoveryPort := nodePorts[0]
	peersPort := nodePorts[1]
	remotingPort := nodePorts[2]

	// create a Cluster startNode
	host := "127.0.0.1"
	// create the various config option
	applicationName := "testApplication"
	actorSystemName := "testSystem"
	natsSubject := "testSubject"

	// create the config
	config := nats.Config{
		ApplicationName: applicationName,
		ActorSystemName: actorSystemName,
		NatsServer:      fmt.Sprintf("nats://%s", serverAddr),
		NatsSubject:     natsSubject,
		Host:            host,
		DiscoveryPort:   discoveryPort,
	}

	node := discovery.Node{
		Name:          host,
		Host:          host,
		DiscoveryPort: discoveryPort,
		PeersPort:     peersPort,
		RemotingPort:  remotingPort,
	}

	// create the instance of provider
	provider := nats.NewDiscovery(&config)

	// create the startNode
	server, err := NewServer(nodeName, provider, &node,
		WithJoinRetryInterval(500*time.Millisecond),
		WithMaxJoinAttempts(5),
		WithJoinTimeout(time.Second),
		WithShutdownTimeout(time.Second),
		WithReplicationInterval(500*time.Millisecond),
		WithLogger(logger))

	require.NoError(t, err)
	require.NotNil(t, server)

	// start the node
	err = server.Start(ctx)
	require.NoError(t, err)

	lib.Pause(100 * time.Millisecond)

	// return the cluster startNode
	return server, provider
}
