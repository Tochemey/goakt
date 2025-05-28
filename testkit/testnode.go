/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/atomic"

	goakt "github.com/tochemey/goakt/v3/actor"
	"github.com/tochemey/goakt/v3/discovery"
	"github.com/tochemey/goakt/v3/internal/eventstream"
)

// TestNode represents a test instance of an actor system cluster node.
// It is used to facilitate unit and integration testing of actor-based systems
// by providing convenient access to the ActorSystem, service discovery, and
// utilities like spawning actors and creating test probes.
type TestNode struct {
	actorSystem goakt.ActorSystem  // The actor system instance used by the node.
	discovery   discovery.Provider // The discovery provider for simulating cluster membership.
	nodeName    string             // The unique name of the test node.
	testingT    *testing.T         // The testing context for reporting errors and assertions.
	created     *atomic.Bool
	testCtx     context.Context
}

// NodeName returns the name of the test node, which can be used for identification
// during multi-node test scenarios.
func (x TestNode) NodeName() string {
	return x.nodeName
}

// Spawn creates and registers a new actor with the provided name in the test node's actor system.
// If the actor fails to spawn, the test will fail immediately.
//
// Parameters:
//   - ctx: context for managing cancellation and deadlines.
//   - name: the name to assign to the actor.
//   - actor: the actor instance to spawn.
//   - opts: optional configuration parameters for spawning the actor, such as mailbox, etc.
func (x TestNode) Spawn(ctx context.Context, name string, actor goakt.Actor, opts ...goakt.SpawnOption) {
	require.True(x.testingT, x.created.Load(), "cannot spawn actor before the test node is created")
	pid, err := x.actorSystem.Spawn(ctx, name, actor, opts...)
	require.NoError(x.testingT, err)
	require.NotNil(x.testingT, pid)
	require.False(x.testingT, pid.Equals(goakt.NoSender))
}

// SpawnSingleton creates a singleton actor in the system.
//
// A singleton actor is instantiated when cluster mode is enabled.
// A singleton actor like any other actor is created only once within the system and in the cluster.
// A singleton actor is created with the default supervisor strategy and directive.
// A singleton actor once created lives throughout the lifetime of the given actor system.
// One cannot create a child actor for a singleton actor.
//
// The cluster singleton is automatically started on the oldest node in the cluster.
// When the oldest node leaves the cluster unexpectedly, the singleton is restarted on the new oldest node.
// This is useful for managing shared resources or coordinating tasks that should be handled by a single actor.
func (x TestNode) SpawnSingleton(ctx context.Context, name string, actor goakt.Actor) {
	require.True(x.testingT, x.created.Load(), "cannot spawn singleton actor before the test node is created")
	err := x.actorSystem.SpawnSingleton(ctx, name, actor)
	require.NoError(x.testingT, err)
}

// SpawnProbe creates a new test probe actor, which can be used to observe and assert
// messages sent by other actors during test execution.
//
// Parameters:
//   - ctx: context for managing cancellation and deadlines.
//
// Returns:
//   - A Probe instance that can be used to send, receive, and assert messages.
func (x TestNode) SpawnProbe(ctx context.Context) Probe {
	require.True(x.testingT, x.created.Load(), "cannot create a test probe before the test node is created")
	testProbe, err := newProbe(ctx, x.actorSystem, x.testingT)
	require.NoError(x.testingT, err)
	return testProbe
}

// Subscribe creates an event subscriber to consume events from the actor system.
// This is useful for testing event-driven behavior in actors, allowing you to
// listen for events such as actor creation, termination, cluster events, or deadletters.
// It unsubscribes automatically when the test completes to prevent memory leaks.
func (x TestNode) Subscribe() eventstream.Subscriber {
	require.True(x.testingT, x.created.Load(), "cannot subscribe to events before the test node is created")
	subscriber, err := x.actorSystem.Subscribe()
	require.NoError(x.testingT, err)
	x.testingT.Cleanup(func() {
		require.NoError(x.testingT, x.actorSystem.Unsubscribe(subscriber))
	})
	return subscriber
}

// Kill stops a given actor in the local actor system.
func (x TestNode) Kill(ctx context.Context, name string) {
	require.True(x.testingT, x.created.Load(), "cannot kill actor before the test node is created")
	pid, err := x.actorSystem.LocalActor(name)
	require.NoError(x.testingT, err)
	require.NotNil(x.testingT, pid)
	require.False(x.testingT, pid.Equals(goakt.NoSender), "cannot kill actor with no address")
	require.NoError(x.testingT, pid.Shutdown(ctx))
}
