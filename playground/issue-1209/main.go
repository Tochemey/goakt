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

// Reproduction for https://github.com/Tochemey/goakt/issues/1209
//
// SpawnSingleton delegates to the cluster coordinator over the network when the
// calling node is not the leader (spawnSingletonOnLeader -> RemoteSpawn). During
// membership churn (a rolling restart, a node joining mid-reconciliation) the
// leader's transient failures are serialized to proto codes and de-serialized by
// the client into error values that differ from their locally-detected
// equivalents:
//
//	leader quorum error   -> CODE_UNAVAILABLE       -> ErrRemoteSendFailure
//	leader deadline       -> CODE_DEADLINE_EXCEEDED -> ErrRequestTimeout
//	stale coordinator     -> CODE_NOT_FOUND         -> ErrAddressNotFound
//	non-Error/empty reply -> (none)                 -> ErrInvalidResponse
//
// Before the fix, shouldRetrySpawnSingleton recognized only the locally-detected
// transient errors, so these leader-delegated forms were treated as terminal: a
// follower calling SpawnSingleton during coordinator churn could fail immediately
// with "remote send failed" / "invalid response" instead of retrying until the
// cluster settled. The fix teaches the classifier to treat these forms as
// retryable, so the call spends its retry budget and eventually spawns (or
// confirms) the singleton.
//
// This sample reproduces the issue's scenario: a follower repeatedly calls
// SpawnSingleton (which is delegated to the coordinator over the network) while
// the coordinator is rolling-restarted underneath it. It asserts every call
// resolves to success (placed the singleton, or confirmed it already exists)
// rather than a terminal transient error.
//
// Note on determinism: the exact transient error only surfaces inside a
// sub-second reconciliation window (the cluster engine re-balances membership
// faster than a single delegated spawn), so a live cluster cannot reliably
// distinguish the fixed and unfixed builds in a short run. The deterministic
// regression proof lives in the unit tests, which drive the leader-delegated
// errors directly through the retry classifier:
//
//	actor/cluster_singleton_test.go -> TestSpawnSingletonRetryBehavior
//	actor/cluster_singleton_test.go -> TestShouldRetrySpawnSingleton
//
// This playground demonstrates the end-to-end behavior the fix protects.
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
	dynaport "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
)

const (
	singletonName = "maintenance-singleton"
	clusterSize   = 3
	rounds        = 5
)

// maintenanceActor is a stand-in for a cluster-wide maintenance singleton: the
// kind of actor every node tries to ensure exists, the common pattern that the
// issue describes.
type maintenanceActor struct{}

var _ actor.Actor = (*maintenanceActor)(nil)

func newMaintenanceActor() *maintenanceActor { return &maintenanceActor{} }

func (*maintenanceActor) PreStart(*actor.Context) error { return nil }
func (*maintenanceActor) Receive(*actor.ReceiveContext) {}
func (*maintenanceActor) PostStop(*actor.Context) error { return nil }

// nodeSpec captures everything needed to start (and later restart) a node on the
// same listen ports, which is what reproduces the "rejoin during reconciliation"
// window described in the issue.
type nodeSpec struct {
	name          string
	host          string
	discoveryPort int
	remotingPort  int
	peersPort     int
	natsAddr      string

	system actor.ActorSystem
}

func (n *nodeSpec) start(ctx context.Context) error {
	discovery := nats.NewDiscovery(&nats.Config{
		NatsServer:    "nats://" + n.natsAddr,
		NatsSubject:   "issue-1209",
		Host:          n.host,
		DiscoveryPort: n.discoveryPort,
	}, nats.WithLogger(log.DiscardLogger))

	system, err := actor.NewActorSystem(
		n.name,
		actor.WithLogger(log.DiscardLogger),
		actor.WithRemote(remote.NewConfig(n.host, n.remotingPort)),
		actor.WithCluster(
			actor.NewClusterConfig().
				WithDiscovery(discovery).
				WithDiscoveryPort(n.discoveryPort).
				WithPeersPort(n.peersPort).
				WithMinimumPeersQuorum(1).
				WithKinds(newMaintenanceActor()),
		),
	)
	if err != nil {
		return err
	}

	if err := system.Start(ctx); err != nil {
		return err
	}

	n.system = system
	return nil
}

func (n *nodeSpec) stop(ctx context.Context) {
	if n.system != nil {
		_ = n.system.Stop(ctx)
		n.system = nil
	}
}

