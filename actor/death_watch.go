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

	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/cluster"
	"github.com/tochemey/goakt/v3/log"
)

// deathWatch removes dead actors from the system
// that helps free non-utilized resources
type deathWatch struct {
	pid            *PID
	logger         log.Logger
	tree           *pidTree
	cluster        cluster.Interface
	clusterEnabled bool
}

// enforce compilation error
var _ Actor = (*deathWatch)(nil)

// newDeathWatch creates an instance of the system deathWatch
func newDeathWatch() *deathWatch {
	return &deathWatch{}
}

// PreStart is the pre-start hook
func (x *deathWatch) PreStart(context.Context) error {
	return nil
}

// Receive handle message received
func (x *deathWatch) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *goaktpb.PostStart:
		x.handlePostStart(ctx)
	case *goaktpb.Terminated:
		ctx.Err(x.handleTerminated(ctx.Context(), msg))
	default:
		ctx.Unhandled()
	}
}

// PostStop is executed when the actor is shutting down.
func (x *deathWatch) PostStop(context.Context) error {
	x.logger.Infof("%s stopped successfully", x.pid.Name())
	return nil
}

// handlePostStart handles PostStart message
func (x *deathWatch) handlePostStart(ctx *ReceiveContext) {
	x.pid = ctx.Self()
	x.logger = ctx.Logger()
	x.tree = ctx.ActorSystem().tree()
	x.cluster = ctx.ActorSystem().getCluster()
	x.clusterEnabled = ctx.ActorSystem().InCluster()
	x.logger.Infof("%s started successfully", x.pid.Name())
}

// handleTerminated handles Terminated message
func (x *deathWatch) handleTerminated(ctx context.Context, msg *goaktpb.Terminated) error {
	actorID := msg.GetActorId()
	x.logger.Infof("%s freeing resource [actor=%s] from system", x.pid.Name(), actorID)
	if node, ok := x.tree.GetNode(actorID); ok {
		x.tree.DeleteNode(node.GetValue())
		if x.clusterEnabled {
			if err := x.cluster.RemoveActor(context.WithoutCancel(ctx), node.GetValue().Name()); err != nil {
				x.logger.Errorf("%s failed to remove [actor=%s] from cluster: %v", x.pid.Name(), actorID, err)
				return err
			}
		}
		x.logger.Infof("%s successfully free resource [actor=%s] from system", x.pid.Name(), actorID)
		return nil
	}
	x.logger.Infof("%s could not locate resource [actor=%s] in system. Maybe already freed.", x.pid.Name(), actorID)
	return nil
}
