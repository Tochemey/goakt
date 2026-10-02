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

// Package main reproduces github.com/Tochemey/goakt/issues/1413 and a second
// defect of the same crash recovery that the investigation turned up.
//
// When a node dies without a shutdown, the cluster leader recovers its actors
// on a goroutine of its own: it waits for the cluster to settle, scans the
// registry for the dead node's actors and recreates them on the surviving
// nodes. An attempt that fails is retried after a five second sleep, four
// attempts in all.
//
// Defect 1, the recovery of a node that came back is lost. The leader caches
// every node's remoting port and the recovery drops the dead node's entry
// when it is done. When the node restarts at the same address while the
// recovery still waits, the recovery gives up, as it should, but drops the
// entry all the same, and by then the entry belongs to the running node. When
// that node dies again the leader cannot tell where its actors lived, every
// attempt fails, and its actors are never recreated.
//
// Defect 2, issue 1413: the recovery only asks whether the system is stopping
// when it wakes from its sleep. Stop clears that flag once it is done, so a
// recovery that slept through the whole shutdown wakes up on a system that
// does not look like it is stopping and carries on against the stopped
// cluster, logging its remaining attempts.
//
// A node has to die without running its shutdown for crash recovery to start,
// so every node is an OS process of its own: this process is node 1, the
// leader, and it starts the same binary again for the other nodes. They join
// one cluster through NATS discovery. Node 2 hosts an actor named worker.
//
// The sample runs four scenarios, each on a fresh three-node cluster. The
// first passes today: it is there so that a fix of the two defects cannot
// break what works.
//
//  1. Node 2 is killed and the leader keeps running. The worker must be
//     recreated on a surviving node.
//  2. Node 2 is killed, restarted at the same address while the leader's
//     recovery waits, and killed again. The worker must be recreated on a
//     surviving node. With defect 1 it never is.
//  3. Node 2 is killed and the leader is stopped while the recovery waits for
//     the cluster to settle, before its first attempt. The leader's log must
//     stay silent once Stop has returned. While it waits, the recovery looks
//     at the stopping flag every 200ms, so with defect 2 this fails whenever
//     Stop takes less than that and passes otherwise.
//  4. The leader is stopped while a recovery sleeps between two attempts. Its
//     log must stay silent once Stop has returned. With defect 2 the recovery
//     keeps logging.
//
// Only a failed attempt puts a recovery to sleep. Scenario 4 gets one without
// leaning on defect 1: a fourth node joins the cluster and dies at once,
// before the leader has announced its join. The leader never cached that
// node's remoting port, so the recovery it starts for the node cannot succeed
// and sleeps between its attempts.
//
// Scenario numbers given on the command line restrict the run to those
// scenarios.
//
// The sample exits with status 1 when a scenario shows a defect, with status
// 2 when a cluster could not be set up or a scenario could not reach the state
// it checks, and with status 0 when every scenario passed.
package main

import (
	"context"
	"encoding/json"
	"errors"
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
	gerrors "github.com/tochemey/goakt/v4/errors"
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	goaktlog "github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
)

const (
	host        = "127.0.0.1"
	systemName  = "issue1413"
	natsSubject = "issue-1413"
	workerName  = "worker"

	// A process started as another node reads its settings from these
	// variables. natsEnv doubles as the switch that tells such a process from
	// the one running the sample.
	natsEnv      = "GOAKT_ISSUE_1413_NATS"
	discoveryEnv = "GOAKT_ISSUE_1413_DISCOVERY"
	peersEnv     = "GOAKT_ISSUE_1413_PEERS"
	remotingEnv  = "GOAKT_ISSUE_1413_REMOTING"
	roleEnv      = "GOAKT_ISSUE_1413_ROLE"
	// verboseEnv, when set to 1, copies the leader's log to stderr.
	verboseEnv = "GOAKT_ISSUE_1413_VERBOSE"

	// What the leader logs when it learns that a node joined (nodeJoined),
	// when it starts crash recovery for a node (recoveryStarted), when the recovery gives up because the node is back
	// (recoveryAbandoned), when an attempt fails for want of the node's
	// remoting port (recoveryNoPort), when the recovery goes to sleep before
	// its next attempt (recoveryAsleep), and when the last attempt failed
	// (recoveryFailed).
	nodeJoined        = "detected node joined event"
	recoveryStarted   = "deriving relocation set from the cluster registry"
	recoveryAbandoned = "the node rejoined the cluster"
	recoveryNoPort    = "no cached remoting port"
	recoveryAsleep    = "retrying in"
	recoveryFailed    = "skipping rebalance"

	// observeWindow is how long the leader's log is read after Stop returned.
	// The recovery makes four attempts five seconds apart, so the last one
	// lands at most fifteen seconds after the first.
	observeWindow = 20 * time.Second

	// nodeLifetime bounds the life of the process of another node, so that it
	// never outlives a sample that exited on a setup failure.
	nodeLifetime = 5 * time.Minute

	// peerWait bounds a membership change and recoveryWait a whole crash
	// recovery: the leader notices a killed node after a few seconds, waits
	// three more for the cluster to settle, and its four attempts span
	// fifteen.
	peerWait       = 20 * time.Second
	recoveryWait   = 45 * time.Second
	stopLimit      = 10 * time.Second
	operationLimit = time.Second
	pollInterval   = 50 * time.Millisecond
)

