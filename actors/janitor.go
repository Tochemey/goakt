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

// janitor removes dead actors from the system
// that helps free non-utilized resources
type janitor struct {
	pid    *PID
	logger log.Logger
}

// enforce compilation error
var _ Actor = (*janitor)(nil)

// newJanitor creates an instance of the system janitor
func newJanitor() *janitor {
	return &janitor{}
}

// PreStart is the pre-start hook
func (j *janitor) PreStart(context.Context) error {
	return nil
}

// Receive handle message received
func (j *janitor) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *goaktpb.PostStart:
		j.pid = ctx.Self()
		j.logger = ctx.Logger()
		j.logger.Infof("%s started successfully", j.pid.Name())

	case *goaktpb.Terminated:
		actorID := msg.GetActorId()
		system := ctx.ActorSystem()
		tree := system.tree()
		clusterEnabled := system.InCluster()
		cluster := system.getCluster()
		logger := j.logger

		logger.Infof("%s freeing resource [actor=%s] from system", j.pid.Name(), actorID)

		// remove node from the actors tree
		if node, ok := tree.GetNode(actorID); ok {
			tree.DeleteNode(node.GetValue())
			if clusterEnabled {
				if err := cluster.RemoveActor(context.WithoutCancel(ctx.Context()), node.GetValue().Name()); err != nil {
					logger.Errorf("failed to remove [actor=%s] from cluster: %v", actorID, err)
				}
			}

			logger.Infof("%s successfully free resource [actor=%s] from system", j.pid.Name(), actorID)
			return
		}

		logger.Infof("%s could not locate resource [actor=%s] in system", j.pid.Name(), actorID)
	default:
		ctx.Unhandled()
	}
}

// PostStop is executed when the actor is shutting down.
func (j *janitor) PostStop(context.Context) error {
	j.logger.Infof("%s stopped successfully", j.pid.Name())
	return nil
}
