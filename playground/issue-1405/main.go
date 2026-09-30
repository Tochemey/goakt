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
// A grain whose bounded mailbox is full when the actor system stops used to
// reject the shutdown PoisonPill like any other message: the grain was
// dropped without OnDeactivate and Stop returned ErrMailboxFull.
//
// One grain with a mailbox of one. The first message parks inside OnReceive,
// the second one fills the mailbox, and the system stops while it is full.
// Once the first message is released, both messages must have been handled
// and answered, OnDeactivate must have run and Stop must return nil.
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

func main() {
	ctx := context.Background()
	actorSystem, err := actor.NewActorSystem("issue1405", actor.WithLogger(log.DiscardLogger))
	if err != nil {
		fail("%v", err)
	}

	if err := actorSystem.Start(ctx); err != nil {
		fail("%v", err)
	}

	identity, err := actor.GrainOf[*slowGrain](ctx, actorSystem, "slow", actor.WithGrainMailboxCapacity(1))
	if err != nil {
		fail("%v", err)
	}

	// The first message parks the turn inside OnReceive and the second one
	// fills the mailbox. Both sends wait for their acknowledgment.
	parkedAck := make(chan error, 1)
	go func() { parkedAck <- actorSystem.TellGrain(ctx, identity, new(testpb.TestSend)) }()
	<-firstMessageEntered

	queuedAck := make(chan error, 1)
	go func() { queuedAck <- actorSystem.TellGrain(ctx, identity, new(testpb.TestSend)) }()
	pause.For(100 * time.Millisecond)

	// The system stops while the mailbox is full, so the pill meets the
	// capacity; the first message is released only after that.
	stopped := make(chan error, 1)
	go func() {
		stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		stopped <- actorSystem.Stop(stopCtx)
	}()
	pause.For(200 * time.Millisecond)
	close(releaseFirstMessage)

	bugs := 0
	if err := <-stopped; err != nil {
		fmt.Printf("BUG: Stop returned an error: %v\n", err)
		bugs++
	}

	if !answered("parked", parkedAck) {
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

// The grain is constructed by GoAkt, so the sample observes it through
// package-level state.
var (
	firstMessageEntered = make(chan struct{}, 1)
	releaseFirstMessage = make(chan struct{})
	handled             atomic.Int32
	deactivated         atomic.Bool
)

// slowGrain parks its first message inside OnReceive until releaseFirstMessage
// is closed and answers every later message at once.
type slowGrain struct{}

// OnActivate does nothing.
func (x *slowGrain) OnActivate(context.Context, *actor.GrainProps) error { return nil }

// OnReceive parks the first message until released and acknowledges every message.
func (x *slowGrain) OnReceive(ctx *actor.GrainContext) {
	if handled.Add(1) == 1 {
		firstMessageEntered <- struct{}{}
		<-releaseFirstMessage
	}
	ctx.NoErr()
}

// OnDeactivate records that the grain was deactivated.
func (x *slowGrain) OnDeactivate(context.Context, *actor.GrainProps) error {
	deactivated.Store(true)
	return nil
}

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
