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
	"net/http"
	_ "net/http/pprof" //nolint:gosec
	"os"
	"os/signal"
	"syscall"

	goakt "github.com/tochemey/goakt/v3/actor"
	"github.com/tochemey/goakt/v3/discovery/static"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/remote"
	"github.com/tochemey/goakt/v3/supervisor"
)

const (
	ActorsCount = 10_000
	ClusterMode = true
)

func init() {
	go func() {
		// visit http://localhost:6060/debug/pprof/
		http.ListenAndServe("localhost:6060", nil) //nolint:gosec
	}()
}

type Actor1 struct {
	SelfID int
}

func NewActor1(selfID int) *Actor1 {
	return &Actor1{
		SelfID: selfID,
	}
}

func (n *Actor1) PreStart(_ *goakt.Context) (err error) {
	return nil
}

func (n *Actor1) Receive(ctx *goakt.ReceiveContext) {
	switch ctx.Message().(type) {
	case *goakt.PostStart:
	default:
		ctx.Unhandled()
	}
}

func (n *Actor1) PostStop(*goakt.Context) error {
	return nil
}

func main() {
	ctx := context.Background()
	logger := log.New(log.InfoLevel, os.Stdout)

	var actorSystem goakt.ActorSystem
	var err error
	if ClusterMode {
		staticConfig := static.Config{
			Hosts: []string{
				"localhost:3322",
			},
		}

		discovery := static.NewDiscovery(&staticConfig)

		clusterConfig := goakt.
			NewClusterConfig().
			WithDiscovery(discovery).
			WithPartitionCount(10).
			WithMinimumPeersQuorum(1).
			WithReplicaCount(1).
			WithDiscoveryPort(3322).
			WithPeersPort(3320).
			WithKinds(new(Actor1))

		actorSystem, err = goakt.NewActorSystem("memleak1",
			goakt.WithLogger(logger),
			goakt.WithActorInitMaxRetries(1),
			goakt.WithShutdownTimeout(10),
			goakt.WithRemote(remote.NewConfig("localhost", 50022)),
			goakt.WithoutRelocation(),
			goakt.WithCluster(clusterConfig),
		)
	} else {
		actorSystem, err = goakt.NewActorSystem("memleak1",
			goakt.WithLogger(logger),
			goakt.WithActorInitMaxRetries(1),
			goakt.WithShutdownTimeout(10),
		)
	}

	if err != nil {
		panic(err)
	}

	logger.Info("Start system")
	err = actorSystem.Start(ctx)
	if err != nil {
		panic(err)
	}
	logger.Info("System started")

	logger.Infof("Swawning %d actors", ActorsCount)

	for i := range ActorsCount {
		_, err = actorSystem.Spawn(
			context.Background(),
			fmt.Sprintf("actor-%d", i),
			NewActor1(i),
			goakt.WithLongLived(),
			goakt.WithSupervisor(
				supervisor.NewSupervisor(
					supervisor.WithStrategy(supervisor.OneForOneStrategy),
					supervisor.WithAnyErrorDirective(supervisor.ResumeDirective),
				)),
		)
		if err != nil {
			panic(err)
		}
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM)
	signal.Notify(ch, syscall.SIGINT)

	<-ch

	_ = actorSystem.Stop(ctx)
	logger.Info("System stopped")
}
