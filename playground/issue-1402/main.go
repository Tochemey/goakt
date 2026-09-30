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

// Reproduction for https://github.com/Tochemey/goakt/issues/1402
//
// Grain options (mailbox capacity, idle timeout, reentrancy, ...) are
// properties of the GrainOf call that activates the grain, not of the
// grain kind. Once the grain passivated, the next bare send (AskGrain or
// TellGrain with the identity) reactivates it with the package defaults:
// an unbounded mailbox and the default idle timeout. A service that bounds
// its mailboxes to fail fast silently loses that bound after every idle
// period, unless every send is preceded by GrainOf with the options again.
//
// This sample activates a grain with a mailbox of one and a short idle
// timeout, checks that a third message is refused while the grain is busy,
// lets the grain passivate, reactivates it with a bare send and checks the
// same thing again.
//
// With the fix, WithGrainDefaults registers the options of a grain kind on
// the actor system, and every activation of that kind starts from them.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// The grain is created by GoAkt, so the sample drives it through package
// state: a TestSend parks the turn until release is closed.
var (
	entered = make(chan struct{}, 16)
	release = make(chan struct{})
)

type boundedGrain struct{}

func (g *boundedGrain) OnActivate(context.Context, *actor.GrainProps) error { return nil }

func (g *boundedGrain) OnReceive(ctx *actor.GrainContext) {
	if _, ok := ctx.Message().(*testpb.TestSend); ok {
		entered <- struct{}{}
		<-release
	}
	ctx.NoErr()
}

func (g *boundedGrain) OnDeactivate(context.Context, *actor.GrainProps) error { return nil }

// thirdMessageIsRefused parks the grain's turn, fills its mailbox with one
// message and reports whether a third one is refused with ErrMailboxFull.
func thirdMessageIsRefused(ctx context.Context, system actor.ActorSystem, identity *actor.GrainIdentity) bool {
	for len(entered) > 0 {
		<-entered
	}
	release = make(chan struct{})
	go func() { _ = system.TellGrain(ctx, identity, new(testpb.TestSend)) }()
	<-entered
	go func() { _ = system.TellGrain(ctx, identity, new(testpb.TestSend)) }()
	time.Sleep(100 * time.Millisecond)
	err := system.TellGrain(ctx, identity, new(testpb.TestSend))
	close(release)
	time.Sleep(100 * time.Millisecond)
	return errors.Is(err, gerrors.ErrMailboxFull)
}

func main() {
	ctx := context.Background()
	actorSystem, err := actor.NewActorSystem("issue1402",
		actor.WithLogger(log.DiscardLogger),
		// The kind's options: every activation of a boundedGrain starts
		// from them.
		actor.WithGrainDefaults[*boundedGrain](
			actor.WithGrainMailboxCapacity(1),
			actor.WithGrainDeactivateAfter(300*time.Millisecond),
		),
	)
	if err != nil {
		fmt.Printf("FAIL: %v\n", err)
		os.Exit(1)
	}
	if err := actorSystem.Start(ctx); err != nil {
		fmt.Printf("FAIL: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = actorSystem.Stop(ctx) }()

	identity, err := actor.GrainOf[*boundedGrain](ctx, actorSystem, "bounded")
	if err != nil {
		fmt.Printf("FAIL: %v\n", err)
		os.Exit(1)
	}
	if !thirdMessageIsRefused(ctx, actorSystem, identity) {
		fmt.Println("FAIL: the mailbox bound is not applied on the first activation")
		os.Exit(1)
	}
	fmt.Println("first activation: a third message is refused, the mailbox is bounded")

	// The grain passivates, and a bare send reactivates it.
	time.Sleep(time.Second)
	if _, err := actorSystem.AskGrain(ctx, identity, new(testpb.TestReply), time.Second); err != nil {
		fmt.Printf("FAIL: reactivating with a bare send: %v\n", err)
		os.Exit(1)
	}
	if !thirdMessageIsRefused(ctx, actorSystem, identity) {
		fmt.Println("FAIL: after passivation a bare send reactivated the grain with an unbounded mailbox")
		os.Exit(1)
	}
	fmt.Println("PASS: after passivation a bare send reactivated the grain with the kind's bounded mailbox")
}
