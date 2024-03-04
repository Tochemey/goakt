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

	goakt "github.com/tochemey/goakt/actors"
	samplepb "github.com/tochemey/goakt/examples/protos/samplepb"
	"github.com/tochemey/goakt/log"
	"go.uber.org/atomic"
)

func main() {
	ctx := context.Background()

	// use the address default log. real-life implement the log interface`
	logger := log.DefaultLogger

	count := 1_000_000
	// create the PingActor system. kindly in real-life application handle the error
	actorSystem, _ := goakt.NewActorSystem("SampleActorSystem",
		goakt.WithPassivationDisabled(),
		goakt.WithLogger(logger),
		goakt.WithMailboxSize(uint64(count)),
		goakt.WithActorInitMaxRetries(3))

	// start the PingActor system
	_ = actorSystem.Start(ctx)

	// create an PingActor
	newActor := NewActor()
	actor, _ := actorSystem.Spawn(ctx, "Ping", newActor)

	// send some messages to the PingActor
	startTime := time.Now()
	for i := 0; i < count; i++ {
		_ = goakt.Tell(ctx, actor, new(samplepb.Ping))
	}

	// log some stats
	logger.Infof("Actor=%s has processed %d messages in %v", actor.ActorPath().String(), newActor.count.Load(), time.Since(startTime))

	// capture ctrl+c
	interruptSignal := make(chan os.Signal, 1)
	signal.Notify(interruptSignal, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-interruptSignal

	// stop the PingActor system
	_ = actorSystem.Stop(ctx)
	os.Exit(0)
}

type PingActor struct {
	mu     sync.Mutex
	count  *atomic.Int32
	logger log.Logger
}

func (x *PingActor) MarshalBinary() (data []byte, err error) {
	return nil, nil
}

func (x *PingActor) UnmarshalBinary(data []byte) error {
	return nil
}

var _ goakt.Actor = (*PingActor)(nil)

func NewActor() *PingActor {
	return &PingActor{
		mu: sync.Mutex{},
	}
}

func (x *PingActor) PreStart(ctx context.Context) error {
	// set the log
	x.mu.Lock()
	defer x.mu.Unlock()
	x.logger = log.DefaultLogger
	x.count = atomic.NewInt32(0)
	x.logger.Info("About to Start")
	return nil
}

func (x *PingActor) Receive(ctx goakt.ReceiveContext) {
	switch ctx.Message().(type) {
	case *samplepb.Ping:
		x.count.Inc()
	default:
		ctx.Unhandled()
	}
}

func (x *PingActor) PostStop(ctx context.Context) error {
	x.logger.Info("About to stop")
	x.logger.Infof("Processed=%d messages", x.count.Load())
	return nil
}