// role is what a node other than the leader does once it has joined.
type role string

const (
	// bystander: the node only makes up the cluster (node 3).
	bystander role = "bystander"
	// workerHost: the node hosts the worker (node 2).
	workerHost role = "worker-host"
	// shortLived: the node dies as soon as it has joined (node 4).
	shortLived role = "short-lived"
)

// outcome is what a scenario observed.
type outcome int

const (
	// passed: the scenario ran and the defect did not show.
	passed outcome = iota
	// broken: the scenario ran and the defect showed.
	broken
	// notExercised: the scenario could not reach the state it checks.
	notExercised
)

// leaderLog collects the leader's log lines in memory, so that the sample can
// wait for a given line and tell which lines were written after Stop returned.
type leaderLog struct {
	// mu guards lines: the logger writes from the actor system's goroutines
	// while the sample reads from main.
	mu    sync.Mutex
	lines []string
}

// Write implements io.Writer: the logger calls it once per log line.
func (x *leaderLog) Write(line []byte) (int, error) {
	x.mu.Lock()
	x.lines = append(x.lines, strings.TrimSpace(string(line)))
	x.mu.Unlock()

	return len(line), nil
}

// since returns the lines written after the first count ones.
func (x *leaderLog) since(count int) []string {
	x.mu.Lock()
	lines := append([]string(nil), x.lines[count:]...)
	x.mu.Unlock()

	return lines
}

// count returns how many lines contain every one of the given parts.
func (x *leaderLog) count(parts ...string) int {
	count := 0

	for _, line := range x.since(0) {
		if containsAll(line, parts...) {
			count++
		}
	}

	return count
}

// waitFor blocks until at least count lines containing every one of the given
// parts have been written, and reports whether that happened within timeout.
func (x *leaderLog) waitFor(timeout time.Duration, count int, parts ...string) bool {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		if x.count(parts...) >= count {
			return true
		}

		pause.For(pollInterval)
	}

	return false
}

// worker is the actor node 2 hosts. It is relocatable, so crash recovery must
// recreate it on a surviving node when node 2 dies.
type worker struct{}

var _ actor.Actor = (*worker)(nil)

// PreStart implements actor.Actor.
func (*worker) PreStart(*actor.Context) error {
	return nil
}

// Receive implements actor.Actor.
func (*worker) Receive(ctx *actor.ReceiveContext) {
	ctx.Unhandled()
}

// PostStop implements actor.Actor.
func (*worker) PostStop(*actor.Context) error {
	return nil
}

// testCluster is one three-node cluster: node 1, the leader, runs in this
// process, node 2 and node 3 in processes of their own. Node 4 only exists in
// the scenario that starts it.
type testCluster struct {
	natsAddress string
	leader      actor.ActorSystem
	// leaderStopped is set once a scenario has stopped the leader itself.
	leaderStopped bool
	// logs receives everything the leader logs.
	logs *leaderLog
	// node2Ports and node4Ports are the discovery, peers and remoting ports
	// of these nodes: node 2 is restarted at the same address, node 4 is
	// started by a scenario.
	node2Ports []int
	node4Ports []int
	node2      *exec.Cmd
	node3      *exec.Cmd
	node4      *exec.Cmd
	// node2Peers and node4Peers are how the leader's log names these nodes,
	// and node2Remoting is the address the worker has while node 2 hosts it.
	node2Peers    string
	node4Peers    string
	node2Remoting string
}

