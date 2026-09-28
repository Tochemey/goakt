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

// Package main demonstrates the fix for github.com/Tochemey/goakt/issues/1391:
// recovering named non-relocatable actors after their owner node dies
// abruptly, with the recovery flow the reporter runs in production, while the
// cluster converges.
//
// The issue reported two things during that window:
//
//   - a lookup returned context.DeadlineExceeded while the caller's context
//     was still alive, and the caller could not tell the registry's internal
//     timeout from its own deadline;
//   - ActorOf reported the departed owner's actor as not found while SpawnOn
//     for the same name returned ErrActorAlreadyExists.
//
// The owner has to die without running its shutdown, so the sample runs two
// OS processes: the parent is the survivor, the child is the owner. The child
// runs the same binary and is killed with SIGKILL.
//
// Sequence:
//
//  1. The survivor starts and launches the owner process. Both join one
//     cluster through static discovery with a replica count of 2, so the
//     registry records survive the loss of the node that wrote them.
//  2. The owner spawns actorCount actors with WithRelocationDisabled and
//     prints a ready line; the survivor resolves each of them remotely.
//  3. The survivor kills the owner and waits until membership reports zero
//     peers.
//  4. The survivor recovers every name at once with the reporter's flow
//     (wakeNamedActor), recording every lookup timeout and every SpawnOn
//     conflict that the next lookup contradicts.
//
// It prints what the flow met: how many names it recovered and how fast, how
// many registry read timeouts it saw under a live caller context and how many
// of them carried ErrClusterRegistryTimeout, and how many times SpawnOn
// reported a name as taken that the lookup reported free. It exits with
// status 0; status 2 means the setup itself failed.
package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery/static"
	gerrors "github.com/tochemey/goakt/v4/errors"
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	goaktlog "github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
)

const (
	host       = "127.0.0.1"
	systemName = "issue1391"

	// actorCount is the number of non-relocatable actors the owner hosts. They
	// are all recovered at once, so the convergence window is met many times.
	actorCount = 64

	// The child process reads its role and ports from these variables.
	ownerModeEnv         = "GOAKT_ISSUE_1391_OWNER"
	survivorDiscoveryEnv = "GOAKT_ISSUE_1391_SURVIVOR_DISCOVERY"
	ownerDiscoveryEnv    = "GOAKT_ISSUE_1391_OWNER_DISCOVERY"
	ownerPeersEnv        = "GOAKT_ISSUE_1391_OWNER_PEERS"
	ownerRemotingEnv     = "GOAKT_ISSUE_1391_OWNER_REMOTING"
	// verboseEnv, when set to 1, makes the survivor log at debug level to
	// stderr.
	verboseEnv = "GOAKT_ISSUE_1391_VERBOSE"

	// peerWait bounds membership changes and lookupWait the first remote
	// resolution. wakeLimit bounds one call of the reporter's flow and
	// recoverWait the recovery of one name, which calls the flow again after
	// a failure the way a caller retrying delivery would.
	peerWait       = 20 * time.Second
	lookupWait     = 10 * time.Second
	wakeLimit      = 5 * time.Second
	recoverWait    = 45 * time.Second
	operationLimit = time.Second
	pollInterval   = 100 * time.Millisecond
	readyLine      = "ready"
)

// worker is the actor recovered after the crash.
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

// observations counts what the reporter's flow met during the recovery. Every
// field is updated by the concurrent recoveries.
type observations struct {
	// lookupTimeouts counts lookups that failed with context.DeadlineExceeded
	// while the caller's context was still alive.
	lookupTimeouts atomic.Int64
	// markedTimeouts counts those of lookupTimeouts that carry
	// ErrClusterRegistryTimeout, so the caller can tell them from its own
	// deadline.
	markedTimeouts atomic.Int64
	// staleClaims counts SpawnOn calls that returned ErrActorAlreadyExists
	// while the lookup that followed reported the name as not found.
	staleClaims atomic.Int64
	// wakeFailures counts calls of the reporter's flow that returned an error
	// and were made again.
	wakeFailures atomic.Int64
	// failureReasons counts the failed calls by what the flow returned.
	failureReasons sync.Map
}

// recordFailure counts one failed call of the flow under its reason.
func (x *observations) recordFailure(reason string) {
	x.wakeFailures.Add(1)
	counter, _ := x.failureReasons.LoadOrStore(reason, new(atomic.Int64))
	counter.(*atomic.Int64).Add(1)
}

