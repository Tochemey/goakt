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

// Relocation-at-scale regression harness for issue #1255.
//
// Scenario A (graceful): a three-node cluster loses one node through a clean
// shutdown; every relocatable actor it hosted must resolve as local on a
// survivor.
//
// Scenario B (crash): the departing node runs as a child OS process and is
// killed with SIGKILL, so no graceful-shutdown snapshot exists; recovery must
// come from the replicated registry (crash-recovery path). Requires a cluster
// replica count above 1.
//
// The harness exits non-zero on any unrecovered actor and prints the
// wall-clock time from departure to full recovery for each scenario.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"golang.org/x/sync/errgroup"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery/nats"
	dynaport "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
)

const (
	systemName = "issue1255"

	roleEnv     = "GOAKT_1255_ROLE"
	natsEnv     = "GOAKT_1255_NATS"
	actorsEnv   = "GOAKT_1255_ACTORS"
	scenarioEnv = "GOAKT_1255_SCENARIO"

	defaultActors    = 10_000
	spawnConcurrency = 128
	recoveryDeadline = 4 * time.Minute
	victimReadyLine  = "VICTIM READY"
)

func main() {
	if os.Getenv(roleEnv) == "victim" {
		runVictim()
		return
	}

	ctx := context.Background()
	numActors := actorCount()

	server := NatServer()
	serverAddr := server.Addr().String()
	defer server.Shutdown()

	scenario := strings.ToLower(os.Getenv(scenarioEnv))
	if scenario == "" {
		scenario = "both"
	}
	runA := scenario == "a" || scenario == "both"
	runB := scenario == "b" || scenario == "both"

	// A replicaCount>1 cluster cannot bootstrap a lone node (olric waits for a
	// backup replica owner), so the seed nodes start concurrently; later nodes
	// (the crash victim) join the established cluster one at a time. The third
	// seed only exists to host scenario A's actors.
	var sys1, sys2, sys3 actor.ActorSystem

	eg := new(errgroup.Group)
	eg.Go(func() error { sys1 = startActorSystem(ctx, serverAddr); return nil })
	eg.Go(func() error { sys2 = startActorSystem(ctx, serverAddr); return nil })

	if runA {
		eg.Go(func() error { sys3 = startActorSystem(ctx, serverAddr); return nil })
	}
	_ = eg.Wait()

	// a freshly formed replicaCount>1 cluster keeps rebalancing partitions for
	// a while; perturbing membership before that settles causes spurious churn
	time.Sleep(5 * time.Second)

	if runA {
		// Scenario A: graceful departure relocates from the shutdown snapshot.
		fmt.Printf("\n--- Scenario A: graceful departure of a node hosting %d actors ---\n", numActors)

		names := spawnActors(ctx, sys3, "graceful-actor", numActors)

		// let the registry writes and their backups settle before the departure
		time.Sleep(3 * time.Second)

		start := time.Now()
		stopActorSystem(ctx, sys3)
		awaitRecovery(ctx, sys1, sys2, names)
		fmt.Printf("Scenario A: all %d actors recovered %v after the departure began.\n", numActors, time.Since(start).Round(time.Millisecond))
	}

	if runB {
		// Scenario B: SIGKILL leaves no snapshot; recovery derives from the registry.
		fmt.Printf("\n--- Scenario B: crash (SIGKILL) of a node hosting %d actors ---\n", numActors)

		victim := startVictim(serverAddr, numActors)

		names := make([]string, numActors)
		for i := range names {
			names[i] = fmt.Sprintf("crash-actor-%d", i)
		}

		// Let the join-triggered partition migration and the victim's registry
		// writes fully replicate before the kill. A node killed while fragments are
		// still migrating to it takes those records with it (the documented
		// join-migration window, see handleNodeJoinedEvent); crash recovery is
		// specified for a settled cluster, so the harness waits it out.
		time.Sleep(20 * time.Second)

		start := time.Now()
		fmt.Println("Killing the victim node with SIGKILL...")

		if err := victim.Process.Kill(); err != nil {
			fmt.Printf("Error killing victim process: %v\n", err)
			os.Exit(1)
		}

		_ = victim.Wait()
		awaitRecovery(ctx, sys1, sys2, names)
		fmt.Printf("Scenario B: all %d actors recovered %v after the crash.\n", numActors, time.Since(start).Round(time.Millisecond))
	}

	stopActorSystem(ctx, sys2)
	stopActorSystem(ctx, sys1)
	fmt.Println("\nAll scenarios passed.")
}

// runVictim joins the cluster, spawns its share of actors, announces readiness
// on stdout and then blocks until the orchestrator kills the process.
func runVictim() {
	ctx := context.Background()

	system := startActorSystem(ctx, os.Getenv(natsEnv))

	// let the join-triggered partition rebalancing settle before loading the
	// registry with this node's actors
	time.Sleep(5 * time.Second)

	spawnActors(ctx, system, "crash-actor", actorCount())

	fmt.Println(victimReadyLine)
	select {}
}

// startVictim launches this binary again as the victim node and waits until it
// reports that all of its actors are spawned and registered.
func startVictim(serverAddr string, numActors int) *exec.Cmd {
	fmt.Println("Starting the victim node as a child process...")

	cmd := exec.Command(os.Args[0]) // nolint
	cmd.Env = append(os.Environ(),
		roleEnv+"=victim",
		natsEnv+"="+serverAddr,
		fmt.Sprintf("%s=%d", actorsEnv, numActors),
	)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Printf("Error piping victim stdout: %v\n", err)
		os.Exit(1)
	}

	if err := cmd.Start(); err != nil {
		fmt.Printf("Error starting victim process: %v\n", err)
		os.Exit(1)
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Printf("[victim] %s\n", line)

		if strings.HasPrefix(line, victimReadyLine) {
			// keep draining stdout so the child never blocks on a full pipe
			go func() {
				for scanner.Scan() {
					fmt.Printf("[victim] %s\n", scanner.Text())
				}
			}()
			return cmd
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("Error reading victim output: %v\n", err)
	}

	fmt.Println("Victim process exited before becoming ready")
	os.Exit(1)
	return nil
}

