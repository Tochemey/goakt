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

// Load test for https://github.com/Tochemey/goakt/issues/1435
//
// AskGrain and TellGrain release the registry record of a grain owner that is
// stopping or has left the cluster. When the owner refused the message, the
// same call delivers it to the grain re-activated on the calling node. Callers
// therefore need no GrainOf call and no retry around a rolling restart.
//
// This sample puts a cluster through a rolling restart under load and checks
// that the feature holds:
//
//   - round 1: three nodes, grains spread over them. Callers send asks, tells
//     and one-way tells without pause while node 3 goes away.
//   - a replacement node joins and more grains are spread over the cluster.
//   - round 2: the same load while node 2 goes away.
//
// It has two modes:
//
//   - graceful (the default): every node runs in this process and a node goes
//     away through Stop. Callers run on every node that stays.
//   - crash: node 1 runs in this process and every other node in a process of
//     its own, which is killed with SIGKILL. Callers run on node 1.
//
// The grains write every handler run and every activation to a ledger: a map
// in graceful mode, and one file per node in crash mode, written before the
// grain replies, so the runs of a killed node are not lost. After each round
// the sample sends to every grain with a bare AskGrain, never GrainOf.
//
// It fails when:
//
//   - a message ran more than once;
//   - an ask or an acknowledged tell that returned no error did not run
//     exactly once;
//   - a grain is active on two live nodes;
//   - a caller was handed the refusal of a stopping node;
//   - a call that started after the node had left the cluster failed. In
//     crash mode a registry read that times out while the registry converges
//     after the loss is reported, not counted: the registry documents it as
//     retryable (ErrClusterRegistryTimeout);
//   - a grain could not be reached with AskGrain after the node left.
//
// Run it with:
//
//	go run ./playground/issue-1435
//	go run ./playground/issue-1435 crash
//
// Exit status 0 means every check held, 1 means one did not, and 2 means the
// setup failed.
package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery/nats"
	gerrors "github.com/tochemey/goakt/v4/errors"
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
)

const (
	// host is the address every node binds to.
	host = "127.0.0.1"
	// grainsPerRound is the number of grains activated before each round.
	grainsPerRound = 150
	// callersPerNode is the number of goroutines that send from each node
	// that carries callers.
	callersPerNode = 16
	// askTimeout bounds one ask.
	askTimeout = 2 * time.Second
	// loadBeforeStop is how long the load runs before a node goes away.
	loadBeforeStop = time.Second
	// loadAfterStop is how long the load keeps running once the node has left
	// the cluster.
	loadAfterStop = 2 * time.Second
	// reachAttempts is how many asks a grain gets to answer after a round.
	reachAttempts = 3
	// crashMode is the argument that selects the crash mode.
	crashMode = "crash"
	// nodeMode is the argument a node process is started with.
	nodeMode = "node"
	// readyLine is what a node process prints once its node has started.
	readyLine = "ready"
	// runRecordSize is the size of one record of a runs file: a message ID.
	runRecordSize = 8
)

// sendMode is the way a caller sends one message.
type sendMode int

const (
	// modeAsk sends with AskGrain.
	modeAsk sendMode = iota
	// modeTell sends with TellGrain and waits for the acknowledgement.
	modeTell
	// modeOneWay sends with TellGrain and WithOneWay.
	modeOneWay
)

// askWork is the request of an ask. ID is unique per message.
type askWork struct {
	ID int64
}

// tellWork is the message of a tell. ID is unique per message.
type tellWork struct {
	ID int64
}

// workDone is the reply to an askWork. ID is the ID of the request.
type workDone struct {
	ID int64
}

// loadGrain records every message it handles in the ledger.
type loadGrain struct{}

// ledger is where the grains record every handler run and every activation.
//
// In graceful mode all nodes share this process and the ledger is kept in
// memory. In crash mode every node, in its own process, appends to two files
// of its own in dir: its runs and its activations. A record is in the file
// once the write returns, so it survives the kill of the process, and the
// sample reads the files of every node back with load.
type ledger struct {
	mu sync.Mutex
	// runs counts the handler runs of each message ID.
	runs map[int64]int
	// active counts the activations of each grain that have not been
	// deactivated yet, on the live nodes.
	active map[string]int
	// overlaps counts the activations that started while the same grain was
	// still active on another live node.
	overlaps int
	// dir is the directory of the ledger files. It is empty in graceful mode.
	dir string
	// runsFile and eventsFile are the files this process appends to in crash
	// mode.
	runsFile   *os.File
	eventsFile *os.File
}

