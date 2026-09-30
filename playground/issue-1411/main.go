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

// Reproduction for https://github.com/Tochemey/goakt/issues/1411
//
// The remoting client decoded the reply to a grain ask with the serializer
// of the request. A grain that answers a CBOR request (a plain Go struct
// registered with remote.WithSerializables) with a proto reply, or the
// other way round, could not be asked from another node: the reply came
// back as a decoding error, although the same ask worked locally.
//
// This sample runs two nodes and asks a grain on node 2, from node 1, with
// a Go struct; the grain answers with a proto message.
//
// With the fix the client decodes the reply with the serializer of the
// reply's own type.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery/nats"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// request is a plain Go struct, serialized with CBOR.
type request struct {
	Amount int
}

// mixedReplyGrain answers a CBOR request with a proto reply.
type mixedReplyGrain struct{}

func (g *mixedReplyGrain) OnActivate(context.Context, *actor.GrainProps) error { return nil }

func (g *mixedReplyGrain) OnReceive(ctx *actor.GrainContext) {
	switch ctx.Message().(type) {
	case *request:
		ctx.Response(new(testpb.Reply))
	default:
		ctx.Unhandled()
	}
}

func (g *mixedReplyGrain) OnDeactivate(context.Context, *actor.GrainProps) error { return nil }

func startNode(ctx context.Context, natsAddress string, discoveryPort, peersPort, remotingPort int) (actor.ActorSystem, error) {
	discovery := nats.NewDiscovery(&nats.Config{
		NatsServer:    "nats://" + natsAddress,
		NatsSubject:   "issue-1411",
		Host:          "localhost",
		DiscoveryPort: discoveryPort,
	})

	clusterConfig := actor.
		NewClusterConfig().
		WithDiscovery(discovery).
		WithDiscoveryPort(discoveryPort).
		WithPeersPort(peersPort).
		WithMinimumPeersQuorum(1).
		WithGrains(new(mixedReplyGrain))

	actorSystem, err := actor.NewActorSystem(
		"issue1411",
		actor.WithRemote(remote.NewConfig("localhost", remotingPort,
			remote.WithSerializables(new(request)),
		)),
		actor.WithLogger(log.DiscardLogger),
		actor.WithCluster(clusterConfig),
	)
	if err != nil {
		return nil, err
	}
	if err := actorSystem.Start(ctx); err != nil {
		return nil, err
	}
	return actorSystem, nil
}

func newNatsServer() *natsserver.Server {
	serv, err := natsserver.NewServer(&natsserver.Options{Host: "localhost", Port: -1})
	if err != nil {
		fmt.Printf("FAIL: creating NATS server: %v\n", err)
		os.Exit(1)
	}
	go serv.Start()
	if !serv.ReadyForConnections(2 * time.Second) {
		fmt.Println("FAIL: NATS server is not ready for connections")
		os.Exit(1)
	}
	return serv
}

func main() {
	ctx := context.Background()
	srv := newNatsServer()
	defer srv.Shutdown()

	node1, err := startNode(ctx, srv.Addr().String(), 9511, 9512, 9513)
	if err != nil {
		fmt.Printf("FAIL: starting node 1: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = node1.Stop(ctx) }()
	node2, err := startNode(ctx, srv.Addr().String(), 9514, 9515, 9516)
	if err != nil {
		fmt.Printf("FAIL: starting node 2: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = node2.Stop(ctx) }()
	for {
		peers, err := node1.Peers(ctx, time.Second)
		if err == nil && len(peers) == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	identity, err := actor.GrainOf[*mixedReplyGrain](ctx, node2, "mixed")
	if err != nil {
		fmt.Printf("FAIL: %v\n", err)
		os.Exit(1)
	}

	// Locally the ask works whatever the types are.
	if _, err := node2.AskGrain(ctx, identity, &request{Amount: 1}, time.Second); err != nil {
		fmt.Printf("FAIL: local ask: %v\n", err)
		os.Exit(1)
	}

	// From the other node the reply must decode as what the grain sent.
	reply, err := node1.AskGrain(ctx, identity, &request{Amount: 1}, time.Second)
	if err != nil {
		fmt.Printf("FAIL: remote ask: %v\n", err)
		os.Exit(1)
	}
	if _, ok := reply.(*testpb.Reply); !ok {
		fmt.Printf("FAIL: remote ask answered %T, not the grain's *testpb.Reply\n", reply)
		os.Exit(1)
	}
	fmt.Println("PASS: a CBOR request answered with a proto reply decodes on the other node")
}
