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

// Package main reproduces github.com/Tochemey/goakt/issues/1396: a grain
// activated on a node that is shutting down leaves a registry record that
// points at that node after it is gone, and the grain can no longer be reached.
//
// The sample runs a three-node cluster:
//
//   - the caller node activates grains and sends to them;
//   - the stopping node is shut down in the middle of the sample;
//   - the third node only keeps the cluster at three members.
//
// The race is narrow in real life. This sample makes it happen on every run by
// holding two things open with channels:
//
//   - the stopping node has a shutdown hook that waits, so the caller node can
//     act while the stopping node is shutting down but has not finished;
//   - the grain's OnActivate waits on the stopping node, so the activation is
//     still in progress when the stopping node cleans up its grains and leaves
//     the cluster.
//
// Steps:
//
//  1. Start the three nodes in one cluster.
//  2. Start stopping the stopping node. Its shutdown hook pauses the shutdown.
//  3. The caller node activates grains round-robin, so some are sent to the
//     stopping node. With the bug, the stopping node accepts one even though
//     it is shutting down. With the fix, it refuses and the grain is activated
//     on a live node.
//  4. Let the stopping node finish its shutdown while that activation is still
//     running.
//  5. The caller node sends to every grain it activated. With the bug, the
//     registry still says the stopping node owns the held grain, and the
//     stopping node answers "remoting is not enabled" on every try.
//
// Run it with: go run ./playground/issue-1396
//
// Exit status 1 means the bug is still there, 0 means the grain is reachable
// again, and 2 means the setup failed.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery/static"
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

const host = "127.0.0.1"

var (
	// stoppingNodeRemotingPort is the remoting port of the stopping node. The
	// grain compares it with its own node's port to know it runs there.
	stoppingNodeRemotingPort int

	// startHoldingActivations is closed when the stopping node must start
	// holding grain activations in OnActivate. Until then, activations run
	// normally.
	startHoldingActivations = make(chan struct{})

	// heldGrainName receives the name of the grain whose activation the
	// stopping node is holding.
	heldGrainName = make(chan string, 1)

	// releaseHeldActivation lets the held activation finish.
	releaseHeldActivation = make(chan struct{})

	// stoppingNodeShutdownStarted is closed when the stopping node enters its
	// shutdown hook.
	stoppingNodeShutdownStarted = make(chan struct{})

	// resumeStoppingNodeShutdown lets the stopping node continue its shutdown.
	resumeStoppingNodeShutdown = make(chan struct{})
)

// nodePorts holds the three ports a clustered node listens on.
type nodePorts struct {
	discovery int
	peers     int
	remoting  int
}

