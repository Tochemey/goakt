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
	"syscall"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/travisjeffery/go-dynaport"

	"github.com/tochemey/goakt/v3/actor"
	"github.com/tochemey/goakt/v3/discovery/nats"
	"github.com/tochemey/goakt/v3/remote"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

type HelloWorld struct{}

var _ actor.Actor = (*HelloWorld)(nil)

// NewHelloWorld creates an instance
func NewHelloWorld() *HelloWorld {
	return &HelloWorld{}
}

func (x *HelloWorld) PreStart(ctx *actor.Context) error {
	fmt.Println("Starting HelloWorld actor")
	return nil
}

func (x *HelloWorld) Receive(ctx *actor.ReceiveContext) {
	fmt.Println("HelloWorld received a message")
}

func (x *HelloWorld) PostStop(ctx *actor.Context) error {
	fmt.Println("Stopping HelloWorld actor")
	return nil
}

func startCluster(ctx context.Context, natsAddress string) (actor.ActorSystem, error) {
	ports := dynaport.Get(3)
	discoveryPort := ports[0]
	remotingPort := ports[1]
	peersPort := ports[2]

	host := "127.0.0.1"
	discovery := nats.NewDiscovery(&nats.Config{
		NatsServer:    "nats://" + natsAddress,
		NatsSubject:   "cluster",
		Host:          host,
		DiscoveryPort: discoveryPort,
	})

	clusterConfig := actor.
		NewClusterConfig().
		WithDiscovery(discovery).
		WithDiscoveryPort(discoveryPort).
		WithPeersPort(peersPort).
		WithKinds(NewHelloWorld())

	actorSystem, err := actor.NewActorSystem(
		"actor",
		actor.WithRemote(remote.NewConfig(host, remotingPort)),
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

	fmt.Printf("NATS server started: %s\n", serv.Addr().String())
	return serv
}

func main() {
	args := os.Args[1:]
	if len(args) > 1 {
		fmt.Println("Usage: crash.go <nats-server-if-second-node>")
		os.Exit(1)
	}

	var natsAddress string
	if len(args) == 0 {
		server := NatServer()
		natsAddress = server.Addr().String()
		defer server.Shutdown()

		fmt.Println("NATS server running at", natsAddress)
	} else {
		natsAddress = args[0]
	}

	ctx := context.Background()

	actorSystem, err := startCluster(ctx, natsAddress)
	if err != nil {
		fmt.Printf("Error starting cluster: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Cluster started")

	err = actorSystem.SpawnSingleton(ctx, "hello-world", NewHelloWorld())
	if err != nil {
		fmt.Printf("Error spawning singleton actor: %v\n", err)
	}

	time.Sleep(2 * time.Second)

	err = actorSystem.NoSender().SendAsync(ctx, "hello-world", &testpb.TestPing{})
	if err != nil {
		fmt.Printf("Error sending message: %v\n", err)
		os.Exit(1)
	}

	// capture ctrl+c
	interruptSignal := make(chan os.Signal, 1)
	signal.Notify(interruptSignal, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-interruptSignal

	// stop the actor system
	_ = actorSystem.Stop(ctx)
	os.Exit(0)
}
