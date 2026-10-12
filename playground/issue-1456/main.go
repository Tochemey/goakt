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

// Reproduction for https://github.com/Tochemey/goakt/issues/1456
//
// ActorSystem.Partition returned 0 for every failure: when the system was not
// clustered, when it was stopped, and when the name had no registry record.
// Partition 0 is a valid partition, so a caller could not tell a failure from
// an actor whose registry record lives in partition 0.
//
// This sample runs a three-node cluster. Node 1 spawns actors until the
// registry places one of them in partition 0, and every node asks for the
// partition of that actor and of a name that was never spawned. It also asks
// a standalone actor system and a stopped node.
//
// With the fix Partition returns the partition the registry reports for the
// actor's record, and an error when there is none: ErrActorNotFound for a name
// without a record, ErrClusterDisabled for a standalone system and
// ErrActorSystemNotStarted for a stopped one.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery/nats"
	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
)

// partitionCount is the cluster registry partition count of every node. It is
// small so that one of the first spawned actors lands in partition 0.
const partitionCount = 7

// missingName is a name that is never spawned.
const missingName = "never-spawned"

func main() {
	ctx := context.Background()
	failures := 0

	// A standalone actor system has no registry partitions.
	standalone, err := actor.NewActorSystem("issue1456standalone", actor.WithLogger(log.DiscardLogger))
	if err != nil {
		fail("%v", err)
	}

	if err := standalone.Start(ctx); err != nil {
		fail("%v", err)
	}

	failures += checkError("standalone system", gerrors.ErrClusterDisabled)(standalone.Partition(ctx, "standalone-actor"))
	_ = standalone.Stop(ctx)

	srv := newNatsServer()
	node1 := startNode(ctx, srv.Addr().String(), 9611, 9612, 9613)
	node2 := startNode(ctx, srv.Addr().String(), 9614, 9615, 9616)
	node3 := startNode(ctx, srv.Addr().String(), 9617, 9618, 9619)

	for {
		peers, err := node1.Peers(ctx, time.Second)
		if err == nil && len(peers) == 2 {
			break
		}

		pause.For(50 * time.Millisecond)
	}

	// Node 1 spawns actors until the registry places one in partition 0.
	spawnedName := ""
	for i := 0; i < 10*partitionCount && spawnedName == ""; i++ {
		name := fmt.Sprintf("actor-%d", i)
		if _, err := node1.Spawn(ctx, name, new(idleActor)); err != nil {
			fail("%v", err)
		}

		if partition, err := node1.Partition(ctx, name); err == nil && partition == 0 {
			spawnedName = name
		}
	}

	if spawnedName == "" {
		fail("no spawned actor was reported in partition 0")
	}

	for i, node := range []actor.ActorSystem{node1, node2, node3} {
		from := fmt.Sprintf("node %d", i+1)
		failures += checkPartition(from+", spawned "+spawnedName, 0)(node.Partition(ctx, spawnedName))
		failures += checkError(from+", never spawned "+missingName, gerrors.ErrActorNotFound)(node.Partition(ctx, missingName))
	}

	// A stopped node has no registry partitions either.
	_ = node3.Stop(ctx)
	failures += checkError("stopped node", gerrors.ErrActorSystemNotStarted)(node3.Partition(ctx, spawnedName))

	_ = node2.Stop(ctx)
	_ = node1.Stop(ctx)
	srv.Shutdown()

	if failures > 0 {
		fmt.Printf("FAIL: %d partition lookups did not return the registry partition or an error\n", failures)
		os.Exit(1)
	}

	fmt.Println("PASS: Partition returns the registry partition on every node, and an error when there is none")
}

// idleActor accepts every message and does nothing with it.
type idleActor struct{}

// PreStart does nothing.
func (x *idleActor) PreStart(*actor.Context) error { return nil }

// Receive does nothing.
func (x *idleActor) Receive(*actor.ReceiveContext) {}

// PostStop does nothing.
func (x *idleActor) PostStop(*actor.Context) error { return nil }

// checkPartition returns a function that takes the outcome of one partition
// lookup, prints whether it returned the expected partition without error and
// returns 1 when it did not, 0 otherwise.
func checkPartition(name string, expected uint64) func(uint64, error) int {
	return func(partition uint64, err error) int {
		if err != nil {
			fmt.Printf("BUG: %s: %v\n", name, err)
			return 1
		}

		if partition != expected {
			fmt.Printf("BUG: %s: Partition returned %d, the registry holds the record in partition %d\n", name, partition, expected)
			return 1
		}

		fmt.Printf("OK: %s: partition %d\n", name, partition)
		return 0
	}
}

// checkError returns a function that takes the outcome of one partition
// lookup, prints whether it failed with the expected error and returns 1 when
// it did not, 0 otherwise.
func checkError(name string, expected error) func(uint64, error) int {
	return func(partition uint64, err error) int {
		if !errors.Is(err, expected) {
			fmt.Printf("BUG: %s: Partition returned %d and error %v, expected %v\n", name, partition, err, expected)
			return 1
		}

		fmt.Printf("OK: %s: %v\n", name, err)
		return 0
	}
}

// startNode starts a cluster node that discovers its peers through the NATS server at natsAddress.
func startNode(ctx context.Context, natsAddress string, discoveryPort, peersPort, remotingPort int) actor.ActorSystem {
	discovery := nats.NewDiscovery(&nats.Config{
		NatsServer:    "nats://" + natsAddress,
		NatsSubject:   "issue-1456",
		Host:          "localhost",
		DiscoveryPort: discoveryPort,
	})

	clusterConfig := actor.
		NewClusterConfig().
		WithDiscovery(discovery).
		WithDiscoveryPort(discoveryPort).
		WithPeersPort(peersPort).
		WithMinimumPeersQuorum(1).
		WithPartitionCount(partitionCount).
		WithKinds(new(idleActor))

	actorSystem, err := actor.NewActorSystem(
		"issue1456",
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
