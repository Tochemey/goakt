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

	goakt "github.com/tochemey/goakt/actors"
	samplepb "github.com/tochemey/goakt/examples/protos/samplepb"
	"github.com/tochemey/goakt/goaktpb"
	"github.com/tochemey/goakt/log"
	"go.uber.org/atomic"
)

const (
	port = 50051
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
	pingActor, _ := actorSystem.Spawn(ctx, "Ping", NewPingActor())

	// start the conversation
	timer := time.AfterFunc(time.Second, func() {
		_ = pingActor.RemoteTell(ctx, &goaktpb.Address{
			Host: host,
			Port: 50052,
			Name: "Pong",
			Id:   "",
		}, new(samplepb.Ping))
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
	count  *atomic.Int32
	logger log.Logger
}

var _ goakt.Actor = (*PingActor)(nil)

func NewPingActor() *PingActor {
	return &PingActor{}
}

func (x *PingActor) PreStart(ctx context.Context) error {
	// set the log
	x.logger = log.DefaultLogger
	x.count = atomic.NewInt32(0)
	x.logger.Info("About to Start")
	return nil
}

func (x *PingActor) Receive(ctx goakt.ReceiveContext) {
	switch ctx.Message().(type) {
	case *samplepb.Pong:
		// reply the sender in case there is a sender
		if ctx.RemoteSender() != goakt.RemoteNoSender {
			x.logger.Infof("received remote from %s", ctx.RemoteSender().String())
			_ = ctx.Self().RemoteTell(context.Background(), ctx.RemoteSender(), new(samplepb.Ping))
		} else if ctx.Sender() != goakt.NoSender {
			x.logger.Infof("received Pong from %s", ctx.Sender().ActorPath().String())
			_ = ctx.Self().Tell(ctx.Context(), ctx.Sender(), new(samplepb.Ping))
		}
		x.count.Add(1)
	default:
		x.logger.Panic(goakt.ErrUnhandled)
	}
}

func (x *PingActor) PostStop(ctx context.Context) error {
	x.logger.Info("About to stop")
	x.logger.Infof("Processed=%d address", x.count.Load())
	return nil
}

func (x *PingActor) MarshalBinary() (data []byte, err error) {
	return nil, nil
}

func (x *PingActor) UnmarshalBinary(data []byte) error {
	return nil
}
