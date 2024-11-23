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
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/goakt/v2/address"
	"github.com/tochemey/goakt/v2/future"
	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/log"
)

// pool holds a pool of ReceiveContext
var pool = sync.Pool{
	New: func() interface{} {
		return new(ReceiveContext)
	},
}

// contextFromPool retrieves a message from the pool
func contextFromPool() *ReceiveContext {
	return pool.Get().(*ReceiveContext)
}

// returnToPool sends the message context back to the pool
func returnToPool(receiveContext *ReceiveContext) {
	receiveContext.reset()
	pool.Put(receiveContext)
}

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

// Self returns the receiver PID of the message
func (rctx *ReceiveContext) Self() *PID {
	return rctx.self
}

// Err is used instead of panicking within a message handler.
func (rctx *ReceiveContext) Err(err error) {
	rctx.err = err
}

// Response sets the message response
func (rctx *ReceiveContext) Response(resp proto.Message) {
	rctx.response <- resp
	close(rctx.response)
}

// Context represents the context attached to the message
func (rctx *ReceiveContext) Context() context.Context {
	return rctx.ctx
}

// Sender of the message
func (rctx *ReceiveContext) Sender() *PID {
	return rctx.sender
}

// RemoteSender defines the remote sender of the message if it is a remote message
// This is set to NoSender when the message is not a remote message
func (rctx *ReceiveContext) RemoteSender() *address.Address {
	return rctx.remoteSender
}

// Message is the actual message sent
func (rctx *ReceiveContext) Message() proto.Message {
	return rctx.message
}

// BecomeStacked sets a new behavior to the actor.
// The current message in process during the transition will still be processed with the current
// behavior before the transition. However, subsequent messages will be processed with the new behavior.
// One needs to call UnBecomeStacked to go the next the actor's behavior.
// which is the default behavior.
func (rctx *ReceiveContext) BecomeStacked(behavior Behavior) {
	rctx.self.setBehaviorStacked(behavior)
}

// UnBecomeStacked sets the actor behavior to the next behavior before BecomeStacked was called
func (rctx *ReceiveContext) UnBecomeStacked() {
	rctx.self.unsetBehaviorStacked()
}

// UnBecome reset the actor behavior to the default one
func (rctx *ReceiveContext) UnBecome() {
	rctx.self.resetBehavior()
}

// Become switch the current behavior of the actor to a new behavior
func (rctx *ReceiveContext) Become(behavior Behavior) {
	rctx.self.setBehavior(behavior)
}

// Stash enables an actor to temporarily buffer all or some messages that cannot or should not be handled using the actor’s current behavior
func (rctx *ReceiveContext) Stash() {
	recipient := rctx.self
	if err := recipient.stash(rctx); err != nil {
		rctx.Err(err)
	}
}

// Unstash unstashes the oldest message in the stash and prepends to the mailbox
func (rctx *ReceiveContext) Unstash() {
	recipient := rctx.self
	if err := recipient.unstash(); err != nil {
		rctx.Err(err)
	}
}

// UnstashAll unstashes all messages from the stash buffer  and prepends in the mailbox
// it keeps the messages in the same order as received, unstashing older messages before newer
func (rctx *ReceiveContext) UnstashAll() {
	recipient := rctx.self
	if err := recipient.unstashAll(); err != nil {
		rctx.Err(err)
	}
}

// Tell sends an asynchronous message to another PID
func (rctx *ReceiveContext) Tell(to *PID, message proto.Message) {
	recipient := rctx.self
	ctx := context.WithoutCancel(rctx.ctx)
	if err := recipient.Tell(ctx, to, message); err != nil {
		rctx.Err(err)
	}
}

// BatchTell sends an asynchronous bunch of messages to the given PID
// The messages will be processed one after the other in the order they are sent
// This is a design choice to follow the simple principle of one message at a time processing by actors.
// When BatchTell encounter a single message it will fall back to a Tell call.
func (rctx *ReceiveContext) BatchTell(to *PID, messages ...proto.Message) {
	recipient := rctx.self
	ctx := context.WithoutCancel(rctx.ctx)
	if err := recipient.BatchTell(ctx, to, messages...); err != nil {
		rctx.Err(err)
	}
}

// Ask sends a synchronous message to another actor and expect a response. This method is good when interacting with a child actor.
// Ask has a timeout which can cause the sender to set the context error. When ask times out, the receiving actor does not know and may still process the message.
// It is recommended to set a good timeout to quickly receive response and try to avoid false positives
func (rctx *ReceiveContext) Ask(to *PID, message proto.Message, timeout time.Duration) (response proto.Message) {
	self := rctx.self
	ctx := context.WithoutCancel(rctx.ctx)
	reply, err := self.Ask(ctx, to, message, timeout)
	if err != nil {
		rctx.Err(err)
	}
	return reply
}

