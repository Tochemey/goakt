package actors

import (
	"context"
	"sync"

	addresspb "github.com/tochemey/goakt/pb/address/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
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
	RemoteSender() *addresspb.Address
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
	// Ask sends a synchronous message to another actor and expect a response.
	Ask(to PID, message proto.Message) (response proto.Message)
	// RemoteTell sends a message to an actor remotely without expecting any reply
	RemoteTell(to *addresspb.Address, message proto.Message)
	// RemoteAsk is used to send a message to an actor remotely and expect a response
	// immediately. This executed within an actor can hinder performance because this is a blocking call.
	RemoteAsk(to *addresspb.Address, message proto.Message) (response *anypb.Any)
	// RemoteLookup look for an actor address on a remote node. If the actorSystem is nil then the lookup will be done
	// using the same actor system as the PID actor system
	RemoteLookup(host string, port int, name string) (addr *addresspb.Address)
}

type receiveContext struct {
	ctx            context.Context
	message        proto.Message
	sender         PID
	remoteSender   *addresspb.Address
	response       chan proto.Message
	recipient      PID
	mu             sync.Mutex
	isAsyncMessage bool
}

// force compilation error
var _ ReceiveContext = &receiveContext{}

// Self returns the receiver PID of the message
func (c *receiveContext) Self() PID {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.recipient
}

// Response sets the message response
func (c *receiveContext) Response(resp proto.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	defer close(c.response)
	// only set a response when the message is sync message
	if !c.isAsyncMessage {
		c.response <- resp
	}
}

// Context represents the context attached to the message
func (c *receiveContext) Context() context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ctx
}

// Sender of the message
func (c *receiveContext) Sender() PID {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sender
}

// RemoteSender defines the remote sender of the message if it is a remote message
// This is set to RemoteNoSender when the message is not a remote message
func (c *receiveContext) RemoteSender() *addresspb.Address {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.remoteSender
}

// Message is the actual message sent
func (c *receiveContext) Message() proto.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.message
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
	defer c.mu.Unlock()
	if err := c.recipient.stash(c); err != nil {
		panic(err)
	}
}

// Unstash unstashes the oldest message in the stash and prepends to the mailbox
func (c *receiveContext) Unstash() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.recipient.unstash(); err != nil {
		panic(err)
	}
}

// UnstashAll unstashes all messages from the stash buffer  and prepends in the mailbox
// it keeps the messages in the same order as received, unstashing older messages before newer
func (c *receiveContext) UnstashAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.recipient.unstashAll(); err != nil {
		panic(err)
	}
}

// Tell sends an asynchronous message to another PID
func (c *receiveContext) Tell(to PID, message proto.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// create a new context from the parent context
	ctx := context.WithoutCancel(c.ctx)
	// send the message to the recipient and let it crash
	if err := c.recipient.Tell(ctx, to, message); err != nil {
		panic(err)
	}
}

// Ask sends a synchronous message to another actor and expect a response.
func (c *receiveContext) Ask(to PID, message proto.Message) (response proto.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// create a new context from the parent context
	ctx := context.WithoutCancel(c.ctx)
	// send the message to the recipient and let it crash
	reply, err := c.recipient.Ask(ctx, to, message)
	if err != nil {
		panic(err)
	}
	return reply
}

// RemoteTell sends a message to an actor remotely without expecting any reply
func (c *receiveContext) RemoteTell(to *addresspb.Address, message proto.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// create a new context from the parent context
	ctx := context.WithoutCancel(c.ctx)
	// send the message to the recipient and let it crash
	if err := c.recipient.RemoteTell(ctx, to, message); err != nil {
		panic(err)
	}
}

// RemoteAsk is used to send a message to an actor remotely and expect a response
// immediately.
func (c *receiveContext) RemoteAsk(to *addresspb.Address, message proto.Message) (response *anypb.Any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// create a new context from the parent context
	ctx := context.WithoutCancel(c.ctx)
	// send the message to the recipient and let it crash
	reply, err := c.recipient.RemoteAsk(ctx, to, message)
	if err != nil {
		panic(err)
	}
	return reply
}

// RemoteLookup look for an actor address on a remote node. If the actorSystem is nil then the lookup will be done
// using the same actor system as the PID actor system
func (c *receiveContext) RemoteLookup(host string, port int, name string) (addr *addresspb.Address) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// create a new context from the parent context
	ctx := context.WithoutCancel(c.ctx)
	// perform the lookup and let it crash
	remoteAddr, err := c.recipient.RemoteLookup(ctx, host, port, name)
	if err != nil {
		panic(err)
	}
	return remoteAddr
}
