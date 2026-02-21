// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package main

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/tochemey/goakt/v3/actor"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/supervisor"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

type A struct{}
type B struct{}

var _ actor.Actor = (*A)(nil)
var _ actor.Actor = (*B)(nil)

func (x *A) PreStart(*actor.Context) error { return nil }
func (x *A) PostStop(*actor.Context) error { return nil }
func (x *A) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *actor.PostStart:
		// here we schedule a message to self after 1 second
		// this will cause the actor to fail and the supervisor
		// strategy will be applied
		// since the directive is Escalate, the parent actor (B)
		// will receive the error and apply its own strategy
		// which is Resume, so the actor A will resume and continue
		// processing messages
		ctx.ActorSystem().Schedule(context.Background(), &testpb.TestSend{}, ctx.Self(), time.Second)
	case *testpb.TestSend:
		ctx.Err(errors.New("err"))
	default:
		ctx.Unhandled()
	}
}

func (x *B) PreStart(*actor.Context) error { return nil }
func (x *B) PostStop(*actor.Context) error { return nil }
func (x *B) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *actor.PostStart:
		ctx.Spawn(
			"a", &A{},
			actor.WithLongLived(),
			actor.WithSupervisor(supervisor.NewSupervisor(
				supervisor.WithAnyErrorDirective(supervisor.EscalateDirective),
			)),
		)
		time.Sleep(5 * time.Second)
	case *actor.PanicSignal:
		ctx.Stop(ctx.Sender())
		ctx.Stop(ctx.Self())
	default:
		ctx.Unhandled()
	}
}

func main() {
	system, err := actor.NewActorSystem(
		"issue-906",
		actor.WithLogger(log.New(log.DebugLevel, os.Stderr)),
	)
	if err != nil {
		panic(err)
	}

	err = system.Start(context.Background())
	if err != nil {
		panic(err)
	}

	_, err = system.Spawn(
		context.Background(), "b", &B{},
		actor.WithLongLived(),
		actor.WithSupervisor(supervisor.NewSupervisor(
			supervisor.WithAnyErrorDirective(supervisor.ResumeDirective),
		)),
	)
	if err != nil {
		panic(err)
	}

	select {}
}
