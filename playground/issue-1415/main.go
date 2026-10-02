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

// Package main reproduces github.com/Tochemey/goakt/issues/1415 and a second
// defect of the same shutdown that the investigation turned up.
//
// A node that stops does two things for the cluster before it leaves it. It
// hands a snapshot of its actors and grains to its peers, so that the leader
// can recreate them on a surviving node, and it removes its own records from
// the registry, which is spread over the cluster nodes.
//
// Defect 1, issue 1415: Stop returned the error of the handover. When the
// peers the node still lists are gone themselves, there is no node to hand
// the snapshot to, the handover fails against every one of them, and Stop
// failed with "failed to replicate state to any peer".
//
// Defect 2: Stop returned the error of the registry removal too. An actor's
// record is removed when the node leaves the cluster, a grain's record when
// the grain is deactivated. The removal of a record fails when the node that
// holds it is gone, or is stopping at the same time, so Stop failed whenever
// nodes stopped together. A record that stays behind names a node that has
// left, and the cluster already lives with such records, as it has to after a
// crash.
//
// The sample runs four scenarios, each on a fresh three-node cluster joined
// through NATS discovery. Node 3 is the node that stops; it runs in this
// process and hosts relocatable actors named worker-0, worker-1 and so on,
// and grains named session-0, session-1 and so on.
//
//  1. Node 1 and node 2 keep running while node 3 stops. Stop must succeed
//     and the workers must be recreated on them. This passes with and without
//     the fix: it is there so that the fix cannot break a handover that has a
//     node to go to.
//  2. Node 1 and node 2 are gone when node 3 stops, and node 3 still lists
//     them. Stop must succeed. Without the fix it returns the error of the
//     handover (defect 1) and the errors of the registry removal (defect 2).
//  3. Node 2 is gone when node 3 stops, node 3 still lists it, and node 1
//     keeps running. Stop must succeed although node 3 could not remove the
//     records node 2 held. Node 1 must recreate every worker all the same,
//     and every grain must answer when asked from node 1. Without the fix
//     Stop returns the errors of the registry removal (defect 2). The
//     recreation and the answers are what show that a record left behind does
//     no harm.
//  4. The three nodes, each hosting workers and grains, stop at the same
//     time. Every Stop must succeed. Without the fix some return the error of
//     the registry removal (defect 2), and now and then the error of the
//     handover (defect 1).
//
// In scenarios 2 and 3 a node has to be gone while membership still lists it.
// A node that stops gracefully is dropped from membership before its remoting
// goes away, so that node is an OS process of its own there, and the sample
// kills it: membership keeps a killed node for a few seconds. Before the kill
// node 3 asks an actor on it, so that it holds an open remoting connection to
// it, as a node of a running cluster does.
//
// Scenario numbers given on the command line restrict the run to those
// scenarios.
//
// The sample exits with status 1 when a scenario shows a defect, with status
// 2 when a cluster could not be set up or a scenario could not reach the
// state it checks, and with status 0 when every scenario passed.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery/nats"
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

const (
	host        = "127.0.0.1"
	systemName  = "issue1415"
	natsSubject = "issue-1415"
	greeterName = "greeter"

	// A process started as another node reads its settings from these
	// variables. natsEnv doubles as the switch that tells such a process from
	// the one running the sample.
	natsEnv      = "GOAKT_ISSUE_1415_NATS"
	discoveryEnv = "GOAKT_ISSUE_1415_DISCOVERY"
	peersEnv     = "GOAKT_ISSUE_1415_PEERS"
	remotingEnv  = "GOAKT_ISSUE_1415_REMOTING"
	// verboseEnv, when set to 1, copies the log of node 3 to stderr.
	verboseEnv = "GOAKT_ISSUE_1415_VERBOSE"

	// handoverFailed is the error of a handover that reached no peer.
	handoverFailed = "failed to replicate state to any peer"

	// What node 3 logs for each registry record it could not remove while
	// stopping: an actor record (actorRecordNotRemoved), and a grain record
	// without the fix (grainRecordNotRemoved) and with it
	// (grainRecordLeftBehind).
	actorRecordNotRemoved = "failed to remove actor="
	grainRecordNotRemoved = "failed to release grain="
	grainRecordLeftBehind = "left its registry record behind"

	// workerCount is how many workers a node hosts. The registry spreads
	// their records over the cluster nodes, so with this many some are held
	// by each node.
	workerCount = 30

	// stopRounds is how many clusters scenario 4 stops. Without the fix a Stop
	// fails in most rounds, not in every one.
	stopRounds = 3

	// nodeLifetime bounds the life of the process of another node, so that it
	// never outlives a sample that exited on a setup failure.
	nodeLifetime = 5 * time.Minute

	// peerWait bounds a membership change, relocationWait the recreation of
	// the workers once node 3 has left, and stopLimit a Stop.
	peerWait       = 20 * time.Second
	relocationWait = 45 * time.Second
	stopLimit      = 30 * time.Second
	operationLimit = time.Second
	pollInterval   = 50 * time.Millisecond
)

