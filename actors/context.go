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
	"sync"
	"time"

	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/goakt/v2/future"
	"github.com/tochemey/goakt/v2/goaktpb"
)

// ReceiveContext is the context that is used by the actor to receive messages
type ReceiveContext interface {
	// Context returns the context attached to the message
	Context() context.Context
	// Sender of the message. In the case of remote message this will be set to NoSender
	Sender() PID
	// Self represents the actor receiving the message.
	Self() PID
	// Message is the actual message sent
	Message() proto.Message
	// Response sets the message response
	// Use this method within the Actor.Receive method of the actor to sets a reply
	// This can only be used when we are request-response pattern. When it is an async communication
	// this operation will amount to nothing.
	Response(resp proto.Message)
	// RemoteSender defines the remote sender of the message if it is a remote message
	// This is set to RemoteNoSender when the message is not a remote message
	RemoteSender() *goaktpb.Address
	// Become switch the current behavior of the actor to a new behavior
	// The current message in process during the transition will still be processed with the current
	// behavior before the transition. However, subsequent messages will be processed with the new behavior.
	// One needs to call UnBecome to reset the actor behavior to the default one which is the Actor.Receive method
	// which is the default behavior.
	Become(behavior Behavior)
	// UnBecome reset the actor behavior to the default one which is the
	// Actor.Receive method
	UnBecome()
	// BecomeStacked sets a new behavior to the actor to the top of the behavior stack, while maintaining the previous ones.
	// The current message in process during the transition will still be processed with the current
	// behavior before the transition. However, subsequent messages will be processed with the new behavior.
	// One needs to call UnBecomeStacked to go the previous the actor's behavior.
	// which is the default behavior.
	BecomeStacked(behavior Behavior)
	// UnBecomeStacked sets the actor behavior to the previous behavior before BecomeStacked was called
	UnBecomeStacked()
	// Stash adds the current message to the stash buffer
	Stash()
	// Unstash unstashes the oldest message in the stash and prepends to the mailbox
	Unstash()
	// UnstashAll unstashes all messages from the stash buffer  and prepends in the mailbox
	// it keeps the messages in the same order as received, unstashing older messages before newer
	UnstashAll()
	// Tell sends an asynchronous message to another PID
	Tell(to PID, message proto.Message)
	// BatchTell sends an asynchronous bunch of messages to the given PID
	// The messages will be processed one after the other in the order they are sent
	// This is a design choice to follow the simple principle of one message at a time processing by actors.
	// When TellStream encounter a single message it will fall back to a Tell call.
	BatchTell(to PID, messages ...proto.Message)
	// Ask sends a synchronous message to another actor and expect a response. This method is good when interacting with a child actor.
	// Ask has a timeout which can cause the sender to panic. When ask times out, the receiving actor does not know and may still process the message.
	// It is recommended to set a good timeout to quickly receive response and try to avoid false positives
	Ask(to PID, message proto.Message) (response proto.Message)
	// BatchAsk sends a synchronous bunch of messages to the given PID and expect responses in the same order as the messages.
	// The messages will be processed one after the other in the order they are sent
	// This is a design choice to follow the simple principle of one message at a time processing by actors.
	BatchAsk(to PID, messages ...proto.Message) (responses chan proto.Message)
	// Forward method works similarly to the Tell() method except that the sender of a forwarded message is kept as the original sender.
	// As a result, the actor receiving the forwarded messages knows who the actual sender of the message is.
	// The message that is forwarded is the current message received by the received context.
	// This operation does nothing when the receiving actor is not running
	Forward(to PID)
	// RemoteTell sends a message to an actor remotely without expecting any reply
	RemoteTell(to *goaktpb.Address, message proto.Message)
	// RemoteBatchTell sends a batch of messages to a remote actor in a way fire-and-forget manner
	// Messages are processed one after the other in the order they are sent.
	RemoteBatchTell(to *goaktpb.Address, messages ...proto.Message)
	// RemoteAsk is used to send a message to an actor remotely and expect a response
	// immediately. This executed within an actor can hinder performance because this is a blocking call.
	RemoteAsk(to *goaktpb.Address, message proto.Message) (response *anypb.Any)
	// RemoteBatchAsk sends a synchronous bunch of messages to a remote actor and expect responses in the same order as the messages.
	// Messages are processed one after the other in the order they are sent.
	// This can hinder performance if it is not properly used.
	RemoteBatchAsk(to *goaktpb.Address, messages ...proto.Message) (responses []*anypb.Any)
	// RemoteLookup look for an actor address on a remote node. If the actorSystem is nil then the lookup will be done
	// using the same actor system as the PID actor system
	RemoteLookup(host string, port int, name string) (addr *goaktpb.Address)
	// Shutdown gracefully shuts down the given actor
	// All current messages in the mailbox will be processed before the actor shutdown after a period of time
	// that can be configured. All child actors will be gracefully shutdown.
	Shutdown()
	// Spawn creates a child actor or panic
	Spawn(name string, actor Actor) PID
	// Children returns the list of all the children of the given actor
	Children() []PID
	// Child returns the named child actor if it is alive
	Child(name string) PID
	// Stop forces the child Actor under the given name to terminate after it finishes processing its current message.
	// Nothing happens if child is already stopped.
	Stop(child PID)
	// Unhandled is used to handle unhandled messages instead of throwing error
	// This will push the given message into the deadletter queue
	Unhandled()
	// RemoteReSpawn restarts an actor on a remote node.
	RemoteReSpawn(host string, port int, name string)
	// Err is used instead of panicking within a message handler.
	// One can also call panic which is not the recommended way
	Err(err error)
	// PipeTo processes a long-running task and pipes the result to the provided actor.
	// The successful result of the task will be put onto the provided actor mailbox.
	// This is useful when interacting with external services.
	// It’s common that you would like to use the value of the response in the actor when the long-running task is completed
	PipeTo(to PID, task future.Task)
}