// reasons renders the failed calls by reason, most frequent first.
func (x *observations) reasons() string {
	var lines []string

	x.failureReasons.Range(func(reason, counter any) bool {
		lines = append(lines, fmt.Sprintf("%d x %s", counter.(*atomic.Int64).Load(), reason))
		return true
	})

	if len(lines) == 0 {
		return "none"
	}

	sort.Strings(lines)
	return strings.Join(lines, "; ")
}

func main() {
	if os.Getenv(ownerModeEnv) == "1" {
		runOwner()
		return
	}

	os.Exit(runSurvivor())
}

// runSurvivor drives the scenario from the surviving node and returns the
// process exit status.
func runSurvivor() int {
	ctx := context.Background()
	ports := inet.Get(6)

	survivorDiscovery, survivorPeers, survivorRemoting := ports[0], ports[1], ports[2]
	ownerDiscovery, ownerPeers, ownerRemoting := ports[3], ports[4], ports[5]
	hosts := []string{
		fmt.Sprintf("%s:%d", host, survivorDiscovery),
		fmt.Sprintf("%s:%d", host, ownerDiscovery),
	}

	survivor := newSystem(survivorDiscovery, survivorPeers, survivorRemoting, hosts, survivorLogger())
	if err := survivor.Start(ctx); err != nil {
		fatal("start survivor: %v", err)
	}

	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = survivor.Stop(stopCtx)
	}()

	executable, err := os.Executable()
	if err != nil {
		fatal("resolve executable: %v", err)
	}

	command := exec.Command(executable)
	command.Env = append(
		os.Environ(),
		ownerModeEnv+"=1",
		survivorDiscoveryEnv+"="+strconv.Itoa(survivorDiscovery),
		ownerDiscoveryEnv+"="+strconv.Itoa(ownerDiscovery),
		ownerPeersEnv+"="+strconv.Itoa(ownerPeers),
		ownerRemotingEnv+"="+strconv.Itoa(ownerRemoting),
	)

	stdout, err := command.StdoutPipe()
	if err != nil {
		fatal("open owner stdout: %v", err)
	}

	// Nothing is ever written to the pipe. It stays open for the life of
	// this process, whatever way it exits, and the owner exits once it
	// closes, so a setup failure that exits before the deferred kill below
	// never strands the owner process with its ports.
	stdin, err := command.StdinPipe()
	if err != nil {
		fatal("open owner stdin: %v", err)
	}

	defer func() { _ = stdin.Close() }()

	var stderr bytes.Buffer
	command.Stderr = &stderr

	if err := command.Start(); err != nil {
		fatal("start owner process: %v", err)
	}

	defer func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()

	if err := waitReady(stdout, 30*time.Second); err != nil {
		fatal("%v; owner stderr: %s", err, stderr.String())
	}

	waitForPeerCount(ctx, survivor, 1, peerWait)

	for index := range actorCount {
		if pid := waitForActor(ctx, survivor, ownerActorName(index), lookupWait); pid.IsLocal() {
			fatal("%s unexpectedly resolved locally before the owner was lost", ownerActorName(index))
		}
	}

	fmt.Printf("owner before crash: %d non-relocatable actors, each resolved remotely by the survivor\n", actorCount)

	// SIGKILL: the owner never runs Stop, so its registry records are not
	// cleaned up by the graceful path.
	if err := command.Process.Kill(); err != nil {
		fatal("kill owner process: %v", err)
	}

	_ = command.Wait()

	waitForPeerCount(ctx, survivor, 0, peerWait)
	fmt.Println("owner process killed; survivor membership now reports zero peers")

	stats := &observations{}
	start := time.Now()
	recovered := recoverAll(ctx, survivor, stats)

	fmt.Printf("recovered %d/%d names with the reporter's flow in %s\n", recovered, actorCount, time.Since(start).Round(time.Millisecond))
	fmt.Printf("calls of the flow made again after a failure: %d (%s)\n", stats.wakeFailures.Load(), stats.reasons())
	fmt.Printf("names resolving to a non-relocatable actor on the survivor after the recovery: %d/%d\n", countLocal(ctx, survivor), actorCount)
	fmt.Printf("registry read timeouts under a live caller context: %d, carrying ErrClusterRegistryTimeout: %d\n", stats.lookupTimeouts.Load(), stats.markedTimeouts.Load())
	fmt.Printf("SpawnOn reported a name as taken while ActorOf reported it free: %d\n", stats.staleClaims.Load())

	return 0
}

