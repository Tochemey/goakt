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

package actor

import (
	"context"
	"errors"
	"time"

	"github.com/tochemey/goakt/v2/test/data/testpb"
)

const (
	delay          = 1 * time.Second
	askTimeout     = 100 * time.Millisecond
	passivateAfter = 200 * time.Millisecond
)

type testActor struct{}

var _ Actor = (*testActor)(nil)

func (actor *testActor) PreStart(context.Context) error { return nil }
func (actor *testActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestTell:
	case *testpb.TestAsk:
		ctx.Response(new(testpb.TestAsk))
	case *testpb.TestAskTimeout:
		time.Sleep(delay)
	case *testpb.TestPanic:
		panic("panic")
	default:
		ctx.Unhandled()
	}
}
func (actor *testActor) PostStop(context.Context) error { return nil }

type faultyPostStopActor struct{}

func (actor *faultyPostStopActor) PostStop(context.Context) error { return errors.New("failed") }
func (actor *faultyPostStopActor) PreStart(context.Context) error { return nil }
func (actor *faultyPostStopActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestTell:
	default:
		ctx.Unhandled()
	}
}

type faultyPreStartActor struct {
	count int
}

func (actor *faultyPreStartActor) PreStart(ctx context.Context) error {
	actor.count++
	if actor.count > 1 {
		return errors.New("failed")
	}
	return nil
}
func (actor *faultyPreStartActor) PostStop(ctx context.Context) error { return nil }
func (actor *faultyPreStartActor) Receive(ctx *ReceiveContext)        {}
