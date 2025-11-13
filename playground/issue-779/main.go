/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/tochemey/goakt/v3/actor"
	errors2 "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/goaktpb"
)

var _actorSystem actor.ActorSystem

func main() {
	ctx := context.Background()

	fmt.Println("Starting actor system...")
	actorSystem, _ := actor.NewActorSystem("Test", actor.WithCoordinatedShutdown(&MyShutdownHook{}))
	if err := actorSystem.Start(ctx); err != nil {
		fmt.Println("Error starting actor system:", err)
		os.Exit(1)
	}
	_actorSystem = actorSystem
	fmt.Printf("Actor system running: %v\n", actorSystemIsRunning())

	fmt.Println("\nSpawning actor...")
	if _, err := actorSystem.Spawn(ctx, "actor1", &MyActor{}); err != nil {
		fmt.Println("Error spawning actor:", err)
		os.Exit(1)
	}

	fmt.Printf("\nStopping actor system...\n\n")
	if err := actorSystem.Stop(ctx); err != nil {
		fmt.Printf("Actor system running: %v\n", actorSystemIsRunning())
		fmt.Println("Error stopping actor system:", err)
		os.Exit(1)
	}

	fmt.Printf("Actor system running: %v\n", actorSystemIsRunning())

	fmt.Println("Actor system stopped successfully")
	actorSystem.Host()
}

func actorSystemIsRunning() bool {
	if _actorSystem == nil {
		return false
	}

	// hack since the status is not exposed
	// the first thing kill does is report an error if the actor system is not started
	// any other error means the actor system is running
	err := _actorSystem.Kill(context.Background(), "")
	return !errors.Is(err, errors2.ErrActorSystemNotStarted)
}

type MyActor struct{}

var _ actor.Actor = (*MyActor)(nil)

func (x *MyActor) PreStart(*actor.Context) error {
	fmt.Println("Actor PreStart")
	return nil
}

func (x *MyActor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
		fmt.Println("Actor PostStart")
	default:
		ctx.Unhandled()
	}
}

func (x *MyActor) PostStop(*actor.Context) error {
	fmt.Println("Actor PostStop")
	return nil
}

type MyShutdownHook struct{}

// Execute implements actor.ShutdownHook.
func (m *MyShutdownHook) Execute(context.Context, actor.ActorSystem) error {
	fmt.Println("Before shutdown hook executed")
	fmt.Printf("Actor system running: %v\n", actorSystemIsRunning()) // should be true
	time.Sleep(1 * time.Second)
	fmt.Printf("Before shutdown hook completed\n\n")
	return nil
}

// Recovery implements actor.ShutdownHook.
func (m *MyShutdownHook) Recovery() *actor.ShutdownHookRecovery {
	return actor.NewShutdownHookRecovery(
		actor.WithShutdownHookRecoveryStrategy(actor.ShouldFail),
	)
}

var _ actor.ShutdownHook = (*MyShutdownHook)(nil)
