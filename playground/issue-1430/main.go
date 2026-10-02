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

// Reproduction for https://github.com/Tochemey/goakt/issues/1430, the actor
// counterpart of https://github.com/Tochemey/goakt/issues/1407
//
// The timeout of Ask is a timer on the sender's side only. An actor handles an
// ask even when its sender gave up while the message waited, and the handler's
// Context() does not carry the ask deadline, so the handler cannot tell that
// nobody waits for its answer.
//
// This sample runs a three-node cluster. The worker actors live on node 3 and
// are asked from node 1, node 2 and node 3. For each asker it checks three
// things:
//
//   - timeout error: an ask that times out must fail with ErrRequestTimeout,
//     whichever node it is sent from.
//   - expired ask: the worker is kept from handling its messages, an ask times
//     out while it waits, then the worker resumes. The ask must not be handed
//     to Receive.
//   - context deadline: an ask handled in time must see the ask deadline on
//     the handler's Context().
//
// No handler blocks. A worker stops handling its messages the way the runtime
// itself does it: it sends a StashNonReentrant request to a gate actor that
// never answers, and resumes when that request times out.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery/nats"
	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/remote"
)

const (
	// expiredAskTimeout is the timeout of the ask sent while the worker does not
	// handle its messages.
	expiredAskTimeout = 100 * time.Millisecond
	// timelyAskTimeout is the timeout of the ask the worker handles in time.
	timelyAskTimeout = 2 * time.Second
	// holdFor is how long a worker stays on hold: long enough for the expired
	// ask to time out on the sender and on the worker's node, and for the
	// timely ask to queue behind it.
	holdFor = 500 * time.Millisecond
)

var (
	// handledMu guards handled.
	handledMu sync.Mutex
	// handled records, per work ID, what the worker's handler saw. The nodes
	// run in this process, so the workers on node 3 write it and main reads it.
	handled = make(map[string]observation)
)

func main() {
	ctx := context.Background()
	srv := newNatsServer()

	node1 := startNode(ctx, srv.Addr().String(), 9541, 9542, 9543)
	node2 := startNode(ctx, srv.Addr().String(), 9544, 9545, 9546)
	node3 := startNode(ctx, srv.Addr().String(), 9547, 9548, 9549)

	for {
		peers, err := node1.Peers(ctx, time.Second)
		if err == nil && len(peers) == 2 {
			break
		}

		pause.For(50 * time.Millisecond)
	}

	// The gate and the workers live on node 3.
	gate, err := node3.Spawn(ctx, "gate", new(gateActor))
	if err != nil {
		fail("%v", err)
	}

	failures := 0

	for i, node := range []actor.ActorSystem{node1, node2, node3} {
		failures += run(ctx, node3, node, i+1, gate)
	}

	_ = node3.Stop(ctx)
	_ = node2.Stop(ctx)
	_ = node1.Stop(ctx)
	srv.Shutdown()

	if failures > 0 {
		fmt.Printf("FAIL: %d of 9 checks show an ask whose timeout is not honored\n", failures)
		os.Exit(1)
	}

	fmt.Println("PASS: an actor skips an ask whose sender stopped waiting, its context carries the ask deadline, and a timeout is ErrRequestTimeout from every node")
}

