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

// Reproduction for https://github.com/Tochemey/goakt/issues/1397
//
// Grain passivation could run OnDeactivate while the grain was still handling
// a message: without reentrancy, the passivation manager called deactivate on
// its own goroutine as soon as the idle deadline passed, even when a turn was
// in progress. With reentrancy, the passivation pill went through the mailbox,
// but the turn kept draining after the pill had deactivated the grain, so the
// messages queued behind the pill were handled by the deactivated instance.
//
// This sample activates one grain whose OnReceive takes longer than the
// deactivateAfter, once without and once with reentrancy, and checks that
// OnDeactivate never runs while OnReceive is running, and that no message is
// handled by an instance whose OnDeactivate already ran.
//
// With the fix the passivation decision always goes through the mailbox, so
// it executes after the message in progress, and a message that was queued
// behind the pill is refused with ErrDead instead of being handled by the
// deactivated grain, so the caller retries against a fresh activation.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

const deactivateAfter = 50 * time.Millisecond

// activations collects every grain instance the actor system activates, so
// the sample can read their reports after the run.
var activations struct {
	mu     sync.Mutex
	grains []*slowGrain
}

// slowGrain holds each message for longer than deactivateAfter, as a slow
// database call would, and records whether OnDeactivate had already run when
// a message was handled.
type slowGrain struct {
	mu          sync.Mutex
	inReceive   bool
	deactivated bool
	violations  []string
}

func (g *slowGrain) OnActivate(context.Context, *actor.GrainProps) error {
	activations.mu.Lock()
	defer activations.mu.Unlock()
	activations.grains = append(activations.grains, g)
	return nil
}

func (g *slowGrain) OnReceive(ctx *actor.GrainContext) {
	g.mu.Lock()
	g.inReceive = true
	if g.deactivated {
		g.violations = append(g.violations, "OnReceive after OnDeactivate")
	}
	g.mu.Unlock()

	time.Sleep(3 * deactivateAfter)

	g.mu.Lock()
	if g.deactivated {
		g.violations = append(g.violations, "OnDeactivate ran while OnReceive was handling a message")
	}
	g.inReceive = false
	g.mu.Unlock()
	ctx.Response(new(testpb.Reply))
}

func (g *slowGrain) OnDeactivate(context.Context, *actor.GrainProps) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.deactivated = true
	if g.inReceive {
		g.violations = append(g.violations, "OnDeactivate ran while OnReceive was handling a message")
	}
	return nil
}

func (g *slowGrain) report() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.violations...)
}

// run sends three messages to one grain: the first holds the turn open past
// the idle deadline, so the passivation fires while the grain is busy, and
// the other two are sent after that deadline, so they queue behind the
// passivation pill. It returns the violations the grains saw and the errors
// the senders got.
func run(ctx context.Context, name string, opts ...actor.GrainOption) ([]string, []error, error) {
	actorSystem, err := actor.NewActorSystem(name, actor.WithLogger(log.DiscardLogger))
	if err != nil {
		return nil, nil, err
	}
	if err := actorSystem.Start(ctx); err != nil {
		return nil, nil, err
	}
	defer func() { _ = actorSystem.Stop(ctx) }()

	opts = append(opts, actor.WithGrainDeactivateAfter(deactivateAfter))
	identity, err := actor.GrainOf[*slowGrain](ctx, actorSystem, "slow", opts...)
	if err != nil {
		return nil, nil, err
	}

	errs := make([]error, 3)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Go(func() {
			_, errs[i] = actorSystem.AskGrain(ctx, identity, new(testpb.TestReply), 5*time.Second)
		})
		if i == 0 {
			// Past the idle deadline, with the first message still in
			// progress: the passivation pill is in the mailbox now.
			time.Sleep(2 * deactivateAfter)
		}
	}
	wg.Wait()
	// Give the passivation manager time to fire after the last message.
	time.Sleep(4 * deactivateAfter)

	var violations []string
	activations.mu.Lock()
	for _, g := range activations.grains {
		violations = append(violations, g.report()...)
	}
	activations.grains = nil
	activations.mu.Unlock()
	return violations, errs, nil
}

func main() {
	ctx := context.Background()
	failed := false
	modes := []struct {
		name string
		opts []actor.GrainOption
	}{
		{name: "default"},
		{name: "reentrancy", opts: []actor.GrainOption{
			actor.WithGrainReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.StashNonReentrant))),
		}},
	}
	for _, mode := range modes {
		violations, errs, err := run(ctx, "issue1397-"+mode.name, mode.opts...)
		if err != nil {
			fmt.Printf("FAIL: %s: %v\n", mode.name, err)
			os.Exit(1)
		}
		for _, v := range violations {
			fmt.Printf("FAIL: %s: %s\n", mode.name, v)
			failed = true
		}
		for i, err := range errs {
			switch {
			case err == nil:
				fmt.Printf("PASS: %s: message %d handled\n", mode.name, i+1)
			case errors.Is(err, gerrors.ErrDead), err.Error() == gerrors.ErrDead.Error():
				// Queued behind the passivation: refused, the sender retries
				// against a fresh activation. The envelope path of a grain
				// with reentrancy carries the error as text.
				fmt.Printf("PASS: %s: message %d refused with ErrDead, to be sent again\n", mode.name, i+1)
			default:
				fmt.Printf("FAIL: %s: message %d: %v\n", mode.name, i+1, err)
				failed = true
			}
		}
	}
	if failed {
		os.Exit(1)
	}
	fmt.Println("PASS: OnDeactivate never overlapped OnReceive and no message reached a deactivated grain")
}
