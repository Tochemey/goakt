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

package actor

import (
	"context"

	sup "github.com/tochemey/goakt/v3/supervisor"
)

// NoSender returns a special PID that represents an anonymous / absent sender.
//
// Use this PID when sending or scheduling messages for which no sender is expected. The PID
// is meaningful only for local messaging and is not routable across the network.
//
// In remote scenarios use address.NoSender(), which encodes the appropriate
// network address semantics for a no-sender value.
//
// Notes:
//   - The returned PID should be used as the Sender in envelopes, not as a target
//     destination for Send operations.
//   - The value is stable for local use and intended to explicitly indicate the
//     absence of a sender (as opposed to nil).
func (x *actorSystem) NoSender() *PID {
	x.locker.RLock()
	noSender := x.noSender
	x.locker.RUnlock()
	return noSender
}

// noSender is a special actor that does not have a sender
// and is used to send messages without a sender. It does not respond to messages
type noSender struct{}

var _ Actor = (*noSender)(nil)

// newNoSender creates a new noSender actor
func newNoSender() *noSender {
	return &noSender{}
}

func (x *noSender) PreStart(*Context) error {
	return nil
}

func (x *noSender) Receive(ctx *ReceiveContext) {
	ctx.Unhandled()
}

func (x *noSender) PostStop(*Context) error {
	return nil
}

func (x *actorSystem) spawnNoSender(ctx context.Context) error {
	actorName := x.reservedName(noSenderType)

	supervisor := sup.NewSupervisor(
		sup.WithStrategy(sup.OneForOneStrategy),
		sup.WithAnyErrorDirective(sup.ResumeDirective),
	)

	x.noSender, _ = x.configPID(ctx,
		actorName,
		newNoSender(),
		asSystem(),
		WithLongLived(),
		WithSupervisor(supervisor),
	)

	return x.actors.addNode(x.systemGuardian, x.noSender)
}
