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

package testkit

import (
	"context"
	"fmt"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery/nats"
	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/internal/collection"
	"github.com/tochemey/goakt/v4/internal/errorschain"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
)

type MultiNodes struct {
	extensions []extension.Extension
	gt         *testing.T
	logger     log.Logger
	nodes      *collection.Map[string, *TestNode]
	kinds      []actor.Actor
	started    *atomic.Bool
	server     *natsserver.Server
	host       string
}

// NewMultiNodes creates a new instance of MultiNodes for testing purposes.
// It initializes the multi-node test environment with the provided testing context,
// logger, actor kinds, and extensions. This setup is essential for running
// multi-node tests in a controlled environment, allowing for the simulation of
// distributed actor systems.
func NewMultiNodes(t *testing.T, logger log.Logger, kinds []actor.Actor, extensions []extension.Extension) *MultiNodes {
	instance := &MultiNodes{
		gt:         t,
		logger:     logger,
		nodes:      collection.NewMap[string, *TestNode](),
		kinds:      kinds,
		extensions: extensions,
		started:    atomic.NewBool(false),
	}

	return instance
}

// Start initializes and launches the multi-node test environment.
//
// This method sets up all foundational infrastructure required for running distributed actor tests,
// including starting an embedded NATS server for transport and coordination,
// initializing the discovery provider, and preparing internal state for managing cluster nodes.
//
// It must be called before spawning any test nodes using StartNode.
//
// Behavior:
//   - Fails the test immediately if the NATS server or discovery provider fails to start.
//   - Ensures that subsequent calls to StartNode will result in nodes joining the same cluster.
//
// Example:
//
//	multiNodes := NewMultiNodes(t)
//	multiNodes.Start()
//
//	node1 := multiNodes.StartNode(ctx, "node-1")
//	node2 := multiNodes.StartNode(ctx, "node-2")
//
//	// Nodes can now spawn actors and communicate
//
// Notes:
//   - This method is intended for use in integration or cluster-level testing only.
//   - It must be paired with a proper shutdown (e.g., via t.Cleanup or a Stop method) to release resources.
func (m *MultiNodes) Start() {
	// no-op when already started
	if m.started.Load() {
		return
	}

	host := "127.0.0.1"
	serv, err := natsserver.NewServer(&natsserver.Options{
		Host:  host,
		Port:  -1,
		NoLog: true,
		Trace: false,
		Debug: false,
	})

	require.NoError(m.gt, err)
	ready := make(chan bool)
	go func() {
		ready <- true
		serv.Start()
	}()
	<-ready

	if !serv.ReadyForConnections(2 * time.Second) {
		m.gt.Fatalf("nats-io server failed to start")
	}

	m.server = serv
	m.host = host
	m.started.Store(true)
}

// Stop gracefully shuts down the entire multi-node test environment.
//
// This method ensures that:
//   - All actor systems across started nodes are properly stopped.
//   - Discovery providers are cleanly closed.
//   - The embedded NATS server (if started via Start) is shut down.
//   - Any background goroutines or resources associated with the environment are released.
//
// It is typically called at the end of a test to ensure a clean and deterministic shutdown
// of all nodes and underlying infrastructure.
//
// Behavior:
//   - If any node fails to shut down gracefully, the method fails the test using t.Fatal.
//   - This method is idempotent and safe to call once, typically via t.Cleanup.
//
// Example:
//
//	t.Cleanup(func() {
//	    multiNodes.Stop()
//	})
//
// Notes:
//   - This method is intended for test environments only and should not be used in production code.
//   - Make sure to call Stop after Start to avoid resource leaks or port conflicts between tests.
func (m *MultiNodes) Stop() {
	// no-op when already stopped
	if !m.started.Load() {
		return
	}

	ctx := context.Background()
	for _, node := range m.nodes.Values() {
		if err := errorschain.
			New(errorschain.ReturnFirst()).
			AddErrorFn(func() error { return node.actorSystem.Stop(ctx) }).
			AddErrorFn(func() error { return node.discovery.Close() }).
			Error(); err != nil {
			m.started.Store(false)
			m.gt.Fatalf("failed to stop actor system %s: %v", node.actorSystem.Name(), err)
		}
	}

	if m.server != nil {
		m.server.Shutdown()
	}

	m.started.Store(false)
	m.nodes.Reset()
}

// StartNode initializes and starts a new test node within the multi-node test environment.
// The newly started node will automatically join the existing actor cluster (if one exists),
// making it immediately available for actor spawning, message routing, and inter-node communication.
//
// This method is typically used in distributed test scenarios to simulate dynamic node joins,
// cluster growth, and multi-node coordination.
//
// Parameters:
//   - ctx: the context used to manage cancellation, deadlines, or timeouts during node startup.
//   - name: the unique name to assign to the node. This is used for identification within the test cluster.
//
// Returns:
//   - A pointer to the newly created TestNode, which provides access to the underlying ActorSystem,
//     discovery provider, and helper methods like Spawn and SpawnProbe.
//
// Behavior:
//   - If the node fails to start, the test will fail immediately using t.Fatal.
//   - The node will automatically bootstrap into the cluster and register itself with the discovery provider.
//
// Example:
//
//	node := multiNodes.StartNode(ctx, "node-1")
//	node.Spawn(ctx, "echo-actor", &EchoActor{})
//
// Notes:
//   - This method is intended for use in test environments only.
//   - For single-node testing, use TestKit directly without relying on MultiNodes.
func (m *MultiNodes) StartNode(ctx context.Context, name string) *TestNode {
	require.True(m.gt, m.started.Load(), "multi-nodes must be started before starting a node")

	ports := dynaport.Get(3)
	discoveryPort := ports[0]
	clusterPort := ports[1]
	remotingPort := ports[2]

	config := nats.Config{
		NatsServer:    fmt.Sprintf("nats://%s", m.server.Addr().String()),
		NatsSubject:   "testSubject",
		Host:          m.host,
		DiscoveryPort: discoveryPort,
	}

	provider := nats.NewDiscovery(&config, nats.WithLogger(m.logger))

	options := []actor.Option{
		actor.WithLogger(m.logger),
		actor.WithRemote(remote.NewConfig(m.host, remotingPort)),
		actor.WithExtensions(m.extensions...),
		actor.WithShutdownTimeout(3 * time.Minute),
		actor.WithCluster(
			actor.NewClusterConfig().
				WithKinds(m.kinds...).
				WithPartitionCount(7).
				WithReplicaCount(1).
				WithPeersPort(clusterPort).
				WithMinimumPeersQuorum(1).
				WithDiscoveryPort(discoveryPort).
				WithClusterStateSyncInterval(300 * time.Millisecond).
				WithPeersStateSyncInterval(500 * time.Millisecond).
				WithDiscovery(provider)),
	}

	actorSystem, err := actor.NewActorSystem("testSystem", options...)
	require.NotNil(m.gt, actorSystem)
	require.NoError(m.gt, err)

	require.NoError(m.gt, actorSystem.Start(ctx))
	pause.For(2 * time.Second)

	node := &TestNode{
		actorSystem: actorSystem,
		discovery:   provider,
		nodeName:    name,
		testingT:    m.gt,
		created:     atomic.NewBool(true),
		testCtx:     ctx,
	}

	m.nodes.Set(name, node)
	return node
}
