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

package actor

import (
	"context"

	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/future"
	"github.com/tochemey/goakt/v2/goaktpb"
)

// ReceiveContext is the context that is used by the actor to receive messages
type ReceiveContext struct {
	ctx          context.Context
	message      proto.Message
	sender       *PID
	remoteSender *goaktpb.Address
	response     chan proto.Message
	recipient    *PID
	err          error
}

// newReceiveContext creates an instance of ReceiveContext
func newReceiveContext(ctx context.Context, from, to *PID, message proto.Message) *ReceiveContext {
	// create a message receiveContext
	return &ReceiveContext{
		ctx:       ctx,
		message:   message,
		sender:    from,
		response:  make(chan proto.Message, 1),
		recipient: to,
	}
}

// WithRemoteSender set the remote sender for a given context
func (c *ReceiveContext) WithRemoteSender(remoteSender *goaktpb.Address) *ReceiveContext {
	c.remoteSender = remoteSender
	return c
}

// Self returns the receiver PID of the message
func (c *ReceiveContext) Self() *PID {
	return c.recipient
}

// Err is used instead of panicking within a message handler.
func (c *ReceiveContext) Err(err error) {
	c.err = err
}

// Response sets the message response
func (c *ReceiveContext) Response(resp proto.Message) {
	c.response <- resp
	close(c.response)
}

// Context represents the context attached to the message
func (c *ReceiveContext) Context() context.Context {
	return c.ctx
}

// Sender of the message
func (c *ReceiveContext) Sender() *PID {
	return c.sender
}

// Message is the actual message sent
func (c *ReceiveContext) Message() proto.Message {
	return c.message
}

// BecomeStacked sets a new behavior to the actor.
// The current message in process during the transition will still be processed with the current
// behavior before the transition. However, subsequent messages will be processed with the new behavior.
// One needs to call UnBecomeStacked to go the previous the actor's behavior.
// which is the default behavior.
func (c *ReceiveContext) BecomeStacked(behavior Behavior) {
	c.recipient.setBehaviorStacked(behavior)
}

// UnBecomeStacked sets the actor behavior to the previous behavior before BecomeStacked was called
func (c *ReceiveContext) UnBecomeStacked() {
	c.recipient.unsetBehaviorStacked()
}

// UnBecome reset the actor behavior to the default one
func (c *ReceiveContext) UnBecome() {
	c.recipient.resetBehavior()
}

// Become switch the current behavior of the actor to a new behavior
func (c *ReceiveContext) Become(behavior Behavior) {
	c.recipient.setBehavior(behavior)
}

// Stash enables an actor to temporarily buffer all or some messages that cannot or should not be handled using the actor’s current behavior
func (c *ReceiveContext) Stash() {
	recipient := c.recipient
	if err := recipient.stash(c); err != nil {
		c.Err(err)
	}
}

// Unstash unstashes the oldest message in the stash and prepends to the mailbox
func (c *ReceiveContext) Unstash() {
	recipient := c.recipient
	if err := recipient.unstash(); err != nil {
		c.Err(err)
	}
}

// UnstashAll unstashes all messages from the stash buffer  and prepends in the mailbox
// it keeps the messages in the same order as received, unstashing older messages before newer
func (c *ReceiveContext) UnstashAll() {
	recipient := c.recipient
	if err := recipient.unstashAll(); err != nil {
		c.Err(err)
	}
}

// Tell sends an asynchronous message to another actor
// The location of the receiving actor is transparent to the caller
func (c *ReceiveContext) Tell(actorName string, message proto.Message) {
	recipient := c.recipient
	ctx := context.WithoutCancel(c.ctx)
	if err := recipient.Tell(ctx, actorName, message); err != nil {
		c.Err(err)
	}
}

// Forward method works similarly to the Tell() method except that the sender of a forwarded message is kept as the original sender.
// As a result, the actor receiving the forwarded messages knows who the actual sender of the message is.
// The message that is forwarded is the current message received by the received context.
// This operation does nothing when the receiving actor is not running
func (c *ReceiveContext) Forward(actorName string) {
	message := c.Message()
	sender := c.Sender()
	ctx := context.WithoutCancel(c.ctx)
	if err := sender.Tell(ctx, actorName, message); err != nil {
		c.Err(err)
	}
}

