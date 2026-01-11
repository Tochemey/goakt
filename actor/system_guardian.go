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

	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/log"
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
func (x *systemGuardian) PreStart(*Context) error {
	return nil
}

// Receive handle message
func (x *systemGuardian) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *goaktpb.PostStart:
		x.handlePostStart(ctx)
	case *internalpb.RebalanceComplete:
		// TODO: TBD with the error
		_ = x.completeRebalancing(msg)
	case *goaktpb.Terminated:
		// pass
	case *goaktpb.PanicSignal:
		x.handlePanicSignal(ctx)
	default:
		ctx.Unhandled()
	}
}

// PostStop is the post-stop hook
func (x *systemGuardian) PostStop(ctx *Context) error {
	ctx.ActorSystem().Logger().Infof("Actor %s stopped successfully", ctx.ActorName())
	return nil
}

// handlePostStart sets the actor up
func (x *systemGuardian) handlePostStart(ctx *ReceiveContext) {
	x.pid = ctx.Self()
	x.logger = ctx.Logger()
	x.system = ctx.ActorSystem()
	x.logger.Infof("Actor %s started successfully", x.pid.Name())
}

func (x *systemGuardian) handlePanicSignal(ctx *ReceiveContext) {
	systemName := x.system.Name()
	actorName := ctx.Sender().Name()
	if !x.system.isStopping() && isSystemName(actorName) {
		// log a message error and stop the actor system
		x.logger.Warnf("Actor %s is down. System %s is going to shutdown. Kindly check logs and fix any potential issue with the system",
			actorName,
			systemName)

		// blindly shutdown the actor system. No need to check any error
		_ = x.system.Stop(context.WithoutCancel(ctx.Context()))
	}
}

// completeRebalancing wraps up the rebalancing of left node in the cluster
func (x *systemGuardian) completeRebalancing(msg *internalpb.RebalanceComplete) error {
	x.logger.Info("Completing rebalancing...")
	x.pid.ActorSystem().completeRelocation()

	x.logger.Infof("Removing left Node (%s) from cluster store", msg.GetPeerAddress())

	ctx := context.Background()
	clusterStore := x.pid.ActorSystem().getClusterStore()
	if err := clusterStore.DeletePeerState(ctx, msg.GetPeerAddress()); err != nil {
		x.logger.Errorf("Failed to remove left Node (%s) from cluster store: %v", msg.GetPeerAddress(), err)
		return err
	}

	x.logger.Infof("Left Node (%s) successfully removed from cache", msg.GetPeerAddress())

	x.logger.Info("Rebalancing completed successfully")
	return nil
}