// outcome is what a scenario observed.
type outcome int

const (
	// passed: the scenario ran and no defect showed.
	passed outcome = iota
	// broken: the scenario ran and a defect showed.
	broken
	// notExercised: the scenario could not reach the state it checks.
	notExercised
)

// scenario is one check of a stopping node, run on a cluster of its own.
type scenario struct {
	title string
	run   func(ctx context.Context, natsAddress string) outcome
}

// nodeLog collects the log lines of node 3 in memory, so that a scenario can
// tell whether node 3 failed to remove registry records while it stopped.
type nodeLog struct {
	// mu guards lines: the logger writes from the actor system's goroutines
	// while the sample reads from main.
	mu    sync.Mutex
	lines []string
}

// Write implements io.Writer: the logger calls it once per log line.
func (x *nodeLog) Write(line []byte) (int, error) {
	x.mu.Lock()
	x.lines = append(x.lines, string(line))
	x.mu.Unlock()

	return len(line), nil
}

// count returns how many collected lines contain part.
func (x *nodeLog) count(part string) int {
	x.mu.Lock()
	total := 0

	for _, line := range x.lines {
		if strings.Contains(line, part) {
			total++
		}
	}

	x.mu.Unlock()

	return total
}

// logger returns a logger that writes into x, and to stderr as well when
// verboseEnv is set.
func (x *nodeLog) logger() log.Logger {
	if os.Getenv(verboseEnv) == "1" {
		return log.NewZap(log.InfoLevel, x, os.Stderr)
	}

	return log.NewZap(log.InfoLevel, x)
}

// worker is the actor node 3 hosts. It is relocatable, so the snapshot node 3
// hands over when it stops lists it.
type worker types.Unit

// PreStart does nothing.
func (*worker) PreStart(*actor.Context) error { return nil }

// Receive handles no message.
func (*worker) Receive(ctx *actor.ReceiveContext) { ctx.Unhandled() }

// PostStop does nothing.
func (*worker) PostStop(*actor.Context) error { return nil }

// session is the grain the nodes host. A grain removes its own registry
// record when it is deactivated, which a stopping node does for every grain it
// hosts.
type session types.Unit

// OnActivate does nothing.
func (*session) OnActivate(context.Context, *actor.GrainProps) error { return nil }

// OnReceive answers a TestReply with a Reply.
func (*session) OnReceive(ctx *actor.GrainContext) {
	if _, ok := ctx.Message().(*testpb.TestReply); ok {
		ctx.Response(new(testpb.Reply))
		return
	}

	ctx.Unhandled()
}

// OnDeactivate does nothing.
func (*session) OnDeactivate(context.Context, *actor.GrainProps) error { return nil }

// greeter is the actor a node run as a process of its own hosts. Node 3 asks
// it once, which opens a remoting connection from node 3 to that node.
type greeter types.Unit

// PreStart does nothing.
func (*greeter) PreStart(*actor.Context) error { return nil }

// Receive answers a TestReply with a Reply.
func (*greeter) Receive(ctx *actor.ReceiveContext) {
	if _, ok := ctx.Message().(*testpb.TestReply); ok {
		ctx.Response(new(testpb.Reply))
		return
	}

	ctx.Unhandled()
}