// outcomes is what the callers of one round observed.
type outcomes struct {
	mu sync.Mutex
	// sent counts the messages sent, by mode.
	sent map[sendMode]int
	// acknowledged holds the IDs of the asks and acknowledged tells that
	// returned no error: each must have run exactly once.
	acknowledged []int64
	// refused counts the calls that returned the refusal of a stopping node.
	refused int
	// timeouts counts the calls that timed out.
	timeouts int
	// failures counts the other errors by text.
	failures map[string]int
	// nodeLeft is set once the node that goes away has left the cluster.
	nodeLeft atomic.Bool
	// failedAfterLeft counts the failed calls that started after that, by
	// text.
	failedAfterLeft map[string]int
	// crash tells that the node is killed. The registry then needs a moment
	// to converge after the cluster has noticed the loss.
	crash bool
	// converging counts, in crash mode, the calls that started after the
	// node had left and ran into the registry still converging: a registry
	// read that timed out (ErrClusterRegistryTimeout), or an ask that timed
	// out behind one.
	converging int
}

// node is one node of the cluster: an actor system of this process, or a node
// process.
type node struct {
	// label names the node in the ledger files.
	label string
	// system is the actor system of a node that runs in this process.
	system actor.ActorSystem
	// process is the process of a node that runs on its own.
	process *exec.Cmd
}

// cluster starts the nodes and holds what they share.
type cluster struct {
	natsAddress string
	// ports hands out free ports, three per node.
	ports []int
	// crash tells that the nodes other than node 1 run in their own process.
	crash bool
	// started counts the nodes started so far.
	started int
}

var (
	// book is the ledger the grains of this process write to.
	book = &ledger{runs: make(map[int64]int), active: make(map[string]int)}
	// nextID hands out the message IDs.
	nextID atomic.Int64
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == nodeMode {
		runNode(os.Args[2:])
		return
	}

	ctx := context.Background()
	srv := newNatsServer()
	nodes := &cluster{natsAddress: srv.Addr().String(), ports: inet.Get(12), crash: len(os.Args) > 1 && os.Args[1] == crashMode}

	if nodes.crash {
		dir, err := os.MkdirTemp("", "issue-1435-ledger")
		if err != nil {
			fail("creating the ledger directory: %v", err)
		}

		book.dir = dir
	}

	node1 := nodes.startNode(ctx)
	node2 := nodes.startNode(ctx)
	node3 := nodes.startNode(ctx)
	waitForPeers(ctx, node1.system, 2)

	failed := false

	// Round 1: node 3 goes away under load.
	grains := activateGrains(ctx, node1.system, "round1")
	fmt.Printf("round 1: %d grains spread over three nodes, node 3 %s under load\n", len(grains), nodes.verb())
	failed = runRound(ctx, []*node{node1, node2}, node3, grains) || failed

	// A replacement node joins, and more grains are spread over the cluster.
	node4 := nodes.startNode(ctx)
	waitForPeers(ctx, node1.system, 2)
	grains = append(grains, activateGrains(ctx, node1.system, "round2")...)

	// Round 2: node 2 goes away under load.
	fmt.Printf("\nround 2: %d grains, a replacement node joined, node 2 %s under load\n", len(grains), nodes.verb())
	failed = runRound(ctx, []*node{node1, node4}, node2, grains) || failed

	failed = checkLedger([]*node{node1, node4}) || failed

	node4.stop(ctx, false)
	_ = node1.system.Stop(ctx)
	srv.Shutdown()

	if book.dir != "" {
		_ = os.RemoveAll(book.dir)
	}

	if failed {
		fmt.Println("\nFAIL: a rolling restart under load broke at least one guarantee")
		os.Exit(1)
	}

	fmt.Println("\nPASS: through a rolling restart under load no message ran twice, no acknowledged message was lost, no grain ran on two nodes, no caller saw a refusal or a failure once the node had left, and every grain stayed reachable without GrainOf")
}

