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

// Reproduction for https://github.com/Tochemey/goakt/issues/1407
//
// The timeout of AskGrain is a timer on the sender's side only. A grain hands
// an ask to OnReceive even when its sender gave up while the message waited in
// the mailbox, and the handler's Context() does not carry the ask deadline, so
// the handler cannot tell that nobody waits for its answer.
//
// This sample runs a three-node cluster. The worker grains live on node 3 and
// are asked from node 1, node 2 and node 3, on both ask paths: the channel
// path of a grain without reentrancy and the envelope path of a grain with
// reentrancy. For each of the six combinations it checks two things:
//
//   - expired ask: the worker is kept from reading its mailbox, an ask times
//     out while it waits there, then the worker resumes. The ask must not be
//     handed to OnReceive.
//   - context deadline: an ask handled in time must see the ask deadline on
//     the handler's Context().
//
// No handler blocks. A worker stops reading its mailbox the way the runtime
// itself does it: it sends a StashNonReentrant request to a gate grain, which
// defers its reply until the sample opens it.
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
	// read its mailbox.
	expiredAskTimeout = 100 * time.Millisecond
	// timelyAskTimeout is the timeout of the ask sent once the worker reads its
	// mailbox again.
	timelyAskTimeout = 2 * time.Second
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

	node1 := startNode(ctx, srv.Addr().String(), 9531, 9532, 9533)
	node2 := startNode(ctx, srv.Addr().String(), 9534, 9535, 9536)
	node3 := startNode(ctx, srv.Addr().String(), 9537, 9538, 9539)

	for {
		peers, err := node1.Peers(ctx, time.Second)
		if err == nil && len(peers) == 2 {
			break
		}

		pause.For(50 * time.Millisecond)
	}

	failures := 0

	for _, channelPath := range []bool{true, false} {
		for i, node := range []actor.ActorSystem{node1, node2, node3} {
			failures += run(ctx, node3, node, i+1, channelPath)
		}
	}

	_ = node3.Stop(ctx)
	_ = node2.Stop(ctx)
	_ = node1.Stop(ctx)
	srv.Shutdown()

	if failures > 0 {
		fmt.Printf("FAIL: %d of 12 checks show an ask that ignores its sender's timeout\n", failures)
		os.Exit(1)
	}

	fmt.Println("PASS: a grain skips an ask whose sender stopped waiting and its context carries the ask deadline")
}

