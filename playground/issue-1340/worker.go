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
	"fmt"

	"google.golang.org/protobuf/types/known/wrapperspb"

	goakt "github.com/tochemey/goakt/v4/actor"
)

// Worker is the grain the driver keeps asking. It answers with its own
// identity and the name of the node it is activated on, so the report shows
// where the grain traffic landed after the kill.
type Worker struct {
	// node is the name of the node this activation runs on. Grains are
	// activated from their kind wherever the cluster places them, so the value
	// is read at activation time.
	node string
}

// enforce compilation error
var _ goakt.Grain = (*Worker)(nil)

// OnActivate reads the name of the node this activation runs on.
func (x *Worker) OnActivate(_ context.Context, _ *goakt.GrainProps) error {
	x.node = envOr(envNodeName, defaultNodeName)
	return nil
}

// OnReceive answers a request with the grain identity and its hosting node.
func (x *Worker) OnReceive(ctx *goakt.GrainContext) {
	switch ctx.Message().(type) {
	case *wrapperspb.StringValue:
		ctx.Response(wrapperspb.String(fmt.Sprintf("%s@%s", ctx.Self().Name(), x.node)))
	default:
		ctx.Unhandled()
	}
}

// OnDeactivate is called before the grain leaves memory. Nothing to release.
func (x *Worker) OnDeactivate(_ context.Context, _ *goakt.GrainProps) error {
	return nil
}
