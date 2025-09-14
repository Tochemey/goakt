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

package actor

import (
	"github.com/tochemey/goakt/v3/address"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/log"
)

// rootGuardian defines the system root actor
// its job is to monitor the userGuardian and the systemGuardian
// when either of those actors get terminated, the actorSystem is shutdown.
type rootGuardian struct {
	pid    *PID
	logger log.Logger
}

// enforce compilation error
var _ Actor = (*rootGuardian)(nil)

// newRootGuardian creates an instance of the rootGuardian
func newRootGuardian() *rootGuardian {
	return &rootGuardian{}
}

// PreStart pre-starts the actor.
func (x *rootGuardian) PreStart(*Context) error {
	return nil
}

// Receive handles message
func (x *rootGuardian) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *goaktpb.PostStart:
		x.pid = ctx.Self()
		x.logger = ctx.Logger()
		x.logger.Infof("%s started successfully", x.pid.Name())
	case *goaktpb.Terminated:
		actorID := address.From(msg.GetAddress()).String()
		x.pid.logger.Debugf("%s terminated", actorID)
		// TODO: decide what to do the actor
	default:
		// pass
	}
}

// PostStop is executed when the actor is shutting down.
func (x *rootGuardian) PostStop(*Context) error {
	x.logger.Infof("%s stopped successfully", x.pid.Name())
	return nil
}
