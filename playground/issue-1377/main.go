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

// Package main reproduces github.com/Tochemey/goakt/issues/1377: concurrent
// SpawnOn calls with the same name on two cluster nodes can both succeed and
// leave two live actors, because the cluster-wide duplicate check
// (checkSpawnPreconditions -> ActorExists) and the registry write (PutActor,
// last writer wins) are two separate steps.
//
// It runs four scenarios:
//
//  1. race: deterministic. Both nodes call SpawnOn with WithPlacement(Local).
//     Each spawn's PreStart blocks until the other spawn has also entered
//     PreStart, so both pass the duplicate check before either writes to the
//     registry, and both then publish with PutActor.
//  2. cleanup: deterministic, continues scenario 1. Stopping the duplicate
//     whose registry record was overwritten makes the death watch delete the
//     record of the other, still running, actor.
//  3. reliable: deterministic. Before the fix, reliable endpoints were the
//     one kind published with an if-absent write, so the losing spawn already
//     got ErrActorAlreadyExists; its rollback stopped the loser, whose death
//     watch cleanup then deleted the winner's record. This is the trap a fix
//     that only makes the write conditional falls into: cleanup must be
//     fenced by the incarnation that owns the record.
//  4. issue recipe: the reporter's setup. Two nodes, 32 concurrent SpawnOn
//     calls split across them with WithPlacement(LeastLoad) and
//     WithRelocationDisabled(), repeated. Timing dependent, so it reports the
//     count of runs that ended with two live actors or an unresolvable name.
//
// It exits with status 1 when any defect is observed and prints OK lines when
// the fixed behavior is observed.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery/static"
	gerrors "github.com/tochemey/goakt/v4/errors"
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/types"
	goaktlog "github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
)

const (
	// host is the loopback address every node binds to.
	host = "127.0.0.1"
	// systemName is shared by every node so they form one cluster.
	systemName = "issue1377"
	// otherSpawnsTimeout bounds how long a PreStart waits for the other spawns
	// to enter PreStart. It stays below initTimeout, so a spawn whose partner
	// never shows up still starts instead of failing.
	otherSpawnsTimeout = 3 * time.Second
	// initTimeout is the PreStart time budget of the overlapping spawns.
	initTimeout = 5 * time.Second
	// settleWait is how long the scenarios wait for asynchronous work (death
	// watch cleanup, registry convergence) before reading the result.
	settleWait = 3 * time.Second
	// recipeRuns is the number of repetitions of the issue recipe.
	recipeRuns = 20
	// recipeCallers is the number of concurrent SpawnOn calls per run, split
	// evenly across the two nodes, as in the issue.
	recipeCallers = 32
)

// spawnOverlap makes concurrent spawns of one name overlap in time. Each
// spawn's PreStart calls enterAndWait, which blocks until every expected spawn
// has entered PreStart. PreStart runs after the duplicate check and before the
// registry write, so when all spawns have entered PreStart, every one of them
// has passed the duplicate check and none has written to the registry yet.
type spawnOverlap struct {
	// expected is the number of spawns that must enter PreStart.
	expected int32
	// entered is the number of spawns that have entered PreStart so far.
	entered atomic.Int32
	// allEntered is closed when the last expected spawn enters PreStart.
	allEntered chan types.Unit
}

// newSpawnOverlap returns a spawnOverlap that waits for expected spawns.
func newSpawnOverlap(expected int32) *spawnOverlap {
	return &spawnOverlap{expected: expected, allEntered: make(chan types.Unit)}
}

// enterAndWait records that one more spawn has entered PreStart, then blocks
// until every expected spawn has entered PreStart or otherSpawnsTimeout
// elapses, whichever comes first.
func (x *spawnOverlap) enterAndWait() {
	if x.entered.Add(1) == x.expected {
		close(x.allEntered)
	}

	select {
	case <-x.allEntered:
	case <-time.After(otherSpawnsTimeout):
	}
}

// allSpawnsEntered reports whether every expected spawn entered PreStart
// before any of them was allowed to continue.
func (x *spawnOverlap) allSpawnsEntered() bool {
	select {
	case <-x.allEntered:
		return true
	default:
		return false
	}
}

// worker is the actor spawned under a contested name. A worker created by the
// remote spawn handler through reflection has no overlap set and starts at once.
type worker struct {
	// overlap, when set, makes PreStart wait until the other spawns of the
	// same name have also entered PreStart.
	overlap *spawnOverlap
}

var _ actor.Actor = (*worker)(nil)

// PreStart waits for the other spawns of the same name, when overlap is set.
func (x *worker) PreStart(*actor.Context) error {
	if x.overlap != nil {
		x.overlap.enterAndWait()
	}

	return nil
}