// awaitRecovery polls both survivors until every named actor resolves as local
// on one of them, meaning it was recreated on a surviving node. A registry
// entry still pointing at the departed node resolves as remote and therefore
// does not count as recovered.
func awaitRecovery(ctx context.Context, sys1, sys2 actor.ActorSystem, names []string) {
	unresolved := make(map[string]struct{}, len(names))
	for _, name := range names {
		unresolved[name] = struct{}{}
	}

	deadline := time.Now().Add(recoveryDeadline)
	for len(unresolved) > 0 {
		if time.Now().After(deadline) {
			fmt.Printf("FAILED: %d of %d actors were not recovered within %v\n", len(unresolved), len(names), recoveryDeadline)
			os.Exit(1)
		}

		for name := range unresolved {
			if resolvedLocally(ctx, sys1, name) || resolvedLocally(ctx, sys2, name) {
				delete(unresolved, name)
			}
		}

		if len(unresolved) > 0 {
			time.Sleep(200 * time.Millisecond)
		}
	}
}

func resolvedLocally(ctx context.Context, system actor.ActorSystem, name string) bool {
	pid, err := system.ActorOf(ctx, name)
	return err == nil && pid != nil && pid.IsLocal()
}

// spawnActors spawns count long-lived relocatable actors named
// "<prefix>-<i>" on the given system and returns their names.
func spawnActors(ctx context.Context, system actor.ActorSystem, prefix string, count int) []string {
	fmt.Printf("Spawning %d actors on %s...\n", count, system.Name())
	start := time.Now()

	names := make([]string, count)
	eg := new(errgroup.Group)
	eg.SetLimit(spawnConcurrency)

	for i := range count {
		name := fmt.Sprintf("%s-%d", prefix, i)
		names[i] = name

		eg.Go(func() error {
			if _, err := system.Spawn(ctx, name, &MyActor{}, actor.WithLongLived()); err != nil {
				return fmt.Errorf("spawning actor %s: %w", name, err)
			}
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		fmt.Printf("Error spawning actors: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Spawned %d actors in %v.\n", count, time.Since(start).Round(time.Millisecond))
	return names
}

func actorCount() int {
	if value := os.Getenv(actorsEnv); value != "" {
		count, err := strconv.Atoi(value)
		if err != nil || count <= 0 {
			fmt.Printf("Invalid %s value %q\n", actorsEnv, value)
			os.Exit(1)
		}
		return count
	}
	return defaultActors
}

func startActorSystem(ctx context.Context, serverAddr string) actor.ActorSystem {
	ports := dynaport.Get(3)
	discoveryPort := ports[0]
	remotingPort := ports[1]
	peersPort := ports[2]

	host := "127.0.0.1"
	actorSystem, err := actor.NewActorSystem(systemName,
		actor.WithLogger(log.DiscardLogger),
		actor.WithRemote(remote.NewConfig(host, remotingPort)),
		actor.WithCluster(actor.NewClusterConfig().
			WithDiscovery(nats.NewDiscovery(&nats.Config{
				NatsServer:    fmt.Sprintf("nats://%s", serverAddr),
				NatsSubject:   "issue-1255",
				Host:          host,
				DiscoveryPort: discoveryPort,
			})).
			WithPartitionCount(7).
			// a replica count above 1 keeps the registry partitions of a
			// crashed node alive on survivors; scenario B depends on it
			WithReplicaCount(2).
			WithPeersPort(peersPort).
			WithMinimumPeersQuorum(1).
			WithDiscoveryPort(discoveryPort).
			WithBootstrapTimeout(20*time.Second).
			WithClusterStateSyncInterval(300*time.Millisecond).
			WithClusterBalancerInterval(100*time.Millisecond).
			WithKinds(&MyActor{}),
		))

	if err != nil {
		fmt.Printf("Error creating actor system: %v\n", err)
		os.Exit(1)
	}

	if err := actorSystem.Start(ctx); err != nil {
		fmt.Printf("Error starting actor system: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Actor system started (remoting=%s:%d).\n", host, remotingPort)
	return actorSystem
}

func stopActorSystem(ctx context.Context, actorSystem actor.ActorSystem) {
	fmt.Println("Stopping actor system...")
	if err := actorSystem.Stop(ctx); err != nil {
		fmt.Printf("Error stopping actor system: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Actor system stopped.")
}

// MyActor is a minimal relocatable actor; it stays silent so the harness
// output remains parseable at 10,000 actors.
type MyActor struct{}

func (x *MyActor) PreStart(*actor.Context) error {
	return nil
}

func (x *MyActor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *actor.PostStart:
	default:
		ctx.Unhandled()
	}
}

func (x *MyActor) PostStop(*actor.Context) error {
	return nil
}

func NatServer() *natsserver.Server {
	serv, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1",
		Port: -1,
	})

	if err != nil {
		fmt.Printf("Error creating NATS server: %v\n", err)
		os.Exit(1)
	}

	ready := make(chan bool)
	go func() {
		ready <- true
		serv.Start()
	}()
	<-ready

	if !serv.ReadyForConnections(2 * time.Second) {
		fmt.Println("NATS server is not ready for connections")
		os.Exit(1)
	}

	return serv
}