// PostStop does nothing.
func (*greeter) PostStop(*actor.Context) error { return nil }

// main runs the scenarios, or a single node when the sample started this
// process as one.
func main() {
	if os.Getenv(natsEnv) != "" {
		runNode()
		return
	}

	ctx := context.Background()
	server := newNatsServer()

	scenarios := []scenario{
		{title: "node 3 stops while node 1 and node 2 keep running", run: handsOverToRunningPeers},
		{title: "node 3 stops while it still lists node 1 and node 2, which are gone", run: stopsWhenListedPeersAreGone},
		{title: "node 3 stops while it still lists node 2, which is gone; node 1 keeps running", run: survivorRecreatesWorkers},
		{title: "the three nodes stop at the same time", run: stopsTogether},
	}

	results := make(map[outcome]int)

	for i, item := range scenarios {
		if !selected(i+1, len(scenarios)) {
			continue
		}

		fmt.Printf("scenario %d: %s\n", i+1, item.title)
		results[item.run(ctx, server.Addr().String())]++
		fmt.Println()
	}

	server.Shutdown()
	fmt.Printf("%d passed, %d broken, %d not exercised\n", results[passed], results[broken], results[notExercised])

	switch {
	case results[broken] > 0:
		os.Exit(1)
	case results[notExercised] > 0:
		os.Exit(2)
	}
}

// selected reports whether the scenario with the given number, out of count,
// is to be run: all of them are, unless the command line names scenario
// numbers. An argument that names no scenario ends the sample, so that a
// mistyped run never passes for a clean one.
func selected(number, count int) bool {
	if len(os.Args) == 1 {
		return true
	}

	chosen := false

	for _, arg := range os.Args[1:] {
		value, err := strconv.Atoi(arg)
		if err != nil || value < 1 || value > count {
			fatal("%q is not a scenario number between 1 and %d", arg, count)
		}

		if value == number {
			chosen = true
		}
	}

	return chosen
}

// handsOverToRunningPeers is scenario 1: node 3 stops in a cluster whose other
// two nodes keep running. Its Stop must succeed and the workers must be
// recreated on node 1 or node 2, which only happens when the snapshot reached
// them.
func handsOverToRunningPeers(ctx context.Context, natsAddress string) outcome {
	ports := inet.Get(9)
	node1 := startSystem(ctx, natsAddress, ports[0:3], log.DiscardLogger)
	node2 := startSystem(ctx, natsAddress, ports[3:6], log.DiscardLogger)
	node3 := startSystem(ctx, natsAddress, ports[6:9], new(nodeLog).logger())
	node3Remoting := remotingAddress(ports[6:9])

	waitForPeerCount(ctx, node3, 2)
	spawnWorkers(ctx, node3, 3)
	awaitWorkersOn(ctx, node1, 3, node3Remoting)
	fmt.Println("node 3 hosts the workers; node 1 and node 2 are running")

	result := reportStop(stop(ctx, node3))

	if !reportRecreated(ctx, node1, 3, node3Remoting) {
		result = broken
	}

	_ = stop(ctx, node2)
	_ = stop(ctx, node1)

	return result
}

// stopsWhenListedPeersAreGone is scenario 2, the case of the issue: node 1 and
// node 2 are killed and node 3 stops while its membership still lists them.
// No node is left to take the snapshot or to hold the registry, so Stop must
// succeed.
func stopsWhenListedPeersAreGone(ctx context.Context, natsAddress string) outcome {
	ports := inet.Get(9)
	node1 := startNodeProcess(natsAddress, ports[0:3])
	node2 := startNodeProcess(natsAddress, ports[3:6])
	node3 := startSystem(ctx, natsAddress, ports[6:9], new(nodeLog).logger())

	waitForPeerCount(ctx, node3, 2)
	spawnWorkers(ctx, node3, workerCount)

	activateSessions(ctx, node3, workerCount)

	// One ask per peer leaves node 3 with an open remoting connection to each.
	greet(ctx, node3, greeterNameOf(ports[2]))
	greet(ctx, node3, greeterNameOf(ports[5]))
	fmt.Println("node 3 hosts the workers and the grains, and has asked an actor on node 1 and on node 2")

	// SIGKILL: the nodes are gone at once, and node 3 keeps listing them until
	// its failure detection notices.
	kill(node1)
	kill(node2)

	if !stillLists(ctx, node3, 2) {
		_ = stop(ctx, node3)
		return notExercised
	}

	fmt.Println("node 1 and node 2 killed; node 3 still lists both")

	return reportStop(stop(ctx, node3))
}