// Receive ignores every message; the worker only needs to exist.
func (x *worker) Receive(ctx *actor.ReceiveContext) {
	ctx.Unhandled()
}

// PostStop does nothing.
func (x *worker) PostStop(*actor.Context) error {
	return nil
}

func main() {
	ctx := context.Background()
	failed := false

	nodes := startCluster(ctx, 3)
	nodeA, nodeB, observer := nodes[0], nodes[1], nodes[2]

	pidA, pidB, ok := raceScenario(ctx, nodeA, nodeB, observer)
	failed = failed || !ok

	if pidA != nil && pidB != nil {
		failed = !cleanupScenario(ctx, nodeA, nodeB, observer, pidA, pidB) || failed
	} else {
		fmt.Println("== scenario 2: cleanup: skipped, scenario 1 left no duplicate to clean up")
	}

	failed = !reliableScenario(ctx, nodeA, nodeB, observer) || failed
	stopCluster(ctx, nodes)

	failed = !recipeScenario(ctx) || failed

	if failed {
		os.Exit(1)
	}
}

// raceScenario spawns the same name on nodeA and nodeB at once with local
// placement. Each spawn waits in PreStart until the other has also entered
// PreStart, so both pass the duplicate check before either writes to the
// registry. It returns both PIDs when both spawns succeeded, and whether the
// fixed behavior (exactly one success) was observed.
func raceScenario(ctx context.Context, nodeA, nodeB, observer actor.ActorSystem) (*actor.PID, *actor.PID, bool) {
	const name = "race"

	fmt.Println("== scenario 1: race (SpawnOn, WithPlacement(Local), both spawns wait for each other in PreStart)")

	overlap := newSpawnOverlap(2)
	pids := make([]*actor.PID, 2)
	errs := make([]error, 2)

	var wg sync.WaitGroup
	for i, node := range []actor.ActorSystem{nodeA, nodeB} {
		wg.Go(func() {
			pids[i], errs[i] = node.SpawnOn(ctx, name, &worker{overlap: overlap}, actor.WithPlacement(actor.Local), actor.WithRelocationDisabled(), actor.WithInitTimeout(initTimeout))
		})
	}
	wg.Wait()

	fmt.Printf("   both spawns were in PreStart at the same time (both passed the duplicate check, neither had written to the registry): %t\n", overlap.allSpawnsEntered())
	fmt.Printf("   node A SpawnOn: err=%v\n", errs[0])
	fmt.Printf("   node B SpawnOn: err=%v\n", errs[1])

	if errs[0] != nil || errs[1] != nil {
		winner := -1
		for i, err := range errs {
			if err == nil {
				winner = i
			}
		}

		if winner == -1 || !errors.Is(errs[1-winner], gerrors.ErrActorAlreadyExists) {
			fmt.Println("UNEXPECTED: expected exactly one success and one ErrActorAlreadyExists")
			return nil, nil, false
		}

		// the loser was rolled back; its cleanup must leave the winner's record
		time.Sleep(settleWait)

		fromObserver := resolve(ctx, observer, name)
		fmt.Printf("   winner %s running: %t\n", pids[winner].ID(), pids[winner].IsRunning())
		fmt.Printf("   ActorOf from observer: %s\n", fromObserver)

		if fromObserver != pids[winner].ID() {
			fmt.Println("REPRO (broken): the losing spawn's rollback deleted the winner's registry record")
			return nil, nil, false
		}

		fmt.Println("OK: exactly one of the two concurrent spawns succeeded and the registry resolves the winner")
		return nil, nil, true
	}

	fromA := resolve(ctx, nodeA, name)
	fromB := resolve(ctx, nodeB, name)
	fromObserver := resolve(ctx, observer, name)

	fmt.Printf("   ActorOf from node A:   %s\n", fromA)
	fmt.Printf("   ActorOf from node B:   %s\n", fromB)
	fmt.Printf("   ActorOf from observer: %s (the one registry record)\n", fromObserver)
	fmt.Printf("   node A instance running: %t, node B instance running: %t\n", pids[0].IsRunning(), pids[1].IsRunning())
	fmt.Println("REPRO (broken): both concurrent SpawnOn calls succeeded; two live actors share one name and nodes A and B resolve different PIDs")

	return pids[0], pids[1], false
}

