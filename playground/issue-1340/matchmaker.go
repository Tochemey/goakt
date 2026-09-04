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
	"google.golang.org/protobuf/types/known/wrapperspb"

	goakt "github.com/tochemey/goakt/v4/actor"
)

// Matchmaker is the relocatable cluster singleton. It answers every request
// with the name of the node that currently hosts it, which is how the driver
// sees the singleton move from the killed node to a survivor.
type Matchmaker struct {
	// node is the name of the node hosting this incarnation of the singleton.
	// It is read at start time because the cluster recreates the singleton
	// from its kind on the new oldest node, without carrying any state over.
	node string
}

// enforce compilation error
var _ goakt.Actor = (*Matchmaker)(nil)

// PreStart reads the name of the node hosting this incarnation.
func (x *Matchmaker) PreStart(*goakt.Context) error {
	x.node = envOr(envNodeName, defaultNodeName)
	return nil
}

// Receive answers a request with the name of the hosting node.
func (x *Matchmaker) Receive(ctx *goakt.ReceiveContext) {
	switch ctx.Message().(type) {
	case *goakt.PostStart:
	case *wrapperspb.StringValue:
		ctx.Response(wrapperspb.String(x.node))
	default:
		ctx.Unhandled()
	}
}

// PostStop is called after the singleton stops. Nothing to release.
func (x *Matchmaker) PostStop(*goakt.Context) error {
	return nil
}
