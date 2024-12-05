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

package actors

import (
	"context"

	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/log"
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
func (r *rootGuardian) PreStart(context.Context) error {
	return nil
}

// Receive handles message
func (r *rootGuardian) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *goaktpb.PostStart:
		r.pid = ctx.Self()
		r.logger = ctx.Logger()
		r.logger.Infof("%s started successfully", r.pid.Name())
	case *goaktpb.Terminated:
		r.pid.logger.Debugf("%s terminated", msg.GetActorId())
		// TODO: decide what to do the actor
	default:
		// pass
	}
}

// PostStop is executed when the actor is shutting down.
func (r *rootGuardian) PostStop(context.Context) error {
	r.logger.Infof("%s stopped successfully", r.pid.Name())
	return nil
}
