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

// Reproduction for https://github.com/Tochemey/goakt/issues/1444
//
// A node handles a NodeLeft on its cluster events goroutine. When the node
// stops while it is handling one, Stop does not wait for the handler: it
// resets the system, which clears the cluster store, and the handler then
// reads that store and panics. The panic is on a goroutine GoAkt starts, so it
// ends the process.
//
// This sample runs a three-node cluster. Node 3's logger holds the goroutine
// that logs node 3's "detected node left event" line for node 1, which is
// logged by the NodeLeft handler after it checked that the system is running.
// That puts node 3's handler in the window the issue describes every time:
//
//  1. node 1 stops; node 3 starts handling its departure and is held
//  2. node 3 stops; Stop returns while the handler is still held
//  3. the handler is released and carries on against the stopped system
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery/nats"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
)

// settle is how long the sample lets node 3's released handler run before it
// reports. The handler needs well under a millisecond to reach the cluster store.
const settle = time.Second

func main() {
	ctx := context.Background()
	srv := newNatsServer()

	node1 := startNode(ctx, srv.Addr().String(), 9741, 9742, 9743, log.DiscardLogger)
	node2 := startNode(ctx, srv.Addr().String(), 9744, 9745, 9746, log.DiscardLogger)

	holding := &holdingLogger{
		Logger:  log.DiscardLogger,
		line:    "detected node left event: node=" + node1.PeersAddress(),
		held:    make(chan types.Unit),
		release: make(chan types.Unit),
	}

	node3 := startNode(ctx, srv.Addr().String(), 9747, 9748, 9749, holding)
	waitForPeers(ctx, node3, 2)

	// node 1 leaves; node 3's NodeLeft handler starts and is held
	if err := node1.Stop(ctx); err != nil {
		fail("stopping node 1: %v", err)
	}

	select {
	case <-holding.held:
	case <-time.After(30 * time.Second):
		fail("node 3 did not handle the departure of node 1")
	}

	fmt.Println("node 3 is handling the departure of node 1")

	// node 3 stops while its handler is held
	if err := node3.Stop(ctx); err != nil {
		fail("stopping node 3: %v", err)
	}

	fmt.Println("node 3 stopped while handling the departure of node 1")

	// the handler carries on against the stopped node; before the fix it
	// panics here and the process exits with status 2
	close(holding.release)
	pause.For(settle)

	_ = node2.Stop(ctx)
	srv.Shutdown()

	fmt.Println("PASS: node 3 handled the departure of node 1 after its Stop returned without crashing")
}

// idleActor is the actor kind a cluster node requires. The sample does not spawn it.
type idleActor struct{}

// PreStart does nothing.
func (x *idleActor) PreStart(*actor.Context) error { return nil }

// Receive does nothing.
func (x *idleActor) Receive(*actor.ReceiveContext) {}

// PostStop does nothing.
func (x *idleActor) PostStop(*actor.Context) error { return nil }

// holdingLogger discards every log line. The first goroutine that logs line
// at info level is held until release is closed: a logger that is slow to
// write, held on purpose.
type holdingLogger struct {
	log.Logger
	// line is the message that holds the goroutine that logs it.
	line string
	// held is closed once a goroutine is held on line.
	held chan types.Unit
	// release lets the held goroutine continue.
	release chan types.Unit
	// once makes sure only the first goroutine that logs line is held.
	once sync.Once
}

// Infof holds the calling goroutine when it logs line for the first time and
// discards the message.
func (x *holdingLogger) Infof(format string, args ...any) {
	if !strings.HasSuffix(fmt.Sprintf(format, args...), x.line) {
		return
	}

	x.once.Do(func() {
		close(x.held)
		<-x.release
	})
}

// waitForPeers waits until node sees exactly count peers.
func waitForPeers(ctx context.Context, node actor.ActorSystem, count int) {
	deadline := time.Now().Add(30 * time.Second)
	for {
		peers, err := node.Peers(ctx, time.Second)
		if err == nil && len(peers) == count {
			return
		}

		if time.Now().After(deadline) {
			fail("%s does not see %d peers", node.PeersAddress(), count)
		}

		pause.For(50 * time.Millisecond)
	}
}

// startNode starts a cluster node that logs to logger and discovers its peers
// through the NATS server at natsAddress.
func startNode(ctx context.Context, natsAddress string, discoveryPort, peersPort, remotingPort int, logger log.Logger) actor.ActorSystem {
	discovery := nats.NewDiscovery(&nats.Config{
		NatsServer:    "nats://" + natsAddress,
		NatsSubject:   "issue-1444",
		Host:          "localhost",
		DiscoveryPort: discoveryPort,
	}, nats.WithLogger(log.DiscardLogger))

	clusterConfig := actor.
		NewClusterConfig().
		WithDiscovery(discovery).
		WithDiscoveryPort(discoveryPort).
		WithPeersPort(peersPort).
		WithMinimumPeersQuorum(1).
		WithKinds(new(idleActor))

	actorSystem, err := actor.NewActorSystem(
		"issue1444",
		actor.WithRemote(remote.NewConfig("localhost", remotingPort)),
		actor.WithLogger(logger),
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

// fail prints a setup error and exits with status 3, apart from the status 2
// the Go runtime exits with on the panic this sample reproduces.
func fail(format string, args ...any) {
	fmt.Printf("setup failed: "+format+"\n", args...)
	os.Exit(3)
}
