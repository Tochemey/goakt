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
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	discoverynats "github.com/tochemey/goakt/v4/discovery/nats"
	"github.com/tochemey/goakt/v4/remote"
)

type markerActor struct{}

func (*markerActor) PreStart(*actor.Context) error { return nil }
func (*markerActor) Receive(*actor.ReceiveContext) {}
func (*markerActor) PostStop(*actor.Context) error { return nil }

type roleGrain struct{}

func (*roleGrain) OnActivate(_ context.Context, props *actor.GrainProps) error {
	log.Printf("ACTIVATED grain=%s node=%s:%d",
		props.Identity().Name(), props.ActorSystem().Host(), props.ActorSystem().Port())
	return nil
}
func (*roleGrain) OnReceive(ctx *actor.GrainContext) { ctx.NoErr() }
func (*roleGrain) OnDeactivate(_ context.Context, props *actor.GrainProps) error {
	log.Printf("DEACTIVATED grain=%s node=%s:%d",
		props.Identity().Name(), props.ActorSystem().Host(), props.ActorSystem().Port())
	return nil
}

func main() {
	id := flag.String("id", "gateway", "node label")
	rolesCSV := flag.String("roles", "gateway", "comma-separated cluster roles")
	discoveryPort := flag.Int("discovery-port", 33220, "discovery port")
	peersPort := flag.Int("peers-port", 33221, "cluster peers port")
	remotingPort := flag.Int("remoting-port", 33222, "remoting port")
	activate := flag.Bool("activate", false, "activate the test Grain after the cluster settles")
	flag.Parse()

	roles := strings.Split(*rolesCSV, ",")
	discovery := discoverynats.NewDiscovery(&discoverynats.Config{
		NatsServer:    "nats://127.0.0.1:4222",
		NatsSubject:   "goakt.grain-role-repro.v1",
		Host:          "127.0.0.1",
		DiscoveryPort: *discoveryPort,
	})
	cluster := actor.NewClusterConfig().
		WithKinds(&markerActor{}).
		WithRoles(roles...).
		WithPartitionCount(7).
		WithReplicaCount(2).
		WithPeersPort(*peersPort).
		WithDiscoveryPort(*discoveryPort).
		WithMinimumPeersQuorum(1).
		WithBootstrapTimeout(15 * time.Second).
		WithClusterStateSyncInterval(300 * time.Millisecond).
		WithDiscovery(discovery)

	system, err := actor.NewActorSystem("grain-role-repro",
		actor.WithRemote(remote.NewConfig("127.0.0.1", *remotingPort)),
		actor.WithCluster(cluster),
		actor.WithShutdownTimeout(30*time.Second),
	)
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	if err := system.Start(ctx); err != nil {
		log.Fatal(err)
	}
	if err := system.RegisterGrainKind(ctx, &roleGrain{}); err != nil {
		log.Fatal(err)
	}
	log.Printf("STARTED id=%s roles=%v remoting=%d", *id, roles, *remotingPort)

	if *activate {
		time.Sleep(8 * time.Second)
		identity, err := actor.GrainOf[*roleGrain](ctx, system, "role-bound-grain",
			actor.WithActivationRole("game-worker"),
			actor.WithActivationStrategy(actor.RoundRobinActivation),
			actor.WithGrainEagerRelocation(),
		)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("CREATED identity=%s", identity.String())
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := system.Stop(stopCtx); err != nil {
		fmt.Println(err)
	}
}
