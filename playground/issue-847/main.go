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
	"errors"
	"fmt"
	"os"
	"os/signal"

	goakt "github.com/tochemey/goakt/v3/actor"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/log"
)

type Foo struct{}

var _ goakt.Actor = (*Foo)(nil)

func (c *Foo) PreStart(*goakt.Context) error { return nil }
func (c *Foo) PostStop(*goakt.Context) error { return nil }

func (c *Foo) Receive(ctx *goakt.ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
		ctx.Spawn("bar", &Bar{},
			goakt.WithSupervisor(goakt.NewSupervisor(
				goakt.WithAnyErrorDirective(goakt.EscalateDirective),
			)),
		)
	case *goaktpb.PanicSignal:
		ctx.Stop(ctx.Sender())
	}
}

type Bar struct{}

var _ goakt.Actor = (*Bar)(nil)

func (c *Bar) PreStart(*goakt.Context) error { return nil }
func (c *Bar) PostStop(*goakt.Context) error {
	// this is unreachable
	fmt.Println("bar stop")
	return nil
}

func (c *Bar) Receive(ctx *goakt.ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
		ctx.Err(errors.New("test"))
	}
}

func main() {
	ctx := context.Background()

	system, err := goakt.NewActorSystem(
		"TestSystem",
		goakt.WithLogger(log.DiscardLogger),
	)
	if err != nil {
		panic(err)
	}
	defer system.Stop(ctx)

	if err := system.Start(ctx); err != nil {
		panic(err)
	}

	_, err = system.Spawn(ctx, "foo", &Foo{}, goakt.WithLongLived())
	if err != nil {
		system.Logger().Fatal(err)
		return
	}

	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, os.Interrupt)
	<-sigint
}
