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
	"github.com/tochemey/goakt/v4/log"
)

// userGuardian defines the main system actor that is the ancestor
// of user created actors in the system.
// When the userGuardian for any reason gets terminated, the actor system will be automatically removed
// from the actors tree
type userGuardian struct {
	pid    *PID
	logger log.Logger
}

// enforce compilation error
var _ Actor = (*userGuardian)(nil)

// newUserGuardian creates an instance of userGuardian
func newUserGuardian() *userGuardian {
	return &userGuardian{}
}

// PreStart is the pre-start hook
func (x *userGuardian) PreStart(*Context) error {
	return nil
}

// Receive handles messages
func (x *userGuardian) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *PostStart:
		x.pid = ctx.Self()
		x.logger = ctx.Logger()
		x.logger.Infof("%s started successfully", x.pid.Name())
	case *Terminated:
		actorID := msg.Address
		x.logger.Debugf("%s received a terminated actor=(%s)", x.pid.Name(), actorID)
		// pass
	default:
		ctx.Unhandled()
	}
}

// PostStop is the post-stop hook
func (x *userGuardian) PostStop(ctx *Context) error {
	ctx.ActorSystem().Logger().Infof("%s stopped successfully", ctx.ActorName())
	return nil
}