// scenario is one check of the crash recovery, run on a cluster of its own.
type scenario struct {
	title string
	run   func(ctx context.Context, cluster *testCluster) outcome
}

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
		{title: "a node crashes; the leader keeps running", run: recoversCrash},
		{title: "a node crashes, rejoins and crashes again; the leader keeps running", run: recoversCrashAfterRejoin},
		{title: "the leader is stopped while a crash recovery waits for the cluster to settle", run: silentWhenStoppedWaiting},
		{title: "the leader is stopped while a crash recovery sleeps between two attempts", run: silentWhenStoppedAsleep},
	}

	results := make(map[outcome]int)

	for i, item := range scenarios {
		if !selected(i+1, len(scenarios)) {
			continue
		}

		fmt.Printf("scenario %d: %s\n", i+1, item.title)

		cluster := startCluster(ctx, server.Addr().String())
		results[item.run(ctx, cluster)]++
		cluster.shutdown(ctx)

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

		chosen = chosen || value == number
	}

	return chosen
}

// recoversCrash runs scenario 1: node 2 is killed and the leader must
// recreate the worker on a surviving node.
func recoversCrash(ctx context.Context, cluster *testCluster) outcome {
	// SIGKILL: the node never runs Stop, so it leaves no snapshot behind and
	// the leader has to recover it from the registry.
	kill(cluster.node2)
	fmt.Println("node 2 killed")

	return cluster.awaitWorkerRecreated(ctx)
}

// recoversCrashAfterRejoin runs scenario 2: node 2 is killed, restarted at
// the same address while the leader's crash recovery still waits for the
// cluster to settle, and killed again once the recovery has seen it back. The
// leader must recreate the worker on a surviving node.
func recoversCrashAfterRejoin(ctx context.Context, cluster *testCluster) outcome {
	cluster.crashNode2()

	cluster.node2 = startNodeProcess(cluster.natsAddress, cluster.node2Ports, workerHost)
	cluster.waitForNode2(ctx, 2)

	if !cluster.logs.waitFor(recoveryWait, 1, recoveryAbandoned, cluster.node2Peers) {
		fatal("the crash recovery did not see node 2 back within %s", recoveryWait)
	}

	fmt.Println("node 2 restarted at the same address and hosts the worker; the crash recovery saw it back and gave up")

	kill(cluster.node2)
	fmt.Println("node 2 killed again")

	return cluster.awaitWorkerRecreated(ctx)
}

// silentWhenStoppedWaiting runs scenario 3: the leader is stopped while the
// recovery of node 2 waits for the cluster to settle, before its first
// attempt.
func silentWhenStoppedWaiting(ctx context.Context, cluster *testCluster) outcome {
	cluster.crashNode2()

	return cluster.stopAndListen(ctx, cluster.node2Peers)
}

// silentWhenStoppedAsleep runs scenario 4: the leader is stopped while a
// recovery sleeps between two attempts. The recovery is that of node 4, which
// joins the cluster and dies before the leader has announced its join: the
// leader never cached its remoting port, so the first attempt fails.
func silentWhenStoppedAsleep(ctx context.Context, cluster *testCluster) outcome {
	cluster.node4 = startNodeProcess(cluster.natsAddress, cluster.node4Ports, shortLived)
	fmt.Println("node 4 started: it joins the cluster and dies at once")

	if !cluster.logs.waitFor(recoveryWait, 1, recoveryAsleep, cluster.node4Peers) {
		fmt.Printf("NOT EXERCISED: no crash recovery of node 4 went to sleep within %s, so stopping the leader in its sleep could not be tried\n", recoveryWait)
		return notExercised
	}

	fmt.Println("the first attempt of its crash recovery failed; the recovery sleeps 5s before the next")

	return cluster.stopAndListen(ctx, cluster.node4Peers)
}

// startCluster starts a three-node cluster and waits until node 2 hosts the
// worker. Node 1 starts first: the oldest member of a cluster is its leader,
// and only the leader runs crash recovery.
func startCluster(ctx context.Context, natsAddress string) *testCluster {
	ports := inet.Get(12)

	cluster := &testCluster{
		natsAddress:   natsAddress,
		logs:          new(leaderLog),
		node2Ports:    ports[3:6],
		node4Ports:    ports[9:12],
		node2Peers:    fmt.Sprintf("%s:%d", host, ports[4]),
		node4Peers:    fmt.Sprintf("%s:%d", host, ports[10]),
		node2Remoting: fmt.Sprintf("%s:%d", host, ports[5]),
	}

	cluster.leader = newSystem(natsAddress, ports[0], ports[1], ports[2], leaderLogger(cluster.logs))
	if err := cluster.leader.Start(ctx); err != nil {
		fatal("start the leader: %v", err)
	}

	cluster.node2 = startNodeProcess(natsAddress, cluster.node2Ports, workerHost)
	cluster.node3 = startNodeProcess(natsAddress, ports[6:9], bystander)

	waitForPeerCount(ctx, cluster.leader, 2, peerWait)
	cluster.waitForNode2(ctx, 1)

	isLeader, err := cluster.leader.IsLeader(ctx)
	if err != nil || !isLeader {
		fatal("node 1 is not the leader (isLeader=%t err=%v)", isLeader, err)
	}

	fmt.Println("three nodes are up; node 1 is the leader and node 2 hosts the worker")

	return cluster
}

