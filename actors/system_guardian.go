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

// systemGuardian defines the main system actor that is the ancestor
// of system created actors in the system.
// When the systemGuardian for any reason gets terminated, the actor system will be automatically removed
// from the actors tree
type systemGuardian struct {
	pid    *PID
	logger log.Logger
}

// enforce compilation error
var _ Actor = (*systemGuardian)(nil)

// newSystemGuardian creates an instance of system guardian
func newSystemGuardian() *systemGuardian {
	return &systemGuardian{}
}

// PreStart is the pre-start hook
func (g *systemGuardian) PreStart(context.Context) error {
	return nil
}

// Receive handle message
func (g *systemGuardian) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *goaktpb.PostStart:
		g.pid = ctx.Self()
		g.logger = ctx.Logger()
		g.logger.Infof("%s started successfully", g.pid.Name())
	case *goaktpb.Terminated:
		actorID := msg.GetActorId()
		system := ctx.ActorSystem()
		systemName := system.Name()
		if actorID == system.reservedName(rebalancerType) {
			// rebalancer is dead which means either there is an issue during the cluster topology changes
			// log a message error and stop the actor system
			g.logger.Warn("%s rebalancer is down. %s is going to shutdown. Kindly check logs and fix any potential issue with the cluster",
				systemName,
				systemName)

			// blindly shutdown the actor system. No need to check any error
			_ = system.Stop(context.WithoutCancel(ctx.Context()))
		}
	default:
		ctx.Unhandled()
	}
}

// PostStop is the post-stop hook
func (g *systemGuardian) PostStop(context.Context) error {
	g.logger.Infof("%s stopped successfully", g.pid.Name())
	return nil
}
