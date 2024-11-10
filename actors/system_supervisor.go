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

// systemSupervisor is an actor which roles is to handle
// escalation failure
type systemSupervisor struct {
	logger log.Logger
}

// enforce compilation error
var _ Actor = (*systemSupervisor)(nil)

// newSupervisor creates an instance of a testSupervisor
func newSystemSupervisor(logger log.Logger) *systemSupervisor {
	return &systemSupervisor{
		logger: logger,
	}
}

func (s *systemSupervisor) PreStart(context.Context) error {
	s.logger.Info("starting the system supervisor actor")
	return nil
}

func (s *systemSupervisor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
		s.logger.Info("system supervisor successfully started")
	default:
		ctx.Unhandled()
	}
}

func (s *systemSupervisor) PostStop(context.Context) error {
	s.logger.Info("system supervisor stopped")
	return nil
}