// crashNode2 kills node 2 and waits until the leader has started its crash
// recovery.
func (x *testCluster) crashNode2() {
	// SIGKILL: the node never runs Stop, so it leaves no snapshot behind and
	// the leader has to recover it from the registry.
	kill(x.node2)

	if !x.logs.waitFor(recoveryWait, 1, recoveryStarted, x.node2Peers) {
		fatal("the leader did not start crash recovery for node 2 within %s", recoveryWait)
	}

	fmt.Println("node 2 killed; the leader started its crash recovery")
}

// awaitWorkerRecreated waits for the leader's crash recovery of node 2 to
// recreate the worker on a surviving node, and reports what happened.
func (x *testCluster) awaitWorkerRecreated(ctx context.Context) outcome {
	deadline := time.Now().Add(recoveryWait)

	for time.Now().Before(deadline) && x.logs.count(recoveryFailed, x.node2Peers) == 0 {
		if x.workerRelocated(ctx) {
			fmt.Println("OK: the leader recreated the worker on a surviving node")
			return passed
		}

		pause.For(pollInterval)
	}

	for _, line := range x.logs.since(0) {
		if containsAll(line, recoveryFailed, x.node2Peers) {
			fmt.Printf("  leader: %s\n", message(line))
		}
	}

	fmt.Printf("  leader: %d attempt(s) failed with %q\n", x.logs.count(recoveryNoPort, x.node2Peers), recoveryNoPort)
	fmt.Println("REPRO (broken): the worker was not recreated on a surviving node")

	return broken
}

// stopAndListen stops the leader, keeps reading its log for observeWindow and
// reports whether it stayed silent about the departed node, which the log
// names by its peers address. Once the leader has stopped, only a crash
// recovery still logs about that node.
func (x *testCluster) stopAndListen(ctx context.Context, departed string) outcome {
	elapsed := x.stopLeader(ctx)
	stopped := len(x.logs.since(0))
	fmt.Printf("leader.Stop returned after %s; reading the leader's log for %s\n", elapsed.Round(time.Millisecond), observeWindow)

	pause.For(observeWindow)

	late := 0

	for _, line := range x.logs.since(stopped) {
		if strings.Contains(line, departed) {
			fmt.Printf("  after Stop: %s\n", message(line))
			late++
		}
	}

	if late > 0 {
		fmt.Printf("REPRO (broken): the crash recovery logged %d line(s) after Stop returned\n", late)
		return broken
	}

	fmt.Println("OK: the crash recovery stayed silent after Stop returned")
	return passed
}

// shutdown ends a scenario: it stops the leader unless the scenario did, and
// kills the other nodes.
func (x *testCluster) shutdown(ctx context.Context) {
	if !x.leaderStopped {
		x.stopLeader(ctx)
	}

	for _, node := range []*exec.Cmd{x.node2, x.node3, x.node4} {
		if node != nil {
			kill(node)
		}
	}
}

// waitForNode2 blocks until the leader has learned of node 2 joining for the
// joins-th time and resolves the worker to it. The leader announces a join a
// moment after the node shows up among its peers, and only then caches the
// node's remoting port: a node killed before that was never known to the
// leader, which is not what the sample is about.
func (x *testCluster) waitForNode2(ctx context.Context, joins int) {
	if !x.logs.waitFor(peerWait, joins, nodeJoined, x.node2Peers) {
		fatal("the leader did not learn of node 2 joining within %s", peerWait)
	}

	deadline := time.Now().Add(peerWait)

	for time.Now().Before(deadline) {
		if id, ok := x.workerID(ctx); ok && strings.Contains(id, x.node2Remoting) {
			return
		}

		pause.For(pollInterval)
	}

	fatal("the worker did not show up on node 2 within %s", peerWait)
}

// workerRelocated reports whether the leader resolves the worker to a node
// other than node 2.
func (x *testCluster) workerRelocated(ctx context.Context) bool {
	id, ok := x.workerID(ctx)
	return ok && !strings.Contains(id, x.node2Remoting)
}

// workerID returns the ID the leader resolves the worker to, which carries the
// address of the node hosting it.
func (x *testCluster) workerID(ctx context.Context) (string, bool) {
	lookupCtx, cancel := context.WithTimeout(ctx, operationLimit)
	pid, err := x.leader.ActorOf(lookupCtx, workerName)
	cancel()

	if err != nil || pid == nil {
		return "", false
	}

	return pid.ID(), true
}