// runRound sends load to grains from the nodes of staying that run in this
// process while leaving goes away, then checks what the callers observed and
// that every grain still answers. It reports whether a check failed.
func runRound(ctx context.Context, staying []*node, leaving *node, grains []*actor.GrainIdentity) bool {
	seen := &outcomes{sent: make(map[sendMode]int), failures: make(map[string]int), failedAfterLeft: make(map[string]int), crash: leaving.process != nil}
	stop := make(chan types.Unit)
	var wg sync.WaitGroup

	callerNodes := 0
	for _, caller := range staying {
		if caller.system == nil {
			continue
		}

		callerNodes++
		for range callersPerNode {
			wg.Go(func() { sendUntil(ctx, caller.system, grains, seen, stop) })
		}
	}

	fmt.Printf("  %d callers on each of %d node(s)\n", callersPerNode, callerNodes)
	pause.For(loadBeforeStop)

	started := time.Now()
	leaving.stop(ctx, true)
	waitForPeers(ctx, staying[0].system, len(staying)-1)
	seen.nodeLeft.Store(true)
	fmt.Printf("  the node left the cluster in %s\n", time.Since(started).Round(time.Millisecond))

	pause.For(loadAfterStop)
	close(stop)
	wg.Wait()

	failed := seen.report()
	return reachEveryGrain(ctx, staying[0].system, grains) || failed
}

// sendUntil sends one message after the other from caller, each to the next
// grain and in the next mode, until stop is closed, and records every outcome
// in seen.
func sendUntil(ctx context.Context, caller actor.ActorSystem, grains []*actor.GrainIdentity, seen *outcomes, stop <-chan types.Unit) {
	for {
		select {
		case <-stop:
			return
		default:
		}

		// the message ID picks the mode and the grain, so every grain gets
		// every kind of message
		id := nextID.Add(1)
		mode := sendMode(id % 3)
		identity := grains[(id/3)%int64(len(grains))]
		afterLeft := seen.nodeLeft.Load()

		var err error
		switch mode {
		case modeAsk:
			_, err = caller.AskGrain(ctx, identity, &askWork{ID: id}, askTimeout)
		case modeTell:
			err = caller.TellGrain(ctx, identity, &tellWork{ID: id})
		default:
			err = caller.TellGrain(ctx, identity, &tellWork{ID: id}, actor.WithOneWay())
		}

		seen.record(mode, id, afterLeft, err)
	}
}

// record stores the outcome of one call. afterLeft tells that the call
// started once the node that goes away had left the cluster.
func (x *outcomes) record(mode sendMode, id int64, afterLeft bool, err error) {
	x.mu.Lock()
	x.sent[mode]++

	if err != nil && afterLeft {
		if x.crash && (errors.Is(err, gerrors.ErrClusterRegistryTimeout) || errors.Is(err, gerrors.ErrRequestTimeout)) {
			x.converging++
		} else {
			x.failedAfterLeft[firstLine(err.Error())]++
		}
	}

	switch {
	case err == nil:
		if mode != modeOneWay {
			x.acknowledged = append(x.acknowledged, id)
		}
	case errors.Is(err, gerrors.ErrSystemShuttingDown), errors.Is(err, gerrors.ErrRemotingDisabled):
		x.refused++
	case errors.Is(err, gerrors.ErrRequestTimeout):
		x.timeouts++
	default:
		x.failures[err.Error()]++
	}

	x.mu.Unlock()
}

// report prints what the callers observed during a round and reports whether
// a check failed: a refusal reached a caller, a call failed once the node had
// left, or an acknowledged message did not run exactly once.
func (x *outcomes) report() bool {
	book.load()
	fmt.Printf("  sent %d asks, %d tells and %d one-way tells\n", x.sent[modeAsk], x.sent[modeTell], x.sent[modeOneWay])

	failed := false

	lost := 0
	for _, id := range x.acknowledged {
		if book.runsOf(id) != 1 {
			lost++
		}
	}

	if lost > 0 {
		fmt.Printf("  BUG: %d acknowledged messages did not run exactly once\n", lost)
		failed = true
	} else {
		fmt.Printf("  OK: the %d acknowledged asks and tells each ran exactly once\n", len(x.acknowledged))
	}

	if x.refused > 0 {
		fmt.Printf("  BUG: %d calls were handed the refusal of the stopping node\n", x.refused)
		failed = true
	} else {
		fmt.Println("  OK: no caller was handed the refusal of the stopping node")
	}

	if len(x.failedAfterLeft) > 0 {
		fmt.Printf("  BUG: %d calls that started after the node had left the cluster failed\n", countOf(x.failedAfterLeft))
		printCounts(x.failedAfterLeft)
		failed = true
	} else {
		fmt.Println("  OK: no call that started after the node had left the cluster failed")
	}

	// A message in flight when its owner goes away may have run, so its call
	// reports the failure instead of sending again. Until the cluster notices
	// that a killed node is gone, calls to its grains fail as well: the node
	// may only be unreachable, so its grains stay where they are.
	if x.converging > 0 {
		fmt.Printf("  %d calls that started after the node had left ran into the registry still converging (retryable)\n", x.converging)
	}

	fmt.Printf("  over the round, %d calls timed out and %d failed otherwise\n", x.timeouts, countOf(x.failures))
	printCounts(x.failures)

	return failed
}

