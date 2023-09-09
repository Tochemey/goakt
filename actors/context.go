package actors

import (
	"context"
	"sync"

	addresspb "github.com/tochemey/goakt/pb/address/v1"
	"google.golang.org/protobuf/proto"
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
func (m *receiveContext) Self() PID {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.recipient
}

// Response sets the message response
func (m *receiveContext) Response(resp proto.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	defer close(m.response)
	// only set a response when the message is sync message
	if !m.isAsyncMessage {
		m.response <- resp
	}
}

// Context represents the context attached to the message
func (m *receiveContext) Context() context.Context {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ctx
}

// Sender of the message
func (m *receiveContext) Sender() PID {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sender
}

// RemoteSender defines the remote sender of the message if it is a remote message
// This is set to RemoteNoSender when the message is not a remote message
func (m *receiveContext) RemoteSender() *addresspb.Address {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.remoteSender
}

// Message is the actual message sent
func (m *receiveContext) Message() proto.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.message
}

// BecomeStacked sets a new behavior to the actor.
// The current message in process during the transition will still be processed with the current
// behavior before the transition. However, subsequent messages will be processed with the new behavior.
// One needs to call UnBecomeStacked to go the previous the actor's behavior.
// which is the default behavior.
func (m *receiveContext) BecomeStacked(behavior Behavior) {
	m.mu.Lock()
	m.recipient.setBehaviorStacked(behavior)
	m.mu.Unlock()
}

// UnBecomeStacked sets the actor behavior to the previous behavior before BecomeStacked was called
func (m *receiveContext) UnBecomeStacked() {
	m.mu.Lock()
	m.recipient.unsetBehaviorStacked()
	m.mu.Unlock()
}

// UnBecome reset the actor behavior to the default one
func (m *receiveContext) UnBecome() {
	m.mu.Lock()
	m.recipient.resetBehavior()
	m.mu.Unlock()
}

// Become switch the current behavior of the actor to a new behavior
func (m *receiveContext) Become(behavior Behavior) {
	m.mu.Lock()
	m.recipient.setBehavior(behavior)
	m.mu.Unlock()
}

// Stash enables an actor to temporarily buffer all or some messages that cannot or should not be handled using the actor’s current behavior
func (m *receiveContext) Stash() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.recipient.stash(m); err != nil {
		panic(err)
	}
}

// Unstash unstashes the oldest message in the stash and prepends to the mailbox
func (m *receiveContext) Unstash() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.recipient.unstash(); err != nil {
		panic(err)
	}
}

// UnstashAll unstashes all messages from the stash buffer  and prepends in the mailbox
// it keeps the messages in the same order as received, unstashing older messages before newer
func (m *receiveContext) UnstashAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.recipient.unstashAll(); err != nil {
		panic(err)
	}
}
