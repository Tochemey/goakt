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

	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/address"
)

// PersistentContext defines the message context used by persistent actors
// This context is a bit different from the normal ReceiveContext of stateless actors.
type PersistentContext struct {
	ctx          context.Context
	command      proto.Message
	currentState *PersistentState
	sender       *PID
	remoteSender *address.Address
	self         *PID
}

// Context represents the context attached to the command
func (pctx *PersistentContext) Context() context.Context {
	return pctx.ctx
}

// CurrentState returns the persistent actor current state
func (pctx *PersistentContext) CurrentState() *PersistentState {
	return pctx.currentState
}

// Self returns the receiver PID of the message
func (pctx *PersistentContext) Self() *PID {
	return pctx.self
}

// Sender of the command
func (pctx *PersistentContext) Sender() *PID {
	return pctx.sender
}

// RemoteSender defines the remote sender of the message if it is a remote message
// This is set to NoSender when the message is not a remote message
func (pctx *PersistentContext) RemoteSender() *address.Address {
	return pctx.remoteSender
}

// Command is the actual command sent
func (pctx *PersistentContext) Command() proto.Message {
	return pctx.command
}

// reset resets the fields of ReceiveContext
func (pctx *PersistentContext) reset() {
	var pid *PID
	pctx.currentState = nil
	pctx.command = nil
	pctx.self = pid
	pctx.sender = pid
}