type receiveContext struct {
	ctx            context.Context
	message        proto.Message
	sender         PID
	remoteSender   *goaktpb.Address
	response       chan proto.Message
	recipient      PID
	mu             sync.Mutex
	isAsyncMessage bool
	sendTime       atomic.Time
}

// force compilation error
var _ ReceiveContext = &receiveContext{}

// newReceiveContext creates an instance of ReceiveContext
func newReceiveContext(ctx context.Context, from, to PID, message proto.Message, async bool) *receiveContext {
	// create a message context
	context := new(receiveContext)

	// set the needed properties of the message context
	context.ctx = ctx
	context.sender = from
	context.recipient = to
	context.message = message
	context.isAsyncMessage = async
	context.mu = sync.Mutex{}
	context.response = make(chan proto.Message, 1)
	context.sendTime.Store(time.Now())

	// return the created context
	return context
}

// WithRemoteSender set the remote sender for a given context
func (c *receiveContext) WithRemoteSender(remoteSender *goaktpb.Address) *receiveContext {
	c.remoteSender = remoteSender
	return c
}

// Self returns the receiver PID of the message
func (c *receiveContext) Self() PID {
	c.mu.Lock()
	self := c.recipient
	c.mu.Unlock()
	return self
}

// Err is used instead of panicking within a message handler.
// One can also call panic which is not the recommended way
func (c *receiveContext) Err(err error) {
	if err != nil {
		// this will be recovered
		panic(err)
	}
}

// Response sets the message response
func (c *receiveContext) Response(resp proto.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// only set a response when the message is sync message
	if !c.isAsyncMessage {
		defer close(c.response)
		c.response <- resp
	}
}

// Context represents the context attached to the message
func (c *receiveContext) Context() context.Context {
	c.mu.Lock()
	ctx := c.ctx
	c.mu.Unlock()
	return ctx
}

// Sender of the message
func (c *receiveContext) Sender() PID {
	c.mu.Lock()
	sender := c.sender
	c.mu.Unlock()
	return sender
}

// RemoteSender defines the remote sender of the message if it is a remote message
// This is set to RemoteNoSender when the message is not a remote message
func (c *receiveContext) RemoteSender() *goaktpb.Address {
	c.mu.Lock()
	sender := c.remoteSender
	c.mu.Unlock()
	return sender
}

// Message is the actual message sent
func (c *receiveContext) Message() proto.Message {
	c.mu.Lock()
	message := c.message
	c.mu.Unlock()
	return message
}

// BecomeStacked sets a new behavior to the actor.
// The current message in process during the transition will still be processed with the current
// behavior before the transition. However, subsequent messages will be processed with the new behavior.
// One needs to call UnBecomeStacked to go the previous the actor's behavior.
// which is the default behavior.
func (c *receiveContext) BecomeStacked(behavior Behavior) {
	c.mu.Lock()
	c.recipient.setBehaviorStacked(behavior)
	c.mu.Unlock()
}

// UnBecomeStacked sets the actor behavior to the previous behavior before BecomeStacked was called
func (c *receiveContext) UnBecomeStacked() {
	c.mu.Lock()
	c.recipient.unsetBehaviorStacked()
	c.mu.Unlock()
}

// UnBecome reset the actor behavior to the default one
func (c *receiveContext) UnBecome() {
	c.mu.Lock()
	c.recipient.resetBehavior()
	c.mu.Unlock()
}

// Become switch the current behavior of the actor to a new behavior
func (c *receiveContext) Become(behavior Behavior) {
	c.mu.Lock()
	c.recipient.setBehavior(behavior)
	c.mu.Unlock()
}

