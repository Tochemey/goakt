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

	"github.com/tochemey/goakt/v3/actor"
)

const numActors = 5

func main() {
	ctx := context.Background()
	actorSystem := startActorSystem(ctx)

	for i := range numActors {
		spawnActor(ctx, actorSystem, fmt.Sprintf("actor%d", i+1))
	}

	stopActorSystem(ctx, actorSystem)
}

func startActorSystem(ctx context.Context) actor.ActorSystem {
	actorSystem, err := actor.NewActorSystem("test-system")
	if err != nil {
		fmt.Printf("Error creating actor system: %v\n", err)
		os.Exit(1)
	}
	if err := actorSystem.Start(ctx); err != nil {
		fmt.Printf("Error starting actor system: %v\n", err)
		os.Exit(1)
	}
	return actorSystem
}

func stopActorSystem(ctx context.Context, actorSystem actor.ActorSystem) {
	if err := actorSystem.Stop(ctx); err != nil {
		fmt.Printf("Error stopping actor system: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func spawnActor(ctx context.Context, actorSystem actor.ActorSystem, actorName string) {
	if _, err := actorSystem.Spawn(ctx, actorName, &MyActor{}); err != nil {
		fmt.Printf("Error spawning actor %s: %v\n", actorName, err)
		os.Exit(1)
	}
}

type MyActor struct{}

func (x *MyActor) PreStart(ctx *actor.Context) error {
	fmt.Printf("%s PreStart\n", ctx.ActorName())
	return nil
}

func (x *MyActor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *actor.PostStart:
		fmt.Printf("%s PostStart\n", ctx.Self().Name())
	default:
		ctx.Unhandled()
	}
}

func (x *MyActor) PostStop(ctx *actor.Context) error {
	fmt.Printf("%s PostStop\n", ctx.ActorName())
	return nil
}
