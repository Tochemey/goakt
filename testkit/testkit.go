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

package testkit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/atomic"

	goakt "github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/eventstream"
	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/log"
)

// TestKit defines actor test kit
type TestKit struct {
	actorSystem goakt.ActorSystem
	extensions  []extension.Extension
	testingT    *testing.T
	logger      log.Logger
	started     *atomic.Bool
}

// New creates an instance of TestKit
func New(ctx context.Context, t *testing.T, opts ...Option) *TestKit {
	// create the testkit instance
	testkit := &TestKit{
		testingT: t,
		logger:   log.DiscardLogger,
	}
	// apply the various options
	for _, opt := range opts {
		opt.Apply(testkit)
	}
	// create an actor system
	system, err := goakt.NewActorSystem(
		"testkit",
		goakt.WithExtensions(testkit.extensions...),
		goakt.WithLogger(testkit.logger),
		goakt.WithActorInitTimeout(time.Second),
		goakt.WithActorInitMaxRetries(5))
	if err != nil {
		t.Fatal(err.Error())
	}

	// start the actor system
	if err := system.Start(ctx); err != nil {
		t.Fatal(err.Error())
	}

	testkit.actorSystem = system
	testkit.started = atomic.NewBool(true)
	return testkit
}

// ActorSystem returns the testkit actor system
func (x *TestKit) ActorSystem() goakt.ActorSystem {
	return x.actorSystem
}

// Spawn creates an actor
func (x *TestKit) Spawn(ctx context.Context, name string, actor goakt.Actor, opts ...goakt.SpawnOption) {
	require.True(x.testingT, x.started.Load(), "require testkit to be started")
	// create and instance of an actor
	pid, err := x.actorSystem.Spawn(ctx, name, actor, opts...)
	require.NoError(x.testingT, err)
	require.NotNil(x.testingT, pid)
	require.False(x.testingT, pid.Equals(x.actorSystem.NoSender()))
}

// SpawnChild creates a child actor for an existing actor that is the parent
func (x *TestKit) SpawnChild(ctx context.Context, childName, parentName string, actor goakt.Actor, opts ...goakt.SpawnOption) {
	require.True(x.testingT, x.started.Load(), "require testkit to be started")
	parent, err := x.actorSystem.ActorOf(ctx, parentName)
	require.NoError(x.testingT, err)
	_, err = parent.SpawnChild(ctx, childName, actor, opts...)
	require.NoError(x.testingT, err)
}

// Subscribe creates an event subscriber to consume events from the actor system.
// This is useful for testing event-driven behavior in actors, allowing you to
// listen for events such as actor creation, termination, cluster events, or deadletters.
// It unsubscribes automatically when the test completes to prevent memory leaks.
func (x *TestKit) Subscribe() eventstream.Subscriber {
	require.True(x.testingT, x.started.Load(), "require testkit to be started")
	subscriber, err := x.actorSystem.Subscribe()
	require.NoError(x.testingT, err)
	x.testingT.Cleanup(func() {
		require.NoError(x.testingT, x.actorSystem.Unsubscribe(subscriber))
	})
	return subscriber
}

// Kill stops a given actor in the local actor system.
func (x *TestKit) Kill(ctx context.Context, name string) {
	require.True(x.testingT, x.started.Load(), "require testkit to be started")
	pid, err := x.actorSystem.ActorOf(ctx, name)
	require.NoError(x.testingT, err)
	require.NotNil(x.testingT, pid)
	require.False(x.testingT, pid.Equals(x.actorSystem.NoSender()), "cannot kill actor with no address")
	require.NoError(x.testingT, pid.Shutdown(ctx))
}

// NewProbe create a test probe
func (x *TestKit) NewProbe(ctx context.Context) Probe {
	require.True(x.testingT, x.started.Load(), "require testkit to be started")
	testProbe, err := newProbe(ctx, x.actorSystem, x.testingT)
	require.NoError(x.testingT, err)
	return testProbe
}

// NewGrainProbe creates a grain test probe
func (x *TestKit) NewGrainProbe(ctx context.Context) GrainProbe {
	require.True(x.testingT, x.started.Load(), "require testkit to be started")
	testProbe, err := newGrainProbe(ctx, x.testingT, x.actorSystem)
	require.NoError(x.testingT, err)
	return testProbe
}

// GrainIdentity creates a grain identity
func (x *TestKit) GrainIdentity(ctx context.Context, name string, factory goakt.GrainFactory, opts ...goakt.GrainOption) *goakt.GrainIdentity {
	require.True(x.testingT, x.started.Load(), "require testkit to be started")
	require.NotNil(x.testingT, factory)
	identity, err := x.actorSystem.GrainIdentity(ctx, name, factory, opts...)
	require.NoError(x.testingT, err)
	require.NotNil(x.testingT, identity)
	return identity
}

// Shutdown stops the test kit
func (x *TestKit) Shutdown(ctx context.Context) {
	require.NoError(x.testingT, x.actorSystem.Stop(ctx))
	x.started.Store(false)
}