// cleanupScenario stops the duplicate whose registry record was overwritten
// and shows that its death watch cleanup deletes the record of the other,
// still running, actor. It reports whether the registry kept the record of
// the surviving actor.
func cleanupScenario(ctx context.Context, nodeA, nodeB, observer actor.ActorSystem, pidA, pidB *actor.PID) bool {
	const name = "race"

	fmt.Println("== scenario 2: cleanup (stop the duplicate that does not own the registry record)")

	owner := resolve(ctx, observer, name)

	stale, survivor := pidA, pidB
	staleNode, survivorNode := nodeA, nodeB
	if owner == pidA.ID() {
		stale, survivor = pidB, pidA
		staleNode, survivorNode = nodeB, nodeA
	}

	fmt.Printf("   registry record owner: %s\n", owner)
	fmt.Printf("   stopping the non-owner: %s\n", stale.ID())

	if err := stale.Shutdown(ctx); err != nil {
		fmt.Printf("UNEXPECTED: failed to stop %s: %v\n", stale.ID(), err)
		return false
	}

	time.Sleep(settleWait)

	fromObserver := resolve(ctx, observer, name)
	fromStaleNode := resolve(ctx, staleNode, name)
	exists, _ := observer.ActorExists(ctx, name)

	fmt.Printf("   survivor %s running: %t\n", survivor.ID(), survivor.IsRunning())
	fmt.Printf("   ActorOf from survivor's node: %s\n", resolve(ctx, survivorNode, name))
	fmt.Printf("   ActorOf from stopped node:    %s\n", fromStaleNode)
	fmt.Printf("   ActorOf from observer:        %s\n", fromObserver)
	fmt.Printf("   ActorExists from observer:    %t\n", exists)

	if survivor.IsRunning() && fromObserver != survivor.ID() {
		fmt.Println("REPRO (broken): the stopped duplicate's death watch cleanup deleted the registry record of the running actor; it is now unresolvable from every other node and its name looks free")
		return false
	}

	fmt.Println("OK: the registry still resolves the running actor")
	return true
}

// reliableScenario spawns the same reliable consumer endpoint name on nodeA
// and nodeB at once. One spawn loses with ErrActorAlreadyExists; the scenario
// checks that the loser's rollback leaves the winner's registry record in
// place.
func reliableScenario(ctx context.Context, nodeA, nodeB, observer actor.ActorSystem) bool {
	const name = "reliable-consumer"

	fmt.Println("== scenario 3: reliable endpoint (loser rollback must leave the winner's record)")

	overlap := newSpawnOverlap(2)
	pids := make([]*actor.PID, 2)
	errs := make([]error, 2)

	var wg sync.WaitGroup
	for i, node := range []actor.ActorSystem{nodeA, nodeB} {
		wg.Go(func() {
			pids[i], errs[i] = node.SpawnOn(ctx, name, &worker{overlap: overlap}, actor.WithPlacement(actor.Local), actor.WithRelocationDisabled(), actor.WithInitTimeout(initTimeout), actor.AsReliableConsumer("reliable-producer"))
		})
	}
	wg.Wait()

	fmt.Printf("   both spawns were in PreStart at the same time: %t\n", overlap.allSpawnsEntered())
	fmt.Printf("   node A SpawnOn: err=%v\n", errs[0])
	fmt.Printf("   node B SpawnOn: err=%v\n", errs[1])

	winner := -1
	for i, err := range errs {
		if err == nil {
			winner = i
		}
	}

	if winner == -1 || errs[1-winner] == nil || !errors.Is(errs[1-winner], gerrors.ErrActorAlreadyExists) {
		fmt.Println("UNEXPECTED: expected exactly one success and one ErrActorAlreadyExists")
		return false
	}

	time.Sleep(settleWait)

	fromObserver := resolve(ctx, observer, name)
	exists, _ := observer.ActorExists(ctx, name)

	fmt.Printf("   winner %s running: %t\n", pids[winner].ID(), pids[winner].IsRunning())
	fmt.Printf("   ActorOf from observer:     %s\n", fromObserver)
	fmt.Printf("   ActorExists from observer: %t\n", exists)

	if pids[winner].IsRunning() && fromObserver != pids[winner].ID() {
		fmt.Println("REPRO (broken): the losing spawn's rollback deleted the winner's registry record; if-absent publication alone does not protect the winner")
		return false
	}

	fmt.Println("OK: the losing spawn's rollback left the winner's registry record in place")
	return true
}

