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

// Sample for https://github.com/Tochemey/goakt/issues/1408
//
// A node queries its discovery provider when it boots. With the default
// minimum peers quorum of 1 it does not query it again: a list without any
// other node means "I am alone, form a cluster". Whether nodes that boot at
// the same moment end up in one cluster therefore depends on the provider,
// which has to follow two rules:
//
//  1. A node is visible to the others before it reads: Register adds the
//     node to what DiscoverPeers returns on the other nodes.
//  2. Reads are consistent: DiscoverPeers returns every node registered
//     before the call.
//
// When both hold, two nodes cannot both miss each other: if node A reads
// before node B is visible, node B reads after node A became visible and
// joins it. A provider whose view may be incomplete has to return an error
// instead of a list without any other node. The cluster engine retries the
// join on an error, once a second for ten seconds, and only then lets the
// node form a cluster alone.
//
// Every scenario boots three nodes at the same moment with a quorum of 1 and
// then hits one counter grain through each node. One cluster answers 1, 2, 3.
// Three clusters of one answer 1, 1, 1, since each has its own activation.
//
//	scenario 1  the built-in NATS provider: one cluster
//	scenario 2  a custom provider that follows both rules: one cluster
//	scenario 3  a custom provider that lists a node only once it is ready,
//	            which breaks rule 1: three clusters
//	scenario 4  a custom provider whose view lags behind the registrations,
//	            which breaks rule 2: three clusters
//	scenario 5  the provider of scenario 4 wrapped in requirePeers, which
//	            fails the query while no other node is listed: one cluster
//	scenario 6  the provider of scenario 3 wrapped in requirePeers: still
//	            three clusters, each after ten seconds of retries, because no
//	            node becomes ready before it has booted
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery"
	"github.com/tochemey/goakt/v4/discovery/nats"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
)

const (
	// nodeCount is the number of nodes every scenario boots.
	nodeCount = 3
	// host is the address every node binds to.
	host = "127.0.0.1"
	// settleTime is how long the nodes get to find each other after they booted.
	settleTime = 10 * time.Second
	// viewLag is how long the lagging registry hides a registration from its readers.
	viewLag = 2 * time.Second
	// grainName is the identity of the counter grain hit through every node.
	grainName = "user-1"
)

// visibility says which registered nodes a registry lists.
type visibility int

const (
	// listRegistered lists every registered node. It follows both rules.
	listRegistered visibility = iota
	// listReady lists only the nodes marked ready. It breaks rule 1.
	listReady
	// listLagging lists the nodes registered at least viewLag ago. It breaks rule 2.
	listLagging
)

// errNoPeerListed is returned by requirePeers so the cluster engine retries the join.
var errNoPeerListed = errors.New("no peer is listed yet")

// scenario is one way for three nodes to discover each other and the number of clusters it must end with.
type scenario struct {
	title string
	// clusters is the expected number of clusters: 1, or nodeCount when every node stays alone.
	clusters int
	// newProvider builds the discovery provider of the node at address.
	newProvider func(address string, discoveryPort int) discovery.Provider
	// markReady tells the scenario's registry that the node at address has booted. It is nil when the scenario has no registry.
	markReady func(address string)
}

func main() {
	ctx := context.Background()
	server := newNatsServer()

	registered := newRegistry(listRegistered)
	ready := newRegistry(listReady)
	lagging := newRegistry(listLagging)
	guardedLagging := newRegistry(listLagging)
	guardedReady := newRegistry(listReady)

	scenarios := []scenario{
		{
			title:    "the built-in NATS provider",
			clusters: 1,
			newProvider: func(_ string, discoveryPort int) discovery.Provider {
				return nats.NewDiscovery(&nats.Config{NatsServer: "nats://" + server.Addr().String(), NatsSubject: "issue-1408", Host: host, DiscoveryPort: discoveryPort})
			},
		},
		{
			title:       "a custom provider that follows both rules",
			clusters:    1,
			newProvider: func(address string, _ int) discovery.Provider { return &provider{registry: registered, self: address} },
			markReady:   registered.markReady,
		},
		{
			title:       "a custom provider that lists a node only once it is ready",
			clusters:    nodeCount,
			newProvider: func(address string, _ int) discovery.Provider { return &provider{registry: ready, self: address} },
			markReady:   ready.markReady,
		},
		{
			title:       "a custom provider whose view lags behind the registrations",
			clusters:    nodeCount,
			newProvider: func(address string, _ int) discovery.Provider { return &provider{registry: lagging, self: address} },
			markReady:   lagging.markReady,
		},
		{
			title:    "the lagging provider wrapped in requirePeers",
			clusters: 1,
			newProvider: func(address string, _ int) discovery.Provider {
				return &requirePeers{Provider: &provider{registry: guardedLagging, self: address}, self: address}
			},
			markReady: guardedLagging.markReady,
		},
		{
			title:    "the ready-only provider wrapped in requirePeers",
			clusters: nodeCount,
			newProvider: func(address string, _ int) discovery.Provider {
				return &requirePeers{Provider: &provider{registry: guardedReady, self: address}, self: address}
			},
			markReady: guardedReady.markReady,
		},
	}

	unexpected := 0

	for i, item := range scenarios {
		fmt.Printf("scenario %d: %s\n", i+1, item.title)

		if !runScenario(ctx, item, 9600+i*10) {
			unexpected++
		}

		fmt.Println()
	}

	server.Shutdown()

	if unexpected > 0 {
		fmt.Printf("FAIL: %d of %d scenarios did not end as expected\n", unexpected, len(scenarios))
		os.Exit(1)
	}

	fmt.Println("PASS: nodes that boot together form one cluster when the provider follows both rules or fails while it sees no peer")
}

