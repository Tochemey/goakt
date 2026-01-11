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
	"strings"

	"go.uber.org/multierr"

	"github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/cluster"
	"github.com/tochemey/goakt/v3/internal/pointer"
	"github.com/tochemey/goakt/v3/internal/registry"
	"github.com/tochemey/goakt/v3/log"
)

// deathWatch removes dead actors from the system
// that helps free non-utilized resources
type deathWatch struct {
	pid         *PID
	logger      log.Logger
	tree        *tree
	cluster     cluster.Cluster
	actorSystem ActorSystem
}

// enforce compilation error
var _ Actor = (*deathWatch)(nil)

// newDeathWatch creates an instance of the system deathWatch
func newDeathWatch() *deathWatch {
	return &deathWatch{}
}

// PreStart is the pre-start hook
func (x *deathWatch) PreStart(*Context) error {
	return nil
}

// Receive a handle message received
func (x *deathWatch) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
		x.handlePostStart(ctx)
	case *goaktpb.Terminated:
		ctx.Err(x.handleTerminated(ctx))
	default:
		ctx.Unhandled()
	}
}

// PostStop is executed when the actor is shutting down.
func (x *deathWatch) PostStop(ctx *Context) error {
	ctx.ActorSystem().Logger().Infof("%s stopped successfully", ctx.ActorName())
	return nil
}

// handlePostStart handles PostStart message
func (x *deathWatch) handlePostStart(ctx *ReceiveContext) {
	x.actorSystem = ctx.ActorSystem()
	x.pid = ctx.Self()
	x.logger = ctx.Logger()
	x.tree = x.actorSystem.tree()
	x.cluster = x.actorSystem.getCluster()
	x.logger.Infof("Actor %s started successfully", x.pid.Name())
}

// handleTerminated handles Terminated message
func (x *deathWatch) handleTerminated(ctx *ReceiveContext) error {
	msg := ctx.Message().(*goaktpb.Terminated)

	addr := msg.GetAddress()
	x.logger.Infof("Removing dead Actor %s resource from system", addr)

	if node, ok := x.tree.node(addr); ok {
		pid := node.value()

		if !pid.isStateSet(systemState) {
			x.actorSystem.decreaseActorsCounter()
		}

		actorName := pid.Name()
		x.tree.deleteNode(pid)
		removeFromCluster := x.actorSystem.InCluster() && !pid.isStateSet(systemState) && !x.actorSystem.isStopping()

		if removeFromCluster {
			ctx := ctx.withoutCancel()
			var err error
			multierr.AppendInto(&err, x.cluster.RemoveActor(ctx, actorName))

			// for singleton actors, we also need to remove the kind entry
			// TODO: add unit tests for this
			if pid.IsSingleton() {
				singletonRole := strings.TrimSpace(pointer.Deref(pid.role, ""))
				singletonKind := registry.Name(pid.Actor())
				if singletonRole != "" {
					singletonKind = kindRole(singletonKind, singletonRole)
				}
				multierr.AppendInto(&err, x.cluster.RemoveKind(ctx, singletonKind))
			}

			if err != nil {
				x.logger.Errorf("Failed to remove dead Actor %s resource from cluster: %v", addr, err)
				return errors.NewInternalError(err)
			}
		}

		x.logger.Infof("Successfully removed dead Actor %s resource from system", addr)
		return nil
	}
	x.logger.Infof("Unable to locate dead Actor %s resource in system. Maybe already freed.", x.pid.Name(), addr)
	return nil
}
