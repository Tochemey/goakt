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
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/remote"
)

const (
	numPasses = 10
	actorName = "actor1"
	system    = "system"
)

func main() {
	ctx := context.Background()

	server := NatServer()
	serverAddr := server.Addr().String()
	defer server.Shutdown()

	// make two actor systems
	sys1 := startActorSystem(ctx, system, serverAddr)
	sys2 := startActorSystem(ctx, system, serverAddr)

	spawnActor(ctx, sys1, actorName)

	for i := range numPasses {
		fmt.Printf("\nStarting pass %d/%d...\n", i+1, numPasses)

		// the actor should be local to system 1, and remote to system 2
		checkLocalActor(sys1, actorName)
		checkRemoteActor(sys2, actorName)

		// restarting system 1 should relocate the actor to system 2
		stopActorSystem(ctx, sys1)

		time.Sleep(3 * time.Second)

		sys1 = startActorSystem(ctx, system, serverAddr)

		// now the actor should be local to system 2, and remote to system 1
		checkLocalActor(sys2, actorName)
		checkRemoteActor(sys1, actorName)

		// restarting system 2 should relocate the actor back to system 1
		stopActorSystem(ctx, sys2)

		// for rebalancing to work, we need to wait a bit
		time.Sleep(3 * time.Second)

		sys2 = startActorSystem(ctx, system, serverAddr)
	}
}

func checkLocalActor(system actor.ActorSystem, actorName string) {
	fmt.Printf("Checking if local actor %s exists on %s ...\n", actorName, system.Name())
	_, err := system.LocalActor(actorName)
	if err != nil {
		fmt.Printf("Error checking local actor %s: %v\n", actorName, err)
		os.Exit(1)
	}
	fmt.Printf("Local actor %s exists on %s.\n", actorName, system.Name())
}

func checkRemoteActor(system actor.ActorSystem, actorName string) {
	_, err := system.RemoteActor(context.Background(), actorName)
	if err != nil {
		fmt.Printf("Error checking remote actor %s: %v\n", actorName, err)
		os.Exit(1)
	}
	fmt.Printf("Remote actor %s exists on %s.\n", actorName, system.Name())
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
		actor.WithLogger(log.DefaultLogger),
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

func stopActorSystem(ctx context.Context, actorSystem actor.ActorSystem) {
	systemName := actorSystem.Name()
	fmt.Printf("Stopping actor system %s...\n", systemName)
	if err := actorSystem.Stop(ctx); err != nil {
		fmt.Printf("Error stopping actor system %s: %v\n", systemName, err)
		os.Exit(1)
	}

	fmt.Printf("Actor system %s stopped successfully.\n", systemName)
}

func spawnActor(ctx context.Context, actorSystem actor.ActorSystem, actorName string) {
	systemName := actorSystem.Name()
	fmt.Printf("Spawning long lived actor %s on %s...\n", actorName, systemName)
	if _, err := actorSystem.Spawn(ctx, actorName, &MyActor{}, actor.WithLongLived()); err != nil {
		fmt.Printf("Error spawning actor %s on %s: %v\n", actorName, systemName, err)
		os.Exit(1)
	}

	time.Sleep(time.Second) // wait for actor to be ready
	fmt.Printf("Actor %s spawned successfully on %s.\n", actorName, systemName)
}

type MyActor struct{}

func (x *MyActor) PreStart(ctx *actor.Context) error {
	fmt.Printf("%s PreStart on %s\n", ctx.ActorName(), ctx.ActorSystem().Name())
	return nil
}

func (x *MyActor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
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
