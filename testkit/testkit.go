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
	"time"

	"github.com/stretchr/testify/require"

	actors "github.com/tochemey/goakt/v3/actor"
	"github.com/tochemey/goakt/v3/extension"
	"github.com/tochemey/goakt/v3/internal/eventstream"
	"github.com/tochemey/goakt/v3/log"
)

// TestKit defines actor test kit
type TestKit struct {
	actorSystem actors.ActorSystem
	extensions  []extension.Extension
	gt          *testing.T
	logger      log.Logger
}

// New creates an instance of TestKit
func New(ctx context.Context, t *testing.T, opts ...Option) *TestKit {
	// create the testkit instance
	testkit := &TestKit{
		gt:     t,
		logger: log.DiscardLogger,
	}
	// apply the various options
	for _, opt := range opts {
		opt.Apply(testkit)
	}
	// create an actor system
	system, err := actors.NewActorSystem(
		"testkit",
		actors.WithExtensions(testkit.extensions...),
		actors.WithPassivationDisabled(),
		actors.WithLogger(testkit.logger),
		actors.WithActorInitTimeout(time.Second),
		actors.WithActorInitMaxRetries(5))
	if err != nil {
		t.Fatal(err.Error())
	}

	// start the actor system
	if err := system.Start(ctx); err != nil {
		t.Fatal(err.Error())
	}

	testkit.actorSystem = system
	return testkit
}

// ActorSystem returns the testkit actor system
func (x *TestKit) ActorSystem() actors.ActorSystem {
	return x.actorSystem
}

// Spawn creates an actor
func (x *TestKit) Spawn(ctx context.Context, name string, actor actors.Actor) {
	// create and instance of an actor
	_, err := x.actorSystem.Spawn(ctx, name, actor)
	require.NoError(x.gt, err)
}

// SpawnChild creates a child actor for an existing actor that is the parent
func (x *TestKit) SpawnChild(ctx context.Context, childName, parentName string, actor actors.Actor) {
	parent, err := x.actorSystem.LocalActor(parentName)
	require.NoError(x.gt, err)
	_, err = parent.SpawnChild(ctx, childName, actor)
	require.NoError(x.gt, err)
}

// Subscribe creates an event subscriber to consume events from the actor system.
// This is useful for testing event-driven behavior in actors, allowing you to
// listen for events such as actor creation, termination, cluster events, or deadletters.
// It unsubscribes automatically when the test completes to prevent memory leaks.
func (x *TestKit) Subscribe() eventstream.Subscriber {
	subscriber, err := x.actorSystem.Subscribe()
	require.NoError(x.gt, err)
	x.gt.Cleanup(func() {
		require.NoError(x.gt, x.actorSystem.Unsubscribe(subscriber))
	})
	return subscriber
}

// NewProbe create a test probe
func (x *TestKit) NewProbe(ctx context.Context) Probe {
	testProbe, err := newProbe(ctx, x.actorSystem, x.gt)
	require.NoError(x.gt, err)
	return testProbe
}

// Shutdown stops the test kit
func (x *TestKit) Shutdown(ctx context.Context) {
	if err := x.actorSystem.Stop(ctx); err != nil {
		x.gt.Fatal(err.Error())
	}
}