// survivorRecreatesWorkers is scenario 3: node 2 is killed and node 3 stops
// while its membership still lists node 2. Node 3 cannot remove the registry
// records node 2 held, so they stay behind. Stop must succeed, and node 1,
// which got the snapshot, must recreate every worker in spite of the records
// left behind. Node 1 is started first: the oldest node is the leader, and the
// leader is the node that recreates the workers.
func survivorRecreatesWorkers(ctx context.Context, natsAddress string) outcome {
	ports := inet.Get(9)
	logs := new(nodeLog)
	node1 := startSystem(ctx, natsAddress, ports[0:3], log.DiscardLogger)
	node2 := startNodeProcess(natsAddress, ports[3:6])
	node3 := startSystem(ctx, natsAddress, ports[6:9], logs.logger())
	node3Remoting := remotingAddress(ports[6:9])

	waitForPeerCount(ctx, node3, 2)
	spawnWorkers(ctx, node3, workerCount)
	activateSessions(ctx, node3, workerCount)
	awaitWorkersOn(ctx, node1, workerCount, node3Remoting)
	greet(ctx, node3, greeterNameOf(ports[5]))
	fmt.Println("node 3 hosts the workers and the grains, and has asked an actor on node 2; node 1 is running")

	kill(node2)

	if !stillLists(ctx, node3, 2) {
		_ = stop(ctx, node3)
		_ = stop(ctx, node1)

		return notExercised
	}

	fmt.Println("node 2 killed; node 3 still lists it")

	result := reportStop(stop(ctx, node3))

	// The scenario only checks what it is meant to when records stayed behind.
	actorsLeft := logs.count(actorRecordNotRemoved)
	grainsLeft := logs.count(grainRecordNotRemoved) + logs.count(grainRecordLeftBehind)

	if actorsLeft == 0 || grainsLeft == 0 {
		fmt.Printf("NOT EXERCISED: node 3 left %d actor records and %d grain records behind\n", actorsLeft, grainsLeft)
		_ = stop(ctx, node1)

		return notExercised
	}

	fmt.Printf("node 3 could not remove %d of its %d actor records and %d of its %d grain records\n", actorsLeft, workerCount, grainsLeft, workerCount)

	if !reportRecreated(ctx, node1, workerCount, node3Remoting) {
		result = broken
	}

	if !reportSessionsAnswer(ctx, node1, workerCount) {
		result = broken
	}

	_ = stop(ctx, node1)

	return result
}

// stopsTogether is scenario 4: the three nodes of a cluster, each hosting
// workers, stop at the same time, as when a whole deployment is taken down.
// Every Stop must succeed. It runs stopRounds clusters, because without the
// fix which Stop fails depends on how the three shutdowns interleave.
func stopsTogether(ctx context.Context, natsAddress string) outcome {
	failed := 0

	for round := 1; round <= stopRounds; round++ {
		ports := inet.Get(9)
		nodes := []actor.ActorSystem{
			startSystem(ctx, natsAddress, ports[0:3], log.DiscardLogger),
			startSystem(ctx, natsAddress, ports[3:6], log.DiscardLogger),
			startSystem(ctx, natsAddress, ports[6:9], log.DiscardLogger),
		}

		waitForPeerCount(ctx, nodes[2], 2)

		for i, node := range nodes {
			for j := range workerCount {
				spawn(ctx, node, fmt.Sprintf("worker-%d-%d", i+1, j))

				if _, err := actor.GrainOf[*session](ctx, node, fmt.Sprintf("session-%d-%d", i+1, j)); err != nil {
					fatal("activate a grain: %v", err)
				}
			}
		}

		errs := make([]error, len(nodes))
		group := new(sync.WaitGroup)

		for i, node := range nodes {
			group.Go(func() { errs[i] = stop(ctx, node) })
		}

		group.Wait()

		for i, err := range errs {
			if err != nil {
				fmt.Printf("BROKEN: round %d: Stop of node %d returned: %s\n", round, i+1, summary(err))
				failed++
			}
		}
	}

	if failed > 0 {
		return broken
	}

	fmt.Printf("PASS: every Stop returned nil in %d rounds\n", stopRounds)

	return passed
}