// recipeScenario runs the reporter's recipe on a fresh two-node cluster:
// recipeCallers concurrent SpawnOn calls with least-load placement, split
// across the two nodes, released together, repeated recipeRuns times with a
// new name each run. A run is a duplicate when both nodes still resolve the
// name to a local actor after settleWait. It reports whether no run produced
// a duplicate.
func recipeScenario(ctx context.Context) bool {
	fmt.Printf("== scenario 4: issue recipe (2 nodes, %d concurrent SpawnOn, LeastLoad, %d runs)\n", recipeCallers, recipeRuns)

	nodes := startCluster(ctx, 2)
	defer stopCluster(ctx, nodes)

	duplicates, lost := 0, 0
	for run := range recipeRuns {
		name := fmt.Sprintf("recipe-%d", run)
		start := make(chan types.Unit)

		var (
			wg        sync.WaitGroup
			successes atomic.Int32
		)

		for caller := range recipeCallers {
			node := nodes[caller%2]
			wg.Go(func() {
				<-start
				if _, err := node.SpawnOn(ctx, name, new(worker), actor.WithPlacement(actor.LeastLoad), actor.WithRelocationDisabled()); err == nil {
					successes.Add(1)
				}
			})
		}

		close(start)
		wg.Wait()
		time.Sleep(settleWait)

		pidA, errA := nodes[0].ActorOf(ctx, name)
		pidB, errB := nodes[1].ActorOf(ctx, name)
		duplicate := errA == nil && errB == nil && pidA.IsLocal() && pidB.IsLocal()
		missing := errA != nil || errB != nil

		switch {
		case duplicate:
			duplicates++
		case missing:
			lost++
		}

		fmt.Printf("   run %2d: successful SpawnOn=%2d  node A resolves %s  node B resolves %s  duplicate=%t unresolvable=%t\n", run+1, successes.Load(), describe(pidA, errA), describe(pidB, errB), duplicate, missing)
	}

	if duplicates > 0 || lost > 0 {
		fmt.Printf("REPRO (broken): %d of %d runs left two live actors under one name, %d left the name unresolvable on a node\n", duplicates, recipeRuns, lost)
		return false
	}

	fmt.Printf("OK: every one of the %d runs left one actor, resolved to the same PID by both nodes\n", recipeRuns)
	return true
}

// resolve returns the address ActorOf yields for name on node, or the error
// text when the lookup fails.
func resolve(ctx context.Context, node actor.ActorSystem, name string) string {
	pid, err := node.ActorOf(ctx, name)
	if err != nil {
		return "error: " + err.Error()
	}

	return pid.ID()
}

// describe renders an ActorOf result as local or remote with its address.
func describe(pid *actor.PID, err error) string {
	if err != nil {
		return "error"
	}

	if pid.IsLocal() {
		return "local(" + pid.ID() + ")"
	}

	return "remote(" + pid.ID() + ")"
}

// startCluster starts count nodes on loopback that discover each other through
// a static host list, and returns once every node sees all the others.
func startCluster(ctx context.Context, count int) []actor.ActorSystem {
	ports := inet.Get(3 * count)
	hosts := make([]string, count)
	for i := range count {
		hosts[i] = fmt.Sprintf("%s:%d", host, ports[3*i])
	}

	nodes := make([]actor.ActorSystem, count)
	for i := range count {
		discoveryPort, peersPort, remotingPort := ports[3*i], ports[3*i+1], ports[3*i+2]

		clusterConfig := actor.NewClusterConfig().
			WithDiscovery(static.NewDiscovery(&static.Config{Hosts: hosts})).
			WithDiscoveryPort(discoveryPort).
			WithPeersPort(peersPort).
			WithPartitionCount(7).
			WithMinimumPeersQuorum(1).
			WithBootstrapTimeout(time.Second).
			WithKinds(new(worker))

		node, err := actor.NewActorSystem(systemName,
			actor.WithLogger(goaktlog.DiscardLogger),
			actor.WithRemote(remote.NewConfig(host, remotingPort)),
			actor.WithCluster(clusterConfig),
		)
		if err != nil {
			fatal("failed to create node %d: %v", i, err)
		}

		nodes[i] = node
	}

	errs := make([]error, count)
	var wg sync.WaitGroup
	for i, node := range nodes {
		wg.Go(func() {
			errs[i] = node.Start(ctx)
		})
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			fatal("failed to start node %d: %v", i, err)
		}
	}

	deadline := time.Now().Add(30 * time.Second)
	for _, node := range nodes {
		for {
			peers, err := node.Peers(ctx, time.Second)
			if err == nil && len(peers) == count-1 {
				break
			}

			if time.Now().After(deadline) {
				fatal("the %d nodes did not see each other in time", count)
			}

			time.Sleep(100 * time.Millisecond)
		}
	}

	return nodes
}

// stopCluster stops every node, ignoring errors: the scenarios are over.
func stopCluster(ctx context.Context, nodes []actor.ActorSystem) {
	for _, node := range nodes {
		_ = node.Stop(ctx)
	}
}

// fatal prints the message and exits with status 2, which marks a broken
// setup rather than a reproduced defect.
func fatal(format string, args ...any) {
	fmt.Printf("SETUP FAILURE: "+format+"\n", args...)
	os.Exit(2)
}
