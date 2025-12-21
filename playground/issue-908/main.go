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
	"time"

	goakt "github.com/tochemey/goakt/v3/actor"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/pause"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/supervisor"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

type A struct{}
type B struct{}

var _ goakt.Actor = (*A)(nil)
var _ goakt.Actor = (*B)(nil)

func (x *A) PreStart(*goakt.Context) error { return nil }
func (x *A) PostStop(*goakt.Context) error { return nil }
func (x *A) Receive(ctx *goakt.ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testpb.TestSend:
		pause.For(1 * time.Second)
		ctx.Context().Value("test")
	default:
		ctx.Unhandled()
	}
}

func (x *B) PreStart(*goakt.Context) error { return nil }
func (x *B) PostStop(*goakt.Context) error { return nil }
func (x *B) Receive(ctx *goakt.ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
		pid := ctx.Spawn(
			"a", &A{},
			goakt.WithLongLived(),
		)
		ctx.Tell(pid, &testpb.TestSend{})
		ctx.Tell(ctx.Self(), &testpb.TestSend{})
	case *goaktpb.PanicSignal:
		ctx.Stop(ctx.Sender())
		ctx.Stop(ctx.Self())
	default:
		ctx.Unhandled()
	}
}

func main() {
	system, err := goakt.NewActorSystem(
		"issue-908",
		goakt.WithLogger(log.New(log.DebugLevel, os.Stderr)),
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
		goakt.WithLongLived(),
		goakt.WithSupervisor(supervisor.NewSupervisor(
			supervisor.WithAnyErrorDirective(supervisor.ResumeDirective),
		)),
	)
	if err != nil {
		panic(err)
	}

	select {}
}
