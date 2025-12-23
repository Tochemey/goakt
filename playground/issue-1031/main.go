/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/tochemey/goakt/v3/actor"
	"github.com/tochemey/goakt/v3/discovery/nats"
	"github.com/tochemey/goakt/v3/log"
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
	return nil
}

func (x *Grain) OnReceive(ctx *actor.GrainContext) {
	switch ctx.Message().(type) {
	case *testpb.TestMessage:
		ctx.NoErr()
	default:
		ctx.Unhandled()
	}
}

func (x *Grain) OnDeactivate(ctx context.Context, _ *actor.GrainProps) error {
	fmt.Println("Grain deactivated")
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
		WithGrains(NewGrain())

	actorSystem, err := actor.NewActorSystem(
		"actor",
		actor.WithRemote(remote.NewConfig("localhost", 8082)),
		actor.WithoutRelocation(),
		actor.WithLogger(log.DebugLogger),
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

func main() {
	server := NatServer()
	natsAddress := server.Addr().String()
	defer server.Shutdown()

	ctx := context.Background()

	actorSystem, err := startCluster(ctx, natsAddress)
	if err != nil {
		fmt.Printf("Error starting cluster: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Cluster started")

	for i := range 10 {
		identity, err := actorSystem.GrainIdentity(
			ctx,
			fmt.Sprintf("test-%d", i),
			func(ctx context.Context) (actor.Grain, error) {
				return NewGrain(), nil
			},
		)
		if err != nil {
			fmt.Printf("Error creating grain: %v\n", err)
			os.Exit(1)
		}
		actorSystem.TellGrain(ctx, identity, &testpb.TestMessage{})
	}

	time.Sleep(100 * time.Millisecond)

	actorSystem.Stop(ctx)
}