// SendAsync sends an asynchronous message to a given actor.
// The location of the given actor is transparent to the caller.
func (rctx *ReceiveContext) SendAsync(actorName string, message proto.Message) {
	self := rctx.self
	ctx := context.WithoutCancel(rctx.ctx)
	if err := self.SendAsync(ctx, actorName, message); err != nil {
		rctx.Err(err)
	}
}

// SendSync sends a synchronous message to another actor and expect a response.
// The location of the given actor is transparent to the caller.
// This block until a response is received or timed out.
func (rctx *ReceiveContext) SendSync(actorName string, message proto.Message, timeout time.Duration) (response proto.Message) {
	self := rctx.self
	ctx := context.WithoutCancel(rctx.ctx)
	reply, err := self.SendSync(ctx, actorName, message, timeout)
	if err != nil {
		rctx.Err(err)
	}
	return reply
}

// BatchAsk sends a synchronous bunch of messages to the given PID and expect responses in the same order as the messages.
// The messages will be processed one after the other in the order they are sent
// This is a design choice to follow the simple principle of one message at a time processing by actors.
func (rctx *ReceiveContext) BatchAsk(to *PID, messages []proto.Message, timeout time.Duration) (responses chan proto.Message) {
	recipient := rctx.self
	ctx := context.WithoutCancel(rctx.ctx)
	reply, err := recipient.BatchAsk(ctx, to, messages, timeout)
	if err != nil {
		rctx.Err(err)
	}
	return reply
}

// RemoteTell sends a message to an actor remotely without expecting any reply
func (rctx *ReceiveContext) RemoteTell(to *address.Address, message proto.Message) {
	recipient := rctx.self
	ctx := context.WithoutCancel(rctx.ctx)
	if err := recipient.RemoteTell(ctx, to, message); err != nil {
		rctx.Err(err)
	}
}

// RemoteAsk is used to send a message to an actor remotely and expect a response
// immediately.
func (rctx *ReceiveContext) RemoteAsk(to *address.Address, message proto.Message, timeout time.Duration) (response *anypb.Any) {
	recipient := rctx.self
	ctx := context.WithoutCancel(rctx.ctx)
	reply, err := recipient.RemoteAsk(ctx, to, message, timeout)
	if err != nil {
		rctx.Err(err)
	}
	return reply
}

// RemoteBatchTell sends a batch of messages to a remote actor in a way fire-and-forget manner
// Messages are processed one after the other in the order they are sent.
func (rctx *ReceiveContext) RemoteBatchTell(to *address.Address, messages []proto.Message) {
	recipient := rctx.self
	ctx := context.WithoutCancel(rctx.ctx)
	if err := recipient.RemoteBatchTell(ctx, to, messages); err != nil {
		rctx.Err(err)
	}
}

// RemoteBatchAsk sends a synchronous bunch of messages to a remote actor and expect responses in the same order as the messages.
// Messages are processed one after the other in the order they are sent.
// This can hinder performance if it is not properly used.
func (rctx *ReceiveContext) RemoteBatchAsk(to *address.Address, messages []proto.Message, timeout time.Duration) (responses []*anypb.Any) {
	recipient := rctx.self
	ctx := context.WithoutCancel(rctx.ctx)
	replies, err := recipient.RemoteBatchAsk(ctx, to, messages, timeout)
	if err != nil {
		rctx.Err(err)
	}
	return replies
}

// RemoteLookup look for an actor address on a remote node. If the actorSystem is nil then the lookup will be done
// using the same actor system as the PID actor system
func (rctx *ReceiveContext) RemoteLookup(host string, port int, name string) (addr *goaktpb.Address) {
	recipient := rctx.self
	ctx := context.WithoutCancel(rctx.ctx)
	remoteAddr, err := recipient.RemoteLookup(ctx, host, port, name)
	if err != nil {
		rctx.Err(err)
	}
	return remoteAddr
}

// Shutdown gracefully shuts down the given actor
// All current messages in the mailbox will be processed before the actor shutdown after a period of time
// that can be configured. All child actors will be gracefully shutdown.
func (rctx *ReceiveContext) Shutdown() {
	recipient := rctx.self
	ctx := context.WithoutCancel(rctx.ctx)
	if err := recipient.Shutdown(ctx); err != nil {
		rctx.Err(err)
	}
}

// Spawn creates a child actor or return error
func (rctx *ReceiveContext) Spawn(name string, actor Actor, opts ...SpawnOption) *PID {
	recipient := rctx.self
	ctx := context.WithoutCancel(rctx.ctx)
	pid, err := recipient.SpawnChild(ctx, name, actor, opts...)
	if err != nil {
		rctx.Err(err)
	}
	return pid
}