// runOwner starts the node that hosts the actors and then blocks until the
// survivor kills the process or exits.
func runOwner() {
	ctx := context.Background()
	survivorDiscovery := mustEnvInt(survivorDiscoveryEnv)
	ownerDiscovery := mustEnvInt(ownerDiscoveryEnv)
	ownerPeers := mustEnvInt(ownerPeersEnv)
	ownerRemoting := mustEnvInt(ownerRemotingEnv)

	hosts := []string{
		fmt.Sprintf("%s:%d", host, survivorDiscovery),
		fmt.Sprintf("%s:%d", host, ownerDiscovery),
	}

	owner := newSystem(ownerDiscovery, ownerPeers, ownerRemoting, hosts, goaktlog.DiscardLogger)
	if err := owner.Start(ctx); err != nil {
		fatal("start owner: %v", err)
	}

	waitForPeerCount(ctx, owner, 1, peerWait)

	for index := range actorCount {
		if _, err := owner.Spawn(ctx, ownerActorName(index), &worker{}, actor.WithRelocationDisabled()); err != nil {
			fatal("spawn %s: %v", ownerActorName(index), err)
		}
	}

	fmt.Println(readyLine)

	// The survivor terminates this process with SIGKILL. Stop is never called:
	// graceful cleanup would remove the condition the sample exercises. The
	// read returns only once the survivor's end of the pipe closes, which
	// happens when the survivor exits by any path, so the owner never
	// outlives it.
	_, _ = io.Copy(io.Discard, os.Stdin)
}

// recoverAll recovers every owner actor at once and returns how many were
// recovered within recoverWait.
func recoverAll(ctx context.Context, system actor.ActorSystem, stats *observations) int {
	var (
		wg        sync.WaitGroup
		recovered atomic.Int64
	)

	for index := range actorCount {
		wg.Go(func() {
			if recoverName(ctx, system, ownerActorName(index), stats) {
				recovered.Add(1)
			}
		})
	}

	wg.Wait()
	return int(recovered.Load())
}

// recoverName calls the reporter's flow for name until it returns an actor,
// the way a caller retrying delivery would, and reports whether it did within
// recoverWait. The actor may come back in its remote form even when it was
// placed on this node: SpawnOn hands back what the placement returned.
func recoverName(ctx context.Context, system actor.ActorSystem, name string, stats *observations) bool {
	deadline := time.Now().Add(recoverWait)

	for time.Now().Before(deadline) {
		wakeCtx, cancel := context.WithTimeout(ctx, wakeLimit)
		pid, err := wakeNamedActor(wakeCtx, system, name, stats)
		cancel()

		if err == nil && pid != nil {
			return true
		}

		if err == nil {
			err = errors.New("no actor and no error")
		}

		stats.recordFailure(err.Error())
		pause.For(pollInterval)
	}

	return false
}

// countLocal returns how many owner actor names resolve to a non-relocatable
// actor on this node.
func countLocal(ctx context.Context, system actor.ActorSystem) int {
	local := 0

	for index := range actorCount {
		lookupCtx, cancel := context.WithTimeout(ctx, operationLimit)
		pid, err := system.ActorOf(lookupCtx, ownerActorName(index))
		cancel()

		if err == nil && pid != nil && pid.IsLocal() && !pid.IsRelocatable() {
			local++
		}
	}

	return local
}

// wakeNamedActor is the reporter's recovery flow, as written in
// https://github.com/Tochemey/goakt/issues/1386#issuecomment-5875950438:
//
//	ActorOf(name)
//	  -> found: send
//	  -> not found / inconclusive registry deadline: SpawnOn(name)
//	  -> ErrActorAlreadyExists: resolve again
//	  -> if lookup now says not found: retry SpawnOn once
//
// A registry deadline is inconclusive when it matches context.DeadlineExceeded
// while ctx is still alive. Every lookup is recorded in stats.
func wakeNamedActor(ctx context.Context, system actor.ActorSystem, name string, stats *observations) (*actor.PID, error) {
	pid, err := lookup(ctx, system, name, stats)
	if err == nil {
		return pid, nil
	}

	if !errors.Is(err, gerrors.ErrActorNotFound) && !isInconclusiveDeadline(ctx, err) {
		return nil, err
	}

	pid, err = spawnPinned(ctx, system, name)
	if err == nil || !errors.Is(err, gerrors.ErrActorAlreadyExists) {
		return pid, err
	}

	pid, err = lookup(ctx, system, name, stats)
	if err == nil {
		return pid, nil
	}

	if !errors.Is(err, gerrors.ErrActorNotFound) {
		return nil, err
	}

	stats.staleClaims.Add(1)
	return spawnPinned(ctx, system, name)
}

