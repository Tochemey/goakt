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

	"github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/pointer"
	"github.com/tochemey/goakt/v4/internal/registry"
)

// deathWatch removes dead actors from the system
// that helps free non-utilized resources
type deathWatch struct{}

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
	case *PostStart:
		x.handlePostStart(ctx)
	case *Terminated:
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
	ctx.Logger().Infof("Actor %s started successfully", ctx.Self().Name())
}

// handleTerminated handles Terminated message
func (x *deathWatch) handleTerminated(ctx *ReceiveContext) error {
	msg := ctx.Message().(*Terminated)

	logger := ctx.Logger()
	actorSys := ctx.ActorSystem()

	addr := msg.Address()
	logger.Infof("Removing dead Actor %s resource from system", addr)

	actorTree := actorSys.tree()
	if node, ok := actorTree.node(addr); ok {
		pid := node.value()

		if !pid.isStateSet(systemState) {
			actorSys.decreaseActorsCounter()
		}

		actorName := pid.Name()
		actorTree.deleteNode(pid)
		removeFromCluster := actorSys.InCluster() && !pid.isStateSet(systemState) && !actorSys.isStopping()

		if removeFromCluster {
			ctx := ctx.withoutCancel()
			cl := actorSys.getCluster()
			var err error
			multierr.AppendInto(&err, cl.RemoveActor(ctx, actorName))

			// for singleton actors, we also need to remove the kind entry
			if pid.IsSingleton() {
				singletonRole := strings.TrimSpace(pointer.Deref(pid.role, ""))
				singletonKind := registry.Name(pid.Actor())
				if singletonRole != "" {
					singletonKind = kindRole(singletonKind, singletonRole)
				}
				multierr.AppendInto(&err, cl.RemoveKind(ctx, singletonKind))
			}

			if err != nil {
				logger.Errorf("Failed to remove dead Actor %s resource from cluster: %v", addr, err)
				return errors.NewInternalError(err)
			}
		}

		logger.Infof("Successfully removed dead Actor %s resource from system", addr)
		return nil
	}
	logger.Infof("Unable to locate dead Actor %s resource in system. Maybe already freed.", ctx.Self().Name(), addr)
	return nil
}
