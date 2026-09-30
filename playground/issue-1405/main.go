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
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

func main() {
	ctx := context.Background()
	actorSystem, err := actor.NewActorSystem("issue1405", actor.WithLogger(log.DiscardLogger))
	if err != nil {
		fail("%v", err)
	}

	if err := actorSystem.Start(ctx); err != nil {
		fail("%v", err)
	}

	gateIdentity, err = actor.GrainOf[*gateGrain](ctx, actorSystem, "gate")
	if err != nil {
		fail("%v", err)
	}

	// The grain has a mailbox of one and takes no other message while a
	// request of its own is in flight.
	identity, err := actor.GrainOf[*slowGrain](ctx, actorSystem, "slow",
		actor.WithGrainMailboxCapacity(1),
		actor.WithGrainReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.StashNonReentrant))),
	)
	if err != nil {
		fail("%v", err)
	}

	// The first message leaves the grain waiting on the gate and the second
	// one fills the mailbox. Both sends wait for their acknowledgment.
	firstAck := make(chan error, 1)
	go func() { firstAck <- actorSystem.TellGrain(ctx, identity, new(testpb.TestSend)) }()
	request := <-firstRequest

	queuedAck := make(chan error, 1)
	go func() { queuedAck <- actorSystem.TellGrain(ctx, identity, new(testpb.TestSend)) }()
	pause.For(100 * time.Millisecond)

	if err := actorSystem.TellGrain(ctx, identity, new(testpb.TestSend), actor.WithOneWay()); !errors.Is(err, gerrors.ErrMailboxFull) {
		fail("the mailbox should be full before the system stops, got: %v", err)
	}

	// The system stops while the mailbox is full, so the pill meets the
	// capacity; the grain is released only after that. Cancelling the request
	// completes it on the grain itself, which is the one path that stays open
	// while the system is stopping.
	stopped := make(chan error, 1)
	go func() {
		stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		stopped <- actorSystem.Stop(stopCtx)
	}()
	pause.For(200 * time.Millisecond)
	if err := request.Cancel(); err != nil {
		fail("release the grain: %v", err)
	}

	bugs := 0
	if err := <-stopped; err != nil {
		fmt.Printf("BUG: Stop returned an error: %v\n", err)
		bugs++
	}

	if !answered("first", firstAck) {
		bugs++
	}

	if !answered("queued", queuedAck) {
		bugs++
	}

	if handled := handled.Load(); handled != 2 {
		fmt.Printf("BUG: %d of 2 messages handled; the one waiting in the full mailbox was dropped.\n", handled)
		bugs++
	}

	if !deactivated.Load() {
		fmt.Println("BUG: OnDeactivate never ran; the full mailbox rejected the shutdown pill.")
		bugs++
	}

	if bugs > 0 {
		os.Exit(1)
	}

	fmt.Println("OK: both messages handled, OnDeactivate ran and Stop returned nil although the mailbox was full when the system stopped.")
}

// The grains are constructed by GoAkt, so the sample observes them through
// package-level state.
var (
	// gateIdentity is the grain the first message waits on. It stands in for a
	// database that has not answered yet.
	gateIdentity *actor.GrainIdentity

	// firstRequest receives the request the first message makes to the gate
	// once it is in flight. Cancelling it releases the grain.
	firstRequest = make(chan actor.RequestCall, 1)

	handled     atomic.Int32
	deactivated atomic.Bool
)

// slowGrain waits on the gate for its first message and acknowledges every
// message at once. OnReceive never blocks: the wait is a request in flight,
// during which the grain takes no other message.
type slowGrain struct{}

// OnActivate does nothing.
func (x *slowGrain) OnActivate(context.Context, *actor.GrainProps) error { return nil }

// OnReceive sends the gate a request on the first message and acknowledges every message.
func (x *slowGrain) OnReceive(ctx *actor.GrainContext) {
	if handled.Add(1) == 1 {
		firstRequest <- ctx.RequestGrain(gateIdentity, new(testpb.TestSend))
	}
	ctx.NoErr()
}

// OnDeactivate records that the grain was deactivated.
func (x *slowGrain) OnDeactivate(context.Context, *actor.GrainProps) error {
	deactivated.Store(true)
	return nil
}

// gateGrain stands in for a database that never answers: it takes ownership
// of the reply and drops it. Its OnReceive returns at once.
type gateGrain struct{}

// OnActivate does nothing.
func (x *gateGrain) OnActivate(context.Context, *actor.GrainProps) error { return nil }

// OnReceive takes the reply away from the turn and never completes it.
func (x *gateGrain) OnReceive(ctx *actor.GrainContext) { ctx.DeferResponse() }

// OnDeactivate does nothing.
func (x *gateGrain) OnDeactivate(context.Context, *actor.GrainProps) error { return nil }

// answered reports whether the sender of the named message got its
// acknowledgment, waiting at most a second so a dropped message does not hold
// the sample until the send times out.
func answered(name string, ack chan error) bool {
	select {
	case err := <-ack:
		if err != nil {
			fmt.Printf("BUG: the %s message failed: %v\n", name, err)
			return false
		}
		return true
	case <-time.After(time.Second):
		fmt.Printf("BUG: the sender of the %s message got no answer.\n", name)
		return false
	}
}

// fail prints a setup error and exits with status 2.
func fail(format string, args ...any) {
	fmt.Printf("setup failed: "+format+"\n", args...)
	os.Exit(2)
}