// lookup calls ActorOf and records a timeout under a live caller context in
// stats, noting whether it carries ErrClusterRegistryTimeout.
func lookup(ctx context.Context, system actor.ActorSystem, name string, stats *observations) (*actor.PID, error) {
	pid, err := system.ActorOf(ctx, name)
	if isInconclusiveDeadline(ctx, err) {
		stats.lookupTimeouts.Add(1)

		if errors.Is(err, gerrors.ErrClusterRegistryTimeout) {
			stats.markedTimeouts.Add(1)
		}
	}

	return pid, err
}

// isInconclusiveDeadline is the reporter's test for a registry deadline that
// does not belong to the caller: the error matches context.DeadlineExceeded
// while ctx is still alive.
func isInconclusiveDeadline(ctx context.Context, err error) bool {
	return errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil
}

// newSystem builds a clustered actor system on the given ports. The replica
// count of 2 keeps the registry records alive on the survivor once the owner
// node is gone, and the short read timeout widens the window in which a
// registry read times out while the cluster converges.
func newSystem(discoveryPort, peersPort, remotingPort int, hosts []string, logger goaktlog.Logger) actor.ActorSystem {
	clusterConfig := actor.NewClusterConfig().
		WithDiscovery(static.NewDiscovery(&static.Config{Hosts: hosts})).
		WithDiscoveryPort(discoveryPort).
		WithPeersPort(peersPort).
		WithKinds(&worker{}).
		WithPartitionCount(7).
		WithReplicaCount(2).
		WithMinimumPeersQuorum(1).
		WithBootstrapTimeout(time.Second).
		WithReadTimeout(250 * time.Millisecond).
		WithWriteTimeout(250 * time.Millisecond).
		WithClusterStateSyncInterval(500 * time.Millisecond).
		WithConvergenceTimeout(2 * time.Second).
		WithShutdownTimeout(2 * time.Second).
		WithNetworkProfile(actor.NetworkProfileLocal)

	system, err := actor.NewActorSystem(
		systemName,
		actor.WithLogger(logger),
		actor.WithRemote(remote.NewConfig(host, remotingPort)),
		actor.WithCluster(clusterConfig),
	)
	if err != nil {
		fatal("construct actor system: %v", err)
	}

	return system
}

// survivorLogger returns the survivor's logger: silent unless verboseEnv is set.
func survivorLogger() goaktlog.Logger {
	if os.Getenv(verboseEnv) == "1" {
		return goaktlog.NewZap(goaktlog.DebugLevel, os.Stderr)
	}

	return goaktlog.DiscardLogger
}

// ownerActorName returns the name of the owner's actor at index.
func ownerActorName(index int) string {
	return fmt.Sprintf("pinned-%03d", index)
}

// spawnPinned spawns a non-relocatable actor under name, the way the
// reporter's flow does.
func spawnPinned(ctx context.Context, system actor.ActorSystem, name string) (*actor.PID, error) {
	return system.SpawnOn(ctx, name, &worker{}, actor.WithRelocationDisabled())
}

// waitReady blocks until the owner process prints its ready line.
func waitReady(reader io.Reader, timeout time.Duration) error {
	result := make(chan error, 1)

	go func() {
		line, err := bufio.NewReader(reader).ReadString('\n')
		if err != nil {
			result <- fmt.Errorf("read owner readiness: %w", err)
			return
		}

		if strings.TrimSpace(line) != readyLine {
			result <- fmt.Errorf("unexpected owner readiness %q", line)
			return
		}

		result <- nil
	}()

	select {
	case err := <-result:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("timed out waiting for owner readiness")
	}
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

	fatal("peer count did not become %d within %s", count, timeout)
}

// waitForActor blocks until the name resolves through the cluster.
func waitForActor(ctx context.Context, system actor.ActorSystem, name string, timeout time.Duration) *actor.PID {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		lookupCtx, cancel := context.WithTimeout(ctx, operationLimit)
		pid, err := system.ActorOf(lookupCtx, name)
		cancel()

		if err == nil && pid != nil {
			return pid
		}

		pause.For(pollInterval)
	}

	fatal("actor %q did not become visible within %s", name, timeout)
	return nil
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

// fatal reports a setup problem that is not the behaviour under demonstration
// and exits with status 2.
func fatal(format string, args ...any) {
	fmt.Printf("SETUP FAILURE: "+format+"\n", args...)
	os.Exit(2)
}
