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

	"github.com/tochemey/goakt/v3/actor"
	"github.com/tochemey/goakt/v3/discovery/nats"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/remote"
)

var systemName = "test"

func main() {
	ctx := context.Background()

	server := NatServer()
	serverAddr := server.Addr().String()

	sys1 := startActorSystem(ctx, serverAddr)
	sys2 := startActorSystem(ctx, serverAddr)

	spawnActor(ctx, sys1, "actor1")

	// ideally this sleep should not be needed, but omitting it will fail the next step
	time.Sleep(1 * time.Second)

	// system 2 should be able to retrieve the remote actor from system 1
	fmt.Println("\nRetrieving remote actor from system 1...")
	if _, err := sys2.RemoteActor(ctx, "actor1"); err != nil {
		fmt.Printf("Error retrieving remote actor: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Remote actor retrieved successfully.")

	// stopping system 1 should relocate the actor to system 2
	stopActorSystem(ctx, sys1)

	// ideally this sleep should not be needed, but let's leave time for the expected relocation to happen
	time.Sleep(5 * time.Second)

	// system 2 should now be able to retrieve the local actor - but it fails to do so
	fmt.Println("\nRetrieving local actor from system 2...")
	if _, err := sys2.LocalActor("actor1"); err != nil {
		fmt.Printf("Error retrieving local actor: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Local actor retrieved successfully.")

	stopActorSystem(ctx, sys2)
}

func startActorSystem(ctx context.Context, serverAddress string) actor.ActorSystem {
	fmt.Printf("\nStarting actor system %s...\n", systemName)

	ports := dynaport.Get(3)
	discoveryPort := ports[0]
	remotingPort := ports[1]
	peersPort := ports[2]

	actorSystem, err := actor.NewActorSystem(systemName,
		actor.WithLogger(log.DiscardLogger),
		actor.WithRemote(remote.NewConfig("127.0.0.1", remotingPort)),
		actor.WithCluster(actor.NewClusterConfig().
			WithDiscovery(nats.NewDiscovery(&nats.Config{
				NatsSubject:   "goakt-gossip",
				NatsServer:    fmt.Sprintf("nats://%s", serverAddress),
				Host:          "127.0.0.1",
				DiscoveryPort: discoveryPort,
			})).
			WithDiscoveryPort(discoveryPort).
			WithPeersPort(peersPort).
			WithPartitionCount(7).
			WithReplicaCount(1).
			WithMinimumPeersQuorum(1).
			WithClusterStateSyncInterval(300*time.Millisecond).
			WithClusterBalancerInterval(100*time.Millisecond).
			WithKinds(&MyActor{}),
		))

	if err != nil {
		fmt.Printf("Error creating actor system %s: %v\n", systemName, err)
		os.Exit(1)
	}

	if err := actorSystem.Start(ctx); err != nil {
		fmt.Printf("Error starting actor system %s: %v\n", systemName, err)
		os.Exit(1)
	}

	fmt.Printf("Actor system %s started successfully.\n", systemName)
	return actorSystem
}

func stopActorSystem(ctx context.Context, actorSystem actor.ActorSystem) {
	systemName := actorSystem.Name()
	fmt.Printf("\nStopping actor system %s...\n", systemName)
	if err := actorSystem.Stop(ctx); err != nil {
		fmt.Printf("Error stopping actor system %s: %v\n", systemName, err)
		os.Exit(1)
	}

	fmt.Printf("Actor system %s stopped successfully.\n\n", systemName)
}

func spawnActor(ctx context.Context, actorSystem actor.ActorSystem, actorName string) {
	systemName := actorSystem.Name()
	fmt.Printf("\nSpawning actor %s on %s...\n", actorName, systemName)
	if _, err := actorSystem.Spawn(ctx, actorName, &MyActor{}); err != nil {
		fmt.Printf("Error spawning actor %s on %s: %v\n", actorName, systemName, err)
		os.Exit(1)
	}
	fmt.Printf("Actor %s spawned successfully on %s.\n", actorName, systemName)
}

type MyActor struct{}

func (x *MyActor) PreStart(ctx *actor.Context) error {
	fmt.Printf("%s PreStart on %s\n", ctx.ActorName(), ctx.ActorSystem().Name())
	return nil
}

func (x *MyActor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *actor.PostStart:
		fmt.Printf("%s PostStart on %s\n", ctx.Self().Name(), ctx.ActorSystem().Name())
	default:
		ctx.Unhandled()
	}
}

func (x *MyActor) PostStop(ctx *actor.Context) error {
	fmt.Printf("%s PostStop on %s \n", ctx.ActorName(), ctx.ActorSystem().Name())
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
