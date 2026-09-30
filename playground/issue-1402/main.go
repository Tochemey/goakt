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
// an unbounded mailbox and the default idle timeout of two minutes. A
// service that configures its grains silently loses that configuration
// after every idle period, unless every send is preceded by GrainOf with
// the options again.
//
// This sample declares a different idle timeout for two grain kinds,
// activates one grain of each, lets the short-lived one passivate,
// reactivates it with a bare send and checks that each grain still follows
// its own kind's idle timeout: the short-lived one passivates again, the
// long-lived one stays active. Before the fix the reactivated grain lived
// on for the package default.
//
// With the fix, WithGrainDefaultOptions registers the options of a grain
// kind on the actor system, and every activation of that kind starts from
// them.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// shortIdle and longIdle are the two kinds' idle timeouts; settle is how
// long the sample waits for a passivation to be observed.
const (
	shortIdle = 300 * time.Millisecond
	longIdle  = time.Hour
	settle    = time.Second
)

// idleGrain is the behavior both kinds share: it answers every message and
// otherwise sits idle until passivated.
type idleGrain struct{}

func (g *idleGrain) OnActivate(context.Context, *actor.GrainProps) error { return nil }

func (g *idleGrain) OnReceive(ctx *actor.GrainContext) { ctx.NoErr() }

func (g *idleGrain) OnDeactivate(context.Context, *actor.GrainProps) error { return nil }

// shortLivedGrain and longLivedGrain are two kinds with the same behavior
// and different default options.
type shortLivedGrain struct{ idleGrain }

type longLivedGrain struct{ idleGrain }

// isActive reports whether the grain is listed among the system's active grains.
func isActive(ctx context.Context, system actor.ActorSystem, identity *actor.GrainIdentity) bool {
	for _, active := range system.Grains(ctx, time.Second) {
		if active.String() == identity.String() {
			return true
		}
	}

	return false
}

// fail prints the message and exits.
func fail(format string, args ...any) {
	fmt.Printf("FAIL: "+format+"\n", args...)
	os.Exit(1)
}

func main() {
	ctx := context.Background()
	actorSystem, err := actor.NewActorSystem("issue1402",
		actor.WithLogger(log.DiscardLogger),
		// One declaration per kind: every activation of that kind starts
		// from its own options. A mailbox capacity or a reentrancy policy
		// is declared the same way.
		actor.WithGrainDefaultOptions[*shortLivedGrain](actor.WithGrainDeactivateAfter(shortIdle)),
		actor.WithGrainDefaultOptions[*longLivedGrain](actor.WithGrainDeactivateAfter(longIdle)),
	)
	if err != nil {
		fail("%v", err)
	}

	if err := actorSystem.Start(ctx); err != nil {
		fail("%v", err)
	}

	short, err := actor.GrainOf[*shortLivedGrain](ctx, actorSystem, "short")
	if err != nil {
		fail("%v", err)
	}

	long, err := actor.GrainOf[*longLivedGrain](ctx, actorSystem, "long")
	if err != nil {
		fail("%v", err)
	}

	pause.For(settle)

	if isActive(ctx, actorSystem, short) {
		fail("the short-lived kind's idle timeout is not applied on the first activation")
	}

	if !isActive(ctx, actorSystem, long) {
		fail("the long-lived kind's idle timeout is not applied on the first activation")
	}

	fmt.Println("first activation: the short-lived grain passivated, the long-lived grain is still active")

	// A bare send reactivates the short-lived grain and reaches the
	// long-lived one where it is.
	if _, err := actorSystem.AskGrain(ctx, short, new(testpb.TestReply), time.Second); err != nil {
		fail("reactivating the short-lived grain with a bare send: %v", err)
	}

	if _, err := actorSystem.AskGrain(ctx, long, new(testpb.TestReply), time.Second); err != nil {
		fail("sending to the long-lived grain: %v", err)
	}

	if !isActive(ctx, actorSystem, short) {
		fail("the bare send did not reactivate the short-lived grain")
	}

	pause.For(settle)

	if isActive(ctx, actorSystem, short) {
		fail("after passivation a bare send reactivated the short-lived grain with the package's idle timeout")
	}

	if !isActive(ctx, actorSystem, long) {
		fail("the long-lived grain passivated on another kind's idle timeout")
	}

	fmt.Println("PASS: each grain kind keeps its own default options across a bare-send reactivation")
	_ = actorSystem.Stop(ctx)
}
