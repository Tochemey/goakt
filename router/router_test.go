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

package router

import (
	"context"

	"github.com/tochemey/goakt/v2/actors"
	"github.com/tochemey/goakt/v2/log"
	"github.com/tochemey/goakt/v2/test/data/testpb"
)

type worker struct {
	counter int
	logger  log.Logger
}

var _ actors.Actor = (*worker)(nil)

func newWorker() *worker {
	return &worker{}
}

func (x *worker) PreStart(context.Context) error {
	x.logger = log.DefaultLogger
	return nil
}

func (x *worker) Receive(ctx actors.ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *testpb.DoLog:
		x.counter++
		x.logger.Infof("Got message: %s", msg.GetText())
	case *testpb.GetCount:
		x.counter++
		ctx.Response(&testpb.Count{Value: int32(x.counter)})
	default:
		ctx.Unhandled()
	}
}

func (x *worker) PostStop(context.Context) error {
	return nil
}