// Stash enables an actor to temporarily buffer all or some messages that cannot or should not be handled using the actor’s current behavior
func (c *receiveContext) Stash() {
	c.mu.Lock()
	recipient := c.recipient
	c.mu.Unlock()
	if err := recipient.stash(c); err != nil {
		c.Err(err)
	}
}

// Unstash unstashes the oldest message in the stash and prepends to the mailbox
func (c *receiveContext) Unstash() {
	c.mu.Lock()
	recipient := c.recipient
	c.mu.Unlock()
	if err := recipient.unstash(); err != nil {
		c.Err(err)
	}
}

// UnstashAll unstashes all messages from the stash buffer  and prepends in the mailbox
// it keeps the messages in the same order as received, unstashing older messages before newer
func (c *receiveContext) UnstashAll() {
	c.mu.Lock()
	recipient := c.recipient
	c.mu.Unlock()
	if err := recipient.unstashAll(); err != nil {
		c.Err(err)
	}
}

// Tell sends an asynchronous message to another PID
func (c *receiveContext) Tell(to PID, message proto.Message) {
	c.mu.Lock()
	recipient := c.recipient
	ctx := context.WithoutCancel(c.ctx)
	c.mu.Unlock()
	if err := recipient.Tell(ctx, to, message); err != nil {
		panic(err)
	}
}

// BatchTell sends an asynchronous bunch of messages to the given PID
// The messages will be processed one after the other in the order they are sent
// This is a design choice to follow the simple principle of one message at a time processing by actors.
// When BatchTell encounter a single message it will fall back to a Tell call.
func (c *receiveContext) BatchTell(to PID, messages ...proto.Message) {
	c.mu.Lock()
	recipient := c.recipient
	ctx := context.WithoutCancel(c.ctx)
	c.mu.Unlock()
	if err := recipient.BatchTell(ctx, to, messages...); err != nil {
		c.Err(err)
	}
}

// Ask sends a synchronous message to another actor and expect a response. This method is good when interacting with a child actor.
// Ask has a timeout which can cause the sender to panic. When ask times out, the receiving actor does not know and may still process the message.
// It is recommended to set a good timeout to quickly receive response and try to avoid false positives
func (c *receiveContext) Ask(to PID, message proto.Message) (response proto.Message) {
	c.mu.Lock()
	recipient := c.recipient
	ctx := context.WithoutCancel(c.ctx)
	c.mu.Unlock()
	reply, err := recipient.Ask(ctx, to, message)
	if err != nil {
		c.Err(err)
	}
	return reply
}

// BatchAsk sends a synchronous bunch of messages to the given PID and expect responses in the same order as the messages.
// The messages will be processed one after the other in the order they are sent
// This is a design choice to follow the simple principle of one message at a time processing by actors.
func (c *receiveContext) BatchAsk(to PID, messages ...proto.Message) (responses chan proto.Message) {
	c.mu.Lock()
	recipient := c.recipient
	ctx := context.WithoutCancel(c.ctx)
	c.mu.Unlock()
	reply, err := recipient.BatchAsk(ctx, to, messages...)
	if err != nil {
		c.Err(err)
	}
	return reply
}

// RemoteTell sends a message to an actor remotely without expecting any reply
func (c *receiveContext) RemoteTell(to *goaktpb.Address, message proto.Message) {
	c.mu.Lock()
	recipient := c.recipient
	ctx := context.WithoutCancel(c.ctx)
	c.mu.Unlock()
	if err := recipient.RemoteTell(ctx, to, message); err != nil {
		c.Err(err)
	}
}

// RemoteAsk is used to send a message to an actor remotely and expect a response
// immediately.
func (c *receiveContext) RemoteAsk(to *goaktpb.Address, message proto.Message) (response *anypb.Any) {
	c.mu.Lock()
	recipient := c.recipient
	ctx := context.WithoutCancel(c.ctx)
	c.mu.Unlock()
	reply, err := recipient.RemoteAsk(ctx, to, message)
	if err != nil {
		c.Err(err)
	}
	return reply
}

// RemoteBatchTell sends a batch of messages to a remote actor in a way fire-and-forget manner
// Messages are processed one after the other in the order they are sent.
func (c *receiveContext) RemoteBatchTell(to *goaktpb.Address, messages ...proto.Message) {
	c.mu.Lock()
	recipient := c.recipient
	ctx := context.WithoutCancel(c.ctx)
	c.mu.Unlock()
	if err := recipient.RemoteBatchTell(ctx, to, messages...); err != nil {
		c.Err(err)
	}
}

