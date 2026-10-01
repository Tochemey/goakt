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

// Reproduction for https://github.com/Tochemey/goakt/issues/1409
//
// A send to a grain on another node lost the error sentinel on the way
// back: a full mailbox came back as an untyped error carrying the text
// "mailbox is full", so a caller could not use errors.Is to tell
// backpressure from a real failure, and the owner logged every one of them
// at ERROR level.
//
// This sample runs two nodes. A grain with a mailbox of one lives on node 2,
// waiting on a request of its own with its mailbox full; node 1 asks it and
// must get ErrMailboxFull, as a local caller does.
//
// With the fix the owner answers with an error code the client maps back
// to the sentinel, and logs expected outcomes at debug level.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery/nats"
	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

func main() {
	ctx := context.Background()
	srv := newNatsServer()

	node1 := startNode(ctx, srv.Addr().String(), 9491, 9492, 9493)
	node2 := startNode(ctx, srv.Addr().String(), 9494, 9495, 9496)

	for {
		peers, err := node1.Peers(ctx, time.Second)
		if err == nil && len(peers) == 1 {
			break
		}

		pause.For(50 * time.Millisecond)
	}

	var err error
	gateIdentity, err = actor.GrainOf[*gateGrain](ctx, node2, "gate")
	if err != nil {
		fail("%v", err)
	}

	// The grain lives on node 2, has a mailbox of one and takes no other
	// message while a request of its own is in flight.
	identity, err := actor.GrainOf[*slowGrain](ctx, node2, "slow",
		actor.WithGrainMailboxCapacity(1),
		actor.WithGrainReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.StashNonReentrant))),
	)
	if err != nil {
		fail("%v", err)
	}

	// The first message leaves the grain waiting on the gate and the second
	// one fills its mailbox.
	go func() { _ = node2.TellGrain(ctx, identity, new(testpb.TestSend)) }()
	request := <-firstRequest

	go func() { _ = node2.TellGrain(ctx, identity, new(testpb.TestSend)) }()
	pause.For(100 * time.Millisecond)

	if err := node2.TellGrain(ctx, identity, new(testpb.TestSend), actor.WithOneWay()); !errors.Is(err, gerrors.ErrMailboxFull) {
		fail("the mailbox should be full for a local caller, got: %v", err)
	}

	// Node 1 sends to it: the answer must be the sentinel a local caller
	// gets, so the caller can back off instead of failing.
	_, askErr := node1.AskGrain(ctx, identity, new(testpb.TestReply), time.Second)

	// Cancelling the request releases the grain before the nodes stop.
	if err := request.Cancel(); err != nil {
		fail("release the grain: %v", err)
	}

	_ = node2.Stop(ctx)
	_ = node1.Stop(ctx)
	srv.Shutdown()

	if askErr == nil {
		fmt.Println("FAIL: the ask to the full grain on node 2 was accepted")
		os.Exit(1)
	}

	if !errors.Is(askErr, gerrors.ErrMailboxFull) {
		fmt.Printf("FAIL: the full mailbox on node 2 does not come back as ErrMailboxFull: %v\n", askErr)
		os.Exit(1)
	}

	fmt.Println("PASS: a full mailbox on the other node comes back as ErrMailboxFull")
}

// The grains are constructed by GoAkt, so the sample observes them through
// package-level state.
var (
	// gateIdentity is the grain the first message waits on. It stands in for a
	// database that has not answered yet.
	gateIdentity *actor.GrainIdentity

	// firstRequest receives the request the first message makes to the gate
	// once it is in flight. Cancelling it releases the grain.
	firstRequest = make(chan actor.RequestCall, 1)

	handled atomic.Int32
)

// slowGrain waits on the gate for its first message and acknowledges every
// message at once. OnReceive never blocks: the wait is a request in flight,
// during which the grain takes no other message.
type slowGrain types.Unit

// OnActivate does nothing.
func (x *slowGrain) OnActivate(context.Context, *actor.GrainProps) error { return nil }

// OnReceive sends the gate a request on the first message and acknowledges every message.
func (x *slowGrain) OnReceive(ctx *actor.GrainContext) {
	if handled.Add(1) == 1 {
		firstRequest <- ctx.RequestGrain(gateIdentity, new(testpb.TestSend))
	}

	ctx.NoErr()
}

// OnDeactivate does nothing.
func (x *slowGrain) OnDeactivate(context.Context, *actor.GrainProps) error { return nil }

// gateGrain stands in for a database that never answers: it takes ownership
// of the reply and drops it. Its OnReceive returns at once.
type gateGrain types.Unit

// OnActivate does nothing.
func (x *gateGrain) OnActivate(context.Context, *actor.GrainProps) error { return nil }

// OnReceive takes the reply away from the turn and never completes it.
func (x *gateGrain) OnReceive(ctx *actor.GrainContext) { ctx.DeferResponse() }

// OnDeactivate does nothing.
func (x *gateGrain) OnDeactivate(context.Context, *actor.GrainProps) error { return nil }

// startNode starts a cluster node that discovers its peer through the NATS server at natsAddress.
func startNode(ctx context.Context, natsAddress string, discoveryPort, peersPort, remotingPort int) actor.ActorSystem {
	discovery := nats.NewDiscovery(&nats.Config{
		NatsServer:    "nats://" + natsAddress,
		NatsSubject:   "issue-1409",
		Host:          "localhost",
		DiscoveryPort: discoveryPort,
	})

	clusterConfig := actor.
		NewClusterConfig().
		WithDiscovery(discovery).
		WithDiscoveryPort(discoveryPort).
		WithPeersPort(peersPort).
		WithMinimumPeersQuorum(1).
		WithGrains(new(slowGrain), new(gateGrain))

	actorSystem, err := actor.NewActorSystem(
		"issue1409",
		actor.WithRemote(remote.NewConfig("localhost", remotingPort)),
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
