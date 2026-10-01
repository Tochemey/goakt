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

// attempts is how many times each scenario runs. A single processor makes the
// race land on almost every attempt; a few attempts keep the sample robust.
const attempts = 20

// settle is how long the sample lets the dispatcher drain after a stop.
const settle = 50 * time.Millisecond

func main() {
	// One processor keeps the dispatcher from running the guardian's or the
	// parent's first turn while the sample is still spawning and stopping.
	runtime.GOMAXPROCS(1)
	ctx := context.Background()

	outOfOrder := 0
	crashed := 0

	for range attempts {
		if terminatedBeforePostStart(ctx) {
			outOfOrder++
		}

		if userGuardianCrashed(ctx) {
			crashed++
		}
	}

	if outOfOrder > 0 {
		fmt.Printf("BUG: a parent handled Terminated before its own PostStart in %d of %d attempts.\n", outOfOrder, attempts)
	}

	if crashed > 0 {
		fmt.Printf("BUG: the user guardian panicked and took the actor system down in %d of %d attempts.\n", crashed, attempts)
	}

	if outOfOrder > 0 || crashed > 0 {
		os.Exit(1)
	}

	fmt.Println("OK: every actor handled PostStart before any other message and the actor system survived every stop.")
}

// terminatedBeforePostStart spawns a parent, spawns a child under it and stops
// the child at once. The parent watches its child, so it receives both its own
// PostStart and the child's Terminated. It reports whether the parent handled
// Terminated first.
func terminatedBeforePostStart(ctx context.Context) bool {
	system := startSystem(ctx)
	firstMessage := make(chan string, 1)

	parentPID, err := system.Spawn(ctx, "parent", &parent{firstMessage: firstMessage})
	if err != nil {
		fail("spawn parent: %v", err)
	}

	childPID, err := parentPID.SpawnChild(ctx, "child", new(idle))
	if err != nil {
		fail("spawn child: %v", err)
	}

	if err := childPID.Shutdown(ctx); err != nil {
		fail("stop child: %v", err)
	}

	pause.For(settle)
	first := <-firstMessage
	_ = system.Stop(ctx)
	return first == "Terminated"
}

// userGuardianCrashed spawns a top-level actor and kills it at once. The user
// guardian is the parent of every top-level actor, so it receives the actor's
// Terminated. It reports whether the actor system went down afterwards.
func userGuardianCrashed(ctx context.Context) bool {
	system := startSystem(ctx)

	if _, err := system.Spawn(ctx, "victim", new(idle)); err != nil {
		fail("spawn victim: %v", err)
	}

	if err := system.Kill(ctx, "victim"); err != nil {
		fail("kill victim: %v", err)
	}

	pause.For(settle)
	crashed := !system.Running()
	_ = system.Stop(ctx)
	return crashed
}

// startSystem creates and starts a quiet actor system.
func startSystem(ctx context.Context) actor.ActorSystem {
	system, err := actor.NewActorSystem("issue1422", actor.WithLogger(log.DiscardLogger))
	if err != nil {
		fail("create actor system: %v", err)
	}

	if err := system.Start(ctx); err != nil {
		fail("start actor system: %v", err)
	}

	return system
}

// parent reports the name of the first lifecycle message it handles.
type parent struct {
	// firstMessage receives the name of the first PostStart or Terminated the
	// parent handles. It is buffered with room for one, and the parent never
	// sends a second, so the send never blocks the actor's turn.
	firstMessage chan string

	// reported records that the first message was sent. Only the actor's own
	// turn touches it.
	reported bool
}

// PreStart does nothing.
func (x *parent) PreStart(*actor.Context) error { return nil }

// Receive reports the first of PostStart and Terminated.
func (x *parent) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *actor.PostStart:
		x.report("PostStart")
	case *actor.Terminated:
		x.report("Terminated")
	default:
		ctx.Unhandled()
	}
}

// PostStop does nothing.
func (x *parent) PostStop(*actor.Context) error { return nil }

// report sends name once, for the first lifecycle message only.
func (x *parent) report(name string) {
	if x.reported {
		return
	}

	x.reported = true
	x.firstMessage <- name
}

// idle is an actor that does nothing. It exists only to be stopped.
type idle struct{}

// PreStart does nothing.
func (x *idle) PreStart(*actor.Context) error { return nil }

// Receive ignores every message.
func (x *idle) Receive(*actor.ReceiveContext) {}

// PostStop does nothing.
func (x *idle) PostStop(*actor.Context) error { return nil }

// fail prints a setup error and exits with status 2.
func fail(format string, args ...any) {
	fmt.Printf("setup failed: "+format+"\n", args...)
	os.Exit(2)
}
