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

package actor

import (
	"context"

	"github.com/tochemey/goakt/v4/log"
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
	case *PostStart:
		x.pid = ctx.Self()
		x.logger = ctx.Logger()
		if x.logger.Enabled(log.InfoLevel) {
			x.logger.Infof("actor=%s started successfully", x.pid.Name())
		}
	case *PanicSignal:
		x.handlePanicSignal(ctx)
	case *Terminated:
		actorID := msg.Address()
		if x.pid.logger.Enabled(log.DebugLevel) {
			x.pid.logger.Debugf("actor=%s terminated", actorID)
		}
		// TODO: decide what to do the actor
	default:
		// pass
	}
}

// PostStop is executed when the actor is shutting down.
func (x *rootGuardian) PostStop(ctx *Context) error {
	logger := ctx.ActorSystem().Logger()
	if logger.Enabled(log.InfoLevel) {
		logger.Infof("actor=%s stopped successfully", ctx.ActorName())
	}
	return nil
}

func (x *rootGuardian) handlePanicSignal(ctx *ReceiveContext) {
	systemName := ctx.ActorSystem().Name()
	actorName := ctx.Sender().Name()
	if !ctx.ActorSystem().isStopping() && isSystemName(actorName) {
		if x.logger.Enabled(log.WarningLevel) {
			x.logger.Warnf("actor=%s system=%s is down, going to shutdown. Check logs and fix any potential issue", actorName, systemName)
		}

		// blindly shutdown the actor system. No need to check any error
		_ = ctx.ActorSystem().Stop(context.WithoutCancel(ctx.Context()))
	}
}
