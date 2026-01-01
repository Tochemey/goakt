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

	"github.com/tochemey/goakt/v3/actor"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/log"
)

func main() {
	ctx := context.Background()

	fmt.Println("Starting actor system...")
	actorSystem, _ := actor.NewActorSystem("Test", actor.WithLogger(log.DiscardLogger))
	_ = actorSystem.Start(ctx)

	fmt.Println("\nSpawning actor...")
	pid, _ := actorSystem.Spawn(ctx, "actor1", &MyActor{}, actor.WithLongLived())

	fmt.Println("\nRestarting actor...")
	pid.Restart(ctx)

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
	case *goaktpb.PostStart:
		fmt.Println("PostStart")
	default:
		ctx.Unhandled()
	}
}

func (x *MyActor) PostStop(*actor.Context) error {
	fmt.Println("PostStop")
	return nil
}
