/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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

	"github.com/tochemey/goakt/v2/actors"
	"github.com/tochemey/goakt/v2/log"
)

// TestKit defines actor test kit
type TestKit struct {
	actorSystem actors.ActorSystem
	kt          *testing.T
	logger      log.Logger
}

// New creates an instance of TestKit
func New(ctx context.Context, t *testing.T, opts ...Option) *TestKit {
	// create the testkit instance
	testkit := &TestKit{
		kt:     t,
		logger: log.DiscardLogger,
	}
	// apply the various options
	for _, opt := range opts {
		opt.Apply(testkit)
	}
	// create an actor system
	system, err := actors.NewActorSystem(
		"testkit",
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

// Spawn create an actor
func (k *TestKit) Spawn(ctx context.Context, name string, actor actors.Actor) {
	// create and instance of actor
	_, err := k.actorSystem.Spawn(ctx, name, actor)
	// handle the error
	if err != nil {
		k.kt.Fatal(err.Error())
	}
}

// NewProbe create a test probe
func (k *TestKit) NewProbe(ctx context.Context) Probe {
	// create an instance of TestProbe
	testProbe, err := newProbe(ctx, k.actorSystem, k.kt)
	// handle the error
	if err != nil {
		k.kt.Fatal(err.Error())
	}

	// return the created test probe
	return testProbe
}

// Shutdown stops the test kit
func (k *TestKit) Shutdown(ctx context.Context) {
	if err := k.actorSystem.Stop(ctx); err != nil {
		k.kt.Fatal(err.Error())
	}
}
