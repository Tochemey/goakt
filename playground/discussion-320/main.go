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
	"os/signal"
	"syscall"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

const (
	actorAName     = "actor-a"
	actorBName     = "actor-b"
	requestTimeout = 2 * time.Second
)

func main() {
	ctx := context.Background()

	system, err := actor.NewActorSystem("reentrancy-demo", actor.WithLogger(log.DiscardLogger))
	if err != nil {
		fmt.Printf("Error creating actor system: %v\n", err)
		os.Exit(1)
	}

	if err := system.Start(ctx); err != nil {
		fmt.Printf("Error starting actor system: %v\n", err)
		os.Exit(1)
	}

	actorA, err := system.Spawn(ctx, actorAName, &ActorA{targetName: actorBName}, actor.WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll), reentrancy.WithMaxInFlight(4))))
	if err != nil {
		fmt.Printf("Error spawning %s: %v\n", actorAName, err)
		os.Exit(1)
	}

	_, err = system.Spawn(ctx, actorBName, &ActorB{actorA: actorA})
	if err != nil {
		fmt.Printf("Error spawning %s: %v\n", actorBName, err)
		os.Exit(1)
	}

	doneCh := make(chan struct{}, 1)
	_, err = system.Spawn(ctx, "client", &Client{target: actorA, doneCh: doneCh})
	if err != nil {
		fmt.Printf("Error spawning client: %v\n", err)
		os.Exit(1)
	}

	select {
	case <-doneCh:
		fmt.Println("Reentrancy cycle completed.")
	case <-time.After(3 * time.Second):
		fmt.Println("Timeout waiting for reentrancy cycle.")
		os.Exit(1)
	}

	fmt.Println("Press Ctrl+C to stop the actor system.")
	interruptSignal := make(chan os.Signal, 1)
	signal.Notify(interruptSignal, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-interruptSignal

	if err := system.Stop(ctx); err != nil {
		fmt.Printf("Error stopping actor system: %v\n", err)
		os.Exit(1)
	}
}

type Client struct {
	target *actor.PID
	doneCh chan struct{}
}

var _ actor.Actor = (*Client)(nil)

func (c *Client) PreStart(*actor.Context) error { return nil }

func (c *Client) Receive(ctx *actor.ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *actor.PostStart:
		ctx.Tell(c.target, new(testpb.TestPing))
	case *testpb.TestCount:
		fmt.Printf("Client received count=%d\n", msg.GetValue())
		select {
		case c.doneCh <- struct{}{}:
		default:
		}
	default:
		ctx.Unhandled()
	}
}

func (c *Client) PostStop(*actor.Context) error { return nil }

type ActorA struct {
	targetName string
}

var _ actor.Actor = (*ActorA)(nil)

func (a *ActorA) PreStart(*actor.Context) error { return nil }

func (a *ActorA) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestPing:
		sender := ctx.Sender()
		self := ctx.Self()
		call := ctx.RequestName(a.targetName, new(testpb.TestPing), actor.WithRequestTimeout(requestTimeout))
		// RequestName records the error on ctx.Err and returns nil on failure.
		if call == nil {
			fmt.Println("ActorA request failed")
			return
		}
		call.Then(func(resp any, err error) {
			// Then runs without a ReceiveContext; log or message yourself to record errors.
			if err != nil {
				fmt.Printf("ActorA request error: %v\n", err)
				return
			}
			if sender == nil {
				fmt.Println("ActorA missing sender for reply")
				return
			}
			if err := self.Tell(context.Background(), sender, resp); err != nil {
				fmt.Printf("ActorA reply failed: %v\n", err)
			}
		})
	case *testpb.TestGetCount:
		ctx.Response(&testpb.TestCount{Value: 42})
	default:
		ctx.Unhandled()
	}
}

func (a *ActorA) PostStop(*actor.Context) error { return nil }

type ActorB struct {
	actorA *actor.PID
}

var _ actor.Actor = (*ActorB)(nil)

func (b *ActorB) PreStart(*actor.Context) error { return nil }

func (b *ActorB) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestPing:
		reply := ctx.Ask(b.actorA, new(testpb.TestGetCount), requestTimeout)
		if reply == nil {
			fmt.Println("ActorB ask failed")
			return
		}
		ctx.Response(reply)
	default:
		ctx.Unhandled()
	}
}

func (b *ActorB) PostStop(*actor.Context) error { return nil }
