/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/goakt/v2/address"
	"github.com/tochemey/goakt/v2/future"
	"github.com/tochemey/goakt/v2/goaktpb"
)

// ReceiveContext is the context that is used by the actor to receive messages
type ReceiveContext struct {
	ctx          context.Context
	message      proto.Message
	sender       *PID
	remoteSender *address.Address
	response     chan proto.Message
	self         *PID
	err          error
}

// with sets the given context
func (recvContext *ReceiveContext) with(ctx context.Context, from, to *PID, message proto.Message) *ReceiveContext {
	recvContext.ctx = ctx
	recvContext.sender = from
	recvContext.self = to
	recvContext.message = message
	recvContext.response = make(chan proto.Message, 1)
	return recvContext
}

// withRemoteSender set the remote sender for a given context
func (recvContext *ReceiveContext) withRemoteSender(remoteSender *address.Address) *ReceiveContext {
	recvContext.remoteSender = remoteSender
	return recvContext
}

// Self returns the receiver PID of the message
func (recvContext *ReceiveContext) Self() *PID {
	return recvContext.self
}

// Err is used instead of panicking within a message handler.
func (recvContext *ReceiveContext) Err(err error) {
	recvContext.err = err
}

// Response sets the message response
func (recvContext *ReceiveContext) Response(resp proto.Message) {
	recvContext.response <- resp
	close(recvContext.response)
}

// Context represents the context attached to the message
func (recvContext *ReceiveContext) Context() context.Context {
	return recvContext.ctx
}

// Sender of the message
func (recvContext *ReceiveContext) Sender() *PID {
	return recvContext.sender
}

// RemoteSender defines the remote sender of the message if it is a remote message
// This is set to NoSender when the message is not a remote message
func (recvContext *ReceiveContext) RemoteSender() *address.Address {
	return recvContext.remoteSender
}

// Message is the actual message sent
func (recvContext *ReceiveContext) Message() proto.Message {
	return recvContext.message
}

// BecomeStacked sets a new behavior to the actor.
// The current message in process during the transition will still be processed with the current
// behavior before the transition. However, subsequent messages will be processed with the new behavior.
// One needs to call UnBecomeStacked to go the next the actor's behavior.
// which is the default behavior.
func (recvContext *ReceiveContext) BecomeStacked(behavior Behavior) {
	recvContext.self.setBehaviorStacked(behavior)
}

// UnBecomeStacked sets the actor behavior to the next behavior before BecomeStacked was called
func (recvContext *ReceiveContext) UnBecomeStacked() {
	recvContext.self.unsetBehaviorStacked()
}

// UnBecome reset the actor behavior to the default one
func (recvContext *ReceiveContext) UnBecome() {
	recvContext.self.resetBehavior()
}

// Become switch the current behavior of the actor to a new behavior
func (recvContext *ReceiveContext) Become(behavior Behavior) {
	recvContext.self.setBehavior(behavior)
}

// Stash enables an actor to temporarily buffer all or some messages that cannot or should not be handled using the actor’s current behavior
func (recvContext *ReceiveContext) Stash() {
	recipient := recvContext.self
	if err := recipient.stash(recvContext); err != nil {
		recvContext.Err(err)
	}
}

// Unstash unstashes the oldest message in the stash and prepends to the mailbox
func (recvContext *ReceiveContext) Unstash() {
	recipient := recvContext.self
	if err := recipient.unstash(); err != nil {
		recvContext.Err(err)
	}
}

// UnstashAll unstashes all messages from the stash buffer  and prepends in the mailbox
// it keeps the messages in the same order as received, unstashing older messages before newer
func (recvContext *ReceiveContext) UnstashAll() {
	recipient := recvContext.self
	if err := recipient.unstashAll(); err != nil {
		recvContext.Err(err)
	}
}

// Tell sends an asynchronous message to another PID
func (recvContext *ReceiveContext) Tell(to *PID, message proto.Message) {
	recipient := recvContext.self
	ctx := context.WithoutCancel(recvContext.ctx)
	if err := recipient.Tell(ctx, to, message); err != nil {
		recvContext.Err(err)
	}
}