// reportStop prints what the Stop of node 3 returned and returns the outcome:
// passed when it returned nil, broken otherwise.
func reportStop(err error) outcome {
	if err != nil {
		fmt.Printf("BROKEN: Stop of node 3 returned: %s\n", summary(err))
		return broken
	}

	fmt.Println("PASS: Stop of node 3 returned nil")

	return passed
}

// summary shortens what Stop returned to one line, followed by the number of
// lines left out. Stop joins the errors of its steps, one per line or more,
// and a node that hosts thirty grains can return sixty lines. The line of the
// failed handover is the one shown when there is one, so that defect 1 is
// never hidden behind the lines of defect 2.
func summary(err error) string {
	lines := strings.Split(strings.TrimSpace(err.Error()), "\n")
	shown := lines[0]

	for _, line := range lines {
		if strings.Contains(line, handoverFailed) {
			shown = line
			break
		}
	}

	if len(lines) == 1 {
		return shown
	}

	return fmt.Sprintf("%s [and %d more lines]", shown, len(lines)-1)
}

// reportRecreated waits until node resolves each of the count workers to
// another node than the one at departedRemoting, prints the result and reports
// whether all of them were recreated.
func reportRecreated(ctx context.Context, node actor.ActorSystem, count int, departedRemoting string) bool {
	elsewhere := func(id string) bool { return !strings.Contains(id, departedRemoting) }
	recreated := countWorkers(ctx, node, count, elsewhere, relocationWait)

	if recreated != count {
		fmt.Printf("BROKEN: %d of %d workers were recreated on another node within %s\n", recreated, count, relocationWait)
		return false
	}

	fmt.Printf("PASS: all %d workers were recreated on another node\n", recreated)

	return true
}

// awaitWorkersOn blocks until node resolves each of the count workers to the
// node at remoting, or exits with a setup failure after peerWait.
func awaitWorkersOn(ctx context.Context, node actor.ActorSystem, count int, remoting string) {
	there := func(id string) bool { return strings.Contains(id, remoting) }
	if found := countWorkers(ctx, node, count, there, peerWait); found != count {
		fatal("%d of %d workers resolved to %s within %s", found, count, remoting, peerWait)
	}
}

// countWorkers polls the ID node resolves each of the count workers to, which
// carries the address of the node hosting it, until accept returns true for
// all of them or timeout passes. It returns how many were accepted.
func countWorkers(ctx context.Context, node actor.ActorSystem, count int, accept func(id string) bool, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	accepted := 0

	for {
		accepted = 0

		for i := range count {
			lookupCtx, cancel := context.WithTimeout(ctx, operationLimit)
			pid, err := node.ActorOf(lookupCtx, workerName(i))
			cancel()

			if err == nil && pid != nil && accept(pid.ID()) {
				accepted++
			}
		}

		if accepted == count || time.Now().After(deadline) {
			return accepted
		}

		pause.For(pollInterval)
	}
}

// stillLists reports whether node lists count peers, and prints why the
// scenario is not exercised when it does not.
func stillLists(ctx context.Context, node actor.ActorSystem, count int) bool {
	peers, err := node.Peers(ctx, operationLimit)
	if err != nil || len(peers) != count {
		fmt.Printf("NOT EXERCISED: node 3 lists %d peers right after the kill (error: %v)\n", len(peers), err)
		return false
	}

	return true
}

