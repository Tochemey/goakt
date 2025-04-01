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
	"github.com/tochemey/goakt/v3/address"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/remote"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

const (
	port = 50051
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
		goakt.WithRemote(remote.NewConfig(host, port)),
	)

	// start the actor system
	_ = actorSystem.Start(ctx)

	// wait for the actor system to be ready
	time.Sleep(time.Second)

	// create an actor
	toSend := 10000

	pid, _ := actorSystem.Spawn(ctx, "Ping", NewPing(toSend),
		goakt.WithSupervisor(
			goakt.NewSupervisor(
				goakt.WithAnyErrorDirective(goakt.ResumeDirective),
			),
		),
	)

	// wait for the actor to be ready
	time.Sleep(time.Second)

	// kick start the benchmark
	_ = goakt.Tell(ctx, pid, new(testpb.TestReady))

	// capture ctrl+c
	interruptSignal := make(chan os.Signal, 1)
	signal.Notify(interruptSignal, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-interruptSignal

	// stop the actor system
	_ = actorSystem.Stop(ctx)
	os.Exit(0)
}

type Ping struct {
	threshold int
	count     int
	start     time.Time
}

var _ goakt.Actor = (*Ping)(nil)

func NewPing(totalScore int) *Ping {
	return &Ping{
		threshold: totalScore,
	}
}

func (act *Ping) PreStart(context.Context) error {
	return nil
}

func (act *Ping) Receive(ctx *goakt.ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testpb.TestReady:
		remoteAddr := ctx.RemoteLookup(host, 50052, "Pong")
		to := address.From(remoteAddr)
		act.start = time.Now()
		ctx.RemoteTell(to, new(testpb.TestPing))
	case *testpb.TestPong:
		act.count++
		if act.count >= act.threshold {
			ctx.RemoteTell(ctx.RemoteSender(), new(testpb.TestBye))
			ctx.Logger().Infof("completed processing message: %d", act.count)
			ctx.Logger().Infof("total time taken: %s", time.Since(act.start))
			return
		}
		ctx.RemoteTell(ctx.RemoteSender(), new(testpb.TestPing))
	default:
		ctx.Unhandled()
	}
}

func (act *Ping) PostStop(context.Context) error {
	return nil
}