// RemoteBatchAsk sends a synchronous bunch of messages to a remote actor and expect responses in the same order as the messages.
// Messages are processed one after the other in the order they are sent.
// This can hinder performance if it is not properly used.
func (c *receiveContext) RemoteBatchAsk(to *goaktpb.Address, messages ...proto.Message) (responses []*anypb.Any) {
	c.mu.Lock()
	recipient := c.recipient
	ctx := context.WithoutCancel(c.ctx)
	c.mu.Unlock()
	replies, err := recipient.RemoteBatchAsk(ctx, to, messages...)
	if err != nil {
		c.Err(err)
	}
	return replies
}

// RemoteLookup look for an actor address on a remote node. If the actorSystem is nil then the lookup will be done
// using the same actor system as the PID actor system
func (c *receiveContext) RemoteLookup(host string, port int, name string) (addr *goaktpb.Address) {
	c.mu.Lock()
	recipient := c.recipient
	ctx := context.WithoutCancel(c.ctx)
	c.mu.Unlock()
	remoteAddr, err := recipient.RemoteLookup(ctx, host, port, name)
	if err != nil {
		c.Err(err)
	}
	return remoteAddr
}

// Shutdown gracefully shuts down the given actor
// All current messages in the mailbox will be processed before the actor shutdown after a period of time
// that can be configured. All child actors will be gracefully shutdown.
func (c *receiveContext) Shutdown() {
	c.mu.Lock()
	recipient := c.recipient
	ctx := context.WithoutCancel(c.ctx)
	c.mu.Unlock()
	if err := recipient.Shutdown(ctx); err != nil {
		c.Err(err)
	}
}

// Spawn creates a child actor or panic
func (c *receiveContext) Spawn(name string, actor Actor) PID {
	c.mu.Lock()
	recipient := c.recipient
	ctx := context.WithoutCancel(c.ctx)
	c.mu.Unlock()
	pid, err := recipient.SpawnChild(ctx, name, actor)
	if err != nil {
		c.Err(err)
	}
	return pid
}

// Children returns the list of all the children of the given actor
func (c *receiveContext) Children() []PID {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.recipient.Children()
}

// Child returns the named child actor if it is alive
func (c *receiveContext) Child(name string) PID {
	c.mu.Lock()
	recipient := c.recipient
	c.mu.Unlock()
	pid, err := recipient.Child(name)
	if err != nil {
		c.Err(err)
	}
	return pid
}

// Stop forces the child Actor under the given name to terminate after it finishes processing its current message.
// Nothing happens if child is already stopped. However, it panics when the child cannot be stopped.
func (c *receiveContext) Stop(child PID) {
	c.mu.Lock()
	recipient := c.recipient
	ctx := context.WithoutCancel(c.ctx)
	c.mu.Unlock()
	if err := recipient.Stop(ctx, child); err != nil {
		c.Err(err)
	}
}

// Forward method works similarly to the Tell() method except that the sender of a forwarded message is kept as the original sender.
// As a result, the actor receiving the forwarded messages knows who the actual sender of the message is.
// The message that is forwarded is the current message received by the received context.
// This operation does nothing when the receiving actor is not running
func (c *receiveContext) Forward(to PID) {
	message := c.Message()
	sender := c.Sender()
	c.mu.Lock()
	defer c.mu.Unlock()

	if to.IsRunning() {
		ctx := context.WithoutCancel(c.ctx)
		receiveContext := &receiveContext{
			ctx:            ctx,
			message:        message,
			sender:         sender,
			recipient:      to,
			mu:             sync.Mutex{},
			isAsyncMessage: false,
		}
		to.doReceive(receiveContext)
	}
}

// Unhandled is used to handle unhandled messages instead of throwing error
func (c *receiveContext) Unhandled() {
	c.mu.Lock()
	me := c.recipient
	c.mu.Unlock()
	me.handleError(c, ErrUnhandled)
}

// RemoteReSpawn restarts an actor on a remote node.
func (c *receiveContext) RemoteReSpawn(host string, port int, name string) {
	c.mu.Lock()
	recipient := c.recipient
	ctx := context.WithoutCancel(c.ctx)
	c.mu.Unlock()
	if err := recipient.RemoteReSpawn(ctx, host, port, name); err != nil {
		c.Err(err)
	}
}

// PipeTo processes a long-running task and pipes the result to the provided actor.
// The successful result of the task will be put onto the provided actor mailbox.
// This is useful when interacting with external services.
// It’s common that you would like to use the value of the response in the actor when the long-running task is completed
func (c *receiveContext) PipeTo(to PID, task future.Task) {
	c.mu.Lock()
	recipient := c.recipient
	ctx := context.WithoutCancel(c.ctx)
	c.mu.Unlock()
	if err := recipient.PipeTo(ctx, to, task); err != nil {
		c.Err(err)
	}
}