// remotingAddress returns the remoting address of the node on the given
// discovery, peers and remoting ports.
func remotingAddress(ports []int) string {
	return fmt.Sprintf("%s:%d", host, ports[2])
}

// greeterNameOf returns the name of the greeter hosted by the node whose
// remoting port is given. Actor names are unique in a cluster, so each node
// names its greeter after its own port.
func greeterNameOf(remotingPort int) string {
	return fmt.Sprintf("%s-%d", greeterName, remotingPort)
}

// reportSessionsAnswer asks each of the count grains from node until all of
// them answer or relocationWait passes, prints the result and reports whether
// all of them answered. A grain answers once it is activated again on a
// running node, which takes the record its stopped node left behind to be
// replaced.
func reportSessionsAnswer(ctx context.Context, node actor.ActorSystem, count int) bool {
	deadline := time.Now().Add(relocationWait)
	answered := 0

	for {
		answered = 0

		for i := range count {
			if sessionAnswers(ctx, node, sessionName(i)) {
				answered++
			}
		}

		if answered == count || time.Now().After(deadline) {
			break
		}

		pause.For(pollInterval)
	}

	if answered != count {
		fmt.Printf("BROKEN: %d of %d grains answered from another node within %s\n", answered, count, relocationWait)
		return false
	}

	fmt.Printf("PASS: all %d grains answer from another node\n", answered)

	return true
}

// sessionAnswers reports whether the grain called name answers an ask sent
// from node.
func sessionAnswers(ctx context.Context, node actor.ActorSystem, name string) bool {
	identity, err := actor.GrainOf[*session](ctx, node, name)
	if err != nil {
		return false
	}

	_, err = node.AskGrain(ctx, identity, new(testpb.TestReply), operationLimit)

	return err == nil
}

// activateSessions activates count grains on node itself.
func activateSessions(ctx context.Context, node actor.ActorSystem, count int) {
	for i := range count {
		if _, err := actor.GrainOf[*session](ctx, node, sessionName(i), actor.WithActivationStrategy(actor.LocalActivation)); err != nil {
			fatal("activate %s: %v", sessionName(i), err)
		}
	}
}

// sessionName returns the name of the grain with the given number.
func sessionName(number int) string {
	return fmt.Sprintf("session-%d", number)
}

// workerName returns the name of the worker with the given number.
func workerName(number int) string {
	return fmt.Sprintf("worker-%d", number)
}

// spawnWorkers spawns count workers on node.
func spawnWorkers(ctx context.Context, node actor.ActorSystem, count int) {
	for i := range count {
		spawn(ctx, node, workerName(i))
	}
}

// spawn spawns a worker called name on node.
func spawn(ctx context.Context, node actor.ActorSystem, name string) {
	if _, err := node.Spawn(ctx, name, new(worker)); err != nil {
		fatal("spawn %s: %v", name, err)
	}
}

// greet asks the actor called name, hosted by another node, from node. It
// retries until the actor is resolved and answers, or peerWait passes.
func greet(ctx context.Context, node actor.ActorSystem, name string) {
	deadline := time.Now().Add(peerWait)

	for time.Now().Before(deadline) {
		lookupCtx, cancel := context.WithTimeout(ctx, operationLimit)
		pid, err := node.ActorOf(lookupCtx, name)
		cancel()

		if err == nil && pid != nil {
			if _, err := actor.Ask(ctx, pid, new(testpb.TestReply), operationLimit); err == nil {
				return
			}
		}

		pause.For(pollInterval)
	}

	fatal("%s did not answer node 3 within %s", name, peerWait)
}

// stop stops node, bounded by stopLimit, and returns what Stop returned.
func stop(ctx context.Context, node actor.ActorSystem) error {
	stopCtx, cancel := context.WithTimeout(ctx, stopLimit)
	err := node.Stop(stopCtx)
	cancel()

	return err
}

