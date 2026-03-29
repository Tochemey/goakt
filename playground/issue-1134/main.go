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

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/travisjeffery/go-dynaport"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/crdt"
	"github.com/tochemey/goakt/v4/discovery/nats"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
)

// sessionsKey is a typed CRDT key for an ORSet of session IDs.
var sessionsKey = crdt.ORSetKey("active-sessions")

// -- Messages --

type AddSession struct{ SessionID string }
type RemoveSession struct{ SessionID string }
type PrintSessions struct{}

// -- SessionTracker: writes and reads the distributed session set --

type SessionTracker struct{}

func (a *SessionTracker) PreStart(*actor.Context) error { return nil }
func (a *SessionTracker) PostStop(*actor.Context) error { return nil }

func (a *SessionTracker) Receive(ctx *actor.ReceiveContext) {
	replicator := ctx.ActorSystem().Replicator()
	if replicator == nil {
		return
	}

	switch msg := ctx.Message().(type) {
	case *actor.PostStart:
		fmt.Printf("[%s] SessionTracker started\n", ctx.ActorSystem().PeersAddress())
	case *AddSession:
		nodeID := ctx.Self().Path().Name()
		ctx.Tell(replicator, &crdt.Update{
			Key:     sessionsKey,
			Initial: crdt.NewORSet(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.ORSet).Add(nodeID, msg.SessionID)
			},
		})
		fmt.Printf("[%s] added session: %s\n", ctx.ActorSystem().PeersAddress(), msg.SessionID)
	case *RemoveSession:
		ctx.Tell(replicator, &crdt.Update{
			Key:     sessionsKey,
			Initial: crdt.NewORSet(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.ORSet).Remove(msg.SessionID)
			},
		})
		fmt.Printf("[%s] removed session: %s\n", ctx.ActorSystem().PeersAddress(), msg.SessionID)
	case *PrintSessions:
		reply := ctx.Ask(replicator, &crdt.Get{
			Key: sessionsKey,
		}, time.Second)
		resp, ok := reply.(*crdt.GetResponse)
		if ok && resp.Data != nil {
			set := resp.Data.(*crdt.ORSet)
			fmt.Printf("[%s] active sessions: %v (count=%d)\n",
				ctx.ActorSystem().PeersAddress(), set.Elements(), set.Len())
		} else {
			fmt.Printf("[%s] no sessions found\n", ctx.ActorSystem().PeersAddress())
		}
	}
}

// -- SessionReporter: watches for changes on the session set --

type SessionReporter struct{}

func (a *SessionReporter) PreStart(*actor.Context) error { return nil }
func (a *SessionReporter) PostStop(*actor.Context) error { return nil }

func (a *SessionReporter) Receive(ctx *actor.ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *actor.PostStart:
		replicator := ctx.ActorSystem().Replicator()
		if replicator != nil {
			ctx.Tell(replicator, &crdt.Subscribe{
				Key: sessionsKey,
			})
			fmt.Printf("[%s] SessionReporter subscribed to session changes\n",
				ctx.ActorSystem().PeersAddress())
		}
	case *crdt.Changed:
		set := msg.Data.(*crdt.ORSet)
		fmt.Printf("[%s] ** change notification ** sessions=%v count=%d\n",
			ctx.ActorSystem().PeersAddress(), set.Elements(), set.Len())
	}
}

