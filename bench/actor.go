/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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

	"github.com/tochemey/goakt/v2/actors"
	"github.com/tochemey/goakt/v2/bench/benchmarkpb"
	"github.com/tochemey/goakt/v2/goaktpb"
)

type Benchmarker struct {
}

func (p *Benchmarker) PreStart(context.Context) error {
	return nil
}

func (p *Benchmarker) Receive(ctx *actors.ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *benchmarkpb.BenchTell:
	case *benchmarkpb.BenchRequest:
		ctx.Response(&benchmarkpb.BenchResponse{})
	default:
		ctx.Unhandled()
	}
}

func (p *Benchmarker) PostStop(context.Context) error {
	return nil
}
