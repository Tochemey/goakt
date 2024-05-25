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
	"sync"
	"syscall"
	"time"

	"go.uber.org/atomic"

	goakt "github.com/tochemey/goakt/v2/actors"
	samplepb "github.com/tochemey/goakt/v2/examples/protos/samplepb"
	"github.com/tochemey/goakt/v2/log"
)

func main() {
	ctx := context.Background()

	// use the address default log. real-life implement the log interface`
	logger := log.DefaultLogger

	// create the actor system. kindly in real-life application handle the error
	actorSystem, _ := goakt.NewActorSystem("SampleActorSystem",
		goakt.WithLogger(logger),
		goakt.WithActorInitMaxRetries(3))

	// start the actor system
	_ = actorSystem.Start(ctx)

	// create teh actors
	pinger := NewPingActor()
	ponger := NewPongActor()
	pingActor, _ := actorSystem.Spawn(ctx, "Ping", pinger)
	pongActor, _ := actorSystem.Spawn(ctx, "Pong", ponger)

	// start the conversation
	_ = pingActor.Tell(ctx, pongActor, new(samplepb.Ping))

	// shutdown both actors after 3 seconds of conversation
	timer := time.AfterFunc(3*time.Second, func() {
		logger.Infof("PingActor=%s has processed %d address", pingActor.ActorPath().String(), pinger.count.Load())
		logger.Infof("PongActor=%s has processed %d address", pongActor.ActorPath().String(), ponger.count.Load())
		_ = pingActor.Shutdown(ctx)
		_ = pongActor.Shutdown(ctx)
	})
	defer timer.Stop()

	// capture ctrl+c
	interruptSignal := make(chan os.Signal, 1)
	signal.Notify(interruptSignal, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-interruptSignal

	// stop the actor system
	_ = actorSystem.Stop(ctx)
	os.Exit(0)
}

type PingActor struct {
	mu     sync.Mutex
	count  *atomic.Int32
	logger log.Logger
}

var _ goakt.Actor = (*PingActor)(nil)

func NewPingActor() *PingActor {
	return &PingActor{
		mu: sync.Mutex{},
	}
}

func (p *PingActor) PreStart(ctx context.Context) error {
	// set the log
	p.mu.Lock()
	defer p.mu.Unlock()
	p.logger = log.DefaultLogger
	p.count = atomic.NewInt32(0)
	p.logger.Info("PingActor is about to Start")
	return nil
}

func (p *PingActor) Receive(ctx goakt.ReceiveContext) {
	switch ctx.Message().(type) {
	case *samplepb.Pong:
		p.logger.Infof("%s received pong message from %s", ctx.Self().ActorPath().String(), ctx.Sender().ActorPath().String())
		// let us reply to the sender
		_ = ctx.Self().Tell(ctx.Context(), ctx.Sender(), new(samplepb.Ping))
		p.count.Add(1)
	default:
		p.logger.Panic(goakt.ErrUnhandled)
	}
}

func (p *PingActor) PostStop(ctx context.Context) error {
	p.logger.Info("PingActor is about to stop")
	p.logger.Infof("PingActor has processed=%d address", p.count.Load())
	return nil
}

type PongActor struct {
	mu     sync.Mutex
	count  *atomic.Int32
	logger log.Logger
}

var _ goakt.Actor = (*PongActor)(nil)

func NewPongActor() *PongActor {
	return &PongActor{
		mu: sync.Mutex{},
	}
}

func (p *PongActor) PreStart(ctx context.Context) error {
	// set the log
	p.mu.Lock()
	defer p.mu.Unlock()
	p.logger = log.DefaultLogger
	p.count = atomic.NewInt32(0)
	p.logger.Info("PongActor is about to Start")
	return nil
}

func (p *PongActor) Receive(ctx goakt.ReceiveContext) {
	switch ctx.Message().(type) {
	case *samplepb.Ping:
		self := ctx.Self()
		sender := ctx.Sender()

		selfPath := self.ActorPath()
		p.logger.Infof("%s received ping message from %s", selfPath.String(), sender.ActorPath().String())
		// reply the sender in case there is a sender
		_ = ctx.Self().Tell(ctx.Context(), ctx.Sender(), new(samplepb.Pong))
		p.count.Add(1)
	default:
		ctx.Unhandled()
	}
}

func (p *PongActor) PostStop(ctx context.Context) error {
	p.logger.Info("PongActor is about to stop")
	p.logger.Infof("PongActor has processed=%d address", p.count.Load())
	return nil
}
