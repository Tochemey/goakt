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

// Reproduction for https://github.com/Tochemey/goakt/issues/1437
//
// A deleted key is protected by a tombstone for the tombstone TTL. A node that
// was away for longer than that and still holds the key brings it back when
// it returns: no node retains the tombstone any more, so anti-entropy treats
// the returning node's copy as a key the others are missing.
//
// This sample runs a three-node cluster with a tombstone TTL of 3 seconds.
// Node 3 keeps its store on disk through CRDT snapshots, so it comes back
// with the keys it held when it left. Two keys are on every node when node 3
// leaves:
//
//   - beyond: deleted on node 1 right after node 3 left. Node 3 returns after
//     its tombstone expired. It must stay deleted on every node.
//   - within: deleted on node 1 just before node 3 returns, while its
//     tombstone is live. It must stay deleted on every node. This is the
//     control: anti-entropy carries a live tombstone to the returning node.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/crdt"
	"github.com/tochemey/goakt/v4/discovery/nats"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
)

const (
	// tombstoneTTL is how long the nodes retain the tombstone of a deleted key.
	tombstoneTTL = 3 * time.Second
	// tick is the anti-entropy, prune and snapshot interval of every node.
	tick = 250 * time.Millisecond
	// settle is how long the sample lets anti-entropy run after node 3
	// returns before it reads the keys: many rounds between every pair.
	settle = 5 * time.Second
)

var (
	// beyondKey is deleted longer than the tombstone TTL before node 3 returns.
	beyondKey = crdt.GCounterKey("beyond")
	// withinKey is deleted within the tombstone TTL before node 3 returns.
	withinKey = crdt.GCounterKey("within")
)

func main() {
	ctx := context.Background()
	srv := newNatsServer()

	snapshotDir, err := os.MkdirTemp("", "issue-1437-*")
	if err != nil {
		fail("%v", err)
	}

	node1 := startNode(ctx, srv.Addr().String(), 9711, 9712, 9713, "")
	node2 := startNode(ctx, srv.Addr().String(), 9714, 9715, 9716, "")
	node3 := startNode(ctx, srv.Addr().String(), 9717, 9718, 9719, snapshotDir)
	waitForPeers(ctx, node1, 2)

	// Both keys are written on node 1 and reach every node.
	increment(ctx, node1, beyondKey)
	increment(ctx, node1, withinKey)

	for _, node := range []actor.ActorSystem{node1, node2, node3} {
		waitFor(ctx, node, beyondKey, true)
		waitFor(ctx, node, withinKey, true)
	}

	// Node 3 leaves. Its final snapshot holds both keys.
	if err := node3.Stop(ctx); err != nil {
		fail("stopping node 3: %v", err)
	}

	waitForPeers(ctx, node1, 1)

	// beyond is deleted now; its tombstone expires while node 3 is away.
	remove(ctx, node1, beyondKey)
	waitFor(ctx, node2, beyondKey, false)
	pause.For(tombstoneTTL + 4*tick)

	// within is deleted just before node 3 returns; its tombstone is live.
	remove(ctx, node1, withinKey)
	waitFor(ctx, node2, withinKey, false)

	// Node 3 returns with the store it had when it left.
	node3 = startNode(ctx, srv.Addr().String(), 9717, 9718, 9719, snapshotDir)
	waitForPeers(ctx, node1, 2)
	pause.For(settle)

	failures := 0
	nodes := []actor.ActorSystem{node1, node2, node3}

	for i, node := range nodes {
		name := fmt.Sprintf("node %d", i+1)
		failures += check(ctx, node, name, beyondKey, "deleted longer than the tombstone TTL before node 3 returned")
		failures += check(ctx, node, name, withinKey, "deleted within the tombstone TTL before node 3 returned")
	}

	_ = node3.Stop(ctx)
	_ = node2.Stop(ctx)
	_ = node1.Stop(ctx)
	srv.Shutdown()
	_ = os.RemoveAll(snapshotDir)

	if failures > 0 {
		fmt.Printf("FAIL: %d of 6 checks show a deleted key back after node 3 returned\n", failures)
		os.Exit(1)
	}

	fmt.Println("PASS: a deleted key stays deleted when a node returns, whether its tombstone has expired or not")
}

// idleActor is the actor kind a cluster node requires. The sample does not spawn it.
type idleActor struct{}

// PreStart does nothing.
func (x *idleActor) PreStart(*actor.Context) error { return nil }

