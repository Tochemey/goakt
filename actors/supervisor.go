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

// supervisor is an actor which roles is to handle
// escalation failure
type supervisor struct {
	logger log.Logger
	system ActorSystem
}

// enforce compilation error
var _ Actor = (*supervisor)(nil)

// newSupervisor creates an instance of a testSupervisor
func newSupervisor() *supervisor {
	return &supervisor{}
}

func (s *supervisor) PreStart(context.Context) error {
	return nil
}

func (s *supervisor) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *goaktpb.PostStart:
		s.system = ctx.ActorSystem()
		s.logger = ctx.Logger()
		s.logger.Info("system supervisor successfully started")
	case *goaktpb.Terminated:
		actorID := msg.GetActorId()
		if actorID == s.system.reservedName(rebalancerType) {
			// rebalancer is dead which means either there is an issue during the cluster topology changes
			// log a message error and stop the actor system
			s.logger.Warn("%s rebalancer is down. %s is going to shutdown. Kindly check logs and fix any potential issue with the cluster",
				s.system.Name(),
				s.system.Name())

			// blindly shutdown the actor system. No need to check any error
			_ = s.system.Stop(context.WithoutCancel(ctx.Context()))
		}
	default:
		ctx.Unhandled()
	}
}

func (s *supervisor) PostStop(context.Context) error {
	s.logger.Info("system supervisor stopped")
	return nil
}