// Children returns the list of all the children of the given actor
func (rctx *ReceiveContext) Children() []*PID {
	return rctx.self.Children()
}

// Child returns the named child actor if it is alive
func (rctx *ReceiveContext) Child(name string) *PID {
	recipient := rctx.self
	pid, err := recipient.Child(name)
	if err != nil {
		rctx.Err(err)
	}
	return pid
}

// Stop forces the child Actor under the given name to terminate after it finishes processing its current message.
// Nothing happens if child is already stopped. However, it returns an error when the child cannot be stopped.
func (rctx *ReceiveContext) Stop(child *PID) {
	recipient := rctx.self
	ctx := context.WithoutCancel(rctx.ctx)
	if err := recipient.Stop(ctx, child); err != nil {
		rctx.Err(err)
	}
}

// Forward method works similarly to the Tell() method except that the sender of a forwarded message is kept as the original sender.
// As a result, the actor receiving the forwarded messages knows who the actual sender of the message is.
// The message that is forwarded is the current message received by the received context.
// This operation does nothing when the receiving actor has not started
func (rctx *ReceiveContext) Forward(to *PID) {
	message := rctx.Message()
	sender := rctx.Sender()

	if to.IsRunning() {
		ctx := context.WithoutCancel(rctx.ctx)
		receiveContext := contextFromPool()
		receiveContext.build(ctx, sender, to, message, true)
		to.doReceive(receiveContext)
	}
}

// Unhandled is used to handle unhandled messages instead of throwing error
func (rctx *ReceiveContext) Unhandled() {
	me := rctx.self
	me.toDeadletterQueue(rctx, ErrUnhandled)
}

// RemoteReSpawn restarts an actor on a remote node.
func (rctx *ReceiveContext) RemoteReSpawn(host string, port int, name string) {
	recipient := rctx.self
	ctx := context.WithoutCancel(rctx.ctx)
	if err := recipient.RemoteReSpawn(ctx, host, port, name); err != nil {
		rctx.Err(err)
	}
}

// PipeTo processes a long-running task and pipes the result to the provided actor.
// The successful result of the task will be put onto the provided actor mailbox.
// This is useful when interacting with external services.
// It’s common that you would like to use the value of the response in the actor when the long-running task is completed
func (rctx *ReceiveContext) PipeTo(to *PID, task future.Task) {
	recipient := rctx.self
	ctx := context.WithoutCancel(rctx.ctx)
	if err := recipient.PipeTo(ctx, to, task); err != nil {
		rctx.Err(err)
	}
}

// Parent returns the parent of the given actor
// in case the receiving actor is a child actor
// this is just a convenient method
func (rctx *ReceiveContext) Parent() *PID {
	return rctx.self.Parent()
}

// Logger returns the logger used in the actor system
func (rctx *ReceiveContext) Logger() log.Logger {
	return rctx.self.Logger()
}

// Watch watches a given actor for a Terminated message
// when the watched actor shutdown
func (rctx *ReceiveContext) Watch(cid *PID) {
	rctx.self.Watch(cid)
}

// UnWatch stops watching a given actor
func (rctx *ReceiveContext) UnWatch(cid *PID) {
	rctx.self.UnWatch(cid)
}

// ActorSystem returns the actor system
func (rctx *ReceiveContext) ActorSystem() ActorSystem {
	return rctx.self.ActorSystem()
}

// getError returns any error during message processing
func (rctx *ReceiveContext) getError() error {
	return rctx.err
}

// newReceiveContext creates an instance of ReceiveContext
func newReceiveContext(ctx context.Context, from, to *PID, message proto.Message) *ReceiveContext {
	// create a message receiveContext
	return &ReceiveContext{
		ctx:      ctx,
		message:  message,
		sender:   from,
		response: make(chan proto.Message, 1),
		self:     to,
	}
}

// build sets the necessary fields of ReceiveContext
func (rctx *ReceiveContext) build(ctx context.Context, from, to *PID, message proto.Message, async bool) *ReceiveContext {
	rctx.ctx = ctx
	rctx.sender = from
	rctx.self = to
	rctx.message = message

	if async {
		return rctx
	}
	rctx.response = make(chan proto.Message, 1)
	return rctx
}

// reset resets the fields of ReceiveContext
func (rctx *ReceiveContext) reset() {
	var pid *PID
	rctx.message = nil
	rctx.self = pid
	rctx.sender = pid
	rctx.err = nil
}

// withRemoteSender set the remote sender for a given context
func (rctx *ReceiveContext) withRemoteSender(remoteSender *address.Address) *ReceiveContext {
	rctx.remoteSender = remoteSender
	return rctx
}