func main() {
	ctx := context.Background()

	// Step 1: start three nodes in one cluster.
	freePorts := inet.Get(9)
	callerPorts := nodePorts{discovery: freePorts[0], peers: freePorts[1], remoting: freePorts[2]}
	stoppingPorts := nodePorts{discovery: freePorts[3], peers: freePorts[4], remoting: freePorts[5]}
	thirdPorts := nodePorts{discovery: freePorts[6], peers: freePorts[7], remoting: freePorts[8]}

	discoveryAddresses := []string{
		fmt.Sprintf("%s:%d", host, callerPorts.discovery),
		fmt.Sprintf("%s:%d", host, stoppingPorts.discovery),
		fmt.Sprintf("%s:%d", host, thirdPorts.discovery),
	}
	stoppingNodeRemotingPort = stoppingPorts.remoting

	callerNode := startNode(ctx, callerPorts, discoveryAddresses)
	defer func() { _ = callerNode.Stop(ctx) }()

	stoppingNode := startNode(ctx, stoppingPorts, discoveryAddresses, actor.WithCoordinatedShutdown(&pausingShutdownHook{}))

	thirdNode := startNode(ctx, thirdPorts, discoveryAddresses)
	defer func() { _ = thirdNode.Stop(ctx) }()

	if !waitFor(10*time.Second, func() bool {
		peers, err := callerNode.Peers(ctx, time.Second)
		return err == nil && len(peers) == 2
	}) {
		fail("the three nodes did not form a cluster")
	}

	fmt.Println("step 1: the caller node, the stopping node and the third node are in one cluster")

	// Step 2: start stopping the stopping node, and wait until its shutdown
	// has begun.
	stoppingNodeStopResult := make(chan error, 1)
	go func() { stoppingNodeStopResult <- stoppingNode.Stop(ctx) }()
	<-stoppingNodeShutdownStarted

	fmt.Println("step 2: the stopping node is shutting down (paused in its shutdown hook)")

	// Step 3: the caller node activates grains round-robin, so some are sent
	// to the stopping node. With the bug, the stopping node accepts one and
	// holds it in OnActivate; the caller node stops waiting after one second
	// and keeps the registry record that names the stopping node as owner.
	// With the fix, the stopping node refuses and the caller node activates
	// the grain on a live node.
	close(startHoldingActivations)
	grainNames, heldName := activateGrainsDuringShutdown(ctx, callerNode)

	if heldName != "" {
		fmt.Printf("step 3: the stopping node accepted an activation of %q while shutting down\n", heldName)
	} else {
		fmt.Printf("step 3: the stopping node refused new activations; %d grains were activated on live nodes\n", len(grainNames))
	}

	// Step 4: let the stopping node finish its shutdown. The grain is still
	// activating, so the stopping node does not clean up its record before it
	// leaves the cluster.
	close(resumeStoppingNodeShutdown)
	if err := <-stoppingNodeStopResult; err != nil {
		fmt.Printf("        the stopping node's Stop returned: %v\n", err)
	}

	close(releaseHeldActivation)

	if !waitFor(10*time.Second, func() bool {
		peers, err := callerNode.Peers(ctx, time.Second)
		return err == nil && len(peers) == 1
	}) {
		fail("the caller node still sees the stopping node as a cluster member")
	}

	fmt.Println("step 4: the stopping node finished shutting down and left the cluster")

	// Step 5: the caller node looks up every grain it activated and sends to
	// it, as a real caller would. With the bug, the held grain's record still
	// names the stopping node, which answers "remoting is not enabled".
	unreachable := 0
	for _, grainName := range grainNames {
		if !grainAnswers(ctx, callerNode, grainName) {
			unreachable++
		}
	}

	if unreachable > 0 {
		fmt.Printf("\nBUG: %d grain(s) unreachable. Their registry record still names the stopping node, which is gone.\n", unreachable)
		os.Exit(1)
	}

	fmt.Printf("step 5: all %d grains answered\n", len(grainNames))
	fmt.Println("\nOK: every grain is reachable after the stopping node left.")
}

// grainAnswers asks the grain up to five times, one second apart, and reports
// whether it answered. Failed attempts are printed.
func grainAnswers(ctx context.Context, callerNode actor.ActorSystem, grainName string) bool {
	for attempt := 1; attempt <= 5; attempt++ {
		err := lookUpAndAskGrain(ctx, callerNode, grainName)
		if err == nil {
			return true
		}

		fmt.Printf("step 5: %s: attempt %d: %v\n", grainName, attempt, err)
		pause.For(time.Second)
	}

	return false
}

// activateGrainsDuringShutdown calls GrainOf on the caller node for up to ten
// grains with round-robin placement. It stops early when the stopping node
// holds an activation. It returns the names of the grains it activated and
// the name of the held grain, which is empty when the stopping node refused.
func activateGrainsDuringShutdown(ctx context.Context, callerNode actor.ActorSystem) (grainNames []string, heldName string) {
	for i := range 10 {
		grainName := fmt.Sprintf("grain-%d", i)
		callCtx, cancel := context.WithTimeout(ctx, time.Second)
		_, err := actor.GrainOf[*replyGrain](callCtx, callerNode, grainName, actor.WithActivationStrategy(actor.RoundRobinActivation))
		cancel()

		select {
		case held := <-heldGrainName:
			return append(grainNames, held), held
		default:
		}

		if err != nil {
			fail("activating %s: %v", grainName, err)
		}

		grainNames = append(grainNames, grainName)
	}

	return grainNames, ""
}

