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

// Reproduction for https://github.com/Tochemey/goakt/issues/1396
//
// A node that was shutting down kept accepting grain activations until the
// end of its shutdown, so grains got claimed on it after it had started to
// leave. The registry then kept an owner record pointing at the departed
// node, and a node that still held a connection to it was answered "remoting
// is not enabled" over that connection, which did not count as a departed
// owner: the record was never released and the grain stayed unreachable.
//
// This sample runs two nodes. Node 1 keeps activating grains round-robin,
// so some activations are sent to node 2, while node 2 stops. Once node 2
// is gone, node 1 sends to every grain it activated: each must answer.
//
// With the fix a stopping node refuses activations, and a node that answers
// that it is shutting down or has remoting off is treated as gone, so its
// record is released and the grain activates on a live node.
package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery/nats"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

const grains = 200

type echoGrain struct{}

func (g *echoGrain) OnActivate(context.Context, *actor.GrainProps) error { return nil }

func (g *echoGrain) OnReceive(ctx *actor.GrainContext) {
	ctx.Response(new(testpb.Reply))
}

func (g *echoGrain) OnDeactivate(context.Context, *actor.GrainProps) error { return nil }

func startNode(ctx context.Context, natsAddress string, discoveryPort, peersPort, remotingPort int) (actor.ActorSystem, error) {
	discovery := nats.NewDiscovery(&nats.Config{
		NatsServer:    "nats://" + natsAddress,
		NatsSubject:   "issue-1396",
		Host:          "localhost",
		DiscoveryPort: discoveryPort,
	})

	clusterConfig := actor.
		NewClusterConfig().
		WithDiscovery(discovery).
		WithDiscoveryPort(discoveryPort).
		WithPeersPort(peersPort).
		WithMinimumPeersQuorum(1).
		WithGrains(new(echoGrain))

	actorSystem, err := actor.NewActorSystem(
		"issue1396",
		actor.WithRemote(remote.NewConfig("localhost", remotingPort)),
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

	node1, err := startNode(ctx, srv.Addr().String(), 9391, 9392, 9393)
	if err != nil {
		fmt.Printf("FAIL: starting node 1: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = node1.Stop(ctx) }()
	node2, err := startNode(ctx, srv.Addr().String(), 9394, 9395, 9396)
	if err != nil {
		fmt.Printf("FAIL: starting node 2: %v\n", err)
		os.Exit(1)
	}
	for {
		peers, err := node1.Peers(ctx, time.Second)
		if err == nil && len(peers) == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Node 1 activates grains round-robin, so every other activation is
	// sent to node 2, while node 2 stops.
	var identities []*actor.GrainIdentity
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Go(func() {
		time.Sleep(20 * time.Millisecond)
		_ = node2.Stop(ctx)
	})
	wg.Go(func() {
		for i := range grains {
			name := fmt.Sprintf("grain-%d", i)
			// An activation refused by the stopping node, or lost with it,
			// is sent again, as a caller would.
			for attempt := 0; attempt < 50; attempt++ {
				identity, err := actor.GrainOf[*echoGrain](ctx, node1, name, actor.WithActivationStrategy(actor.RoundRobinActivation))
				if err == nil {
					mu.Lock()
					identities = append(identities, identity)
					mu.Unlock()
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
		}
	})
	wg.Wait()

	// Node 2 is gone. Every grain node 1 activated must answer, from
	// wherever it lives now.
	unreachable := 0
	for _, identity := range identities {
		if _, err := node1.AskGrain(ctx, identity, new(testpb.TestReply), 2*time.Second); err != nil {
			unreachable++
			fmt.Printf("FAIL: %s: %v\n", identity.Name(), err)
		}
	}
	if unreachable > 0 {
		fmt.Printf("FAIL: %d of %d grains unreachable after node 2 left\n", unreachable, len(identities))
		os.Exit(1)
	}
	fmt.Printf("PASS: all %d grains answered after node 2 left\n", len(identities))
}
