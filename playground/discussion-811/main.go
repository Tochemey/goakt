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
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tochemey/goakt/v3/actor"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/passivation"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

func main() {
	ctx := context.Background()

	// use the address default log. real-life implement the log interface`
	logger := log.DefaultLogger

	// create the actor system. kindly in real-life application handle the error
	actorSystem, _ := actor.NewActorSystem("ParenChild",
		actor.WithLogger(logger),
		actor.WithActorInitMaxRetries(3))

	// start the actor system
	_ = actorSystem.Start(ctx)

	// wait for system to start properly
	time.Sleep(1 * time.Second)

	// create the parent actor
	_, err := actorSystem.Spawn(ctx, "Parent", &Parent{},
		actor.WithPassivationStrategy(passivation.NewTimeBasedStrategy(24*time.Hour)),
		actor.WithSupervisor(actor.NewSupervisor(actor.WithAnyErrorDirective(actor.StopDirective))))
	if err != nil {
		logger.Fatal(err)
	}

	time.Sleep(1 * time.Second)

	// send a message to child actor
	err = actorSystem.NoSender().SendAsync(ctx, "child", &testpb.TestBye{})
	if err != nil {
		logger.Fatal(err)
	}

	// capture ctrl+c
	interruptSignal := make(chan os.Signal, 1)
	signal.Notify(interruptSignal, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-interruptSignal

	// stop the actor system
	_ = actorSystem.Stop(ctx)
	os.Exit(0)
}

type Parent struct {
	child *actor.PID
}

var _ actor.Actor = (*Parent)(nil)

func (p *Parent) PreStart(ctx *actor.Context) error {
	ctx.ActorSystem().Logger().Infof("Starting Parent actor %s", ctx.ActorName())
	return nil
}

func (p *Parent) Receive(ctx *actor.ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *goaktpb.PostStart:
		ctx.Logger().Infof("%s successfully started", ctx.Self().Name())
		// Spawn a child actor
		pid := ctx.Spawn("child", &Child{},
			actor.WithPassivationStrategy(passivation.NewTimeBasedStrategy(24*time.Hour)),
			actor.WithSupervisor(actor.NewSupervisor(actor.WithAnyErrorDirective(actor.StopDirective))),
		)

		// check if the child actor is running
		if pid.IsRunning() {
			ctx.Logger().Infof("Child actor %s successfully spawned", pid.Name())
			p.child = pid
		}
	case *goaktpb.Terminated:
		// Handle termination of child actors
		actorID := msg.GetAddress()
		ctx.Logger().Infof("Child actor %s has been terminated", actorID)
	default:
		ctx.Unhandled()
	}
}

func (p *Parent) PostStop(ctx *actor.Context) error {
	ctx.ActorSystem().Logger().Infof("Stopping Parent actor %s", ctx.ActorName())
	return nil
}

type Child struct{}

var _ actor.Actor = (*Child)(nil)

func (x *Child) PreStart(ctx *actor.Context) error {
	ctx.ActorSystem().Logger().Infof("Starting child actor %s", ctx.ActorName())
	return nil
}

func (x *Child) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
		ctx.Logger().Infof("%s successfully started", ctx.Self().Name())
	case *testpb.TestBye:
		ctx.Logger().Infof("Received bye message, stopping Child actor %s", ctx.Self().Name())
		ctx.Shutdown()
	default:
		ctx.Unhandled()
	}
}

func (x *Child) PostStop(ctx *actor.Context) error {
	ctx.ActorSystem().Logger().Infof("Stopping child actor %s", ctx.ActorName())
	return nil
}