// lookUpAndAskGrain looks up the grain with GrainOf and sends it one AskGrain.
func lookUpAndAskGrain(ctx context.Context, node actor.ActorSystem, grainName string) error {
	identity, err := actor.GrainOf[*replyGrain](ctx, node, grainName)
	if err != nil {
		return fmt.Errorf("GrainOf failed: %w", err)
	}

	if _, err := node.AskGrain(ctx, identity, new(testpb.TestReply), time.Second); err != nil {
		return fmt.Errorf("AskGrain failed: %w", err)
	}

	return nil
}

// replyGrain answers every message with a Reply. Its OnActivate waits when it
// runs on the stopping node after startHoldingActivations is closed.
type replyGrain struct{}

var _ actor.Grain = (*replyGrain)(nil)

// OnActivate holds the activation on the stopping node until
// releaseHeldActivation is closed. Everywhere else it returns at once.
func (g *replyGrain) OnActivate(_ context.Context, props *actor.GrainProps) error {
	if props.ActorSystem().Port() != stoppingNodeRemotingPort {
		return nil
	}

	select {
	case <-startHoldingActivations:
	default:
		return nil
	}

	heldGrainName <- props.Identity().Name()
	<-releaseHeldActivation
	return nil
}

// OnReceive answers every message with a Reply.
func (g *replyGrain) OnReceive(ctx *actor.GrainContext) {
	ctx.Response(new(testpb.Reply))
}

// OnDeactivate does nothing.
func (g *replyGrain) OnDeactivate(context.Context, *actor.GrainProps) error {
	return nil
}

// pausingShutdownHook is the stopping node's shutdown hook. It signals that
// the shutdown has begun, then waits until resumeStoppingNodeShutdown is
// closed.
type pausingShutdownHook struct{}

var _ actor.ShutdownHook = (*pausingShutdownHook)(nil)

// Execute signals stoppingNodeShutdownStarted and waits for
// resumeStoppingNodeShutdown.
func (h *pausingShutdownHook) Execute(context.Context, actor.ActorSystem) error {
	close(stoppingNodeShutdownStarted)
	<-resumeStoppingNodeShutdown
	return nil
}

// Recovery returns the default recovery settings.
func (h *pausingShutdownHook) Recovery() *actor.ShutdownHookRecovery {
	return actor.NewShutdownHookRecovery()
}

// startNode starts a clustered actor system on the given ports. It finds the
// other nodes through static discovery at discoveryAddresses.
func startNode(ctx context.Context, ports nodePorts, discoveryAddresses []string, opts ...actor.Option) actor.ActorSystem {
	clusterConfig := actor.NewClusterConfig().
		WithDiscovery(static.NewDiscovery(&static.Config{Hosts: discoveryAddresses})).
		WithDiscoveryPort(ports.discovery).
		WithPeersPort(ports.peers).
		WithGrains(&replyGrain{}).
		WithMinimumPeersQuorum(1).
		WithBootstrapTimeout(time.Second).
		WithShutdownTimeout(2 * time.Second).
		WithNetworkProfile(actor.NetworkProfileLocal)

	opts = append(opts,
		actor.WithLogger(log.DiscardLogger),
		actor.WithRemote(remote.NewConfig(host, ports.remoting)),
		actor.WithCluster(clusterConfig),
	)

	system, err := actor.NewActorSystem("issue1396", opts...)
	if err != nil {
		fail("create actor system: %v", err)
	}

	if err := system.Start(ctx); err != nil {
		fail("start actor system: %v", err)
	}

	return system
}

// waitFor checks condition every 100ms until it returns true or timeout
// passes. It reports whether condition became true.
func waitFor(timeout time.Duration, condition func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}

		pause.For(100 * time.Millisecond)
	}

	return false
}

// fail prints a setup error and exits with status 2.
func fail(format string, args ...any) {
	fmt.Printf("setup failed: "+format+"\n", args...)
	os.Exit(2)
}
