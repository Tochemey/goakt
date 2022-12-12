package actors

import (
	"context"
	"sync"

	"google.golang.org/protobuf/proto"
)

// MessageContext represents the message sent
// to an actor. The message is immutable
type MessageContext interface {
	// Context returns the context attached to the message
	Context() context.Context
	// Sender of the message
	Sender() PID
	// Message is the actual message sent
	Message() proto.Message
	// Response is the response to the message
	// This help handle request-response paradigm
	Response() proto.Message
	// Err returns the error while processing the message
	Err() error
	// Self represents the actor receiving the message.
	// This is used to reply to the Sender
	Self() PID
	// WithSelf sets the actor receiving the message.
	WithSelf(pid PID)
	// WithResponse sets the message response
	WithResponse(resp proto.Message)
	// WithErr is used to set any error encountered during the message processing
	WithErr(err error)
	// WithSender set the sender of the message
	WithSender(sender PID)
}

type messageContext struct {
	ctx      context.Context
	content  proto.Message
	sender   PID
	response proto.Message
	err      error
	self     PID
	mu       sync.Mutex
}

// NewMessageContext creates a new message context
func NewMessageContext(ctx context.Context, content proto.Message) MessageContext {
	// create an instance of message
	m := &messageContext{
		ctx:      ctx,
		content:  content,
		sender:   NoSender,
		response: nil,
		err:      nil,
		mu:       sync.Mutex{},
	}

	return m
}

// Self returns the receiver PID of the message
func (m *messageContext) Self() PID {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.self
}

// Err returns the processing error message
func (m *messageContext) Err() error {
	m.mu.Lock()
	err := m.err
	m.mu.Unlock()
	return err
}

// WithErr sets the error response during processing
func (m *messageContext) WithErr(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

// Response is the response to the message
// This help handle request-response paradigm
func (m *messageContext) Response() proto.Message {
	m.mu.Lock()
	resp := m.response
	m.mu.Unlock()
	return resp
}

// WithResponse sets the message response
func (m *messageContext) WithResponse(resp proto.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.response = resp
}

// Context represents the context attached to the message
func (m *messageContext) Context() context.Context {
	m.mu.Lock()
	ctx := m.ctx
	m.mu.Unlock()
	return ctx
}

// Sender of the message
func (m *messageContext) Sender() PID {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sender
}

// Message is the actual message sent
func (m *messageContext) Message() proto.Message {
	m.mu.Lock()
	content := m.content
	m.mu.Unlock()
	return content
}

// WithSelf set the self of the message context
func (m *messageContext) WithSelf(pid PID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.self = pid
}

// WithSender sets the message sender
func (m *messageContext) WithSender(sender PID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sender = sender
}