// Receive does nothing.
func (x *idleActor) Receive(*actor.ReceiveContext) {}

// PostStop does nothing.
func (x *idleActor) PostStop(*actor.Context) error { return nil }

// check reads key on node, prints whether it stayed deleted and returns 1
// when it came back, 0 otherwise.
func check(ctx context.Context, node actor.ActorSystem, name string, key crdt.Key, deletion string) int {
	value, found := read(ctx, node, key)
	if found {
		fmt.Printf("BUG: %s, %s (%s): back with value %d\n", name, key.ID(), deletion, value)
		return 1
	}

	fmt.Printf("OK: %s, %s (%s): stays deleted\n", name, key.ID(), deletion)
	return 0
}

// increment adds one to the counter at key on node.
func increment(ctx context.Context, node actor.ActorSystem, key crdt.Key) {
	nodeID := node.PeersAddress()
	update := &crdt.Update{
		Key:     key,
		Initial: crdt.NewGCounter(),
		Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
			return current.(*crdt.GCounter).Increment(nodeID, 1)
		},
	}

	if _, err := actor.Ask(ctx, node.Replicator(), update, time.Second); err != nil {
		fail("updating %s: %v", key.ID(), err)
	}
}

// remove deletes key on node.
func remove(ctx context.Context, node actor.ActorSystem, key crdt.Key) {
	if _, err := actor.Ask(ctx, node.Replicator(), &crdt.Delete{Key: key}, time.Second); err != nil {
		fail("deleting %s: %v", key.ID(), err)
	}
}

// read returns the value of the counter at key on node and whether node holds it.
func read(ctx context.Context, node actor.ActorSystem, key crdt.Key) (uint64, bool) {
	reply, err := actor.Ask(ctx, node.Replicator(), &crdt.Get{Key: key}, time.Second)
	if err != nil {
		fail("reading %s: %v", key.ID(), err)
	}

	resp, ok := reply.(*crdt.GetResponse)
	if !ok || resp.Data == nil {
		return 0, false
	}

	return resp.Data.(*crdt.GCounter).Value(), true
}

// waitFor waits until node holds key when present is true, or no longer holds it otherwise.
func waitFor(ctx context.Context, node actor.ActorSystem, key crdt.Key, present bool) {
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, found := read(ctx, node, key); found == present {
			return
		}

		if time.Now().After(deadline) {
			fail("%s on %s: present=%t not reached", key.ID(), node.PeersAddress(), present)
		}

		pause.For(50 * time.Millisecond)
	}
}

// waitForPeers waits until node sees exactly count peers.
func waitForPeers(ctx context.Context, node actor.ActorSystem, count int) {
	deadline := time.Now().Add(30 * time.Second)
	for {
		peers, err := node.Peers(ctx, time.Second)
		if err == nil && len(peers) == count {
			return
		}

		if time.Now().After(deadline) {
			fail("%s does not see %d peers", node.PeersAddress(), count)
		}

		pause.For(50 * time.Millisecond)
	}
}

// startNode starts a cluster node with distributed data that discovers its
// peers through the NATS server at natsAddress. A non-empty snapshotDir keeps
// the node's store on disk there, and a node started on it again restores it.
func startNode(ctx context.Context, natsAddress string, discoveryPort, peersPort, remotingPort int, snapshotDir string) actor.ActorSystem {
	discovery := nats.NewDiscovery(&nats.Config{
		NatsServer:    "nats://" + natsAddress,
		NatsSubject:   "issue-1437",
		Host:          "localhost",
		DiscoveryPort: discoveryPort,
	}, nats.WithLogger(log.DiscardLogger))

	options := []crdt.Option{
		crdt.WithAntiEntropyInterval(tick),
		crdt.WithPruneInterval(tick),
		crdt.WithTombstoneTTL(tombstoneTTL),
	}

	if snapshotDir != "" {
		options = append(options, crdt.WithSnapshotInterval(tick), crdt.WithSnapshotDir(snapshotDir))
	}

	clusterConfig := actor.
		NewClusterConfig().
		WithDiscovery(discovery).
		WithDiscoveryPort(discoveryPort).
		WithPeersPort(peersPort).
		WithMinimumPeersQuorum(1).
		WithKinds(new(idleActor)).
		WithCRDT(options...)

	actorSystem, err := actor.NewActorSystem(
		"issue1437",
		actor.WithRemote(remote.NewConfig("localhost", remotingPort)),
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
