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

package client

import (
	"context"
	"fmt"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/actors"
	"github.com/tochemey/goakt/v2/discovery"
	"github.com/tochemey/goakt/v2/discovery/nats"
	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/internal/lib"
	"github.com/tochemey/goakt/v2/log"
	"github.com/tochemey/goakt/v2/test/data/testpb"
	testspb "github.com/tochemey/goakt/v2/test/data/testpb"
)

func TestClient(t *testing.T) {
	t.Run("With defaults", func(t *testing.T) {
		ctx := context.TODO()

		logger := log.DiscardLogger

		// start the NATS server
		srv := startNatsServer(t)
		addr := srv.Addr().String()

		sys1, node1Host, node1Port, sd1 := startNode(t, logger, "node1", addr)
		sys2, node2Host, node2Port, sd2 := startNode(t, logger, "node2", addr)
		sys3, node3Host, node3Port, sd3 := startNode(t, logger, "node3", addr)

		// wait for a proper and clean setup of the cluster
		lib.Pause(time.Second)

		addresses := []string{
			fmt.Sprintf("%s:%d", node1Host, node1Port),
			fmt.Sprintf("%s:%d", node2Host, node2Port),
			fmt.Sprintf("%s:%d", node3Host, node3Port),
		}

		client, err := New(ctx, addresses)
		require.NoError(t, err)
		require.NotNil(t, client)

		kinds, err := client.Kinds(ctx)
		require.NoError(t, err)
		require.NotNil(t, kinds)
		require.NotEmpty(t, kinds)
		require.Len(t, kinds, 2)

		expected := []string{
			"actors.funcactor",
			"client.testactor",
		}

		require.ElementsMatch(t, expected, kinds)
		actor := NewActor("client.testactor").WithName("actorName")

		err = client.Spawn(ctx, actor)
		require.NoError(t, err)

		lib.Pause(time.Second)

		// send a message
		reply, err := client.Ask(ctx, actor, new(testspb.TestReply), actors.DefaultAskTimeout)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expectedReply := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expectedReply, reply))

		lib.Pause(time.Second)

		err = client.Tell(ctx, actor, new(testspb.TestSend))
		require.NoError(t, err)

		err = client.Stop(ctx, actor)
		require.NoError(t, err)

		t.Cleanup(func() {
			client.Close()

			require.NoError(t, sys1.Stop(ctx))
			require.NoError(t, sys2.Stop(ctx))
			require.NoError(t, sys3.Stop(ctx))

			require.NoError(t, sd1.Close())
			require.NoError(t, sd2.Close())
			require.NoError(t, sd3.Close())

			srv.Shutdown()
			lib.Pause(time.Second)
		})
	})
	t.Run("With randomRouter strategy", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		// start the NATS server
		srv := startNatsServer(t)
		addr := srv.Addr().String()

		sys1, node1Host, node1Port, sd1 := startNode(t, logger, "node1", addr)
		sys2, node2Host, node2Port, sd2 := startNode(t, logger, "node2", addr)
		sys3, node3Host, node3Port, sd3 := startNode(t, logger, "node3", addr)

		// wait for a proper and clean setup of the cluster
		lib.Pause(time.Second)

		addresses := []string{
			fmt.Sprintf("%s:%d", node1Host, node1Port),
			fmt.Sprintf("%s:%d", node2Host, node2Port),
			fmt.Sprintf("%s:%d", node3Host, node3Port),
		}

		client, err := New(ctx,
			addresses,
			WithBalancerStrategy(RoundRobinStrategy))

		require.NoError(t, err)
		require.NotNil(t, client)

		kinds, err := client.Kinds(ctx)
		require.NoError(t, err)
		require.NotNil(t, kinds)
		require.NotEmpty(t, kinds)
		require.Len(t, kinds, 2)

		expected := []string{
			"actors.funcactor",
			"client.testactor",
		}

		require.ElementsMatch(t, expected, kinds)
		actor := NewActor("client.testactor").WithName("actorName")

		err = client.Spawn(ctx, actor)
		require.NoError(t, err)

		lib.Pause(time.Second)

		// send a message
		reply, err := client.Ask(ctx, actor, new(testspb.TestReply), actors.DefaultAskTimeout)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expectedReply := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expectedReply, reply))

		lib.Pause(time.Second)

		err = client.Tell(ctx, actor, new(testspb.TestSend))
		require.NoError(t, err)

		err = client.Stop(ctx, actor)
		require.NoError(t, err)

		t.Cleanup(func() {
			client.Close()

			require.NoError(t, sys1.Stop(ctx))
			require.NoError(t, sys2.Stop(ctx))
			require.NoError(t, sys3.Stop(ctx))

			require.NoError(t, sd1.Close())
			require.NoError(t, sd2.Close())
			require.NoError(t, sd3.Close())

			srv.Shutdown()

			lib.Pause(time.Second)
		})
	})
	t.Run("With Least-Load strategy", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		// start the NATS server
		srv := startNatsServer(t)
		addr := srv.Addr().String()

		sys1, node1Host, node1Port, sd1 := startNode(t, logger, "node1", addr)
		sys2, node2Host, node2Port, sd2 := startNode(t, logger, "node2", addr)
		sys3, node3Host, node3Port, sd3 := startNode(t, logger, "node3", addr)

		// wait for a proper and clean setup of the cluster
		lib.Pause(time.Second)

		addresses := []string{
			fmt.Sprintf("%s:%d", node1Host, node1Port),
			fmt.Sprintf("%s:%d", node2Host, node2Port),
			fmt.Sprintf("%s:%d", node3Host, node3Port),
		}

		client, err := New(ctx,
			addresses,
			WithBalancerStrategy(LeastLoadStrategy))

		require.NoError(t, err)
		require.NotNil(t, client)

		kinds, err := client.Kinds(ctx)
		require.NoError(t, err)
		require.NotNil(t, kinds)
		require.NotEmpty(t, kinds)
		require.Len(t, kinds, 2)

		expected := []string{
			"actors.funcactor",
			"client.testactor",
		}

		require.ElementsMatch(t, expected, kinds)
		actor := NewActor("client.testactor").WithName("actorName")

		err = client.Spawn(ctx, actor)
		require.NoError(t, err)

		lib.Pause(time.Second)

		// send a message
		reply, err := client.Ask(ctx, actor, new(testspb.TestReply), actors.DefaultAskTimeout)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expectedReply := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expectedReply, reply))

		lib.Pause(time.Second)

		err = client.Tell(ctx, actor, new(testspb.TestSend))
		require.NoError(t, err)

		err = client.Stop(ctx, actor)
		require.NoError(t, err)

		t.Cleanup(func() {
			client.Close()

			require.NoError(t, sys1.Stop(ctx))
			require.NoError(t, sys2.Stop(ctx))
			require.NoError(t, sys3.Stop(ctx))

			require.NoError(t, sd1.Close())
			require.NoError(t, sd2.Close())
			require.NoError(t, sd3.Close())

			srv.Shutdown()
			lib.Pause(time.Second)
		})
	})
	t.Run("With Refresh Interval", func(t *testing.T) {
		ctx := context.TODO()

		logger := log.DiscardLogger

		// start the NATS server
		srv := startNatsServer(t)
		addr := srv.Addr().String()

		sys1, node1Host, node1Port, sd1 := startNode(t, logger, "node1", addr)
		sys2, node2Host, node2Port, sd2 := startNode(t, logger, "node2", addr)
		sys3, node3Host, node3Port, sd3 := startNode(t, logger, "node3", addr)

		// wait for a proper and clean setup of the cluster
		lib.Pause(time.Second)

		addresses := []string{
			fmt.Sprintf("%s:%d", node1Host, node1Port),
			fmt.Sprintf("%s:%d", node2Host, node2Port),
			fmt.Sprintf("%s:%d", node3Host, node3Port),
		}

		client, err := New(ctx, addresses, WithRefresh(time.Minute))
		require.NoError(t, err)
		require.NotNil(t, client)

		kinds, err := client.Kinds(ctx)
		require.NoError(t, err)
		require.NotNil(t, kinds)
		require.NotEmpty(t, kinds)
		require.Len(t, kinds, 2)

		expected := []string{
			"actors.funcactor",
			"client.testactor",
		}

		require.ElementsMatch(t, expected, kinds)
		actor := NewActor("client.testactor").WithName("actorName")

		err = client.Spawn(ctx, actor)
		require.NoError(t, err)

		lib.Pause(time.Second)

		// send a message
		reply, err := client.Ask(ctx, actor, new(testspb.TestReply), actors.DefaultAskTimeout)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expectedReply := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expectedReply, reply))

		lib.Pause(time.Second)

		err = client.Tell(ctx, actor, new(testspb.TestSend))
		require.NoError(t, err)

		err = client.Stop(ctx, actor)
		require.NoError(t, err)

		t.Cleanup(func() {
			client.Close()

			require.NoError(t, sys1.Stop(ctx))
			require.NoError(t, sys2.Stop(ctx))
			require.NoError(t, sys3.Stop(ctx))

			require.NoError(t, sd1.Close())
			require.NoError(t, sd2.Close())
			require.NoError(t, sd3.Close())

			srv.Shutdown()

			lib.Pause(time.Second)
		})
	})
	t.Run("With SpawnWithBalancer", func(t *testing.T) {
		ctx := context.TODO()

		logger := log.DiscardLogger

		// start the NATS server
		srv := startNatsServer(t)
		addr := srv.Addr().String()

		sys1, node1Host, node1Port, sd1 := startNode(t, logger, "node1", addr)
		sys2, node2Host, node2Port, sd2 := startNode(t, logger, "node2", addr)
		sys3, node3Host, node3Port, sd3 := startNode(t, logger, "node3", addr)

		// wait for a proper and clean setup of the cluster
		lib.Pause(time.Second)

		addresses := []string{
			fmt.Sprintf("%s:%d", node1Host, node1Port),
			fmt.Sprintf("%s:%d", node2Host, node2Port),
			fmt.Sprintf("%s:%d", node3Host, node3Port),
		}

		client, err := New(ctx, addresses)
		require.NoError(t, err)
		require.NotNil(t, client)

		kinds, err := client.Kinds(ctx)
		require.NoError(t, err)
		require.NotNil(t, kinds)
		require.NotEmpty(t, kinds)
		require.Len(t, kinds, 2)

		expected := []string{
			"actors.funcactor",
			"client.testactor",
		}

		require.ElementsMatch(t, expected, kinds)
		actor := NewActor("client.testactor").WithName("actorName")

		err = client.SpawnWithBalancer(ctx, actor, RandomStrategy)
		require.NoError(t, err)

		lib.Pause(time.Second)

		// send a message
		reply, err := client.Ask(ctx, actor, new(testspb.TestReply), actors.DefaultAskTimeout)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expectedReply := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expectedReply, reply))

		lib.Pause(time.Second)

		err = client.Tell(ctx, actor, new(testspb.TestSend))
		require.NoError(t, err)

		err = client.Stop(ctx, actor)
		require.NoError(t, err)

		t.Cleanup(func() {
			client.Close()

			require.NoError(t, sys1.Stop(ctx))
			require.NoError(t, sys2.Stop(ctx))
			require.NoError(t, sys3.Stop(ctx))

			require.NoError(t, sd1.Close())
			require.NoError(t, sd2.Close())
			require.NoError(t, sd3.Close())

			srv.Shutdown()
			lib.Pause(time.Second)
		})
	})
	t.Run("With ReSpawn", func(t *testing.T) {
		ctx := context.TODO()

		logger := log.DiscardLogger

		// start the NATS server
		srv := startNatsServer(t)
		addr := srv.Addr().String()

		sys1, node1Host, node1Port, sd1 := startNode(t, logger, "node1", addr)
		sys2, node2Host, node2Port, sd2 := startNode(t, logger, "node2", addr)
		sys3, node3Host, node3Port, sd3 := startNode(t, logger, "node3", addr)

		// wait for a proper and clean setup of the cluster
		lib.Pause(time.Second)

		addresses := []string{
			fmt.Sprintf("%s:%d", node1Host, node1Port),
			fmt.Sprintf("%s:%d", node2Host, node2Port),
			fmt.Sprintf("%s:%d", node3Host, node3Port),
		}

		client, err := New(ctx, addresses)
		require.NoError(t, err)
		require.NotNil(t, client)

		kinds, err := client.Kinds(ctx)
		require.NoError(t, err)
		require.NotNil(t, kinds)
		require.NotEmpty(t, kinds)
		require.Len(t, kinds, 2)

		expected := []string{
			"actors.funcactor",
			"client.testactor",
		}

		require.ElementsMatch(t, expected, kinds)
		actor := NewActor("client.testactor").WithName("actorName")

		err = client.Spawn(ctx, actor)
		require.NoError(t, err)

		lib.Pause(time.Second)

		// send a message
		reply, err := client.Ask(ctx, actor, new(testspb.TestReply), actors.DefaultAskTimeout)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expectedReply := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expectedReply, reply))

		lib.Pause(time.Second)

		err = client.Tell(ctx, actor, new(testspb.TestSend))
		require.NoError(t, err)

		err = client.ReSpawn(ctx, actor)
		require.NoError(t, err)

		lib.Pause(time.Second)

		err = client.Stop(ctx, actor)
		require.NoError(t, err)

		t.Cleanup(func() {
			client.Close()

			require.NoError(t, sys1.Stop(ctx))
			require.NoError(t, sys2.Stop(ctx))
			require.NoError(t, sys3.Stop(ctx))

			require.NoError(t, sd1.Close())
			require.NoError(t, sd2.Close())
			require.NoError(t, sd3.Close())

			srv.Shutdown()
			lib.Pause(time.Second)
		})
	})
	t.Run("With ReSpawn after Stop", func(t *testing.T) {
		ctx := context.TODO()

		logger := log.DiscardLogger

		// start the NATS server
		srv := startNatsServer(t)
		addr := srv.Addr().String()

		sys1, node1Host, node1Port, sd1 := startNode(t, logger, "node1", addr)
		sys2, node2Host, node2Port, sd2 := startNode(t, logger, "node2", addr)
		sys3, node3Host, node3Port, sd3 := startNode(t, logger, "node3", addr)

		// wait for a proper and clean setup of the cluster
		lib.Pause(time.Second)

		addresses := []string{
			fmt.Sprintf("%s:%d", node1Host, node1Port),
			fmt.Sprintf("%s:%d", node2Host, node2Port),
			fmt.Sprintf("%s:%d", node3Host, node3Port),
		}

		client, err := New(ctx, addresses)
		require.NoError(t, err)
		require.NotNil(t, client)

		kinds, err := client.Kinds(ctx)
		require.NoError(t, err)
		require.NotNil(t, kinds)
		require.NotEmpty(t, kinds)
		require.Len(t, kinds, 2)

		expected := []string{
			"actors.funcactor",
			"client.testactor",
		}

		require.ElementsMatch(t, expected, kinds)
		actor := NewActor("client.testactor").WithName("actorName")

		err = client.Spawn(ctx, actor)
		require.NoError(t, err)

		lib.Pause(time.Second)

		// send a message
		reply, err := client.Ask(ctx, actor, new(testspb.TestReply), actors.DefaultAskTimeout)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expectedReply := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expectedReply, reply))

		lib.Pause(time.Second)

		err = client.Tell(ctx, actor, new(testspb.TestSend))
		require.NoError(t, err)

		lib.Pause(time.Second)

		err = client.Stop(ctx, actor)
		require.NoError(t, err)

		err = client.ReSpawn(ctx, actor)
		require.NoError(t, err)

		t.Cleanup(func() {
			client.Close()

			require.NoError(t, sys1.Stop(ctx))
			require.NoError(t, sys2.Stop(ctx))
			require.NoError(t, sys3.Stop(ctx))

			require.NoError(t, sd1.Close())
			require.NoError(t, sd2.Close())
			require.NoError(t, sd3.Close())

			srv.Shutdown()
			lib.Pause(time.Second)
		})
	})
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

