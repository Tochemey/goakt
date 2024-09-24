/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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

	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v2/actors"
	"github.com/tochemey/goakt/v2/bench/benchmarkpb"
	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/internal/lib"
	"github.com/tochemey/goakt/v2/log"
)

func main() {
	ctx := context.Background()

	// use the address default log. real-life implement the log interface`
	logger := log.DefaultLogger

	// create the actor system. kindly in real-life application handle the error
	actorSystem, _ := actors.NewActorSystem("SampleActorSystem",
		actors.WithLogger(logger),
		actors.WithPassivationDisabled(),
		actors.WithActorInitMaxRetries(3))

	// start the actor system
	_ = actorSystem.Start(ctx)

	// wait for system to start properly
	lib.Pause(1 * time.Second)

	// create the actors
	ping := NewPing()
	pong := NewPong()
	pingActor, _ := actorSystem.Spawn(ctx, "Ping", ping)
	pongActor, _ := actorSystem.Spawn(ctx, "Pong", pong)

	// wait for actors to start properly
	lib.Pause(1 * time.Second)

	duration := time.Minute
	if err := pingActor.Tell(ctx, pongActor, new(benchmarkpb.Ping)); err != nil {
		panic(err)
	}

	// Start the timer
	done := make(chan struct{})
	go func() {
		for await := time.After(duration); ; {
			select { // nolint
			case <-await:
				done <- struct{}{}
				return
			}
		}
	}()

	<-done

	pingCount := ping.count.Load()
	pongCount := pong.count.Load()

	logger.Infof("Ping=%s has processed %d messages in %v", pingActor.Address().String(), pingCount, duration)
	logger.Infof("Pong=%s has processed %d messages in %v", pongActor.Address().String(), pongCount, duration)

	logger.Infof("Ping has processed per second: %d", int64(pingCount)/int64(duration.Seconds()))
	logger.Infof("Pong has processed per second: %d", int64(pongCount)/int64(duration.Seconds()))

	// capture ctrl+c
	interruptSignal := make(chan os.Signal, 1)
	signal.Notify(interruptSignal, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-interruptSignal

	// stop the actor system
	_ = actorSystem.Stop(ctx)
	os.Exit(0)
}

type Ping struct {
	count *atomic.Int32
}

var _ actors.Actor = (*Ping)(nil)

func NewPing() *Ping {
	return &Ping{}
}

func (p *Ping) PreStart(context.Context) error {
	p.count = atomic.NewInt32(0)
	return nil
}

func (p *Ping) Receive(ctx *actors.ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *benchmarkpb.Pong:
		p.count.Inc()
		// let us reply to the sender
		ctx.Tell(ctx.Sender(), new(benchmarkpb.Ping))
	default:
		ctx.Unhandled()
	}
}

func (p *Ping) PostStop(context.Context) error {
	return nil
}

type Pong struct {
	count *atomic.Int32
}

var _ actors.Actor = (*Pong)(nil)

func NewPong() *Pong {
	return &Pong{}
}

func (p *Pong) PreStart(context.Context) error {
	p.count = atomic.NewInt32(0)
	return nil
}

func (p *Pong) Receive(ctx *actors.ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *benchmarkpb.Ping:
		p.count.Inc()
		ctx.Tell(ctx.Sender(), new(benchmarkpb.Pong))
	default:
		ctx.Unhandled()
	}
}

func (p *Pong) PostStop(context.Context) error {
	return nil
}