// run checks one worker on owner, asked from asker, which is node number
// askerNumber. gate is the actor the worker waits on while on hold. It returns
// the number of failed checks.
func run(ctx context.Context, owner, asker actor.ActorSystem, askerNumber int, gate *actor.PID) int {
	name := fmt.Sprintf("asked from node %d", askerNumber)
	workerName := fmt.Sprintf("worker-%d", askerNumber)

	local, err := owner.Spawn(ctx, workerName, new(workerActor), actor.WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.StashNonReentrant))))
	if err != nil {
		fail("%v", err)
	}

	// The asker resolves the worker: through the cluster from another node.
	worker := local
	for asker != owner {
		worker, err = asker.ActorOf(ctx, workerName)
		if err == nil {
			break
		}

		pause.For(50 * time.Millisecond)
	}

	// The worker stops handling its messages for holdFor.
	if _, err := actor.Ask(ctx, local, &hold{Gate: gate}, time.Second); err != nil {
		fail("holding the worker: %v", err)
	}

	// This ask waits and its sender gives up.
	expired := name + ", expired ask"
	_, askErr := actor.Ask(ctx, worker, &work{ID: expired}, expiredAskTimeout)
	if !errors.Is(askErr, gerrors.ErrRequestTimeout) && !errors.Is(askErr, context.DeadlineExceeded) {
		fail("%s: expected a timeout, got %v", expired, askErr)
	}

	// The worker's node counts the timeout of an ask from another node from the
	// moment the request reaches it, a moment after the sender started
	// waiting. The pause lets that deadline pass too.
	pause.For(expiredAskTimeout)

	// The timely ask queues behind the expired one, so once it is answered the
	// expired one has been dealt with.
	timely := name + ", context deadline"
	if _, err := actor.Ask(ctx, worker, &work{ID: timely}, timelyAskTimeout); err != nil {
		fail("%s: %v", timely, err)
	}

	handledMu.Lock()
	_, expiredHandled := handled[expired]
	timelySeen := handled[timely]
	handledMu.Unlock()

	failures := 0

	// A timed out ask must be recognizable the same way from every node.
	if !errors.Is(askErr, gerrors.ErrRequestTimeout) {
		fmt.Printf("BUG: %s, timeout error: not ErrRequestTimeout: %v\n", name, askErr)
		failures++
	} else {
		fmt.Printf("OK: %s, timeout error: ErrRequestTimeout\n", name)
	}

	if expiredHandled {
		fmt.Printf("BUG: %s: handed to Receive after its sender timed out\n", expired)
		failures++
	} else {
		fmt.Printf("OK: %s: not handed to Receive\n", expired)
	}

	switch {
	case !timelySeen.hasDeadline:
		fmt.Printf("BUG: %s: the handler's Context() has no deadline\n", timely)
		failures++
	case timelySeen.remaining > timelyAskTimeout:
		fmt.Printf("BUG: %s: the handler's Context() ends %s after the ask timeout\n", timely, (timelySeen.remaining - timelyAskTimeout).Round(time.Millisecond))
		failures++
	default:
		fmt.Printf("OK: %s: the handler's Context() ends within the ask timeout\n", timely)
	}

	return failures
}

// observation is what the worker's handler saw for one work message.
type observation struct {
	// hasDeadline reports whether the handler's Context() carried a deadline.
	hasDeadline bool
	// remaining is the time the handler had left before that deadline.
	remaining time.Duration
}

// work is the request the nodes ask the workers with, serialized with CBOR.
type work struct {
	// ID identifies the ask in the handled record.
	ID string
}

// done is the worker's reply, serialized with CBOR.
type done struct{}

// hold tells a worker to stop handling its messages for holdFor. It is only
// sent on node 3, where the workers and the gate live.
type hold struct {
	// Gate is the actor the worker waits on.
	Gate *actor.PID
}

// wait is the request a worker on hold sends to the gate.
type wait struct{}

// workerActor records every work message it handles.
type workerActor struct{}

// PreStart does nothing.
func (x *workerActor) PreStart(*actor.Context) error { return nil }

// Receive records a work message and goes on hold when told to.
func (x *workerActor) Receive(ctx *actor.ReceiveContext) {
	switch message := ctx.Message().(type) {
	case *hold:
		// A StashNonReentrant request in flight keeps the worker from handling
		// its messages until the request ends, here on its timeout.
		ctx.Request(message.Gate, new(wait), actor.WithRequestTimeout(holdFor))
		ctx.Response(new(done))
	case *work:
		deadline, hasDeadline := ctx.Context().Deadline()

		handledMu.Lock()
		handled[message.ID] = observation{hasDeadline: hasDeadline, remaining: time.Until(deadline)}
		handledMu.Unlock()

		ctx.Response(new(done))
	default:
		ctx.Unhandled()
	}
}

// PostStop does nothing.
func (x *workerActor) PostStop(*actor.Context) error { return nil }

// gateActor never answers the wait of a worker.
type gateActor struct{}

// PreStart does nothing.
func (x *gateActor) PreStart(*actor.Context) error { return nil }

// Receive leaves every wait unanswered.
func (x *gateActor) Receive(ctx *actor.ReceiveContext) {
	if _, ok := ctx.Message().(*wait); !ok {
		ctx.Unhandled()
	}
}

// PostStop does nothing.
func (x *gateActor) PostStop(*actor.Context) error { return nil }

// startNode starts a cluster node that discovers its peers through the NATS server at natsAddress.
func startNode(ctx context.Context, natsAddress string, discoveryPort, peersPort, remotingPort int) actor.ActorSystem {
	discovery := nats.NewDiscovery(&nats.Config{
		NatsServer:    "nats://" + natsAddress,
		NatsSubject:   "issue-1430",
		Host:          "localhost",
		DiscoveryPort: discoveryPort,
	})

	clusterConfig := actor.
		NewClusterConfig().
		WithDiscovery(discovery).
		WithDiscoveryPort(discoveryPort).
		WithPeersPort(peersPort).
		WithMinimumPeersQuorum(1).
		WithKinds(new(workerActor), new(gateActor))

	actorSystem, err := actor.NewActorSystem(
		"issue1430",
		actor.WithRemote(remote.NewConfig("localhost", remotingPort, remote.WithSerializables(new(work), new(done)))),
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
