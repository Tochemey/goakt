package actors

import (
	"context"
	"sync"

	"google.golang.org/protobuf/proto"
)

// ReceiveContext is the context that is used by the actor to receive public
type ReceiveContext interface {
	// Context returns the context attached to the message
	Context() context.Context
	// Sender of the message
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
	// Become switch the current behavior of the actor to a new behavior
	// The current message in process during the transition will still be processed with the current
	// behavior before the transition. However, subsequent public will be processed with the new behavior.
	// One needs to call UnBecome to reset the actor behavior to the default one which is the Actor.Receive method
	// which is the default behavior.
	Become(behavior Behavior)
	// UnBecome reset the actor behavior to the default one which is the
	// Actor.Receive method
	UnBecome()
	// BecomeStacked sets a new behavior to the actor.
	// The current message in process during the transition will still be processed with the current
	// behavior before the transition. However, subsequent public will be processed with the new behavior.
	// One needs to call UnBecomeStacked to go the previous the actor's behavior.
	// which is the default behavior.
	BecomeStacked(behavior Behavior)
	// UnBecomeStacked sets the actor behavior to the previous behavior before BecomeStacked was called
	UnBecomeStacked()
}

type receiveContext struct {
	ctx            context.Context
	message        proto.Message
	sender         PID
	response       chan proto.Message
	recipient      PID
	mu             sync.Mutex
	isAsyncMessage bool
}

// BecomeStacked sets a new behavior to the actor.
// The current message in process during the transition will still be processed with the current
// behavior before the transition. However, subsequent public will be processed with the new behavior.
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

// Self returns the receiver PID of the message
func (m *receiveContext) Self() PID {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.recipient
}

// Response sets the message response
func (m *receiveContext) Response(resp proto.Message) {
	m.mu.Lock()
	// only set a response when the message is sync message
	if !m.isAsyncMessage {
		m.response <- resp
		m.mu.Unlock()
		close(m.response)
		return
	}
	m.mu.Unlock()
}

// Context represents the context attached to the message
func (m *receiveContext) Context() context.Context {
	m.mu.Lock()
	ctx := m.ctx
	m.mu.Unlock()
	return ctx
}

// Sender of the message
func (m *receiveContext) Sender() PID {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sender
}

// Message is the actual message sent
func (m *receiveContext) Message() proto.Message {
	m.mu.Lock()
	content := m.message
	m.mu.Unlock()
	return content
}
