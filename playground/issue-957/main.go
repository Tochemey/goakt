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
	"errors"
	"os"
	"os/signal"
	"syscall"

	goakt "github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery/dnssd"
	goakterrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
)

type MySingleton struct{}

var _ goakt.Actor = (*MySingleton)(nil)

func NewSingleton() *MySingleton { return &MySingleton{} }

func (m MySingleton) PreStart(*goakt.Context) error {
	return nil
}

func (m MySingleton) Receive(*goakt.ReceiveContext) {
}

func (m MySingleton) PostStop(*goakt.Context) error {
	return nil
}

func main() {
	ctx := context.Background()
	discoConfig := dnssd.Config{
		DomainName: "mydockername",
	}
	disco := dnssd.NewDiscovery(&discoConfig)

	clusterConfig := goakt.
		NewClusterConfig().
		WithKinds(new(MySingleton)).
		WithDiscovery(disco).
		WithDiscoveryPort(3322).
		WithPeersPort(3320)

	hostname, _ := os.Hostname()

	actorSystem, err := goakt.NewActorSystem("test",
		goakt.WithLogger(log.DefaultLogger),
		goakt.WithActorInitMaxRetries(3),
		goakt.WithRemote(remote.NewConfig(hostname, 50052)),
		goakt.WithCluster(clusterConfig),
	)
	if err != nil {
		panic(err)
	}

	err = actorSystem.Start(ctx)
	if err != nil {
		panic(err)
	}

	err = actorSystem.SpawnSingleton(context.Background(), "MySingleton", NewSingleton())
	if err != nil {
		if !errors.Is(err, goakterrors.ErrSingletonAlreadyExists) {
			panic(err)
		}
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM)
	signal.Notify(ch, syscall.SIGINT)

	<-ch
	actorSystem.Stop(ctx)
}
