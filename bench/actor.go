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

package bench

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	actors "github.com/tochemey/goakt/v3/actor"
	"github.com/tochemey/goakt/v3/bench/benchpb"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/log"
)

type Actor struct {
}

func (p *Actor) PreStart(*actors.Context) error {
	return nil
}

func (p *Actor) Receive(ctx *actors.ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *benchpb.BenchTell:
	case *benchpb.BenchRequest:
		ctx.Response(&benchpb.BenchResponse{})
	case *benchpb.BenchPriorityMailbox:
	default:
		ctx.Unhandled()
	}
}

func (p *Actor) PostStop(*actors.Context) error {
	return nil
}

// Benchmark defines a load testing engine
type Benchmark struct {
	// toSend define the number of message senders
	toSend       int
	pid          *actors.PID
	sender       *actors.PID
	system       actors.ActorSystem
	tellMessages []proto.Message
	askMessages  []proto.Message
}

// NewBenchmark creates an instance of Loader
func NewBenchmark(messagesCount int) *Benchmark {
	return &Benchmark{
		toSend:       messagesCount,
		tellMessages: make([]proto.Message, messagesCount),
		askMessages:  make([]proto.Message, messagesCount),
	}
}

// ActorRef returns the actor reference
func (x *Benchmark) ActorRef() *actors.PID {
	return x.pid
}

// Start starts the Benchmark
func (x *Benchmark) Start(ctx context.Context) error {
	// create the benchmark actor system
	name := "benchmark-system"
	var err error
	x.system, _ = actors.NewActorSystem(name,
		actors.WithLogger(log.DiscardLogger),
		actors.WithActorInitMaxRetries(1))

	if err := x.system.Start(ctx); err != nil {
		return err
	}

	// wait for the actor system to properly start
	time.Sleep(500 * time.Millisecond)

	actorName := "actor-benchmark"
	x.pid, err = x.system.Spawn(ctx, actorName, &Actor{})
	if err != nil {
		return err
	}

	x.sender, err = x.system.Spawn(ctx, "sender", &Actor{})
	if err != nil {
		return err
	}

	// wait for the actors to properly start
	time.Sleep(500 * time.Millisecond)

	// build the messages to be sent
	for i := range x.toSend {
		x.tellMessages[i] = new(benchpb.BenchTell)
		x.askMessages[i] = new(benchpb.BenchRequest)
	}

	return nil
}

// Stop stops the benchmark
func (x *Benchmark) Stop(ctx context.Context) error {
	return x.system.Stop(ctx)
}

// BenchTell sends messages to an actor
func (x *Benchmark) BenchTell(ctx context.Context) error {
	// wait for the tellMessages to be delivered
	if err := x.sender.BatchTell(ctx, x.pid, x.tellMessages...); err != nil {
		return fmt.Errorf("failed to bench: %w", err)
	}

	// wait for the messages to be processed
	time.Sleep(time.Second)
	return nil
}

// BenchAsk sends messages to an actor
func (x *Benchmark) BenchAsk(ctx context.Context) error {
	// wait for the tellMessages to be delivered
	if _, err := x.sender.BatchAsk(ctx, x.pid, x.askMessages, time.Minute); err != nil {
		return fmt.Errorf("failed to bench: %w", err)
	}

	// wait for the messages to be processed
	time.Sleep(time.Second)
	return nil
}
