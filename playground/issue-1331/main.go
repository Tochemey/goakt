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
	"net"
	"os"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery/nats"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
)

const systemName = "node-left-delay-repro"

type singleton struct{}

func (*singleton) PreStart(*actor.Context) error { return nil }
func (*singleton) PostStop(*actor.Context) error { return nil }
func (*singleton) Receive(ctx *actor.ReceiveContext) {
	if _, ok := ctx.Message().(*wrapperspb.StringValue); ok {
		ctx.Response(wrapperspb.String("pong"))
		return
	}
	ctx.Unhandled()
}

func main() {
	ctx := context.Background()
	ns, err := natsserver.NewServer(&natsserver.Options{Host: "127.0.0.1", Port: -1, NoLog: true})
	must(err)
	go ns.Start()
	if !ns.ReadyForConnections(2 * time.Second) {
		panic("NATS did not start")
	}
	defer ns.Shutdown()

	nodes := make([]actor.ActorSystem, 0, 3)
	for i := range 3 {
		nodes = append(nodes, startNode(ctx, i+1, ns.ClientURL()))
		time.Sleep(500 * time.Millisecond)
	}
	defer func() {
		for _, node := range nodes[1:] {
			stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = node.Stop(stopCtx)
			cancel()
		}
	}()

	_, err = nodes[0].SpawnSingleton(ctx, "singleton", &singleton{})
	must(err)
	time.Sleep(2 * time.Second)
	must(ping(ctx, nodes[2]))

	started := time.Now()
	stopCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	_ = nodes[0].Stop(stopCtx)
	cancel()
	stopped := time.Since(started)
	fmt.Printf("node-1 stopped after %s\n", stopped.Round(10*time.Millisecond))

	// probe only once node-1 has fully stopped: while it is still shutting down
	// its own singleton can answer, which would report a recovery that never
	// happened
	for time.Since(started) < 45*time.Second {
		if err := ping(ctx, nodes[2]); err == nil {
			fmt.Printf("singleton recovered after %s (%s after node-1 stopped)\n", time.Since(started).Round(10*time.Millisecond), (time.Since(started) - stopped).Round(10*time.Millisecond))
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Println("singleton did not recover within 45s")
	os.Exit(1)
}

func startNode(ctx context.Context, number int, natsURL string) actor.ActorSystem {
	discoveryPort, remotingPort, peersPort := freePort(), freePort(), freePort()
	disco := nats.NewDiscovery(&nats.Config{
		NatsServer: natsURL, NatsSubject: "node-left-delay-repro",
		Host: "127.0.0.1", DiscoveryPort: discoveryPort,
	}, nats.WithLogger(log.DefaultLogger))
	cluster := actor.NewClusterConfig().
		WithDiscovery(disco).
		WithDiscoveryPort(discoveryPort).
		WithPeersPort(peersPort).
		WithKinds(&singleton{}).
		WithReplicaCount(2).
		WithMinimumPeersQuorum(1).
		WithBootstrapTimeout(2 * time.Second).
		WithClusterStateSyncInterval(time.Second).
		WithClusterBalancerInterval(500 * time.Millisecond)
	system, err := actor.NewActorSystem(systemName,
		actor.WithLogger(log.DefaultLogger),
		actor.WithRemote(remote.NewConfig("127.0.0.1", remotingPort)),
		actor.WithCluster(cluster),
	)
	must(err)
	must(system.Start(ctx))
	fmt.Printf("node-%d started: %s:%d\n", number, system.Host(), system.Port())
	return system
}

func ping(ctx context.Context, system actor.ActorSystem) error {
	pid, err := system.ActorOf(ctx, "singleton")
	if err != nil {
		return err
	}
	response, err := actor.Ask(ctx, pid, wrapperspb.String("ping"), time.Second)
	if err != nil {
		return err
	}
	if response.(*wrapperspb.StringValue).Value != "pong" {
		return fmt.Errorf("unexpected response: %v", response)
	}
	return nil
}

func freePort() int {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	must(err)
	port := listener.Addr().(*net.TCPAddr).Port
	must(listener.Close())
	return port
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
