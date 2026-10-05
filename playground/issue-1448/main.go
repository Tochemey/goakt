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

// Reproduction for https://github.com/Tochemey/goakt/issues/1448
//
// A name lookup finds the actor's node in the actors tree under the tree's
// lock, then reads the node's PID after releasing it. When the actor stops,
// the tree clears that PID slot. A lookup that found the node just before the
// actor left reads a nil PID and dereferences it. The panic is on the caller's
// goroutine, so it ends the process.
//
// This sample keeps stopping and spawning eight actors again from two
// goroutines while eight goroutines call one lookup API on the same names. The
// API is the first argument: ActorOf (the default), ActorExists, Kill or Child.
package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
)

const (
	// names is the number of actor names the sample stops and spawns again.
	names = 8
	// churners is the number of goroutines that stop and spawn the actors.
	churners = 2
	// readers is the number of goroutines that look the actors up.
	readers = 8
	// duration is how long the readers look the actors up. Before the fix the
	// first panic comes within milliseconds.
	duration = 3 * time.Second
)

// idleActor is the actor the sample spawns and stops. It handles no messages.
type idleActor struct{}

// scenario is the churn and the lookup the sample runs for one API.
type scenario struct {
	// churn spawns the actor called name and stops it again.
	churn func(name string)
	// lookup calls the API under test for the actor called name.
	lookup func(name string)
}

func main() {
	api := "ActorOf"
	if len(os.Args) > 1 {
		api = os.Args[1]
	}

	ctx := context.Background()

	system, err := actor.NewActorSystem("issue1448", actor.WithLogger(log.DiscardLogger))
	if err != nil {
		fail("%v", err)
	}

	if err := system.Start(ctx); err != nil {
		fail("%v", err)
	}

	run, ok := newScenario(ctx, system, api)
	if !ok {
		fail("unknown API %q: use ActorOf, ActorExists, Kill or Child", api)
	}

	fmt.Printf("calling %s on %d actors while they stop and start again, for %s\n", api, names, duration)

	var (
		stop atomic.Bool
		wg   sync.WaitGroup
	)

	for range churners {
		wg.Go(func() {
			for n := 0; !stop.Load(); n = (n + 1) % names {
				run.churn(actorName(n))
			}
		})
	}

	// before the fix one of these lookups panics and the process exits with status 2
	for range readers {
		wg.Go(func() {
			for n := 0; !stop.Load(); n = (n + 1) % names {
				run.lookup(actorName(n))
			}
		})
	}

	pause.For(duration)
	stop.Store(true)
	wg.Wait()

	if err := system.Stop(ctx); err != nil {
		fail("stopping the actor system: %v", err)
	}

	fmt.Printf("PASS: %s answered for actors leaving the system without crashing\n", api)
}

// PreStart does nothing.
func (x *idleActor) PreStart(*actor.Context) error { return nil }

// Receive does nothing.
func (x *idleActor) Receive(*actor.ReceiveContext) {}

// PostStop does nothing.
func (x *idleActor) PostStop(*actor.Context) error { return nil }

// newScenario returns the churn and the lookup for api. Child looks up the
// children of one parent actor; the other APIs look up top-level actors. It
// reports false for an API the sample does not know.
func newScenario(ctx context.Context, system actor.ActorSystem, api string) (scenario, bool) {
	topLevel := func(name string) {
		if pid, err := system.Spawn(ctx, name, new(idleActor)); err == nil {
			_ = pid.Shutdown(ctx)
		}
	}

	switch api {
	case "ActorOf":
		return scenario{churn: topLevel, lookup: func(name string) { _, _ = system.ActorOf(ctx, name) }}, true
	case "ActorExists":
		return scenario{churn: topLevel, lookup: func(name string) { _, _ = system.ActorExists(ctx, name) }}, true
	case "Kill":
		return scenario{churn: topLevel, lookup: func(name string) { _ = system.Kill(ctx, name) }}, true
	case "Child":
		parent, err := system.Spawn(ctx, "parent", new(idleActor))
		if err != nil {
			fail("spawning the parent: %v", err)
		}

		churn := func(name string) {
			if child, err := parent.SpawnChild(ctx, name, new(idleActor)); err == nil {
				_ = child.Shutdown(ctx)
			}
		}

		return scenario{churn: churn, lookup: func(name string) { _, _ = parent.Child(name) }}, true
	default:
		return scenario{}, false
	}
}

// actorName returns the name of the n-th actor the sample stops and spawns.
func actorName(n int) string {
	return fmt.Sprintf("actor-%d", n)
}

// fail prints a setup error and exits with status 3, apart from the status 2
// the Go runtime exits with on the panic this sample reproduces.
func fail(format string, args ...any) {
	fmt.Printf("setup failed: "+format+"\n", args...)
	os.Exit(3)
}
