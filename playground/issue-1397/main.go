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

// Package main reproduces github.com/Tochemey/goakt/issues/1397: grain
// passivation can run OnDeactivate while the grain is still handling a
// message, and a second activation of the same grain can start while the
// first is still running.
//
// A grain is written against three promises: one activation per identity,
// one message at a time, and OnDeactivate last. The sample checks all three.
//
// Steps:
//
//  1. Start one node. The bug is local to a grain, so no cluster is needed.
//  2. Activate a grain that passivates after idleTimeout.
//  3. Send it a slow message that holds OnReceive open past idleTimeout.
//  4. While the slow message is still running, send two more messages.
//  5. Let the slow message finish, and report what the grain saw.
//
// Run it with: go run ./playground/issue-1397
//
// Exit status 1 means the bug is still there, 0 means every promise held,
// and 2 means the setup failed.
package main

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// idleTimeout is how long the grain may stay idle before it passivates.
const idleTimeout = 300 * time.Millisecond

var (
	// slowMessageStarted is closed when the grain starts handling the slow message.
	slowMessageStarted = make(chan struct{})

	// releaseSlowMessage lets the slow message finish.
	releaseSlowMessage = make(chan struct{})

	// messagesInProgress counts messages inside OnReceive across every instance
	// of the grain. A grain handles one message at a time, so more than one
	// means two instances were running at once.
	messagesInProgress atomic.Int32

	// maxMessagesInProgress is the highest value messagesInProgress reached.
	maxMessagesInProgress atomic.Int32

	// deactivatedDuringReceive is set when OnDeactivate ran while OnReceive was running.
	deactivatedDuringReceive atomic.Bool

	// receivedAfterDeactivate is set when an instance handled a message after its OnDeactivate.
	receivedAfterDeactivate atomic.Bool
)

func main() {
	ctx := context.Background()

	// Step 1: start one node.
	system, err := actor.NewActorSystem("issue1397", actor.WithLogger(log.DiscardLogger))
	if err != nil {
		fail("create actor system: %v", err)
	}

	if err := system.Start(ctx); err != nil {
		fail("start actor system: %v", err)
	}

	defer func() { _ = system.Stop(ctx) }()

	// Step 2: activate a grain that passivates after idleTimeout.
	identity, err := actor.GrainOf[*slowGrain](ctx, system, "slow-grain", actor.WithGrainDeactivateAfter(idleTimeout))
	if err != nil {
		fail("activate grain: %v", err)
	}

	fmt.Printf("step 2: grain activated, it passivates after %s idle\n", idleTimeout)

	// Step 3: send the slow message and wait until OnReceive holds it.
	slowResult := make(chan error, 1)
	go func() {
		_, err := system.AskGrain(ctx, identity, new(testpb.TestWait), 10*time.Second)
		slowResult <- err
	}()
	<-slowMessageStarted

	pause.For(3 * idleTimeout)

	fmt.Printf("step 3: the slow message has been running for %s, longer than the idle timeout\n", 3*idleTimeout)

	// Step 4: send two more messages while the slow one is still running.
	fastResults := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := system.AskGrain(ctx, identity, new(testpb.TestReply), 5*time.Second)
			fastResults <- err
		}()
	}

	pause.For(idleTimeout)

	fmt.Println("step 4: sent two more messages while the slow one is still running")

	// Step 5: let the slow message finish and collect every answer.
	close(releaseSlowMessage)
	if err := <-slowResult; err != nil {
		fail("slow message: %v", err)
	}

	for i := 1; i <= 2; i++ {
		if err := <-fastResults; err != nil {
			fail("message %d: %v", i, err)
		}

		fmt.Printf("step 5: message %d was answered\n", i)
	}

	bugs := 0
	if deactivatedDuringReceive.Load() {
		fmt.Println("\nBUG: OnDeactivate ran while OnReceive was still handling the slow message.")
		bugs++
	}

	if maxMessagesInProgress.Load() > 1 {
		fmt.Printf("BUG: %d instances of the grain handled messages at the same time.\n", maxMessagesInProgress.Load())
		bugs++
	}

	if receivedAfterDeactivate.Load() {
		fmt.Println("BUG: a deactivated instance handled a message after OnDeactivate.")
		bugs++
	}

	if bugs > 0 {
		os.Exit(1)
	}

	fmt.Println("\nOK: one activation at a time, one message at a time, and OnDeactivate last.")
}

// slowGrain holds a TestWait message in OnReceive until releaseSlowMessage is
// closed, and answers every other message at once. It records any broken
// promise in the package-level flags.
type slowGrain struct {
	// deactivated is set when this instance's OnDeactivate runs.
	deactivated atomic.Bool
}

var _ actor.Grain = (*slowGrain)(nil)

// OnActivate does nothing.
func (g *slowGrain) OnActivate(context.Context, *actor.GrainProps) error {
	return nil
}

// OnReceive handles the slow message and the fast ones.
func (g *slowGrain) OnReceive(ctx *actor.GrainContext) {
	recordMessageStart()
	defer messagesInProgress.Add(-1)

	if g.deactivated.Load() {
		receivedAfterDeactivate.Store(true)
	}

	if _, ok := ctx.Message().(*testpb.TestWait); ok {
		close(slowMessageStarted)
		<-releaseSlowMessage

		if g.deactivated.Load() {
			deactivatedDuringReceive.Store(true)
		}
	}

	ctx.Response(new(testpb.Reply))
}

// OnDeactivate marks this instance as deactivated.
func (g *slowGrain) OnDeactivate(context.Context, *actor.GrainProps) error {
	g.deactivated.Store(true)
	return nil
}

// recordMessageStart counts a message entering OnReceive and keeps the highest
// count seen.
func recordMessageStart() {
	inProgress := messagesInProgress.Add(1)
	for {
		highest := maxMessagesInProgress.Load()
		if inProgress <= highest || maxMessagesInProgress.CompareAndSwap(highest, inProgress) {
			return
		}
	}
}

// fail prints a setup error and exits with status 2.
func fail(format string, args ...any) {
	fmt.Printf("setup failed: "+format+"\n", args...)
	os.Exit(2)
}
