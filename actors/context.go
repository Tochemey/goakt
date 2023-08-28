package actors

import (
	"context"
	"sync"

	pb "github.com/tochemey/goakt/pb/v1"
	"google.golang.org/protobuf/proto"
)

// ReceiveContext is the context that is used by the actor to receive public
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
	RemoteSender() *pb.Address
}

type receiveContext struct {
	ctx            context.Context
	message        proto.Message
	sender         PID
	remoteSender   *pb.Address
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
func (m *receiveContext) RemoteSender() *pb.Address {
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