// BatchTell sends an asynchronous bunch of messages to the given PID
// The messages will be processed one after the other in the order they are sent
// This is a design choice to follow the simple principle of one message at a time processing by actors.
// When BatchTell encounter a single message it will fall back to a Tell call.
func (recvContext *ReceiveContext) BatchTell(to *PID, messages ...proto.Message) {
	recipient := recvContext.self
	ctx := context.WithoutCancel(recvContext.ctx)
	if err := recipient.BatchTell(ctx, to, messages...); err != nil {
		recvContext.Err(err)
	}
}

// Ask sends a synchronous message to another actor and expect a response. This method is good when interacting with a child actor.
// Ask has a timeout which can cause the sender to set the context error. When ask times out, the receiving actor does not know and may still process the message.
// It is recommended to set a good timeout to quickly receive response and try to avoid false positives
func (recvContext *ReceiveContext) Ask(to *PID, message proto.Message) (response proto.Message) {
	self := recvContext.self
	ctx := context.WithoutCancel(recvContext.ctx)
	reply, err := self.Ask(ctx, to, message)
	if err != nil {
		recvContext.Err(err)
	}
	return reply
}

// SendAsync sends an asynchronous message to a given actor.
// The location of the given actor is transparent to the caller.
func (recvContext *ReceiveContext) SendAsync(actorName string, message proto.Message) {
	self := recvContext.self
	ctx := context.WithoutCancel(recvContext.ctx)
	if err := self.SendAsync(ctx, actorName, message); err != nil {
		recvContext.Err(err)
	}
}

// SendSync sends a synchronous message to another actor and expect a response.
// The location of the given actor is transparent to the caller.
// This block until a response is received or timed out.
func (recvContext *ReceiveContext) SendSync(actorName string, message proto.Message) (response proto.Message) {
	self := recvContext.self
	ctx := context.WithoutCancel(recvContext.ctx)
	reply, err := self.SendSync(ctx, actorName, message)
	if err != nil {
		recvContext.Err(err)
	}
	return reply
}

// BatchAsk sends a synchronous bunch of messages to the given PID and expect responses in the same order as the messages.
// The messages will be processed one after the other in the order they are sent
// This is a design choice to follow the simple principle of one message at a time processing by actors.
func (recvContext *ReceiveContext) BatchAsk(to *PID, messages ...proto.Message) (responses chan proto.Message) {
	recipient := recvContext.self
	ctx := context.WithoutCancel(recvContext.ctx)
	reply, err := recipient.BatchAsk(ctx, to, messages...)
	if err != nil {
		recvContext.Err(err)
	}
	return reply
}

// RemoteTell sends a message to an actor remotely without expecting any reply
func (recvContext *ReceiveContext) RemoteTell(to *address.Address, message proto.Message) {
	recipient := recvContext.self
	ctx := context.WithoutCancel(recvContext.ctx)
	if err := recipient.RemoteTell(ctx, to, message); err != nil {
		recvContext.Err(err)
	}
}

// RemoteAsk is used to send a message to an actor remotely and expect a response
// immediately.
func (recvContext *ReceiveContext) RemoteAsk(to *address.Address, message proto.Message) (response *anypb.Any) {
	recipient := recvContext.self
	ctx := context.WithoutCancel(recvContext.ctx)
	reply, err := recipient.RemoteAsk(ctx, to, message)
	if err != nil {
		recvContext.Err(err)
	}
	return reply
}

// RemoteBatchTell sends a batch of messages to a remote actor in a way fire-and-forget manner
// Messages are processed one after the other in the order they are sent.
func (recvContext *ReceiveContext) RemoteBatchTell(to *address.Address, messages ...proto.Message) {
	recipient := recvContext.self
	ctx := context.WithoutCancel(recvContext.ctx)
	if err := recipient.RemoteBatchTell(ctx, to, messages...); err != nil {
		recvContext.Err(err)
	}
}

// RemoteBatchAsk sends a synchronous bunch of messages to a remote actor and expect responses in the same order as the messages.
// Messages are processed one after the other in the order they are sent.
// This can hinder performance if it is not properly used.
func (recvContext *ReceiveContext) RemoteBatchAsk(to *address.Address, messages ...proto.Message) (responses []*anypb.Any) {
	recipient := recvContext.self
	ctx := context.WithoutCancel(recvContext.ctx)
	replies, err := recipient.RemoteBatchAsk(ctx, to, messages...)
	if err != nil {
		recvContext.Err(err)
	}
	return replies
}

