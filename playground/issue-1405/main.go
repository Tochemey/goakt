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

// Reproduction for https://github.com/Tochemey/goakt/issues/1405
//
// A grain with a bounded mailbox that is full when the actor system stops
// rejected the shutdown PoisonPill like any other message. The grain was
// then dropped without OnDeactivate, and the messages waiting in its
// mailbox were never handled.
//
// This sample runs one grain with a mailbox of one. The first message parks
// inside OnReceive, the second one fills the mailbox, and the system stops.
// Once OnReceive is released, both messages must have been handled and
// OnDeactivate must have run.
//
// With the fix the pill is a system message that goes past the capacity of
// the mailbox, behind the messages already queued.
package main

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// The grain is created by GoAkt, so the sample observes it through package
// state.
var (
	entered     = make(chan struct{}, 1)
	release     = make(chan struct{})
	handled     atomic.Int32
	deactivated atomic.Bool
)

type slowGrain struct{}

func (g *slowGrain) OnActivate(context.Context, *actor.GrainProps) error { return nil }

func (g *slowGrain) OnReceive(ctx *actor.GrainContext) {
	if handled.Add(1) == 1 {
		entered <- struct{}{}
		<-release
	}
	ctx.NoErr()
}

func (g *slowGrain) OnDeactivate(context.Context, *actor.GrainProps) error {
	deactivated.Store(true)
	return nil
}

func main() {
	ctx := context.Background()
	actorSystem, err := actor.NewActorSystem("issue1405", actor.WithLogger(log.DiscardLogger))
	if err != nil {
		fmt.Printf("FAIL: %v\n", err)
		os.Exit(1)
	}
	if err := actorSystem.Start(ctx); err != nil {
		fmt.Printf("FAIL: %v\n", err)
		os.Exit(1)
	}

	identity, err := actor.GrainOf[*slowGrain](ctx, actorSystem, "slow", actor.WithGrainMailboxCapacity(1))
	if err != nil {
		fmt.Printf("FAIL: %v\n", err)
		os.Exit(1)
	}

	// The first message parks the turn inside OnReceive, the second one
	// fills the mailbox. Both sends wait for their acknowledgment.
	go func() { _ = actorSystem.TellGrain(ctx, identity, new(testpb.TestSend)) }()
	<-entered
	go func() { _ = actorSystem.TellGrain(ctx, identity, new(testpb.TestSend)) }()
	time.Sleep(100 * time.Millisecond)

	stopped := make(chan error, 1)
	go func() {
		stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		stopped <- actorSystem.Stop(stopCtx)
	}()
	// The system is stopping while the mailbox is full; then the turn ends.
	time.Sleep(100 * time.Millisecond)
	close(release)
	if err := <-stopped; err != nil {
		fmt.Printf("FAIL: stop: %v\n", err)
		os.Exit(1)
	}
	// Give the last turn a moment to finish.
	time.Sleep(100 * time.Millisecond)

	failed := false
	if n := handled.Load(); n != 2 {
		fmt.Printf("FAIL: %d of 2 messages handled, the one waiting in the full mailbox was dropped\n", n)
		failed = true
	}
	if !deactivated.Load() {
		fmt.Println("FAIL: OnDeactivate never ran, the shutdown pill was rejected by the full mailbox")
		failed = true
	}
	if failed {
		os.Exit(1)
	}
	fmt.Println("PASS: both messages handled and OnDeactivate ran although the mailbox was full when the system stopped")
}