// runNode runs a node in a process of its own. The node hosts a greeter and
// idles until the sample kills the process.
func runNode() {
	ctx := context.Background()
	ports := []int{mustEnvInt(discoveryEnv), mustEnvInt(peersEnv), mustEnvInt(remotingEnv)}
	node := startSystem(ctx, os.Getenv(natsEnv), ports, log.DiscardLogger)

	if _, err := node.Spawn(ctx, greeterNameOf(ports[2]), new(greeter), actor.WithRelocationDisabled()); err != nil {
		fatal("spawn the greeter: %v", err)
	}

	// The sample terminates this process with SIGKILL. Stop is never called:
	// a node that stops gracefully leaves membership before it is gone.
	pause.For(nodeLifetime)
}

// startNodeProcess starts this binary again as a cluster node on the given
// discovery, peers and remoting ports.
func startNodeProcess(natsAddress string, ports []int) *exec.Cmd {
	executable, err := os.Executable()
	if err != nil {
		fatal("resolve the executable: %v", err)
	}

	command := exec.Command(executable)
	command.Env = append(
		os.Environ(),
		natsEnv+"="+natsAddress,
		discoveryEnv+"="+strconv.Itoa(ports[0]),
		peersEnv+"="+strconv.Itoa(ports[1]),
		remotingEnv+"="+strconv.Itoa(ports[2]),
	)
	command.Stdout = os.Stdout

	if err := command.Start(); err != nil {
		fatal("start a node process: %v", err)
	}

	return command
}

// kill terminates a node process with SIGKILL and reaps it. A process that has
// ended already is left as it is.
func kill(command *exec.Cmd) {
	_ = command.Process.Kill()
	_ = command.Wait()
}

// startSystem starts a cluster node on the given discovery, peers and remoting
// ports, logging to logger. It discovers its peers through the NATS server at
// natsAddress.
func startSystem(ctx context.Context, natsAddress string, ports []int, logger log.Logger) actor.ActorSystem {
	discovery := nats.NewDiscovery(&nats.Config{
		NatsServer:    "nats://" + natsAddress,
		NatsSubject:   natsSubject,
		Host:          host,
		DiscoveryPort: ports[0],
	})

	clusterConfig := actor.NewClusterConfig().
		WithDiscovery(discovery).
		WithDiscoveryPort(ports[0]).
		WithPeersPort(ports[1]).
		WithKinds(new(worker), new(greeter)).
		WithGrains(new(session)).
		WithMinimumPeersQuorum(1)

	system, err := actor.NewActorSystem(
		systemName,
		actor.WithLogger(logger),
		actor.WithRemote(remote.NewConfig(host, ports[2])),
		actor.WithCluster(clusterConfig),
	)
	if err != nil {
		fatal("construct the actor system: %v", err)
	}

	if err := system.Start(ctx); err != nil {
		fatal("start a node: %v", err)
	}

	return system
}

// newNatsServer starts an in-process NATS server on a free port for the nodes'
// discovery.
func newNatsServer() *natsserver.Server {
	server, err := natsserver.NewServer(&natsserver.Options{Host: host, Port: -1})
	if err != nil {
		fatal("create the NATS server: %v", err)
	}

	go server.Start()

	if !server.ReadyForConnections(2 * time.Second) {
		fatal("the NATS server is not ready for connections")
	}

	return server
}

// waitForPeerCount blocks until system lists count peers, or exits with a
// setup failure after peerWait.
func waitForPeerCount(ctx context.Context, system actor.ActorSystem, count int) {
	deadline := time.Now().Add(peerWait)

	for time.Now().Before(deadline) {
		peers, err := system.Peers(ctx, operationLimit)
		if err == nil && len(peers) == count {
			return
		}

		pause.For(pollInterval)
	}

	fatal("the peer count did not become %d within %s", count, peerWait)
}

// mustEnvInt reads a positive integer from the environment.
func mustEnvInt(name string) int {
	value := os.Getenv(name)
	port, err := strconv.Atoi(value)
	if err != nil || port <= 0 {
		fatal("invalid %s=%q", name, value)
	}

	return port
}

// fatal reports a setup problem that is not the defect under test and exits
// with status 2.
func fatal(format string, args ...any) {
	fmt.Printf("SETUP FAILURE: "+format+"\n", args...)
	os.Exit(2)
}
