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

package actor

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"

	"github.com/tochemey/goakt/v2/discovery"
	"github.com/tochemey/goakt/v2/discovery/nats"
	"github.com/tochemey/goakt/v2/log"
	"github.com/tochemey/goakt/v2/test/data/testpb"
)

const (
	delay          = 1 * time.Second
	askTimeout     = 100 * time.Millisecond
	passivateAfter = 200 * time.Millisecond
)

type testActor struct{}

var _ Actor = (*testActor)(nil)

func (actor *testActor) PreStart(context.Context) error { return nil }
func (actor *testActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestTell:
	case *testpb.TestAsk:
		ctx.Response(new(testpb.TestAsk))
	case *testpb.TestAskTimeout:
		time.Sleep(delay)
	case *testpb.TestPanic:
		panic("panic")
	default:
		ctx.Unhandled()
	}
}
func (actor *testActor) PostStop(context.Context) error { return nil }

type postStopActor struct{}

func (actor *postStopActor) PostStop(context.Context) error { return errors.New("failed") }
func (actor *postStopActor) PreStart(context.Context) error { return nil }
func (actor *postStopActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestTell:
	default:
		ctx.Unhandled()
	}
}

type preStartActor struct {
	count int
}

func (actor *preStartActor) PreStart(context.Context) error {
	actor.count++
	if actor.count > 1 {
		return errors.New("failed")
	}
	return nil
}
func (actor *preStartActor) PostStop(context.Context) error { return nil }
func (actor *preStartActor) Receive(*ReceiveContext)        {}

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

func startNode(t *testing.T, nodeName, serverAddr string) (*ActorSystem, discovery.Provider) {
	ctx := context.Background()

	// generate the ports for the single startNode
	nodePorts := dynaport.Get(3)
	gossipPort := nodePorts[0]
	peersPort := nodePorts[1]
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
		Name:         nodeName,
		Host:         host,
		GossipPort:   gossipPort,
		PeersPort:    peersPort,
		RemotingPort: remotingPort,
	}

	// create the instance of provider
	provider := nats.NewDiscovery(&config, &hostNode)

	// create the actor system
	system, err := NewActorSystem(
		nodeName,
		WithPassivationDisabled(),
		WithLogger(log.DiscardLogger),
		WithAskTimeout(time.Minute),
		WithHost(host),
		WithCluster(
			NewClusterConfig().
				WithKinds(new(testActor)).
				WithPartitionCount(10).
				WithReplicaCount(1).
				WithRemotingPort(remotingPort).
				WithGossipPort(gossipPort).
				WithPeersPort(peersPort).
				WithMinimumPeersQuorum(1).
				WithDiscovery(provider)))

	require.NotNil(t, system)
	require.NoError(t, err)

	// start the node
	require.NoError(t, system.Start(ctx))
	time.Sleep(2 * time.Second)

	// return the cluster startNode
	return system, provider
}
