/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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

	goakt "github.com/tochemey/goakt/v3/actor"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/remote"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

const (
	port = 50052
	host = "127.0.0.1"
)

func main() {
	ctx := context.Background()

	// use the address default log. real-life implement the log interface`
	logger := log.New(log.DebugLevel, os.Stdout)

	// create the actor system. kindly in real-life application handle the error
	actorSystem, _ := goakt.NewActorSystem(
		"RemotingBenchmark",
		goakt.WithPassivationDisabled(),
		goakt.WithLogger(logger),
		goakt.WithActorInitMaxRetries(3),
		goakt.WithRemote(remote.NewConfig(host, port)))

	// start the actor system
	_ = actorSystem.Start(ctx)

	// wait for the actor system to be ready
	time.Sleep(time.Second)

	// create an actor
	_, _ = actorSystem.Spawn(ctx, "Pong",
		NewPong(),
		goakt.WithSupervisor(
			goakt.NewSupervisor(
				goakt.WithAnyErrorDirective(goakt.ResumeDirective),
			),
		),
	)

	// capture ctrl+c
	interruptSignal := make(chan os.Signal, 1)
	signal.Notify(interruptSignal, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-interruptSignal

	// stop the actor system
	_ = actorSystem.Stop(ctx)
	os.Exit(0)
}

type Pong struct {
	count int
	start time.Time
}

var _ goakt.Actor = (*Pong)(nil)

func NewPong() *Pong {
	return &Pong{}
}

func (act *Pong) PreStart(ctx context.Context) error {
	return nil
}

func (act *Pong) Receive(ctx *goakt.ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testpb.TestBye:
		ctx.Logger().Infof("completed processing message: %d", act.count)
		ctx.Logger().Infof("total time taken: %s", time.Since(act.start))
	case *testpb.TestPing:
		if act.count == 0 {
			act.start = time.Now()
		}
		act.count++
		ctx.RemoteTell(ctx.RemoteSender(), new(testpb.TestPong))
	default:
		ctx.Unhandled()
	}
}

func (act *Pong) PostStop(context.Context) error {
	return nil
}
