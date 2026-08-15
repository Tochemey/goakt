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

// Package main is a living sample for github.com/Tochemey/goakt/issues/1312:
// replies to stashed Asks were silently dropped after Unstash under
// concurrent load.
//
// An actor stashes every Ask once, releases it with Unstash on a self-sent
// flush, and answers the unstashed delivery with Response. Every Stash is
// paired with exactly one flush, and every flush performs exactly one
// Unstash and grants one reply slot, so the number of replies always
// matches the number of commands no matter how messages interleave. A
// timeout can only mean Response dropped the reply, which is what a
// recycled ReceiveContext with a tripped late-reply guard did before the
// fix.
package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
)

// concurrency is the number of simultaneous Asks per round and rounds is
// how many times the burst repeats. The dropped reply needs context-pool
// churn to surface, so a single burst is not enough. askTimeout bounds
// each Ask; a healthy run answers in microseconds.
const (
	concurrency = 5
	rounds      = 40
	askTimeout  = 3 * time.Second
)

// command asks the stasher to eventually reply with its sequence number.
type command struct{ n int }

// flush releases exactly one stashed command.
type flush struct{}

// reply answers a command with the same sequence number.
type reply struct{ n int }

// stasher defers every command once via Stash, then answers it with
// Response after its paired flush has released it. repliesOwed counts the
// reply slots granted by processed flushes.
type stasher struct {
	repliesOwed int
}

// PreStart implements actor.Actor.
func (x *stasher) PreStart(*actor.Context) error { return nil }

// PostStop implements actor.Actor.
func (x *stasher) PostStop(*actor.Context) error { return nil }

// Receive stashes each first-seen command and answers commands released by
// a flush, keeping replies and commands in one-to-one correspondence.
func (x *stasher) Receive(ctx *actor.ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *command:
		if x.repliesOwed > 0 {
			x.repliesOwed--
			ctx.Response(&reply{n: msg.n})
			return
		}

		ctx.Stash()
		ctx.Tell(ctx.Self(), new(flush))

	case *flush:
		x.repliesOwed++
		ctx.Unstash()
	}
}

// main runs rounds of concurrent Asks against the stasher and exits
// non-zero when any reply is dropped or answers the wrong command.
func main() {
	ctx := context.Background()

	system, err := actor.NewActorSystem("repro", actor.WithLogger(log.DiscardLogger))
	if err != nil {
		panic(err)
	}

	if err := system.Start(ctx); err != nil {
		panic(err)
	}

	pid, err := system.Spawn(ctx, "stasher", new(stasher), actor.WithStashing(), actor.WithLongLived())
	if err != nil {
		panic(err)
	}

	var failures int

	for round := range rounds {
		var wg sync.WaitGroup

		errs := make([]error, concurrency)
		responses := make([]any, concurrency)

		wg.Add(concurrency)

		for i := range concurrency {
			go func(idx int) {
				defer wg.Done()
				responses[idx], errs[idx] = actor.Ask(ctx, pid, &command{n: idx}, askTimeout)
			}(i)
		}

		wg.Wait()

		for i := range concurrency {
			if errs[i] != nil {
				failures++
				fmt.Printf("round %d command %d: %v\n", round, i, errs[i])
				continue
			}

			if answer, ok := responses[i].(*reply); !ok || answer.n != i {
				failures++
				fmt.Printf("round %d command %d: unexpected reply %v\n", round, i, responses[i])
			}
		}
	}

	if err := system.Stop(ctx); err != nil {
		panic(err)
	}

	if failures > 0 {
		fmt.Printf("FAIL: %d/%d asks lost or mismatched their reply\n", failures, rounds*concurrency)
		os.Exit(1)
	}

	fmt.Printf("OK: all %d asks received their reply after stash and unstash\n", rounds*concurrency)
}
