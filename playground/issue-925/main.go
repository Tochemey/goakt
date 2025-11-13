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
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tochemey/goakt/v3/actor"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/log"
)

func main() {
	ctx := context.Background()
	logger := log.DiscardLogger

	fmt.Println("Starting actor system...")
	actorSystem, _ := actor.NewActorSystem("Test", actor.WithLogger(logger))
	_ = actorSystem.Start(ctx)

	fmt.Println("\nSpawning actor...")
	pid, err := actorSystem.Spawn(ctx, "actor1", &MyActor{}, actor.WithLongLived())
	if err != nil {
		logger.Fatal()
	}

	// wait for a while to let actor start and spawn its children
	time.Sleep(time.Second)

	fmt.Println("\nTotal children...")
	kids := pid.Children()
	for _, kid := range kids {
		fmt.Println(" - ", kid.Name())
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM)
	signal.Notify(ch, syscall.SIGINT)

	<-ch

	_ = actorSystem.Stop(ctx)

	fmt.Println("\nStopping actor system...")
	_ = actorSystem.Stop(ctx)
}

type MyActor struct{}

var _ actor.Actor = (*MyActor)(nil)

func (x *MyActor) PreStart(*actor.Context) error {
	return nil
}

func (x *MyActor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
		ctx.Spawn("child", &MyActor{})
		_, err := ctx.Self().SpawnChild(ctx.Context(), "child2", &MyActor{})
		if err != nil {
			ctx.Err(err)
		}
	default:
		ctx.Unhandled()
	}
}

func (x *MyActor) PostStop(*actor.Context) error {
	fmt.Println("PostStop")
	return nil
}