// runScenario boots three nodes at the same moment, lets them settle and reports whether they formed the expected number of clusters.
func runScenario(ctx context.Context, item scenario, basePort int) bool {
	nodes := make([]actor.ActorSystem, nodeCount)
	started := time.Now()

	var wg sync.WaitGroup

	for i := range nodeCount {
		wg.Add(1)

		go func() {
			discoveryPort := basePort + i*3
			address := fmt.Sprintf("%s:%d", host, discoveryPort)
			nodes[i] = startNode(ctx, item.newProvider(address, discoveryPort), discoveryPort)

			if item.markReady != nil {
				item.markReady(address)
			}

			wg.Done()
		}()
	}

	wg.Wait()
	fmt.Printf("  the nodes booted in %s\n", time.Since(started).Round(100*time.Millisecond))

	// A split never heals, so the full settle time is spent before it is reported.
	deadline := time.Now().Add(settleTime)
	for !peersEach(ctx, nodes, nodeCount-1) && time.Now().Before(deadline) {
		pause.For(100 * time.Millisecond)
	}

	clusters := 0

	switch {
	case peersEach(ctx, nodes, nodeCount-1):
		clusters = 1
	case peersEach(ctx, nodes, 0):
		clusters = nodeCount
	}

	counts := hit(ctx, nodes)
	fmt.Printf("  clusters=%d, %s answered %v through the three nodes\n", clusters, grainName, counts)

	for _, node := range nodes {
		_ = node.Stop(ctx)
	}

	activations := 1
	if counts[0] == 1 && counts[1] == 1 && counts[2] == 1 {
		activations = nodeCount
	}

	if clusters != item.clusters || activations != item.clusters {
		fmt.Printf("  UNEXPECTED: wanted %d cluster(s)\n", item.clusters)
		return false
	}

	fmt.Printf("  OK: %d cluster(s), %d activation(s) of the grain, as expected\n", clusters, activations)
	return true
}

// peersEach reports whether every node sees exactly count peers.
func peersEach(ctx context.Context, nodes []actor.ActorSystem, count int) bool {
	for _, node := range nodes {
		peers, err := node.Peers(ctx, time.Second)
		if err != nil || len(peers) != count {
			return false
		}
	}

	return true
}

// hit sends one hit to the counter grain through every node and returns the counts it answered.
func hit(ctx context.Context, nodes []actor.ActorSystem) []int {
	counts := make([]int, 0, len(nodes))

	for _, node := range nodes {
		identity, err := actor.GrainOf[*counterGrain](ctx, node, grainName)
		if err != nil {
			fail("%v", err)
		}

		reply, err := node.AskGrain(ctx, identity, new(hitRequest), 5*time.Second)
		if err != nil {
			fail("%v", err)
		}

		counts = append(counts, reply.(*hitReply).Count)
	}

	return counts
}

// hitRequest asks the counter grain to count one hit.
type hitRequest struct {
	// Amount is unused. It gives the message a field to serialize.
	Amount int
}

// hitReply carries the counter grain's count after a hit.
type hitReply struct {
	Count int
}

// counterGrain counts the hits it receives.
type counterGrain struct {
	// count is the number of hits this activation has received.
	count int
}

// OnActivate does nothing.
func (x *counterGrain) OnActivate(context.Context, *actor.GrainProps) error { return nil }

