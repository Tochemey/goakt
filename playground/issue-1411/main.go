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
// The remoting client decoded the reply to an ask with the serializer of the
// request. A grain or an actor that answers a CBOR request (a plain Go struct
// registered with remote.WithSerializables) with a proto reply, or a proto
// request with a CBOR reply, could not be asked from another node: the reply
// came back as a decoding error, although the same ask worked locally.
//
// This sample runs a three-node cluster. A grain and an actor live on node 3
// and answer every request with a reply of the other serializer. Node 1 and
// node 2 each ask both of them with a CBOR request and with a proto request.
//
// With the fix the client falls back to every registered serializer when the
// request's serializer cannot decode the reply.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery/nats"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

func main() {
	ctx := context.Background()
	srv := newNatsServer()

	node1 := startNode(ctx, srv.Addr().String(), 9511, 9512, 9513)
	node2 := startNode(ctx, srv.Addr().String(), 9514, 9515, 9516)
	node3 := startNode(ctx, srv.Addr().String(), 9517, 9518, 9519)

	for {
		peers, err := node1.Peers(ctx, time.Second)
		if err == nil && len(peers) == 2 {
			break
		}

		pause.For(50 * time.Millisecond)
	}

	// The grain and the actor live on node 3.
	identity, err := actor.GrainOf[*crossedGrain](ctx, node3, "crossed-grain")
	if err != nil {
		fail("%v", err)
	}

	if _, err := node3.Spawn(ctx, "crossed-actor", new(crossedActor)); err != nil {
		fail("%v", err)
	}

	// Locally the asks work whatever the types are.
	if _, err := node3.AskGrain(ctx, identity, &request{Amount: 1}, time.Second); err != nil {
		fail("local ask: %v", err)
	}

	// From the other nodes each reply must decode as what the target sent.
	failures := 0

	for i, node := range []actor.ActorSystem{node1, node2} {
		// The node resolves the actor on node 3 through the cluster.
		var remoteActor *actor.PID
		for {
			remoteActor, err = node.ActorOf(ctx, "crossed-actor")
			if err == nil {
				break
			}

			pause.For(50 * time.Millisecond)
		}

		from := fmt.Sprintf("node %d", i+1)
		failures += check(from+", grain, CBOR request, proto reply", isProtoReply)(node.AskGrain(ctx, identity, &request{Amount: 1}, time.Second))
		failures += check(from+", grain, proto request, CBOR reply", isCBORReply)(node.AskGrain(ctx, identity, new(testpb.TestSend), time.Second))
		failures += check(from+", actor, CBOR request, proto reply", isProtoReply)(actor.Ask(ctx, remoteActor, &request{Amount: 1}, time.Second))
		failures += check(from+", actor, proto request, CBOR reply", isCBORReply)(actor.Ask(ctx, remoteActor, new(testpb.TestSend), time.Second))
	}

	_ = node3.Stop(ctx)
	_ = node2.Stop(ctx)
	_ = node1.Stop(ctx)
	srv.Shutdown()

	if failures > 0 {
		fmt.Printf("FAIL: %d of 8 replies of another serializer did not decode on the other nodes\n", failures)
		os.Exit(1)
	}

	fmt.Println("PASS: a reply of another serializer than the request decodes on the other nodes")
}

// request is a plain Go request, serialized with CBOR.
type request struct {
	Amount int
}

// receipt is a plain Go reply, serialized with CBOR.
type receipt struct {
	Amount int
}

// crossedReply returns the reply of the other serializer than the one message
// belongs to: a proto reply to a CBOR request and a CBOR reply to a proto
// request. It returns nil for a message it does not know.
func crossedReply(message any) any {
	switch message.(type) {
	case *request:
		return new(testpb.Reply)
	case *testpb.TestSend:
		return &receipt{Amount: 1}
	default:
		return nil
	}
}

// crossedGrain answers every request with a reply of the other serializer.
type crossedGrain types.Unit

// OnActivate does nothing.
func (x *crossedGrain) OnActivate(context.Context, *actor.GrainProps) error { return nil }

// OnReceive answers the request with a reply of the other serializer.
func (x *crossedGrain) OnReceive(ctx *actor.GrainContext) {
	reply := crossedReply(ctx.Message())
	if reply == nil {
		ctx.Unhandled()
		return
	}

	ctx.Response(reply)
}

// OnDeactivate does nothing.
func (x *crossedGrain) OnDeactivate(context.Context, *actor.GrainProps) error { return nil }

// crossedActor answers every request with a reply of the other serializer.
type crossedActor types.Unit

// PreStart does nothing.
func (x *crossedActor) PreStart(*actor.Context) error { return nil }

// Receive answers the request with a reply of the other serializer.
func (x *crossedActor) Receive(ctx *actor.ReceiveContext) {
	reply := crossedReply(ctx.Message())
	if reply == nil {
		ctx.Unhandled()
		return
	}

	ctx.Response(reply)
}

// PostStop does nothing.
func (x *crossedActor) PostStop(*actor.Context) error { return nil }

// isProtoReply reports whether reply is the proto reply the targets send.
func isProtoReply(reply any) bool {
	_, ok := reply.(*testpb.Reply)
	return ok
}

// isCBORReply reports whether reply is the CBOR reply the targets send.
func isCBORReply(reply any) bool {
	_, ok := reply.(*receipt)
	return ok
}

// check returns a function that takes the outcome of one ask, prints whether
// the reply is the expected one and returns 1 when it is not, 0 otherwise.
func check(name string, expected func(any) bool) func(any, error) int {
	return func(reply any, err error) int {
		if err != nil {
			fmt.Printf("BUG: %s: %v\n", name, err)
			return 1
		}

		if !expected(reply) {
			fmt.Printf("BUG: %s: answered %T\n", name, reply)
			return 1
		}

		fmt.Printf("OK: %s\n", name)
		return 0
	}
}

// startNode starts a cluster node that discovers its peers through the NATS server at natsAddress.
func startNode(ctx context.Context, natsAddress string, discoveryPort, peersPort, remotingPort int) actor.ActorSystem {
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
		WithKinds(new(crossedActor)).
		WithGrains(new(crossedGrain))

	actorSystem, err := actor.NewActorSystem(
		"issue1411",
		actor.WithRemote(remote.NewConfig("localhost", remotingPort, remote.WithSerializables(new(request), new(receipt)))),
		actor.WithLogger(log.DiscardLogger),
		actor.WithCluster(clusterConfig),
	)
	if err != nil {
		fail("%v", err)
	}

	if err := actorSystem.Start(ctx); err != nil {
		fail("%v", err)
	}

	return actorSystem
}

// newNatsServer starts an in-process NATS server on a free port for the nodes' discovery.
func newNatsServer() *natsserver.Server {
	serv, err := natsserver.NewServer(&natsserver.Options{Host: "localhost", Port: -1})
	if err != nil {
		fail("creating NATS server: %v", err)
	}

	go serv.Start()
	if !serv.ReadyForConnections(2 * time.Second) {
		fail("NATS server is not ready for connections")
	}

	return serv
}

// fail prints a setup error and exits with status 2.
func fail(format string, args ...any) {
	fmt.Printf("setup failed: "+format+"\n", args...)
	os.Exit(2)
}
