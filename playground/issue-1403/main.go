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

// Reproduction for https://github.com/Tochemey/goakt/issues/1403
//
// A cluster node keeps its peer state in a BoltDB file under
// "$HOME/.goakt/cluster", and nothing let a caller choose another place.
// A pod that runs with a read-only root filesystem, a test sandbox without
// a home directory, or any process whose home is not writable could not
// start a cluster node at all.
//
// This sample points HOME at a read-only directory, as such a pod sees it,
// and starts a one-node cluster twice: once as before, and once with the
// store directory set to a writable place.
//
// With the fix, ClusterConfig.WithStoreDir puts the store where the caller
// says, and the node starts.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery/nats"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
)

type echoGrain struct{}

func (g *echoGrain) OnActivate(context.Context, *actor.GrainProps) error   { return nil }
func (g *echoGrain) OnReceive(ctx *actor.GrainContext)                     { ctx.NoErr() }
func (g *echoGrain) OnDeactivate(context.Context, *actor.GrainProps) error { return nil }

func startNode(ctx context.Context, natsAddress string, storeDir string) error {
	discovery := nats.NewDiscovery(&nats.Config{
		NatsServer:    "nats://" + natsAddress,
		NatsSubject:   "issue-1403",
		Host:          "localhost",
		DiscoveryPort: 9431,
	})

	clusterConfig := actor.
		NewClusterConfig().
		WithDiscovery(discovery).
		WithDiscoveryPort(9431).
		WithPeersPort(9432).
		WithMinimumPeersQuorum(1).
		WithGrains(new(echoGrain))
	if storeDir != "" {
		clusterConfig = clusterConfig.WithStoreDir(storeDir)
	}

	actorSystem, err := actor.NewActorSystem(
		"issue1403",
		actor.WithRemote(remote.NewConfig("localhost", 9433)),
		actor.WithLogger(log.DiscardLogger),
		actor.WithCluster(clusterConfig),
	)
	if err != nil {
		return err
	}
	if err := actorSystem.Start(ctx); err != nil {
		return err
	}
	return actorSystem.Stop(ctx)
}

func newNatsServer() *natsserver.Server {
	serv, err := natsserver.NewServer(&natsserver.Options{Host: "localhost", Port: -1})
	if err != nil {
		fmt.Printf("FAIL: creating NATS server: %v\n", err)
		os.Exit(1)
	}
	go serv.Start()
	if !serv.ReadyForConnections(2 * time.Second) {
		fmt.Println("FAIL: NATS server is not ready for connections")
		os.Exit(1)
	}
	return serv
}

func main() {
	ctx := context.Background()
	srv := newNatsServer()
	defer srv.Shutdown()

	// The home directory of a pod with a read-only root filesystem: it
	// exists, but nothing can be created in it. /tmp stays writable, as an
	// emptyDir volume would.
	root, err := os.MkdirTemp("", "issue-1403-*")
	if err != nil {
		fmt.Printf("FAIL: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(root)
	home := filepath.Join(root, "home")
	writable := filepath.Join(root, "tmp")
	for _, dir := range []string{home, writable} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			fmt.Printf("FAIL: %v\n", err)
			os.Exit(1)
		}
	}
	if err := os.Chmod(home, 0o555); err != nil {
		fmt.Printf("FAIL: %v\n", err)
		os.Exit(1)
	}
	defer os.Chmod(home, 0o755) //nolint:errcheck
	os.Setenv("HOME", home)

	if err := startNode(ctx, srv.Addr().String(), ""); err == nil {
		fmt.Println("the read-only home directory is writable on this machine (root?), the sample cannot show the failure here")
	} else {
		fmt.Printf("as expected, the node cannot start with its store under a read-only home: %v\n", err)
	}

	if err := startNode(ctx, srv.Addr().String(), writable); err != nil {
		fmt.Printf("FAIL: the node cannot start with the store directory set to a writable place: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("PASS: the node started with its cluster store in the directory the caller chose")
}