// OnReceive counts a hit and answers with the count.
func (x *counterGrain) OnReceive(ctx *actor.GrainContext) {
	if _, ok := ctx.Message().(*hitRequest); !ok {
		ctx.Unhandled()
		return
	}

	x.count++
	ctx.Response(&hitReply{Count: x.count})
}

// OnDeactivate does nothing.
func (x *counterGrain) OnDeactivate(context.Context, *actor.GrainProps) error { return nil }

// registry is the directory the custom providers of one scenario register in and read from.
type registry struct {
	mu sync.Mutex
	// visibility says which registered nodes the registry lists.
	visibility visibility
	// registered holds the registration time of every node by discovery address.
	registered map[string]time.Time
	// ready holds the discovery addresses of the nodes that have booted.
	ready map[string]bool
}

// newRegistry creates an empty registry that lists its nodes as visibility says.
func newRegistry(visibility visibility) *registry {
	return &registry{visibility: visibility, registered: make(map[string]time.Time), ready: make(map[string]bool)}
}

// register adds the node at address to the registry.
func (x *registry) register(address string) {
	x.mu.Lock()
	x.registered[address] = time.Now()
	x.mu.Unlock()
}

// deregister removes the node at address from the registry.
func (x *registry) deregister(address string) {
	x.mu.Lock()
	delete(x.registered, address)
	delete(x.ready, address)
	x.mu.Unlock()
}

// markReady records that the node at address has booted, the way a readiness probe would.
func (x *registry) markReady(address string) {
	x.mu.Lock()
	x.ready[address] = true
	x.mu.Unlock()
}

// list returns the discovery addresses the registry lists at this moment.
func (x *registry) list() []string {
	x.mu.Lock()
	addresses := make([]string, 0, len(x.registered))

	for address, registeredAt := range x.registered {
		switch x.visibility {
		case listReady:
			if !x.ready[address] {
				continue
			}
		case listLagging:
			if time.Since(registeredAt) < viewLag {
				continue
			}
		default:
		}

		addresses = append(addresses, address)
	}

	x.mu.Unlock()
	return addresses
}

// provider is a custom discovery provider backed by a registry.
type provider struct {
	registry *registry
	// self is the discovery address of the node that owns this provider.
	self string
}

// ID returns the provider name.
func (x *provider) ID() string { return "issue1408" }

// Initialize does nothing.
func (x *provider) Initialize() error { return nil }

// Register adds this node to the registry. The cluster engine calls it before DiscoverPeers.
func (x *provider) Register() error {
	x.registry.register(x.self)
	return nil
}

// Deregister removes this node from the registry.
func (x *provider) Deregister() error {
	x.registry.deregister(x.self)
	return nil
}

// DiscoverPeers returns the nodes the registry lists. The list may be empty, which the cluster engine reads as "this node is alone".
func (x *provider) DiscoverPeers() ([]string, error) { return x.registry.list(), nil }

// Close does nothing.
func (x *provider) Close() error { return nil }

// requirePeers wraps a discovery provider and fails the query while the
// provider lists no node other than this one, so the cluster engine retries
// the join during boot instead of forming a cluster of one.
type requirePeers struct {
	discovery.Provider
	// self is the discovery address of the node that owns this provider, as the wrapped provider lists it.
	self string
}

// DiscoverPeers returns the wrapped provider's peers, or errNoPeerListed when none of them is another node.
func (x *requirePeers) DiscoverPeers() ([]string, error) {
	peers, err := x.Provider.DiscoverPeers()
	if err != nil {
		return nil, err
	}

	for _, peer := range peers {
		if peer != x.self {
			return peers, nil
		}
	}

	return nil, errNoPeerListed
}

// startNode starts a cluster node with a quorum of 1 that discovers its peers through provider.
func startNode(ctx context.Context, provider discovery.Provider, discoveryPort int) actor.ActorSystem {
	clusterConfig := actor.
		NewClusterConfig().
		WithDiscovery(provider).
		WithDiscoveryPort(discoveryPort).
		WithPeersPort(discoveryPort + 1).
		WithMinimumPeersQuorum(1).
		WithGrains(new(counterGrain))

	actorSystem, err := actor.NewActorSystem(
		"issue1408",
		actor.WithRemote(remote.NewConfig(host, discoveryPort+2, remote.WithSerializables(new(hitRequest), new(hitReply)))),
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

// newNatsServer starts an in-process NATS server on a free port for the discovery of scenario 1.
func newNatsServer() *natsserver.Server {
	serv, err := natsserver.NewServer(&natsserver.Options{Host: host, Port: -1})
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
