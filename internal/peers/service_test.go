/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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

package peers

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

func TestClusterEvents(t *testing.T) {
	ctx := context.Background()

	// start the NATS server
	srv := startNatsServer(t)

	// create a cluster node1
	node1, sd1 := startService(t, srv.Addr().String())
	require.NotNil(t, node1)

	// create an actor on node2
	actorName := "actor"
	actorKind := "actorKind"
	actor := &internalpb.ActorRef{
		ActorAddress: &goaktpb.Address{Name: actorName},
		ActorType:    actorKind,
	}

	require.NoError(t, node1.PutActor(ctx, actor))
	actors := node1.Actors()
	require.Len(t, actors, 1)

	// create a cluster node2
	node2, sd2 := startService(t, srv.Addr().String())
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
	actualAddr := event.Peer.PeerAddress()
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

	actors = node2.Actors()
	require.Len(t, actors, 1)

	// wait for some time
	lib.Pause(time.Second)

	// stop the second node
	node2Address := node2.Address()
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
	actualAddr = event.Peer.PeerAddress()
	require.Equal(t, node2Address, actualAddr)

	require.NoError(t, node1.Stop(ctx))
	require.NoError(t, sd1.Close())
	require.NoError(t, sd2.Close())
	srv.Shutdown()
}

func TestCluster(t *testing.T) {
	t.Run("With actor replication", func(t *testing.T) {
		ctx := context.Background()

		// start the NATS server
		srv := startNatsServer(t)

		// create a cluster node1
		node1, sd1 := startService(t, srv.Addr().String())
		require.NotNil(t, node1)
		require.NotNil(t, sd1)

		// create a cluster node2
		node2, sd2 := startService(t, srv.Addr().String())
		require.NotNil(t, node2)
		require.NotNil(t, sd2)

		// create a cluster node3
		node3, sd3 := startService(t, srv.Addr().String())
		require.NotNil(t, node3)
		require.NotNil(t, sd3)

		// create an actor on node2
		actorName := "actor"
		actorKind := "actorKind"
		actor := &internalpb.ActorRef{
			ActorAddress: &goaktpb.Address{Name: actorName},
			ActorType:    actorKind,
		}
		err := node2.PutActor(ctx, actor)
		require.NoError(t, err)

		// wait for replication
		lib.Pause(2 * time.Second)

		// get the actor from node1 and node3
		actual, err := node1.GetActor(actorName)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.True(t, proto.Equal(actor, actual))

		actual, err = node3.GetActor(actorName)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.True(t, proto.Equal(actor, actual))

		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node2.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd3.Close())
		srv.Shutdown()
	})
	t.Run("With peer deletion", func(t *testing.T) {
		ctx := context.Background()

		// start the NATS server
		srv := startNatsServer(t)

		// create a cluster node1
		node1, sd1 := startService(t, srv.Addr().String())
		require.NotNil(t, node1)
		require.NotNil(t, sd1)

		// create a cluster node2
		node2, sd2 := startService(t, srv.Addr().String())
		require.NotNil(t, node2)
		require.NotNil(t, sd2)
		node2Address := node2.Address()

		// create a cluster node3
		node3, sd3 := startService(t, srv.Addr().String())
		require.NotNil(t, node3)
		require.NotNil(t, sd3)

		for i := 0; i < 4; i++ {
			actorName := fmt.Sprintf("node-1actor-%d", i)
			actorKind := "actorKind"
			actor := &internalpb.ActorRef{
				ActorAddress: &goaktpb.Address{Name: actorName},
				ActorType:    actorKind,
			}
			err := node1.PutActor(ctx, actor)
			require.NoError(t, err)
		}

		// wait for replication
		lib.Pause(time.Second)

		for i := 0; i < 2; i++ {
			actorName := fmt.Sprintf("node2-actor-%d", i)
			actorKind := "actorKind"
			actor := &internalpb.ActorRef{
				ActorAddress: &goaktpb.Address{Name: actorName},
				ActorType:    actorKind,
			}
			err := node2.PutActor(ctx, actor)
			require.NoError(t, err)
		}

		// wait for replication
		lib.Pause(time.Second)

		for i := 0; i < 3; i++ {
			actorName := fmt.Sprintf("node3-actor-%d", i)
			actorKind := "actorKind"
			actor := &internalpb.ActorRef{
				ActorAddress: &goaktpb.Address{Name: actorName},
				ActorType:    actorKind,
			}
			err := node3.PutActor(ctx, actor)
			require.NoError(t, err)
		}

		// wait for replication
		lib.Pause(time.Second)
		require.Len(t, node1.Actors(), 9)
		require.Len(t, node2.Actors(), 9)
		require.Len(t, node3.Actors(), 9)

		// stop the second node
		require.NoError(t, node2.Stop(ctx))
		lib.Pause(time.Second)

		require.Len(t, node1.Actors(), 9)
		require.Zero(t, node2.Actors())
		require.Len(t, node3.Actors(), 9)

		leader, err := node1.Leader()
		require.NoError(t, err)
		require.NotNil(t, leader)
		require.Equal(t, node1.Address(), leader.PeerAddress())
		err = node1.RemovePeerState(ctx, node2Address)
		require.NoError(t, err)

		lib.Pause(time.Second)

		require.Len(t, node1.Actors(), 7)
		require.Zero(t, node2.Actors())
		require.Len(t, node3.Actors(), 7)

		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd3.Close())
		srv.Shutdown()

	})
	t.Run("With actor deletion", func(t *testing.T) {
		ctx := context.Background()

		// start the NATS server
		srv := startNatsServer(t)

		// create a cluster node1
		node1, sd1 := startService(t, srv.Addr().String())
		require.NotNil(t, node1)
		require.NotNil(t, sd1)

		// create a cluster node2
		node2, sd2 := startService(t, srv.Addr().String())
		require.NotNil(t, node2)
		require.NotNil(t, sd2)

		// create a cluster node3
		node3, sd3 := startService(t, srv.Addr().String())
		require.NotNil(t, node3)
		require.NotNil(t, sd3)

		// create an actor on node2
		actorName := "actor"
		actorKind := "actorKind"
		actor := &internalpb.ActorRef{
			ActorAddress: &goaktpb.Address{Name: actorName},
			ActorType:    actorKind,
		}
		err := node2.PutActor(ctx, actor)
		require.NoError(t, err)

		// wait for replication
		lib.Pause(2 * time.Second)

		// get the actor from node1 and node3
		actual, err := node1.GetActor(actorName)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.True(t, proto.Equal(actor, actual))

		actual, err = node3.GetActor(actorName)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.True(t, proto.Equal(actor, actual))

		require.Len(t, node1.Actors(), 1)
		require.Len(t, node2.Actors(), 1)
		require.Len(t, node3.Actors(), 1)

		// let us delete the actor
		err = node2.RemoveActor(ctx, actorName)
		require.NoError(t, err)
		// wait for replication
		lib.Pause(100 * time.Millisecond)
		_, err = node3.GetActor(actorName)
		require.Error(t, err)
		require.Equal(t, err, ErrActorNotFound)

		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node2.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd3.Close())
		srv.Shutdown()
	})
	t.Run("With JobKeys replication", func(t *testing.T) {
		ctx := context.Background()

		// start the NATS server
		srv := startNatsServer(t)

		// create a cluster node1
		node1, sd1 := startService(t, srv.Addr().String())
		require.NotNil(t, node1)
		require.NotNil(t, sd1)

		// create a cluster node2
		node2, sd2 := startService(t, srv.Addr().String())
		require.NotNil(t, node2)
		require.NotNil(t, sd2)

		// create a cluster node3
		node3, sd3 := startService(t, srv.Addr().String())
		require.NotNil(t, node3)
		require.NotNil(t, sd3)

		jobKey := "job-key"
		err := node2.PutJobKey(ctx, jobKey)
		require.NoError(t, err)

		// wait for replication
		lib.Pause(500 * time.Millisecond)

		// get the job key from node1 and node3
		actual, err := node1.GetJobKey(jobKey)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.Equal(t, jobKey, *actual)

		actual, err = node3.GetJobKey(jobKey)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.Equal(t, jobKey, *actual)

		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node2.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd3.Close())
		srv.Shutdown()
	})
	t.Run("With JobKeys deletion", func(t *testing.T) {
		ctx := context.Background()

		// start the NATS server
		srv := startNatsServer(t)

		// create a cluster node1
		node1, sd1 := startService(t, srv.Addr().String())
		require.NotNil(t, node1)
		require.NotNil(t, sd1)

		// create a cluster node2
		node2, sd2 := startService(t, srv.Addr().String())
		require.NotNil(t, node2)
		require.NotNil(t, sd2)

		// create a cluster node3
		node3, sd3 := startService(t, srv.Addr().String())
		require.NotNil(t, node3)
		require.NotNil(t, sd3)

		jobKey := "job-key"
		err := node2.PutJobKey(ctx, jobKey)
		require.NoError(t, err)

		// wait for replication
		lib.Pause(500 * time.Millisecond)

		// get the job key from node1 and node3
		actual, err := node1.GetJobKey(jobKey)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.Equal(t, jobKey, *actual)

		actual, err = node3.GetJobKey(jobKey)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.Equal(t, jobKey, *actual)

		// let us remove the job key
		err = node2.RemoveJobKey(ctx, jobKey)
		require.NoError(t, err)

		lib.Pause(500 * time.Millisecond)

		// get the job key from node1 and node3
		actual, err = node1.GetJobKey(jobKey)
		require.Error(t, err)
		require.Nil(t, actual)
		require.EqualError(t, err, ErrKeyNotFound.Error())

		actual, err = node3.GetJobKey(jobKey)
		require.Error(t, err)
		require.Nil(t, actual)
		require.EqualError(t, err, ErrKeyNotFound.Error())

		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node2.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd3.Close())
		srv.Shutdown()
	})
}

func startService(t *testing.T, serverAddr string) (*Service, discovery.Provider) {
	// create a context
	ctx := context.TODO()

	logger := log.DiscardLogger

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

	node := &discovery.Node{
		Name:          host,
		Host:          host,
		DiscoveryPort: discoveryPort,
		PeersPort:     peersPort,
		RemotingPort:  remotingPort,
	}

	// create the instance of provider
	provider := nats.NewDiscovery(&config)

	// create the startNode
	service, err := NewService(provider, node,
		WithJoinRetryInterval(500*time.Millisecond),
		WithJoinTimeout(time.Second),
		WithShutdownTimeout(time.Second),
		WithLogger(logger))

	require.NoError(t, err)
	require.NotNil(t, service)

	// start the node
	err = service.Start(ctx)
	require.NoError(t, err)

	lib.Pause(100 * time.Millisecond)

	// return the cluster startNode
	return service, provider
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
