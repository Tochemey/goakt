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

// Reproduction for https://github.com/Tochemey/goakt/issues/1227
//
// Cross-node grain activation always failed with "must contain only word
// characters (i.e. [a-zA-Z0-9] plus non-leading '-' or '_')", even for
// perfectly valid grain names. sendRemoteActivateGrain populated the wire
// request's Name field with GrainId.Value (the full "kind/name" identity)
// instead of the bare name. The remote client then rebuilt the identity as
// Kind + "/" + Name, producing a double-prefixed value ("kind/kind/name")
// that the receiving node rejected during identity validation.
//
// This sample follows the issue's "How to reproduce" steps: it starts a
// two-node cluster using the NATS discovery provider with a grain kind
// registered via ClusterConfig.WithGrains, then calls GrainIdentity on node A
// with WithActivationStrategy(RoundRobinActivation) for several distinct
// names. Round-robin placement across the two members sends a portion of the
// activations to node B through sendRemoteActivateGrain.
//
// Before the fix every activation placed on node B failed with the validation
// error above. With the fix all activations succeed, and each grain records
// the node it was activated on so the sample can assert that at least one
// activation genuinely happened on the remote node with a well-formed
// "kind/name" identity.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery/nats"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
)

const grainsCount = 6

// activations records, per grain name, the "host:port" of the node that ran
// OnActivate. Both nodes live in this process, so a package-level map is
// enough to tell which node each grain landed on.
var activations sync.Map

// demoGrain records its activation node so the sample can prove that some
// activations were placed on the remote peer.
type demoGrain struct{}

func (g *demoGrain) OnActivate(_ context.Context, props *actor.GrainProps) error {
	system := props.ActorSystem()
	activations.Store(props.Identity().Name(), fmt.Sprintf("%s:%d", system.Host(), system.Port()))
	return nil
}

func (g *demoGrain) OnReceive(ctx *actor.GrainContext) {
	ctx.Unhandled()
}

func (g *demoGrain) OnDeactivate(context.Context, *actor.GrainProps) error {
	return nil
}

func startNode(ctx context.Context, natsAddress string, discoveryPort, peersPort, remotingPort int) (actor.ActorSystem, error) {
	discovery := nats.NewDiscovery(&nats.Config{
		NatsServer:    "nats://" + natsAddress,
		NatsSubject:   "issue-1227",
		Host:          "localhost",
		DiscoveryPort: discoveryPort,
	})

	clusterConfig := actor.
		NewClusterConfig().
		WithDiscovery(discovery).
		WithDiscoveryPort(discoveryPort).
		WithPeersPort(peersPort).
		WithMinimumPeersQuorum(1).
		WithReplicaCount(1).
		WithGrains(new(demoGrain))

	actorSystem, err := actor.NewActorSystem(
		"issue1227",
		actor.WithRemote(remote.NewConfig("localhost", remotingPort)),
		actor.WithoutRelocation(),
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
	serv, err := natsserver.NewServer(&natsserver.Options{
		Host: "localhost",
		Port: -1,
	})
	if err != nil {
		fmt.Printf("Error creating NATS server: %v\n", err)
		os.Exit(1)
	}

	ready := make(chan bool)
	go func() {
		ready <- true
		serv.Start()
	}()
	<-ready

	if !serv.ReadyForConnections(2 * time.Second) {
		fmt.Println("NATS server is not ready for connections")
		os.Exit(1)
	}
	return serv
}

func main() {
	server := newNatsServer()
	defer server.Shutdown()

	ctx := context.Background()

	nodeA, err := startNode(ctx, server.Addr().String(), 9151, 9152, 9153)
	if err != nil {
		fmt.Printf("FAIL: error starting node A: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = nodeA.Stop(ctx) }()

	nodeB, err := startNode(ctx, server.Addr().String(), 9161, 9162, 9163)
	if err != nil {
		fmt.Printf("FAIL: error starting node B: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = nodeB.Stop(ctx) }()

	// Give the two nodes a moment to discover each other and settle.
	pause.For(2 * time.Second)

	// Activate grains from node A with round-robin placement. With two
	// members, placement alternates between the nodes, so a portion of these
	// activations goes to node B via sendRemoteActivateGrain. Before the fix
	// each of those failed with the "must contain only word characters" error.
	for i := range grainsCount {
		name := fmt.Sprintf("my-grain-%d", i)
		identity, err := actor.GrainOf[*demoGrain](ctx, nodeA, name, actor.WithActivationStrategy(actor.RoundRobinActivation))
		if err != nil {
			fmt.Printf("FAIL: activation of %s failed (issue #1227): %v\n", name, err)
			os.Exit(1)
		}

		// A double-prefixed identity would carry two separators (kind/kind/name).
		if strings.Count(identity.String(), "/") != 1 || identity.Name() != name {
			fmt.Printf("FAIL: malformed identity %s for grain %s\n", identity.String(), name)
			os.Exit(1)
		}
	}

	fmt.Printf("PASS: all %d grains activated with well-formed identities\n", grainsCount)

	// Prove that cross-node activation actually happened: at least one grain
	// must have run OnActivate on node B.
	nodeBAddress := fmt.Sprintf("%s:%d", nodeB.Host(), nodeB.Port())
	remoteActivations := 0

	for i := range grainsCount {
		name := fmt.Sprintf("my-grain-%d", i)
		node, ok := activations.Load(name)
		if !ok {
			fmt.Printf("FAIL: grain %s was never activated\n", name)
			os.Exit(1)
		}

		fmt.Printf("grain %s activated on node %s\n", name, node)
		if node == nodeBAddress {
			remoteActivations++
		}
	}

	if remoteActivations == 0 {
		fmt.Println("FAIL: no grain was placed on the remote node; the cross-node path was never exercised")
		os.Exit(1)
	}

	fmt.Printf("PASS: %d/%d grains activated remotely on node B\n", remoteActivations, grainsCount)
	fmt.Println("issue #1227 reproduced and fixed: cross-node grain activation succeeds")
}
