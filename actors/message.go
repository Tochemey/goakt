package actors

import (
	"context"
	"sync"

	"google.golang.org/protobuf/proto"
)

type Unit struct{}

// Message represents the message sent
// to an actor. The message is immutable
type Message interface {
	// Context returns the context attached to the message
	Context() context.Context
	// Sender of the message
	Sender() *PID
	// Payload is the actual message sent
	Payload() proto.Message
	// Response is the response to the message
	// This help handle request-response paradigm
	Response() proto.Message
	// SetResponse sets the message response
	SetResponse(resp proto.Message)
}

// MessageOption defines the message option
type MessageOption = func(message *message)

type message struct {
	ctx      context.Context
	content  proto.Message
	sender   *PID
	response proto.Message
	mu       sync.Mutex
}

// Response is the response to the message
// This help handle request-response paradigm
func (m *message) Response() proto.Message {
	m.mu.Lock()
	resp := m.response
	m.mu.Unlock()
	return resp
}

// SetResponse sets the message response
func (m *message) SetResponse(resp proto.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.response = resp
}

// Context represents the context attached to the message
func (m *message) Context() context.Context {
	m.mu.Lock()
	ctx := m.ctx
	m.mu.Unlock()
	return ctx
}

// Sender of the message
func (m *message) Sender() *PID {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sender
}

// Payload is the actual message sent
func (m *message) Payload() proto.Message {
	m.mu.Lock()
	content := m.content
	m.mu.Unlock()
	return content
}

// NewMessage creates a new message to send
func NewMessage(ctx context.Context, content proto.Message, opts ...MessageOption) Message {
	// create an instance of message
	m := &message{
		ctx:      ctx,
		content:  content,
		sender:   NoSender,
		response: nil,
	}
	// set the custom options to override the default values
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// WithSender sets the message sender
func WithSender(sender *PID) MessageOption {
	return func(message *message) {
		message.sender = sender
	}
}
