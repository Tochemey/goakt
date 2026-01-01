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
	"os/signal"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/tochemey/goakt/v3/actor"
	"github.com/tochemey/goakt/v3/discovery/nats"
	"github.com/tochemey/goakt/v3/remote"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

type Grain struct {
	name string
}

func NewGrain() *Grain {
	return &Grain{}
}

func (x *Grain) OnActivate(ctx context.Context, props *actor.GrainProps) error {
	x.name = props.Identity().Name()
	fmt.Printf("Grain activated: %s\n", x.name)
	return nil
}

func (x *Grain) OnReceive(ctx *actor.GrainContext) {
	switch ctx.Message().(type) {
	case *testpb.TestReadiness:
		fmt.Printf("Grain %s received TestReadiness message\n", x.name)
		ctx.Response(&testpb.TestReady{})
	default:
		ctx.Unhandled()
	}
}

func (x *Grain) OnDeactivate(ctx context.Context, _ *actor.GrainProps) error {
	return nil
}

func startCluster(ctx context.Context, portOffset int, natsAddress string) (actor.ActorSystem, error) {
	discovery := nats.NewDiscovery(&nats.Config{
		NatsServer:    "nats://" + natsAddress,
		NatsSubject:   "cluster",
		Host:          "localhost",
		DiscoveryPort: 8080 + portOffset,
	})

	clusterConfig := actor.
		NewClusterConfig().
		WithDiscovery(discovery).
		WithDiscoveryPort(8080 + portOffset).
		WithPeersPort(8081 + portOffset).
		WithGrains(NewGrain())

	actorSystem, err := actor.NewActorSystem(
		"actor",
		actor.WithRemote(remote.NewConfig("localhost", 8082+portOffset)),
		actor.WithoutRelocation(),
		actor.WithCluster(clusterConfig),
	)
	if err != nil {
		return nil, err
	}

	fmt.Println("Starting actor system ...")

	err = actorSystem.Start(ctx)
	if err != nil {
		return nil, err
	}

	return actorSystem, nil
}

func NatServer() *natsserver.Server {
	serv, err := natsserver.NewServer(&natsserver.Options{
		Host: "localhost",
		Port: 42222,
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

func main() {
	args := os.Args[1:]
	if len(args) > 1 {
		fmt.Println("Usage: crash.go <nats-server-if-second-node>")
		os.Exit(1)
	}

	var natsAddress string
	portOffset := 0
	if len(args) == 0 {
		server := NatServer()
		natsAddress = server.Addr().String()
		defer server.Shutdown()

		fmt.Println("NATS server running at", natsAddress)
	} else {
		natsAddress = args[0]
		portOffset = 10
	}

	ctx := context.Background()

	actorSystem, err := startCluster(ctx, portOffset, natsAddress)
	if err != nil {
		fmt.Printf("Error starting cluster: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Cluster started")

	if len(args) == 0 {
		for i := range 10000 {
			time.Sleep(5 * time.Millisecond)

			identity, err := actorSystem.GrainIdentity(
				ctx,
				fmt.Sprintf("name-%d", i),
				func(ctx context.Context) (actor.Grain, error) {
					return NewGrain(), nil
				},
				actor.WithActivationStrategy(actor.RandomActivation),
			)
			if err != nil {
				fmt.Printf("Error getting grain: %v\n", err)
				continue
			}

			fmt.Printf("Asking grain %s\n", identity.String())
			actorSystem.AskGrain(ctx, identity, &testpb.TestReadiness{}, time.Second)
		}
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	<-ch
}
