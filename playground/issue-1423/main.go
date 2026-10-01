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
	"os"
	"runtime"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
)

// actorName is the name every round kills and spawns again.
const actorName = "worker"

// rounds is how many times the sample kills the actor and spawns it again. A
// single processor makes the race land on every round; a few rounds keep the
// sample robust.
const rounds = 20

// settle is how long the sample lets the death watch release a stopped actor's name.
const settle = 50 * time.Millisecond

func main() {
	// One processor keeps the death watch from handling the killed actor's
	// Terminated before the sample spawns the name again.
	runtime.GOMAXPROCS(1)
	ctx := context.Background()

	system, err := actor.NewActorSystem("issue1423", actor.WithLogger(log.DiscardLogger))
	if err != nil {
		fail("create actor system: %v", err)
	}

	if err := system.Start(ctx); err != nil {
		fail("start actor system: %v", err)
	}

	current := spawn(ctx, system)
	stale := 0
	orphaned := 0

	for range rounds {
		if err := system.Kill(ctx, actorName); err != nil {
			fail("kill %s: %v", actorName, err)
		}

		// Kill has returned, so the old incarnation is stopped. A successful
		// Spawn must now hand back a new, running incarnation.
		killed := current
		current = spawn(ctx, system)
		if current != killed && current.IsRunning() {
			continue
		}

		stale++

		// Once the death watch has released the name, nothing answers to it:
		// the caller holds a dead PID and the name resolves to no actor.
		pause.For(settle)

		exists, err := system.ActorExists(ctx, actorName)
		if err != nil {
			fail("lookup %s: %v", actorName, err)
		}

		if !exists {
			orphaned++
		}

		// put a running actor back under the name for the next round
		current = spawn(ctx, system)
	}

	_ = system.Stop(ctx)

	if stale > 0 {
		fmt.Printf("BUG: Spawn after Kill returned the killed, stopped PID with a nil error in %d of %d rounds.\n", stale, rounds)
		fmt.Printf("BUG: the name resolved to no actor afterwards in %d of those %d rounds.\n", orphaned, stale)
		os.Exit(1)
	}

	fmt.Printf("OK: Spawn after Kill returned a new running actor in all %d rounds.\n", rounds)
}

// spawn spawns the worker under actorName and returns the PID that Spawn hands back.
func spawn(ctx context.Context, system actor.ActorSystem) *actor.PID {
	pid, err := system.Spawn(ctx, actorName, new(worker))
	if err != nil {
		fail("spawn %s: %v", actorName, err)
	}

	return pid
}

// worker is an actor that does nothing. It exists only to be killed and spawned again.
type worker struct{}

// PreStart does nothing.
func (x *worker) PreStart(*actor.Context) error { return nil }

// Receive ignores every message.
func (x *worker) Receive(*actor.ReceiveContext) {}

// PostStop does nothing.
func (x *worker) PostStop(*actor.Context) error { return nil }

// fail prints a setup error and exits with status 2.
func fail(format string, args ...any) {
	fmt.Printf("setup failed: "+format+"\n", args...)
	os.Exit(2)
}
