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

package actors

import (
	"context"

	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/log"
)

// systemGuardian defines the main system actor that is the ancestor
// of system created actors in the system.
// When the systemGuardian for any reason gets terminated, the actor system will be automatically removed
// from the actors tree
type systemGuardian struct {
	pid    *PID
	logger log.Logger
	system ActorSystem
}

// enforce compilation error
var _ Actor = (*systemGuardian)(nil)

// newSystemGuardian creates an instance of system guardian
func newSystemGuardian() *systemGuardian {
	return &systemGuardian{}
}

// PreStart is the pre-start hook
func (x *systemGuardian) PreStart(context.Context) error {
	return nil
}

// Receive handle message
func (x *systemGuardian) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *goaktpb.PostStart:
		x.handlePostStart(ctx)
	case *internalpb.RebalanceComplete:
		x.completeRebalancing(ctx.Context(), msg)
	case *goaktpb.Terminated:
		x.handleTerminated(ctx.Context(), msg)
	default:
		ctx.Unhandled()
	}
}

// PostStop is the post-stop hook
func (x *systemGuardian) PostStop(context.Context) error {
	x.logger.Infof("%s stopped successfully", x.pid.Name())
	return nil
}

// handlePostStart sets the actor up
func (x *systemGuardian) handlePostStart(ctx *ReceiveContext) {
	x.pid = ctx.Self()
	x.logger = ctx.Logger()
	x.system = ctx.ActorSystem()
	x.logger.Infof("%s started successfully", x.pid.Name())
}

// handleTerminated handles Terminated message
func (x *systemGuardian) handleTerminated(ctx context.Context, msg *goaktpb.Terminated) {
	actorID := msg.GetActorId()
	systemName := x.system.Name()
	if isReservedName(actorID) {
		// log a message error and stop the actor system
		x.logger.Warnf("%s is down. %s is going to shutdown. Kindly check logs and fix any potential issue with the cluster",
			actorID,
			systemName)

		// blindly shutdown the actor system. No need to check any error
		_ = x.system.Stop(context.WithoutCancel(ctx))
	}
}

// completeRebalancing wraps up the rebalancing of dead node in the cluster
func (x *systemGuardian) completeRebalancing(ctx context.Context, msg *internalpb.RebalanceComplete) {
	x.logger.Infof("%s completing rebalancing", x.pid.Name())
	x.pid.ActorSystem().completeRebalancing()
	x.logger.Infof("%s rebalancing successfully completed", x.pid.Name())
}