// stopLeader stops node 1 and returns how long Stop took.
func (x *testCluster) stopLeader(ctx context.Context) time.Duration {
	stopCtx, cancel := context.WithTimeout(ctx, stopLimit)
	started := time.Now()
	err := x.leader.Stop(stopCtx)
	cancel()

	x.leaderStopped = true

	if err != nil {
		fatal("stop the leader: %v", err)
	}

	return time.Since(started)
}

// runNode runs a node other than the leader. Node 4 dies as soon as it has
// joined; the others idle until the sample kills the process.
func runNode() {
	ctx := context.Background()
	node := newSystem(os.Getenv(natsEnv), mustEnvInt(discoveryEnv), mustEnvInt(peersEnv), mustEnvInt(remotingEnv), goaktlog.DiscardLogger)

	if err := node.Start(ctx); err != nil {
		fatal("start a node: %v", err)
	}

	switch role(os.Getenv(roleEnv)) {
	case shortLived:
		// The process ends without Stop, which the cluster sees as a crash.
		os.Exit(0)
	case workerHost:
		// A node that restarts at the same address recreates the relocatable
		// actors of its previous run by itself, so the worker may be there
		// already.
		if _, err := node.Spawn(ctx, workerName, new(worker)); err != nil && !errors.Is(err, gerrors.ErrActorAlreadyExists) {
			fatal("spawn the worker: %v", err)
		}
	}

	// The sample terminates this process with SIGKILL. Stop is never called: a
	// graceful stop leaves a snapshot behind and no crash recovery would run.
	pause.For(nodeLifetime)
}

// startNodeProcess starts this binary again as a cluster node with the given
// role, on the given discovery, peers and remoting ports.
func startNodeProcess(natsAddress string, ports []int, nodeRole role) *exec.Cmd {
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
		roleEnv+"="+string(nodeRole),
	)

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

// newSystem builds a cluster node that discovers its peers through the NATS
// server at natsAddress. The replica count of 2 keeps the worker's registry
// record on a surviving node once node 2 is gone, so that crash recovery has
// something to recover.
func newSystem(natsAddress string, discoveryPort, peersPort, remotingPort int, logger goaktlog.Logger) actor.ActorSystem {
	discovery := nats.NewDiscovery(&nats.Config{
		NatsServer:    "nats://" + natsAddress,
		NatsSubject:   natsSubject,
		Host:          host,
		DiscoveryPort: discoveryPort,
	})

	clusterConfig := actor.NewClusterConfig().
		WithDiscovery(discovery).
		WithDiscoveryPort(discoveryPort).
		WithPeersPort(peersPort).
		WithKinds(new(worker)).
		WithReplicaCount(2).
		WithMinimumPeersQuorum(1)

	system, err := actor.NewActorSystem(
		systemName,
		actor.WithLogger(logger),
		actor.WithRemote(remote.NewConfig(host, remotingPort)),
		actor.WithCluster(clusterConfig),
	)
	if err != nil {
		fatal("construct the actor system: %v", err)
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

// leaderLogger returns the leader's logger. It writes into logs, and to stderr
// as well when verboseEnv is set.
func leaderLogger(logs *leaderLog) goaktlog.Logger {
	if os.Getenv(verboseEnv) == "1" {
		return goaktlog.NewZap(goaktlog.InfoLevel, logs, os.Stderr)
	}

	return goaktlog.NewZap(goaktlog.InfoLevel, logs)
}

// waitForPeerCount blocks until membership reports count peers.
func waitForPeerCount(ctx context.Context, system actor.ActorSystem, count int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		peers, err := system.Peers(ctx, operationLimit)
		if err == nil && len(peers) == count {
			return
		}

		pause.For(pollInterval)
	}

	fatal("the peer count did not become %d within %s", count, timeout)
}

// message returns the level and the text of a log line, which the logger
// writes as a JSON object, or the line itself when it is not one.
func message(line string) string {
	var entry struct {
		Level string `json:"level"`
		Msg   string `json:"msg"`
	}

	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		return line
	}

	return entry.Level + ": " + entry.Msg
}

// containsAll reports whether line contains every one of parts.
func containsAll(line string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(line, part) {
			return false
		}
	}

	return true
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

// fatal reports a setup problem that is not a defect under test and exits with
// status 2.
func fatal(format string, args ...any) {
	fmt.Printf("SETUP FAILURE: "+format+"\n", args...)
	os.Exit(2)
}
