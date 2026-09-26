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

// Package main reproduces github.com/Tochemey/goakt/issues/1386: a named
// actor spawned with WithRelocationDisabled keeps its cluster registry record
// after the node hosting it is killed, so the dead incarnation still owns the
// name once membership has dropped the node. ActorExists and ActorOf keep
// reporting the dead owner, and a fresh spawn of the same name fails with
// ErrActorAlreadyExists.
//
// The owner has to die without running its shutdown, since a graceful Stop
// removes the record, so the sample runs two OS processes: the parent is the
// survivor, the child is the owner. The child runs the same binary and is
// killed with SIGKILL.
//
// Sequence:
//
//  1. The survivor starts and launches the owner process. Both join one
//     cluster through static discovery with a replica count of 2, so the
//     registry record survives the loss of the node that wrote it.
//  2. The owner spawns the actor with WithRelocationDisabled and prints a
//     ready line.
//  3. The survivor resolves the actor remotely, kills the owner, and waits
//     until membership reports zero peers.
//  4. The survivor waits for ActorExists to report the name as free, then
//     spawns a fresh non-relocatable actor under the same name.
//
// It exits with status 1 when the dead incarnation still owns the name, and
// prints an OK line and exits with status 0 once the name is released and a
// fresh actor can claim it. Status 2 means the setup itself failed.
package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery/static"
	inet "github.com/tochemey/goakt/v4/internal/net"
	goaktlog "github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
)

const (
	host       = "127.0.0.1"
	systemName = "issue1386"
	actorName  = "stable-worker"

	// The child process reads its role and ports from these variables.
	ownerModeEnv         = "GOAKT_ISSUE_1386_OWNER"
	survivorDiscoveryEnv = "GOAKT_ISSUE_1386_SURVIVOR_DISCOVERY"
	ownerDiscoveryEnv    = "GOAKT_ISSUE_1386_OWNER_DISCOVERY"
	ownerPeersEnv        = "GOAKT_ISSUE_1386_OWNER_PEERS"
	ownerRemotingEnv     = "GOAKT_ISSUE_1386_OWNER_REMOTING"
	// verboseEnv, when set to 1, makes the survivor log at debug level to
	// stderr so the leader's crash recovery can be followed.
	verboseEnv = "GOAKT_ISSUE_1386_VERBOSE"

	// peerWait bounds membership changes and lookupWait the first remote
	// resolution. releaseWait bounds the release of the dead owner's claim:
	// crash recovery scans the registry only once olric's partition repair has
	// been quiet for three seconds, and waits at most thirty for that.
	peerWait       = 20 * time.Second
	lookupWait     = 10 * time.Second
	releaseWait    = 45 * time.Second
	operationLimit = time.Second
	readyLine      = "ready "
)

// worker is the actor whose name is reserved by the dead incarnation.
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

	if err := waitReady(stdout, 15*time.Second); err != nil {
		fatal("%v; owner stderr: %s", err, stderr.String())
	}

	waitForPeerCount(ctx, survivor, 1, peerWait)

	owner := waitForActor(ctx, survivor, actorName, lookupWait)
	if owner.IsLocal() {
		fatal("actor unexpectedly resolved locally before the owner was lost")
	}

	fmt.Printf("owner before crash: %s (remote=%t)\n", owner.ID(), owner.IsRemote())

	// SIGKILL: the owner never runs Stop, so its registry record is not
	// cleaned up by the graceful path.
	if err := command.Process.Kill(); err != nil {
		fatal("kill owner process: %v", err)
	}

	_ = command.Wait()

	waitForPeerCount(ctx, survivor, 0, peerWait)
	fmt.Println("owner process killed; survivor membership now reports zero peers")

	lookupCtx, cancel := context.WithTimeout(ctx, operationLimit)
	stalePID, lookupErr := survivor.ActorOf(lookupCtx, actorName)
	cancel()

	switch {
	case lookupErr == nil && stalePID != nil:
		fmt.Printf("ActorOf right after departure: %s (remote=%t)\n", stalePID.ID(), stalePID.IsRemote())
	default:
		fmt.Printf("ActorOf right after departure: err=%v\n", lookupErr)
	}

	released, lastErr := waitForActorAbsent(ctx, survivor, actorName, releaseWait)
	fmt.Printf("name released within %s: %t", releaseWait, released)

	if lastErr != nil {
		fmt.Printf(" (last error: %v)", lastErr)
	}

	fmt.Println()

	spawnCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	recreated, spawnErr := survivor.SpawnOn(
		spawnCtx,
		actorName,
		&worker{},
		actor.WithPlacement(actor.Local),
		actor.WithRelocationDisabled(),
	)
	cancel()

	if spawnErr != nil {
		fmt.Printf("recreate same name: err=%v\n", spawnErr)
	} else {
		fmt.Printf("recreate same name: %s (local=%t relocatable=%t)\n", recreated.ID(), recreated.IsLocal(), recreated.IsRelocatable())
	}

	if !released || spawnErr != nil || recreated == nil || !recreated.IsLocal() || recreated.IsRelocatable() {
		fmt.Println("REPRO (broken): the dead non-relocatable actor still blocks reuse of its stable name")
		return 1
	}

	fmt.Println("OK: the dead incarnation no longer owns the name and a fresh non-relocatable actor claimed it")
	return 0
}

// runOwner starts the node that hosts the actor and then blocks until the
// survivor kills the process.
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

	pid, err := owner.Spawn(ctx, actorName, &worker{}, actor.WithRelocationDisabled())
	if err != nil {
		fatal("spawn owner actor: %v", err)
	}

	fmt.Printf("%s%s\n", readyLine, pid.ID())

	// The survivor terminates this process with SIGKILL. Stop is never called:
	// graceful cleanup would remove the condition the sample demonstrates.
	select {}
}

// newSystem builds a clustered actor system on the given ports. The replica
// count of 2 keeps the registry record alive on the survivor once the owner
// node is gone, so the sample exercises crash cleanup rather than the loss of
// the registry partition.
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

// waitReady blocks until the owner process prints its ready line.
func waitReady(reader io.Reader, timeout time.Duration) error {
	result := make(chan error, 1)

	go func() {
		line, err := bufio.NewReader(reader).ReadString('\n')
		if err != nil {
			result <- fmt.Errorf("read owner readiness: %w", err)
			return
		}

		if !strings.HasPrefix(line, readyLine) {
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

		time.Sleep(100 * time.Millisecond)
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

		time.Sleep(100 * time.Millisecond)
	}

	fatal("actor %q did not become visible within %s", name, timeout)
	return nil
}

// waitForActorAbsent polls ActorExists until the name is reported free and
// returns whether that happened, with the last lookup error seen.
func waitForActorAbsent(ctx context.Context, system actor.ActorSystem, name string, timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)

	var lastErr error

	for time.Now().Before(deadline) {
		lookupCtx, cancel := context.WithTimeout(ctx, operationLimit)
		exists, err := system.ActorExists(lookupCtx, name)
		cancel()

		if err == nil && !exists {
			return true, nil
		}

		if err != nil {
			lastErr = err
		}

		time.Sleep(100 * time.Millisecond)
	}

	return false, lastErr
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