// run checks one worker on owner, asked from asker, which is node number
// askerNumber. channelPath selects the ask path of a grain without reentrancy,
// otherwise the envelope path of a grain with reentrancy. It returns the
// number of failed checks.
func run(ctx context.Context, owner, asker actor.ActorSystem, askerNumber int, channelPath bool) int {
	path := "envelope path"
	if channelPath {
		path = "channel path"
	}

	name := fmt.Sprintf("%s, asked from node %d", path, askerNumber)
	suffix := fmt.Sprintf("%d-%t", askerNumber, channelPath)

	worker, err := actor.GrainOf[*workerGrain](ctx, owner, "worker-"+suffix, actor.WithGrainReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.StashNonReentrant))))
	if err != nil {
		fail("%v", err)
	}

	gate, err := actor.GrainOf[*gateGrain](ctx, owner, "gate-"+suffix)
	if err != nil {
		fail("%v", err)
	}

	// The worker stops reading its mailbox until the gate opens.
	if err := owner.TellGrain(ctx, worker, &hold{Gate: gate, ChannelPath: channelPath}); err != nil {
		fail("holding the worker: %v", err)
	}

	// This ask waits in the mailbox and its sender gives up.
	expired := name + ", expired ask"
	if _, err := asker.AskGrain(ctx, worker, &work{ID: expired}, expiredAskTimeout); !errors.Is(err, gerrors.ErrRequestTimeout) {
		fail("%s: expected a request timeout, got %v", expired, err)
	}

	// The owner counts the timeout of an ask from another node from the moment
	// the request reaches it, a moment after the sender started waiting. The
	// pause keeps the worker on hold until the owner's deadline passed too.
	pause.For(expiredAskTimeout)

	// The worker resumes. The timely ask is behind the expired one in the
	// mailbox, so once it is answered the expired one has been dealt with.
	if err := owner.TellGrain(ctx, gate, new(open)); err != nil {
		fail("opening the gate: %v", err)
	}

	timely := name + ", context deadline"
	if _, err := asker.AskGrain(ctx, worker, &work{ID: timely}, timelyAskTimeout); err != nil {
		fail("%s: %v", timely, err)
	}

	handledMu.Lock()
	_, expiredHandled := handled[expired]
	timelySeen := handled[timely]
	handledMu.Unlock()

	failures := 0

	if expiredHandled {
		fmt.Printf("BUG: %s: handed to OnReceive after its sender timed out\n", expired)
		failures++
	} else {
		fmt.Printf("OK: %s: not handed to OnReceive\n", expired)
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

// done is the worker's reply to work, serialized with CBOR.
type done struct{}

// hold tells a worker to stop reading its mailbox until Gate opens. It is only
// sent on node 3, where the workers and the gates live.
type hold struct {
	// Gate is the grain whose deferred reply resumes the worker.
	Gate *actor.GrainIdentity
	// ChannelPath turns the worker's reentrancy off once it is on hold, so the
	// asks that follow take the channel path of a grain without reentrancy.
	ChannelPath bool
}

// wait is the request a worker on hold sends to its gate.
type wait struct{}

// open tells a gate to answer the wait it holds.
type open struct{}

// workerGrain records every work message it handles.
type workerGrain struct{}

// OnActivate does nothing.
func (x *workerGrain) OnActivate(context.Context, *actor.GrainProps) error { return nil }

// OnReceive records a work message and goes on hold when told to.
func (x *workerGrain) OnReceive(ctx *actor.GrainContext) {
	switch message := ctx.Message().(type) {
	case *hold:
		// A StashNonReentrant request in flight keeps the worker from reading
		// its mailbox until the gate answers.
		ctx.RequestGrain(message.Gate, new(wait), actor.WithRequestTimeout(time.Minute))

		if message.ChannelPath {
			ctx.DisableReentrancy()
		}

		ctx.NoErr()
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

// OnDeactivate does nothing.
func (x *workerGrain) OnDeactivate(context.Context, *actor.GrainProps) error { return nil }

// gateGrain holds the reply to a worker's wait until it is told to open.
type gateGrain struct {
	// reply answers the wait the gate holds; nil when it holds none.
	reply *actor.GrainReply
}

// OnActivate does nothing.
func (x *gateGrain) OnActivate(context.Context, *actor.GrainProps) error { return nil }

// OnReceive defers the reply to a wait and sends it when the gate opens.
func (x *gateGrain) OnReceive(ctx *actor.GrainContext) {
	switch ctx.Message().(type) {
	case *wait:
		x.reply = ctx.DeferResponse()
	case *open:
		x.reply.NoErr()
		x.reply = nil
		ctx.NoErr()
	default:
		ctx.Unhandled()
	}
}

// OnDeactivate does nothing.
func (x *gateGrain) OnDeactivate(context.Context, *actor.GrainProps) error { return nil }

// startNode starts a cluster node that discovers its peers through the NATS server at natsAddress.
func startNode(ctx context.Context, natsAddress string, discoveryPort, peersPort, remotingPort int) actor.ActorSystem {
	discovery := nats.NewDiscovery(&nats.Config{
		NatsServer:    "nats://" + natsAddress,
		NatsSubject:   "issue-1407",
		Host:          "localhost",
		DiscoveryPort: discoveryPort,
	})

	clusterConfig := actor.
		NewClusterConfig().
		WithDiscovery(discovery).
		WithDiscoveryPort(discoveryPort).
		WithPeersPort(peersPort).
		WithMinimumPeersQuorum(1).
		WithGrains(new(workerGrain), new(gateGrain))

	actorSystem, err := actor.NewActorSystem(
		"issue1407",
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