// RemoteLookup look for an actor address on a remote node. If the actorSystem is nil then the lookup will be done
// using the same actor system as the PID actor system
func (recvContext *ReceiveContext) RemoteLookup(host string, port int, name string) (addr *goaktpb.Address) {
	recipient := recvContext.self
	ctx := context.WithoutCancel(recvContext.ctx)
	remoteAddr, err := recipient.RemoteLookup(ctx, host, port, name)
	if err != nil {
		recvContext.Err(err)
	}
	return remoteAddr
}

// Shutdown gracefully shuts down the given actor
// All current messages in the mailbox will be processed before the actor shutdown after a period of time
// that can be configured. All child actors will be gracefully shutdown.
func (recvContext *ReceiveContext) Shutdown() {
	recipient := recvContext.self
	ctx := context.WithoutCancel(recvContext.ctx)
	if err := recipient.Shutdown(ctx); err != nil {
		recvContext.Err(err)
	}
}

// Spawn creates a child actor or return error
func (recvContext *ReceiveContext) Spawn(name string, actor Actor, opts ...SpawnOption) *PID {
	recipient := recvContext.self
	ctx := context.WithoutCancel(recvContext.ctx)
	pid, err := recipient.SpawnChild(ctx, name, actor, opts...)
	if err != nil {
		recvContext.Err(err)
	}
	return pid
}

// Children returns the list of all the children of the given actor
func (recvContext *ReceiveContext) Children() []*PID {
	return recvContext.self.Children()
}

// Child returns the named child actor if it is alive
func (recvContext *ReceiveContext) Child(name string) *PID {
	recipient := recvContext.self
	pid, err := recipient.Child(name)
	if err != nil {
		recvContext.Err(err)
	}
	return pid
}

// Stop forces the child Actor under the given name to terminate after it finishes processing its current message.
// Nothing happens if child is already stopped. However, it returns an error when the child cannot be stopped.
func (recvContext *ReceiveContext) Stop(child *PID) {
	recipient := recvContext.self
	ctx := context.WithoutCancel(recvContext.ctx)
	if err := recipient.Stop(ctx, child); err != nil {
		recvContext.Err(err)
	}
}

// Forward method works similarly to the Tell() method except that the sender of a forwarded message is kept as the original sender.
// As a result, the actor receiving the forwarded messages knows who the actual sender of the message is.
// The message that is forwarded is the current message received by the received context.
// This operation does nothing when the receiving actor is not running
func (recvContext *ReceiveContext) Forward(to *PID) {
	message := recvContext.Message()
	sender := recvContext.Sender()

	if to.IsRunning() {
		ctx := context.WithoutCancel(recvContext.ctx)
		to.doReceive(to.newReceiveContext().with(ctx, sender, to, message))
	}
}

// Unhandled is used to handle unhandled messages instead of throwing error
func (recvContext *ReceiveContext) Unhandled() {
	me := recvContext.self
	me.toDeadletterQueue(recvContext, ErrUnhandled)
}

// RemoteReSpawn restarts an actor on a remote node.
func (recvContext *ReceiveContext) RemoteReSpawn(host string, port int, name string) {
	recipient := recvContext.self
	ctx := context.WithoutCancel(recvContext.ctx)
	if err := recipient.RemoteReSpawn(ctx, host, port, name); err != nil {
		recvContext.Err(err)
	}
}

// PipeTo processes a long-running task and pipes the result to the provided actor.
// The successful result of the task will be put onto the provided actor mailbox.
// This is useful when interacting with external services.
// It’s common that you would like to use the value of the response in the actor when the long-running task is completed
func (recvContext *ReceiveContext) PipeTo(to *PID, task future.Task) {
	recipient := recvContext.self
	ctx := context.WithoutCancel(recvContext.ctx)
	if err := recipient.PipeTo(ctx, to, task); err != nil {
		recvContext.Err(err)
	}
}

// getError returns any error during message processing
func (recvContext *ReceiveContext) getError() error {
	return recvContext.err
}