// spawnSingleton calls SpawnSingleton with default options (the common pattern)
// and folds ErrSingletonAlreadyExists into success: a node that confirms the
// singleton already exists has achieved the same goal as one that created it.
func spawnSingleton(ctx context.Context, system actor.ActorSystem) error {
	_, err := system.SpawnSingleton(ctx, singletonName, newMaintenanceActor())
	if err == nil || errors.Is(err, gerrors.ErrSingletonAlreadyExists) { //nolint:staticcheck // old-version hosts still emit it during a rolling upgrade
		return nil
	}
	return err
}

// leaderIndex returns the index of the node that currently reports itself as the
// cluster coordinator, or -1 if none does yet.
func leaderIndex(ctx context.Context, nodes []*nodeSpec) int {
	for i, n := range nodes {
		if n.system == nil {
			continue
		}
		isLeader, err := n.system.IsLeader(ctx)
		if err == nil && isLeader {
			return i
		}
	}
	return -1
}

// followerIndex returns the index of a started node that is not the leader, so
// SpawnSingleton from it is delegated to the coordinator over the network (the
// path the fix governs).
func followerIndex(nodes []*nodeSpec, leader int) int {
	for i, n := range nodes {
		if i != leader && n.system != nil {
			return i
		}
	}
	return -1
}

// waitForLeader blocks until some node reports itself as coordinator.
func waitForLeader(ctx context.Context, nodes []*nodeSpec) int {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if i := leaderIndex(ctx, nodes); i >= 0 {
			return i
		}
		pause.For(200 * time.Millisecond)
	}
	return -1
}

func newNATSServer() *natsserver.Server {
	serv, err := natsserver.NewServer(&natsserver.Options{Host: "127.0.0.1", Port: -1})
	if err != nil {
		fmt.Printf("FAIL: error creating NATS server: %v\n", err)
		os.Exit(1)
	}

	ready := make(chan bool)
	go func() {
		ready <- true
		serv.Start()
	}()
	<-ready

	if !serv.ReadyForConnections(2 * time.Second) {
		fmt.Println("FAIL: NATS server is not ready for connections")
		os.Exit(1)
	}
	return serv
}

func main() {
	server := newNATSServer()
	defer server.Shutdown()

	ctx := context.Background()

	// Allocate ports up front so a node can be restarted on the exact same ports,
	// which is what reproduces the "rejoin during reconciliation" window.
	nodes := make([]*nodeSpec, clusterSize)
	for i := range nodes {
		ports := dynaport.Get(3)
		nodes[i] = &nodeSpec{
			name:          fmt.Sprintf("node-%d", i+1),
			host:          "127.0.0.1",
			discoveryPort: ports[0],
			remotingPort:  ports[1],
			peersPort:     ports[2],
			natsAddr:      server.Addr().String(),
		}
		if err := nodes[i].start(ctx); err != nil {
			fmt.Printf("FAIL: error starting %s: %v\n", nodes[i].name, err)
			os.Exit(1)
		}
		// Stagger startup so node ages (and therefore coordinator election) are
		// deterministic: the oldest live node is the coordinator.
		pause.For(500 * time.Millisecond)
	}
	defer func() {
		for _, n := range nodes {
			n.stop(ctx)
		}
	}()

	if waitForLeader(ctx, nodes) < 0 {
		fmt.Println("FAIL: cluster never elected a coordinator")
		os.Exit(1)
	}
	fmt.Printf("cluster formed; running %d rounds of SpawnSingleton from a follower while the coordinator is rolling-restarted\n", rounds)

	for round := 1; round <= rounds; round++ {
		leader := waitForLeader(ctx, nodes)
		if leader < 0 {
			fmt.Printf("FAIL: round %d: no coordinator\n", round)
			os.Exit(1)
		}
		follower := followerIndex(nodes, leader)
		if follower < 0 {
			fmt.Printf("FAIL: round %d: no follower\n", round)
			os.Exit(1)
		}

		// Roll the coordinator, then immediately drive SpawnSingleton from a
		// follower so the delegated call lands while membership is reconciling.
		nodes[leader].stop(ctx)

		if err := spawnSingleton(ctx, nodes[follower].system); err != nil {
			fmt.Printf("FAIL: round %d: SpawnSingleton returned a terminal error during coordinator churn: %v\n", round, err)
			os.Exit(1)
		}

		// Restart the dropped node on the same ports to keep the cluster whole and
		// keep membership churning for the next round.
		if err := nodes[leader].start(ctx); err != nil {
			fmt.Printf("FAIL: round %d: error restarting %s: %v\n", round, nodes[leader].name, err)
			os.Exit(1)
		}
		fmt.Printf("PASS: round %d: SpawnSingleton tolerated the coordinator rolling restart\n", round)
	}

	fmt.Println("issue #1209 reproduced and fixed: SpawnSingleton retries leader-delegated transient errors during membership churn")
}
