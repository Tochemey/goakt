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

// Reproduction for https://github.com/Tochemey/goakt/issues/1203
//
// In cluster mode, TellGrain/AskGrain resolved the grain owner via the cluster
// registry and then went straight over the remoting stack, even when the owner
// was the calling node. For a node-local grain that turned every send into a
// loopback TCP round trip (serialize, loopback connection, deserialize,
// synchronous ack), capping local grain throughput at roughly 500 msgs/s.
//
// This sample starts a single-node "cluster of one", activates a grain on the
// node, then sends to it. To tell the two transports apart in a way that does
// not depend on machine speed, it relies on pointer identity: the loopback
// remoting path serializes the message, so the grain receives a freshly
// deserialized copy (a different pointer), whereas in-process delivery hands
// the grain the exact pointer the caller passed. Pointer identity at the grain
// therefore proves whether the send went over the wire or stayed local.
//
// With the fix the owner is recognized as local, the grain receives the same
// pointer the caller sent, and a throughput sample sits well above the
// ~500 msgs/s loopback ceiling.
package main

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery/nats"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

const throughputCount = 20_000

var (
	// delivered counts every message the activated grain receives, so the
	// sample can confirm the fast path does not drop messages. It is a
	// package-level counter so the sample can read it without holding a
	// reference to the grain instance the activation factory builds.
	delivered atomic.Int64

	// sentinel is the exact message pointer used for the transport probe;
	// samePointer records whether the grain received that very pointer.
	sentinel    atomic.Pointer[testpb.TestSend]
	samePointer atomic.Bool
)

// counterGrain records what it receives so the sample can assert both delivery
// and the transport used to reach it.
type counterGrain struct{}

func newCounterGrain() *counterGrain {
	return &counterGrain{}
}

func (g *counterGrain) OnActivate(context.Context, *actor.GrainProps) error {
	return nil
}

func (g *counterGrain) OnReceive(ctx *actor.GrainContext) {
	switch msg := ctx.Message().(type) {
	case *testpb.TestSend:
		// Same pointer the caller passed => delivered in-process. A fresh
		// pointer => the message was serialized and round-tripped through the
		// remoting loopback.
		if want := sentinel.Load(); want != nil && msg == want {
			samePointer.Store(true)
		}
		delivered.Add(1)
		ctx.NoErr()
	default:
		ctx.Unhandled()
	}
}

func (g *counterGrain) OnDeactivate(context.Context, *actor.GrainProps) error {
	return nil
}

func startCluster(ctx context.Context, natsAddress string) (actor.ActorSystem, error) {
	discovery := nats.NewDiscovery(&nats.Config{
		NatsServer:    "nats://" + natsAddress,
		NatsSubject:   "cluster",
		Host:          "localhost",
		DiscoveryPort: 8080,
	})

	clusterConfig := actor.
		NewClusterConfig().
		WithDiscovery(discovery).
		WithDiscoveryPort(8080).
		WithPeersPort(8081).
		WithGrains(newCounterGrain())

	actorSystem, err := actor.NewActorSystem(
		"actor",
		actor.WithRemote(remote.NewConfig("localhost", 8082)),
		actor.WithoutRelocation(),
		actor.WithLogger(log.DiscardLogger),
		actor.WithCluster(clusterConfig),
	)
	if err != nil {
		return nil, err
	}

	if err := actorSystem.Start(ctx); err != nil {
		return nil, err
	}

	return actorSystem, nil
}

func newNatsServer() *natsserver.Server {
	serv, err := natsserver.NewServer(&natsserver.Options{
		Host: "localhost",
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

// waitForDelivered blocks until the grain has received at least n messages or
// the deadline elapses, returning the final count.
func waitForDelivered(n int64) int64 {
	deadline := time.Now().Add(5 * time.Second)
	for delivered.Load() < n && time.Now().Before(deadline) {
		pause.For(5 * time.Millisecond)
	}
	return delivered.Load()
}

func main() {
	server := newNatsServer()
	defer server.Shutdown()

	ctx := context.Background()

	actorSystem, err := startCluster(ctx, server.Addr().String())
	if err != nil {
		fmt.Printf("FAIL: error starting cluster: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = actorSystem.Stop(ctx) }()

	// Give the single-node cluster a moment to settle.
	pause.For(time.Second)

	// Activate the grain on this node so it registers as node-local in the
	// cluster registry; every subsequent send resolves the local node as owner.
	identity, err := actorSystem.GrainIdentity(ctx, "local-grain", func(context.Context) (actor.Grain, error) {
		return newCounterGrain(), nil
	})
	if err != nil {
		fmt.Printf("FAIL: error activating grain: %v\n", err)
		os.Exit(1)
	}

	// Deterministic transport probe: send one uniquely identified message and
	// check whether the grain received the exact same pointer (in-process) or a
	// deserialized copy (loopback remoting).
	probe := &testpb.TestSend{}
	sentinel.Store(probe)
	if err := actorSystem.TellGrain(ctx, identity, probe); err != nil {
		fmt.Printf("FAIL: TellGrain (probe) returned an error: %v\n", err)
		os.Exit(1)
	}

	if got := waitForDelivered(1); got < 1 {
		fmt.Println("FAIL: probe message was never delivered to the grain")
		os.Exit(1)
	}

	if !samePointer.Load() {
		fmt.Println("FAIL: grain received a deserialized copy; node-local send routed through the remoting loopback (issue #1203)")
		os.Exit(1)
	}

	fmt.Println("PASS: grain received the caller's exact message pointer; node-local send was delivered in-process")

	// Throughput sample (informational): in-process delivery clears the
	// ~500 msgs/s loopback ceiling by more than an order of magnitude.
	start := time.Now()
	for range throughputCount {
		if err := actorSystem.TellGrain(ctx, identity, &testpb.TestSend{}); err != nil {
			fmt.Printf("FAIL: TellGrain returned an error: %v\n", err)
			os.Exit(1)
		}
	}

	rate := float64(throughputCount) / time.Since(start).Seconds()
	fmt.Printf("throughput: %.0f msgs/s (%d messages)\n", rate, throughputCount)

	// The optimization must not drop messages: probe + throughput batch.
	want := int64(throughputCount + 1)
	if got := waitForDelivered(want); got != want {
		fmt.Printf("FAIL: grain received %d/%d messages\n", got, want)
		os.Exit(1)
	}

	fmt.Printf("PASS: grain received all %d messages\n", want)
	fmt.Println("issue #1203 reproduced and fixed: node-local grain delivery is in-process")
}
