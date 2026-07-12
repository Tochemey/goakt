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
	goakt "github.com/tochemey/goakt/v4/actor"
)

// workerPrefix names the relocatable workers worker-0..worker-N-1 so the
// verification endpoint can enumerate them deterministically.
const workerPrefix = "worker"

// Worker is a relocatable actor. It carries no state: its whole demonstration
// value is existing, being findable in the cluster registry, and coming back
// on a surviving node after its host crashes.
type Worker struct{}

// enforce compilation error
var _ goakt.Actor = (*Worker)(nil)

// PreStart is called before the actor starts. Nothing to initialize.
func (w *Worker) PreStart(*goakt.Context) error {
	return nil
}

// Receive handles messages sent to the worker.
func (w *Worker) Receive(ctx *goakt.ReceiveContext) {
	switch ctx.Message().(type) {
	case *goakt.PostStart:
	default:
		ctx.Unhandled()
	}
}

// PostStop is called after the actor stops. Nothing to release.
func (w *Worker) PostStop(*goakt.Context) error {
	return nil
}