func startNode(t *testing.T, logger log.Logger, nodeName, serverAddr string) (system actors.ActorSystem, remotingHost string, remotingPort int, provider discovery.Provider) {
	ctx := context.TODO()

	// generate the ports for the single startNode
	nodePorts := dynaport.Get(3)
	gossipPort := nodePorts[0]
	peersPort := nodePorts[1]
	remotePort := nodePorts[2]

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
		Name:          nodeName,
		Host:          host,
		DiscoveryPort: gossipPort,
		PeersPort:     peersPort,
		RemotingPort:  remotePort,
	}

	// create the instance of provider
	natsProvider := nats.NewDiscovery(&config, &hostNode, nats.WithLogger(logger))

	clusterConfig := actors.
		NewClusterConfig().
		WithKinds(new(testActor)).
		WithDiscovery(natsProvider).
		WithPeersPort(peersPort).
		WithDiscoveryPort(gossipPort).
		WithReplicaCount(1).
		WithMinimumPeersQuorum(1).
		WithPartitionCount(10)

	// create the actor system
	system, err := actors.NewActorSystem(
		actorSystemName,
		actors.WithPassivationDisabled(),
		actors.WithLogger(logger),
		actors.WithReplyTimeout(time.Minute),
		actors.WithRemoting(host, int32(remotePort)),
		actors.WithPeerStateLoopInterval(100*time.Millisecond),
		actors.WithCluster(clusterConfig))

	require.NotNil(t, system)
	require.NoError(t, err)

	// start the node
	require.NoError(t, system.Start(ctx))

	lib.Pause(time.Second)

	logger.Infof("node information=%s", hostNode.String())

	// return the cluster startNode
	return system, host, remotePort, natsProvider
}

type testActor struct {
	logger log.Logger
}

// enforce compilation error
var _ actors.Actor = (*testActor)(nil)

// newTestActor creates a testActor
func newTestActor() *testActor {
	return &testActor{}
}

// Init initialize the actor. This function can be used to set up some database connections
// or some sort of initialization before the actor init processing public
func (p *testActor) PreStart(context.Context) error {
	p.logger = log.DiscardLogger
	p.logger.Info("pre start")
	return nil
}

// Shutdown gracefully shuts down the given actor
func (p *testActor) PostStop(context.Context) error {
	p.logger.Info("post stop")
	return nil
}

// Receive processes any message dropped into the actor mailbox without a reply
func (p *testActor) Receive(ctx *actors.ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
		p.logger.Info("post start")
	case *testspb.TestSend:
	case *testspb.TestReply:
		p.logger.Info("received a test reply message...")
		ctx.Response(&testspb.Reply{Content: "received message"})
	default:
		ctx.Unhandled()
	}
}
