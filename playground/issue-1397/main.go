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
	"sync/atomic"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// idleTimeout is how long the grain may stay idle before it passivates.
const idleTimeout = 300 * time.Millisecond

var (
	// gateIdentity is the grain the slow message waits on. It stands in for a
	// database: it answers only when the sample tells it to.
	gateIdentity *actor.GrainIdentity

	// gateReplies receives the deferred reply of every request the gate gets.
	// Receiving from it tells the sample that the slow message is in progress.
	gateReplies = make(chan *actor.GrainReply, 1)

	// messagesInProgress counts messages the grain is handling, from the start
	// of OnReceive to the reply, across every instance of the grain. A grain
	// handles one message at a time, so more than one means two instances
	// were running at once.
	messagesInProgress atomic.Int32

	// maxMessagesInProgress is the highest value messagesInProgress reached.
	maxMessagesInProgress atomic.Int32

	// deactivatedDuringSlowMessage is set when OnDeactivate ran while the slow message was still in progress.
	deactivatedDuringSlowMessage atomic.Bool

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

	// Step 2: activate the gate and a grain that passivates after idleTimeout.
	// The grain uses StashNonReentrant reentrancy so that it takes no other
	// message while it waits on the gate.
	gateIdentity, err = actor.GrainOf[*gateGrain](ctx, system, "gate")
	if err != nil {
		fail("activate gate: %v", err)
	}

	identity, err := actor.GrainOf[*slowGrain](ctx, system, "slow-grain",
		actor.WithGrainDeactivateAfter(idleTimeout),
		actor.WithGrainReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.StashNonReentrant))),
	)
	if err != nil {
		fail("activate grain: %v", err)
	}

	fmt.Printf("step 2: grain activated, it passivates after %s idle\n", idleTimeout)

	// Step 3: send the slow message and wait until the grain is waiting on the gate.
	slowResult := make(chan error, 1)
	go func() {
		_, err := system.AskGrain(ctx, identity, new(testpb.TestWait), 10*time.Second)
		slowResult <- err
	}()
	gateReply := <-gateReplies

	pause.For(3 * idleTimeout)

	fmt.Printf("step 3: the slow message has been in progress for %s, longer than the idle timeout\n", 3*idleTimeout)

	// Step 4: send two more messages while the slow one is still in progress.
	fastResults := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := system.AskGrain(ctx, identity, new(testpb.TestReply), 5*time.Second)
			fastResults <- err
		}()
	}

	pause.For(idleTimeout)

	fmt.Println("step 4: sent two more messages while the slow one is still in progress")

	// Step 5: let the gate answer, so the slow message finishes, and collect every answer.
	gateReply.Response(new(testpb.Reply))
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
	if deactivatedDuringSlowMessage.Load() {
		fmt.Println("\nBUG: OnDeactivate ran while the slow message was still in progress.")
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

// slowGrain waits on the gate for a TestWait message, and answers every other
// message at once. OnReceive never blocks: the slow message is a request to
// the gate, and the reply to the caller is completed when the gate answers.
// It records any broken promise in the package-level flags.
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

	if g.deactivated.Load() {
		receivedAfterDeactivate.Store(true)
	}

	if _, ok := ctx.Message().(*testpb.TestWait); ok {
		// The slow message waits on the gate the way a handler waits on a
		// database: the request goes out, OnReceive returns, and the caller is
		// answered from the continuation once the gate has replied. Meanwhile
		// the grain takes no other message.
		reply := ctx.DeferResponse()
		ctx.RequestGrain(gateIdentity, new(testpb.TestWait)).Then(func(_ any, err error) {
			if g.deactivated.Load() {
				deactivatedDuringSlowMessage.Store(true)
			}

			messagesInProgress.Add(-1)
			if err != nil {
				reply.Err(err)
				return
			}

			reply.Response(new(testpb.Reply))
		})
		return
	}

	messagesInProgress.Add(-1)
	ctx.Response(new(testpb.Reply))
}

// OnDeactivate marks this instance as deactivated.
func (g *slowGrain) OnDeactivate(context.Context, *actor.GrainProps) error {
	g.deactivated.Store(true)
	return nil
}

// gateGrain stands in for a database. It takes ownership of the reply to every
// request it gets and hands it to the sample, which answers when it chooses.
// Its OnReceive returns at once.
type gateGrain struct{}

var _ actor.Grain = (*gateGrain)(nil)

// OnActivate does nothing.
func (g *gateGrain) OnActivate(context.Context, *actor.GrainProps) error {
	return nil
}

// OnReceive hands the reply to the sample.
func (g *gateGrain) OnReceive(ctx *actor.GrainContext) {
	gateReplies <- ctx.DeferResponse()
}

// OnDeactivate does nothing.
func (g *gateGrain) OnDeactivate(context.Context, *actor.GrainProps) error {
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
