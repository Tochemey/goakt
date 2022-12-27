package stream

import (
	"context"
	"sync"

	"go.uber.org/atomic"
)

// Message is the stream unit transfer.
// Once the message is sent it will be either acknowledged or not.
// Message processing follow a bit the TCP processing mechanism when once is message is sent, an Accept/Reject need to be
// received from the consumer to allow the next message to be sent.
type Message struct {
	// ID is the message unique identifier
	id string
	// the message payload
	payload []byte

	// states whether the message has been accepted
	// this is set to TRUE when an acknowledgement is received.
	accepted *atomic.Bool
	// states that the message has not been accepted.
	// A accepted message is a message that has been received and processed
	rejected *atomic.Bool

	// this two channels are closed whenever respectively a successful delivery receipt or non-delivery receipt is received
	acceptChan chan struct{}
	rejectChan chan struct{}

	ctx context.Context

	mu sync.Mutex
}

// NewMessage creates an instance of Message given the ID and the message content
func NewMessage(id string, payload []byte) *Message {
	return &Message{
		id:         id,
		payload:    payload,
		accepted:   atomic.NewBool(false),
		rejected:   atomic.NewBool(false),
		acceptChan: make(chan struct{}, 1),
		rejectChan: make(chan struct{}, 1),
		mu:         sync.Mutex{},
	}
}

// ID returns the message id
func (m *Message) ID() string {
	// acquire the lock
	m.mu.Lock()
	// release lock once the function is done
	defer m.mu.Unlock()
	return m.id
}

// Payload returns the message content
func (m *Message) Payload() []byte {
	// acquire the lock
	m.mu.Lock()
	// release lock once the function is done
	defer m.mu.Unlock()
	return m.payload
}

// Accept states that the message has been delivered and processed successfully.
//
// A given message can only either be accepted or rejected. This function will return true
// when an accepted is received and false on the contrary.
// This will no-op when an already accepted is received
func (m *Message) Accept() bool {
	// acquire the lock
	m.mu.Lock()
	// release lock once the function is done
	defer m.mu.Unlock()

	// check whether for this message there is already a rejection
	// one cannot accept and reject at the same time
	if m.rejected.Load() {
		// damn we cannot set an accepted receipt
		return false
	}

	// check whether there is already an accepted receipt on the message
	if m.accepted.Load() {
		// no-op
		return true
	}

	// here no receipt has not yet been set
	m.accepted.Store(true)
	// close the accepted chan
	close(m.acceptChan)
	return true
}

// Reject states that the message has not been successfully delivered.
//
// An unsuccessful delivered message can occur when there is an error during processing.
// This function will return true when a rejected is received and false on the contrary.
// This will no-op when an already accepted is received
func (m *Message) Reject() bool {
	// acquire the lock
	m.mu.Lock()
	// release lock once the function is done
	defer m.mu.Unlock()

	// check whether for this message there is already an acceptance
	// one cannot accept and reject at the same time
	if m.accepted.Load() {
		// damn we cannot set an accepted receipt
		return false
	}

	// check whether there is already an accepted receipt on the message
	if m.rejected.Load() {
		// no-op
		return true
	}

	// here no receipt has not yet been set
	m.rejected.Store(true)
	// close the rejected chan
	close(m.rejectChan)
	return true
}

// Accepted is a getter to return the accepted chan
func (m *Message) Accepted() <-chan struct{} {
	return m.acceptChan
}

// Rejected is a getter to return the rejected chan
func (m *Message) Rejected() <-chan struct{} {
	return m.rejectChan
}

// Context returns the provided context
func (m *Message) Context() context.Context {
	m.mu.Lock()
	ctx := m.ctx
	m.mu.Unlock()
	return ctx
}

// SetContext set the message context
func (m *Message) SetContext(ctx context.Context) {
	m.mu.Lock()
	m.ctx = ctx
	m.mu.Unlock()
}

// Clone creates a copy of the original message
func (m *Message) Clone() *Message {
	return NewMessage(m.id, m.payload)
}
