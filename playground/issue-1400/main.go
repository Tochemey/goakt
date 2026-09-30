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

// Reproduction for https://github.com/Tochemey/goakt/issues/1400
//
// Every actor and grain turn runs on one shared dispatcher pool of
// max(GOMAXPROCS, 2) workers, and nothing let a caller size it. A grain
// whose OnReceive waits on I/O (a database transaction, an RPC) holds a
// worker for the whole wait, so a node handles at most GOMAXPROCS such
// turns at a time: on a two-CPU pod, two database calls in flight, whatever
// the number of grains.
//
// This sample runs 200 independent grains, each asked 5 times, with an
// OnReceive that sleeps 10 ms as a stand-in for a database call, and all
// 1000 asks in flight at once. The work of one grain is 50 ms; the sample
// measures how long the node takes for all of them, with the default pool
// and with a pool of 256 workers.
//
// With the fix, WithDispatcherWorkerCount sizes the pool, and the node
// finishes in about the time of one grain's work instead of
// 1000 x 10 ms / GOMAXPROCS.
package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

const (
	grains           = 200
	messagesPerGrain = 5
	databaseCall     = 10 * time.Millisecond
)

type slowGrain struct{}

func (g *slowGrain) OnActivate(context.Context, *actor.GrainProps) error { return nil }

func (g *slowGrain) OnReceive(ctx *actor.GrainContext) {
	time.Sleep(databaseCall)
	ctx.Response(new(testpb.Reply))
}

func (g *slowGrain) OnDeactivate(context.Context, *actor.GrainProps) error { return nil }

// run returns how long the node takes to answer all asks.
func run(ctx context.Context, opts ...actor.Option) time.Duration {
	actorSystem, err := actor.NewActorSystem("issue1400", append(opts, actor.WithLogger(log.DiscardLogger))...)
	if err != nil {
		fmt.Printf("FAIL: %v\n", err)
		os.Exit(1)
	}
	if err := actorSystem.Start(ctx); err != nil {
		fmt.Printf("FAIL: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = actorSystem.Stop(ctx) }()

	identities := make([]*actor.GrainIdentity, grains)
	for i := range identities {
		identity, err := actor.GrainOf[*slowGrain](ctx, actorSystem, fmt.Sprintf("grain-%d", i))
		if err != nil {
			fmt.Printf("FAIL: %v\n", err)
			os.Exit(1)
		}
		identities[i] = identity
	}

	start := time.Now()
	var wg sync.WaitGroup
	for _, identity := range identities {
		for range messagesPerGrain {
			wg.Go(func() {
				if _, err := actorSystem.AskGrain(ctx, identity, new(testpb.TestReply), time.Minute); err != nil {
					fmt.Printf("FAIL: %v\n", err)
					os.Exit(1)
				}
			})
		}
	}
	wg.Wait()
	return time.Since(start)
}

func main() {
	ctx := context.Background()
	oneGrain := messagesPerGrain * databaseCall
	fmt.Printf("GOMAXPROCS=%d, %d grains x %d messages x %v = %v of work per grain\n",
		runtime.GOMAXPROCS(0), grains, messagesPerGrain, databaseCall, oneGrain)

	withDefault := run(ctx)
	fmt.Printf("default pool:       %v (%d messages / %v = %.0f msg/s)\n",
		withDefault.Round(time.Millisecond), grains*messagesPerGrain, withDefault.Round(time.Millisecond),
		float64(grains*messagesPerGrain)/withDefault.Seconds())

	withPool := run(ctx, actor.WithDispatcherWorkerCount(256))
	fmt.Printf("256 workers:        %v (%d messages / %v = %.0f msg/s)\n",
		withPool.Round(time.Millisecond), grains*messagesPerGrain, withPool.Round(time.Millisecond),
		float64(grains*messagesPerGrain)/withPool.Seconds())

	// Independent grains must not wait for each other: the whole run should
	// take a few times the work of one grain, not hundreds.
	if withPool > 5*oneGrain {
		fmt.Printf("FAIL: %d independent grains took %v, %.0f times the work of one grain\n",
			grains, withPool.Round(time.Millisecond), float64(withPool)/float64(oneGrain))
		os.Exit(1)
	}
	fmt.Printf("PASS: with a sized pool, %d independent grains finish in %.1f times the work of one grain\n",
		grains, float64(withPool)/float64(oneGrain))
}