// Ask sends a synchronous message to another actor and expect a response. This method is good when interacting with a child actor.
// Ask has a timeout which can cause the sender to set the context error. When ask times out, the receiving actor does not know and may still process the message.
// It is recommended to set a good timeout to quickly receive response and try to avoid false positives.
// The location of the receiving actor is transparent to the caller
func (c *ReceiveContext) Ask(actorName string, message proto.Message) (response proto.Message) {
	recipient := c.recipient
	ctx := context.WithoutCancel(c.ctx)
	reply, err := recipient.Ask(ctx, actorName, message)
	if err != nil {
		c.Err(err)
	}
	return reply
}

// BatchTell sends an asynchronous bunch of messages to the given actor
// The messages will be processed one after the other in the order they are sent
// This is a design choice to follow the simple principle of one message at a time processing by actors.
// When BatchTell encounter a single message it will fall back to a Tell call.
func (c *ReceiveContext) BatchTell(actorName string, messages ...proto.Message) {
	recipient := c.recipient
	ctx := context.WithoutCancel(c.ctx)
	if err := recipient.BatchTell(ctx, actorName, messages...); err != nil {
		c.Err(err)
	}
}

// BatchAsk sends a synchronous bunch of messages to the given actor and expect responses in the same order as the messages.
// The messages will be processed one after the other in the order they are sent
// This is a design choice to follow the simple principle of one message at a time processing by actors.
func (c *ReceiveContext) BatchAsk(actorName string, messages ...proto.Message) (responses chan proto.Message) {
	recipient := c.recipient
	ctx := context.WithoutCancel(c.ctx)
	reply, err := recipient.BatchAsk(ctx, actorName, messages...)
	if err != nil {
		c.Err(err)
	}
	return reply
}

// Shutdown gracefully shuts down the given actor
// All current messages in the mailbox will be processed before the actor shutdown after a period of time
// that can be configured. All child actors will be gracefully shutdown.
func (c *ReceiveContext) Shutdown() {
	recipient := c.recipient
	ctx := context.WithoutCancel(c.ctx)
	if err := recipient.Shutdown(ctx); err != nil {
		c.Err(err)
	}
}

// Spawn creates a child actor or return error
func (c *ReceiveContext) Spawn(name string, actor Actor) *PID {
	recipient := c.recipient
	ctx := context.WithoutCancel(c.ctx)
	pid, err := recipient.SpawnChild(ctx, name, actor)
	if err != nil {
		c.Err(err)
	}
	return pid
}

// Children returns the list of all the children of the given actor
func (c *ReceiveContext) Children() []*PID {
	return c.recipient.Children()
}

// Child returns the named child actor if it is alive
func (c *ReceiveContext) Child(name string) *PID {
	recipient := c.recipient
	pid, err := recipient.Child(name)
	if err != nil {
		c.Err(err)
	}
	return pid
}

// Stop forces the child Actor under the given name to terminate after it finishes processing its current message.
// Nothing happens if child is already stopped. However, it returns an error when the child cannot be stopped.
func (c *ReceiveContext) Stop(child *PID) {
	recipient := c.recipient
	ctx := context.WithoutCancel(c.ctx)
	if err := recipient.Stop(ctx, child); err != nil {
		c.Err(err)
	}
}

// Unhandled is used to handle unhandled messages instead of throwing error
func (c *ReceiveContext) Unhandled() {
	me := c.recipient
	me.toDeadletterQueue(c, ErrUnhandled)
}

// RemoteReSpawn restarts an actor on a remote node.
func (c *ReceiveContext) RemoteReSpawn(host string, port int, name string) {
	recipient := c.recipient
	ctx := context.WithoutCancel(c.ctx)
	if err := recipient.RemoteReSpawn(ctx, host, port, name); err != nil {
		c.Err(err)
	}
}

// PipeTo processes a long-running task and pipes the result to the provided actor.
// The successful result of the task will be put onto the provided actor mailbox.
// This is useful when interacting with external services.
// It’s common that you would like to use the value of the response in the actor when the long-running task is completed
func (c *ReceiveContext) PipeTo(actorName string, task future.Task) {
	recipient := c.recipient
	ctx := context.WithoutCancel(c.ctx)
	if err := recipient.PipeTo(ctx, actorName, task); err != nil {
		c.Err(err)
	}
}

// getError returns any error during message processing
func (c *ReceiveContext) getError() error {
	return c.err
}
