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
	"time"

	"github.com/tochemey/goakt/v3/actor"
	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/log"
)

func main() {
	ctx := context.Background()
	logger := log.DiscardLogger

	fmt.Println("Starting actor system...")
	actorSystem, _ := actor.NewActorSystem("Test", actor.WithLogger(logger))
	if err := actorSystem.Start(ctx); err != nil {
		fmt.Printf("failed to start actor system: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\nSpawning actor...")
	pid, err := actorSystem.Spawn(ctx, "actor1", &MyActor{}, actor.WithLongLived())
	if err != nil {
		fmt.Printf("failed to spawn actor: %v\n", err)
		os.Exit(1)
	}

	time.Sleep(time.Second)

	fmt.Println("\nStopping actor...")
	if err := pid.Shutdown(ctx); err != nil {
		fmt.Printf("failed to stop actor: %v\n", err)
		os.Exit(1)
	}

	// wait for a while to ensure the actor is stopped
	time.Sleep(time.Second)

	ok, err := actorSystem.ActorExists(ctx, "actor1")
	if err != nil {
		fmt.Println("failed to check if actor exists:", err)
		os.Exit(1)
	}

	if ok {
		fmt.Println("actor should be stopped but still exists")
		os.Exit(1)
	}

	addr, pid, err := actorSystem.ActorOf(ctx, "actor1")
	if err != nil {
		if !errors.Is(err, gerrors.ErrActorNotFound) {
			fmt.Println("failed to get actor:", err)
			os.Exit(1)
		}
	}

	if addr != nil || pid != nil {
		fmt.Println("actor should be stopped but still exists")
		os.Exit(1)
	}

	fmt.Println("Actor stopped successfully")

	fmt.Println("\nStopping actor system...")
	_ = actorSystem.Stop(ctx)
}

type MyActor struct{}

var _ actor.Actor = (*MyActor)(nil)

func (x *MyActor) PreStart(*actor.Context) error {
	fmt.Println("PreStart")
	return nil
}

func (x *MyActor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *actor.PostStart:
		fmt.Println("PostStart")
	default:
		ctx.Unhandled()
	}
}

func (x *MyActor) PostStop(*actor.Context) error {
	fmt.Println("PostStop")
	return nil
}