func main() {
	ctx := context.Background()

	// start embedded NATS server
	server := startNATSServer()
	defer server.Shutdown()
	serverAddr := server.Addr().String()
	fmt.Printf("NATS server started at %s\n\n", serverAddr)

	// start a 3-node cluster with CRDT enabled
	sys1 := startNode(ctx, "crdt-demo", serverAddr)
	sys2 := startNode(ctx, "crdt-demo", serverAddr)
	sys3 := startNode(ctx, "crdt-demo", serverAddr)
	pause.For(3 * time.Second) // wait for cluster formation

	fmt.Println("\n=== Cluster formed with 3 nodes ===")
	fmt.Printf("  Node 1: %s (replicator=%v)\n", sys1.PeersAddress(), sys1.Replicator() != nil)
	fmt.Printf("  Node 2: %s (replicator=%v)\n", sys2.PeersAddress(), sys2.Replicator() != nil)
	fmt.Printf("  Node 3: %s (replicator=%v)\n", sys3.PeersAddress(), sys3.Replicator() != nil)

	// spawn tracker on node 1, reporter on node 2
	tracker1, err := sys1.Spawn(ctx, "tracker", &SessionTracker{})
	if err != nil {
		fmt.Printf("Error spawning tracker: %v\n", err)
		os.Exit(1)
	}

	_, err = sys2.Spawn(ctx, "reporter", &SessionReporter{})
	if err != nil {
		fmt.Printf("Error spawning reporter: %v\n", err)
		os.Exit(1)
	}
	pause.For(time.Second)

	// --- Add sessions from node 1 ---
	fmt.Println("\n=== Adding sessions from Node 1 ===")
	_ = actor.Tell(ctx, tracker1, &AddSession{SessionID: "sess-alice"})
	_ = actor.Tell(ctx, tracker1, &AddSession{SessionID: "sess-bob"})
	_ = actor.Tell(ctx, tracker1, &AddSession{SessionID: "sess-charlie"})
	pause.For(3 * time.Second) // wait for delta replication

	// --- Read from node 1 (local) ---
	fmt.Println("\n=== Reading sessions from Node 1 (local read) ===")
	_ = actor.Tell(ctx, tracker1, &PrintSessions{})
	pause.For(500 * time.Millisecond)

	// --- Spawn tracker on node 3 and read (should see replicated data) ---
	fmt.Println("\n=== Reading sessions from Node 3 (replicated data) ===")
	tracker3, err := sys3.Spawn(ctx, "tracker-3", &SessionTracker{})
	if err != nil {
		fmt.Printf("Error spawning tracker on node 3: %v\n", err)
		os.Exit(1)
	}
	pause.For(500 * time.Millisecond)
	_ = actor.Tell(ctx, tracker3, &PrintSessions{})
	pause.For(500 * time.Millisecond)

	// --- Remove a session from node 1 ---
	fmt.Println("\n=== Removing session from Node 1 ===")
	_ = actor.Tell(ctx, tracker1, &RemoveSession{SessionID: "sess-bob"})
	pause.For(3 * time.Second)

	// --- Read again from node 3 ---
	fmt.Println("\n=== Reading sessions from Node 3 (after removal) ===")
	_ = actor.Tell(ctx, tracker3, &PrintSessions{})
	pause.For(500 * time.Millisecond)

	// --- Shutdown ---
	fmt.Println("\n=== Shutting down ===")
	_ = sys1.Stop(ctx)
	_ = sys2.Stop(ctx)
	_ = sys3.Stop(ctx)
	fmt.Println("Done.")
}

func startNode(ctx context.Context, systemName, natsAddr string) actor.ActorSystem {
	ports := dynaport.Get(3)
	host := "127.0.0.1"

	disco := nats.NewDiscovery(&nats.Config{
		NatsServer:    fmt.Sprintf("nats://%s", natsAddr),
		NatsSubject:   "crdt-demo-discovery",
		Host:          host,
		DiscoveryPort: ports[0],
	}, nats.WithLogger(log.DiscardLogger))

	sys, err := actor.NewActorSystem(systemName,
		actor.WithLogger(log.DiscardLogger),
		actor.WithRemote(remote.NewConfig(host, ports[1])),
		actor.WithCluster(actor.NewClusterConfig().
			WithDiscovery(disco).
			WithPeersPort(ports[2]).
			WithMinimumPeersQuorum(1).
			WithDiscoveryPort(ports[0]).
			WithBootstrapTimeout(time.Second).
			WithKinds(&SessionTracker{}, &SessionReporter{}).
			WithCRDT(
				crdt.WithAntiEntropyInterval(5*time.Second),
				crdt.WithPruneInterval(30*time.Second),
				crdt.WithTombstoneTTL(time.Minute),
			),
		),
	)
	if err != nil {
		fmt.Printf("Error creating actor system: %v\n", err)
		os.Exit(1)
	}

	if err := sys.Start(ctx); err != nil {
		fmt.Printf("Error starting actor system: %v\n", err)
		os.Exit(1)
	}

	return sys
}

func startNATSServer() *natsserver.Server {
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
		fmt.Println("NATS server failed to start")
		os.Exit(1)
	}
	return serv
}