// reachEveryGrain sends a bare AskGrain to every grain from caller, without
// GrainOf, and reports whether a grain did not answer within reachAttempts.
func reachEveryGrain(ctx context.Context, caller actor.ActorSystem, grains []*actor.GrainIdentity) bool {
	unreachable, retried := 0, 0

	for _, identity := range grains {
		answered := false

		for attempt := 1; attempt <= reachAttempts; attempt++ {
			if _, err := caller.AskGrain(ctx, identity, &askWork{ID: nextID.Add(1)}, askTimeout); err == nil {
				answered = true
				break
			}

			retried++
		}

		if !answered {
			unreachable++
		}
	}

	if unreachable > 0 {
		fmt.Printf("  BUG: %d of %d grains did not answer AskGrain after the node left\n", unreachable, len(grains))
		return true
	}

	fmt.Printf("  OK: all %d grains answered AskGrain without GrainOf (%d asks had to be repeated)\n", len(grains), retried)
	return false
}

// checkLedger checks what the grains recorded over the whole run and reports
// whether a check failed: a message ran more than once, or a grain is active
// on two of the live nodes.
func checkLedger(live []*node) bool {
	book.load()
	book.loadActivations(live)

	book.mu.Lock()
	total, repeated := len(book.runs), 0

	for _, runs := range book.runs {
		if runs > 1 {
			repeated++
		}
	}

	overlaps := book.overlaps
	book.mu.Unlock()

	fmt.Println("\nwhole run:")
	failed := false

	if repeated > 0 {
		fmt.Printf("  BUG: %d of %d messages ran more than once\n", repeated, total)
		failed = true
	} else {
		fmt.Printf("  OK: none of the %d messages that ran did so more than once\n", total)
	}

	if overlaps > 0 {
		fmt.Printf("  BUG: %d grains were active on two live nodes at the same time\n", overlaps)
		failed = true
	} else {
		fmt.Println("  OK: no grain was active on two live nodes at the same time")
	}

	return failed
}

