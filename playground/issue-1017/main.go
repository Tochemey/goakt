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
	"github.com/tochemey/goakt/v4/discovery/nats"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/supervisor"
)

func main() {
	ctx := context.Background()
	systemName := "test"
	server := NatServer()
	serverAddr := server.Addr().String()

	sys1 := startActorSystem(ctx, systemName, serverAddr)
	sys2 := startActorSystem(ctx, systemName, serverAddr)

	spawnActor(ctx, sys1, "actor1")

	// ideally this sleep should not be needed, but omitting it will fail the next step
	pause.For(1 * time.Second)

	pid, _ := sys1.LocalActor("actor1")
	children := pid.Children()
	fmt.Printf("Actor has %d children before relocation\n", len(children))

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
	pause.For(5 * time.Second)

	// system 2 should now be able to retrieve the local actor - but it fails to do so
	fmt.Println("\nRetrieving local actor from system 2...")
	pid, err := sys2.LocalActor("actor1")
	if err != nil {
		fmt.Printf("Error retrieving local actor: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Local actor retrieved successfully.")
	children = pid.Children()
	fmt.Printf("Actor has %d children after relocation\n", len(children))

	stopActorSystem(ctx, sys2)
}

func startActorSystem(ctx context.Context, systemName, serverAddr string) actor.ActorSystem {
	fmt.Printf("Starting actor system %s...\n", systemName)

	ports := dynaport.Get(3)
	discoveryPort := ports[0]
	remotingPort := ports[1]
	peersPort := ports[2]

	fmt.Printf("System %s is using ports - Discovery: %d, Remoting: %d, Peers: %d\n", systemName, discoveryPort, remotingPort, peersPort)

	host := "127.0.0.1"
	actorSystem, err := actor.NewActorSystem(systemName,
		actor.WithLogger(log.DiscardLogger),
		actor.WithRemote(remote.NewConfig(host, remotingPort)),
		actor.WithCluster(actor.NewClusterConfig().
			WithDiscovery(nats.NewDiscovery(&nats.Config{
				NatsServer:    fmt.Sprintf("nats://%s", serverAddr),
				NatsSubject:   "some-subject",
				Host:          host,
				DiscoveryPort: discoveryPort,
			})).
			WithPartitionCount(7).
			WithReplicaCount(1).
			WithPeersPort(peersPort).
			WithMinimumPeersQuorum(1).
			WithDiscoveryPort(discoveryPort).
			WithBootstrapTimeout(time.Second).
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

func spawnActor(ctx context.Context, actorSystem actor.ActorSystem, actorName string) {
	peersAddr := actorSystem.PeersAddress()

	supervisor := supervisor.NewSupervisor(
		supervisor.WithStrategy(supervisor.OneForOneStrategy),
		supervisor.WithAnyErrorDirective(supervisor.RestartDirective),
	)

	fmt.Printf("Spawning long lived actor %s on %s...\n", actorName, peersAddr)
	pid, err := actorSystem.Spawn(ctx, actorName, &MyActor{}, actor.WithLongLived(), actor.WithSupervisor(supervisor))
	if err != nil {
		fmt.Printf("Error spawning actor %s on %s: %v\n", actorName, peersAddr, err)
		os.Exit(1)
	}

	pause.For(time.Second) // wait for actor to be ready
	fmt.Printf("Actor %s spawned successfully on %s.\n", actorName, peersAddr)

	// let us start three children for this actor
	for i := range 3 {
		childName := fmt.Sprintf("%s-child-%d", actorName, i)
		fmt.Printf("Spawning child actor %s under %s on %s...\n", childName, actorName, peersAddr)
		_, err := pid.SpawnChild(ctx, childName, &MyActor{}, actor.WithLongLived(), actor.WithSupervisor(supervisor))
		if err != nil {
			fmt.Printf("Error spawning child actor %s under %s on %s: %v\n", childName, actorName, peersAddr, err)
			os.Exit(1)
		}
		pause.For(time.Second) // wait for child actor to be ready
		fmt.Printf("Child actor %s under %s spawned successfully on %s.\n", childName, actorName, peersAddr)
	}
}

func stopActorSystem(ctx context.Context, actorSystem actor.ActorSystem) {
	peersAddress := actorSystem.PeersAddress()
	fmt.Printf("Stopping actor system %s...\n", peersAddress)
	if err := actorSystem.Stop(ctx); err != nil {
		fmt.Printf("Error stopping actor system %s: %v\n", peersAddress, err)
		os.Exit(1)
	}

	fmt.Printf("Actor system %s stopped successfully.\n", peersAddress)
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
