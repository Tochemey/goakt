/*
 * MIT License
 *
 * Copyright (c) 2022-2023 Tochemey
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

	goakt "github.com/tochemey/goakt/actors"
	samplepb "github.com/tochemey/goakt/examples/protos/pb/v1"
	"github.com/tochemey/goakt/log"
	"go.uber.org/atomic"
)

const (
	port = 50052
	host = "localhost"
)

func main() {
	ctx := context.Background()

	// use the address default log. real-life implement the log interface`
	logger := log.New(log.DebugLevel, os.Stdout)

	// create the actor system. kindly in real-life application handle the error
	actorSystem, _ := goakt.NewActorSystem("SampleActorSystem",
		goakt.WithPassivationDisabled(), // set big passivation time
		goakt.WithLogger(logger),
		goakt.WithActorInitMaxRetries(3),
		goakt.WithRemoting(host, port))

	// start the actor system
	_ = actorSystem.Start(ctx)

	// create an actor
	_, _ = actorSystem.Spawn(ctx, "Pong", NewPongActor(logger))

	// capture ctrl+c
	interruptSignal := make(chan os.Signal, 1)
	signal.Notify(interruptSignal, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-interruptSignal

	// stop the actor system
	_ = actorSystem.Stop(ctx)
	os.Exit(0)
}

type PongActor struct {
	count  *atomic.Int32
	logger log.Logger
}

var _ goakt.Actor = (*PongActor)(nil)

func NewPongActor(logger log.Logger) *PongActor {
	return &PongActor{
		logger: logger,
	}
}

func (p *PongActor) PreStart(ctx context.Context) error {
	p.count = atomic.NewInt32(0)
	p.logger.Info("About to Start")
	return nil
}

func (p *PongActor) Receive(ctx goakt.ReceiveContext) {
	switch ctx.Message().(type) {
	case *samplepb.Ping:
		// reply the sender in case there is a sender
		if ctx.RemoteSender() != goakt.RemoteNoSender {
			p.logger.Infof("received remote from %s", ctx.RemoteSender().String())
			_ = ctx.Self().RemoteTell(context.Background(), ctx.RemoteSender(), new(samplepb.Pong))
		} else if ctx.Sender() != goakt.NoSender {
			p.logger.Infof("received Ping from %s", ctx.Sender().ActorPath().String())
			_ = ctx.Self().Tell(ctx.Context(), ctx.Sender(), new(samplepb.Pong))
		}
		p.count.Add(1)
	default:
		p.logger.Panic(goakt.ErrUnhandled)
	}
}

func (p *PongActor) PostStop(ctx context.Context) error {
	p.logger.Info("About to stop")
	p.logger.Infof("Processed=%d public", p.count.Load())
	return nil
}