// open opens the ledger files of the node label in dir, for a node of crash
// mode. Every record is appended with one write.
func (x *ledger) open(dir, label string) {
	var err error

	x.dir = dir
	if x.runsFile, err = os.OpenFile(filepath.Join(dir, label+".runs"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err != nil {
		fail("opening the runs file: %v", err)
	}

	if x.eventsFile, err = os.OpenFile(filepath.Join(dir, label+".events"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err != nil {
		fail("opening the activations file: %v", err)
	}
}

// ran records one handler run of the message id.
func (x *ledger) ran(id int64) {
	if x.runsFile != nil {
		var record [runRecordSize]byte
		binary.LittleEndian.PutUint64(record[:], uint64(id))
		_, _ = x.runsFile.Write(record[:])
		return
	}

	x.mu.Lock()
	x.runs[id]++
	x.mu.Unlock()
}

// runsOf returns how many times the message id ran.
func (x *ledger) runsOf(id int64) int {
	x.mu.Lock()
	runs := x.runs[id]
	x.mu.Unlock()
	return runs
}

// activated records an activation of the grain name. In graceful mode it also
// counts an overlap when the grain is still active elsewhere.
func (x *ledger) activated(name string) {
	if x.eventsFile != nil {
		_, _ = x.eventsFile.WriteString("A " + name + "\n")
		return
	}

	x.mu.Lock()
	x.active[name]++

	if x.active[name] > 1 {
		x.overlaps++
	}

	x.mu.Unlock()
}

// deactivated records the deactivation of the grain name.
func (x *ledger) deactivated(name string) {
	if x.eventsFile != nil {
		_, _ = x.eventsFile.WriteString("D " + name + "\n")
		return
	}

	x.mu.Lock()
	x.active[name]--
	x.mu.Unlock()
}

// load reads the runs of every node back from the ledger files. It does
// nothing in graceful mode, where the runs are already in memory.
func (x *ledger) load() {
	if x.dir == "" {
		return
	}

	files, err := filepath.Glob(filepath.Join(x.dir, "*.runs"))
	if err != nil {
		fail("listing the runs files: %v", err)
	}

	runs := make(map[int64]int)

	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			fail("reading a runs file: %v", err)
		}

		for ; len(content) >= runRecordSize; content = content[runRecordSize:] {
			runs[int64(binary.LittleEndian.Uint64(content))]++
		}
	}

	x.mu.Lock()
	x.runs = runs
	x.mu.Unlock()
}

// loadActivations reads the activations of the live nodes back from the ledger
// files and counts the grains that are active on more than one of them. A
// killed node records no deactivation, so only live nodes are counted. It does
// nothing in graceful mode, where overlaps are counted as they happen.
func (x *ledger) loadActivations(live []*node) {
	if x.dir == "" {
		return
	}

	activeOn := make(map[string]int)

	for _, node := range live {
		content, err := os.ReadFile(filepath.Join(x.dir, node.label+".events"))
		if err != nil {
			fail("reading an activations file: %v", err)
		}

		active := make(map[string]int)

		for line := range strings.Lines(string(content)) {
			event, name, ok := strings.Cut(strings.TrimSpace(line), " ")
			if !ok {
				continue
			}

			if event == "A" {
				active[name]++
			} else {
				active[name]--
			}
		}

		for name, count := range active {
			if count > 0 {
				activeOn[name]++
			}
		}
	}

	overlaps := 0
	for _, nodes := range activeOn {
		if nodes > 1 {
			overlaps++
		}
	}

	x.mu.Lock()
	x.overlaps = overlaps
	x.mu.Unlock()
}

// OnActivate records the activation in the ledger.
func (x *loadGrain) OnActivate(_ context.Context, props *actor.GrainProps) error {
	book.activated(props.Identity().Name())
	return nil
}

// OnReceive records the run of the message in the ledger, then answers an ask
// and acknowledges a tell.
func (x *loadGrain) OnReceive(ctx *actor.GrainContext) {
	switch message := ctx.Message().(type) {
	case *askWork:
		book.ran(message.ID)
		ctx.Response(&workDone{ID: message.ID})
	case *tellWork:
		book.ran(message.ID)
		ctx.NoErr()
	default:
		ctx.Unhandled()
	}
}

// OnDeactivate records the deactivation in the ledger.
func (x *loadGrain) OnDeactivate(_ context.Context, props *actor.GrainProps) error {
	book.deactivated(props.Identity().Name())
	return nil
}

// activateGrains activates grainsPerRound grains from system, spread over the
// cluster round-robin, and returns their identities. This is the only place
// the sample calls GrainOf.
func activateGrains(ctx context.Context, system actor.ActorSystem, prefix string) []*actor.GrainIdentity {
	identities := make([]*actor.GrainIdentity, 0, grainsPerRound)

	for i := range grainsPerRound {
		identity, err := actor.GrainOf[*loadGrain](ctx, system, fmt.Sprintf("%s-grain-%d", prefix, i), actor.WithActivationStrategy(actor.RoundRobinActivation))
		if err != nil {
			fail("activating a grain: %v", err)
		}

		identities = append(identities, identity)
	}

	return identities
}

// verb says how a node goes away in the mode of the cluster.
func (x *cluster) verb() string {
	if x.crash {
		return "is killed"
	}

	return "stops"
}

// startNode starts the next node of the cluster on the next three free ports.
// In crash mode every node but the first runs in a process of its own.
func (x *cluster) startNode(ctx context.Context) *node {
	discoveryPort, peersPort, remotingPort := x.ports[0], x.ports[1], x.ports[2]
	x.ports = x.ports[3:]
	x.started++
	label := fmt.Sprintf("node%d", x.started)

	if !x.crash {
		return &node{label: label, system: startSystem(ctx, x.natsAddress, discoveryPort, peersPort, remotingPort)}
	}

	if x.started == 1 {
		book.open(book.dir, label)
		return &node{label: label, system: startSystem(ctx, x.natsAddress, discoveryPort, peersPort, remotingPort)}
	}

	return &node{label: label, process: startNodeProcess(x.natsAddress, discoveryPort, peersPort, remotingPort, book.dir, label)}
}

// stop makes the node go away. A node of this process is stopped. A node
// process is killed when crash is true, and asked to stop otherwise.
func (x *node) stop(ctx context.Context, crash bool) {
	if x.system != nil {
		if err := x.system.Stop(ctx); err != nil {
			fmt.Printf("  the node's Stop returned: %v\n", err)
		}

		return
	}

	signal := syscall.SIGTERM
	if crash {
		signal = syscall.SIGKILL
	}

	_ = x.process.Process.Signal(signal)
	_ = x.process.Wait()
}

// startNodeProcess starts a node in a process of its own and waits until the
// node has started.
func startNodeProcess(natsAddress string, discoveryPort, peersPort, remotingPort int, ledgerDir, label string) *exec.Cmd {
	binary, err := os.Executable()
	if err != nil {
		fail("locating the sample binary: %v", err)
	}

	process := exec.Command(binary, nodeMode, natsAddress, strconv.Itoa(discoveryPort), strconv.Itoa(peersPort), strconv.Itoa(remotingPort), ledgerDir, label)
	process.Stderr = os.Stderr

	output, err := process.StdoutPipe()
	if err != nil {
		fail("reading the output of a node process: %v", err)
	}

	if err := process.Start(); err != nil {
		fail("starting a node process: %v", err)
	}

	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != readyLine {
		fail("the node process %s did not start: %q %v", label, line, err)
	}

	return process
}

// runNode is the main of a node process: it starts a node that records to its
// own ledger files, says it is ready, and stops the node when the process is
// asked to stop. A killed process never gets there.
func runNode(args []string) {
	if len(args) != 6 {
		fail("a node process takes 6 arguments, got %d", len(args))
	}

	ports := make([]int, 3)
	for i := range ports {
		port, err := strconv.Atoi(args[i+1])
		if err != nil {
			fail("reading a port: %v", err)
		}

		ports[i] = port
	}

	ctx := context.Background()
	book.open(args[4], args[5])
	system := startSystem(ctx, args[0], ports[0], ports[1], ports[2])
	fmt.Println(readyLine)

	stopRequested := make(chan os.Signal, 1)
	signal.Notify(stopRequested, syscall.SIGTERM, os.Interrupt)
	<-stopRequested
	_ = system.Stop(ctx)
}

// startSystem starts a cluster node in this process. It discovers its peers
// through the NATS server at natsAddress.
func startSystem(ctx context.Context, natsAddress string, discoveryPort, peersPort, remotingPort int) actor.ActorSystem {
	discovery := nats.NewDiscovery(&nats.Config{
		NatsServer:    "nats://" + natsAddress,
		NatsSubject:   "issue-1435",
		Host:          host,
		DiscoveryPort: discoveryPort,
	})

	clusterConfig := actor.
		NewClusterConfig().
		WithDiscovery(discovery).
		WithDiscoveryPort(discoveryPort).
		WithPeersPort(peersPort).
		WithMinimumPeersQuorum(1).
		WithGrains(new(loadGrain))

	actorSystem, err := actor.NewActorSystem(
		"issue1435",
		actor.WithRemote(remote.NewConfig(host, remotingPort, remote.WithSerializables(new(askWork), new(tellWork), new(workDone)))),
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

// waitForPeers waits until system sees exactly peers other nodes.
func waitForPeers(ctx context.Context, system actor.ActorSystem, peers int) {
	deadline := time.Now().Add(time.Minute)

	for time.Now().Before(deadline) {
		seen, err := system.Peers(ctx, time.Second)
		if err == nil && len(seen) == peers {
			return
		}

		pause.For(50 * time.Millisecond)
	}

	fail("node 1 did not see %d peers", peers)
}

// countOf returns the sum of the counts.
func countOf(counts map[string]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}

	return total
}

// printCounts prints the counts, one text per line, in the order of the texts.
func printCounts(counts map[string]int) {
	texts := make([]string, 0, len(counts))
	for text := range counts {
		texts = append(texts, text)
	}

	sort.Strings(texts)

	for _, text := range texts {
		fmt.Printf("    %d x %s\n", counts[text], firstLine(text))
	}
}

// firstLine returns the first line of text.
func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

// newNatsServer starts an in-process NATS server on a free port for the nodes' discovery.
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
