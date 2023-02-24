package actors

import (
	"github.com/alphadose/zenq/v2"
	"go.uber.org/atomic"
)

// Mailbox represents the actor mailbox
// Any implementation should be lock-free FIFO
type Mailbox interface {
	// Start starts the mailbox
	Start()
	// Shutdown stops the mailbox
	Shutdown()
	// Post puts a receiveContext into the mailbox
	Post(messageContext ReceiveContext)
	// Read reads from the mailbox
	Read() ReceiveContext
	// Size returns the size of the Mailbox
	Size() uint64
	// Restart restarts the mailbox
	Restart()
}

// mailbox implements Mailbox
type mailbox struct {
	mailQueue    *zenq.ZenQ[ReceiveContext]
	writeCounter *atomic.Uint64
	readCounter  *atomic.Uint64
}

// enforces compilation error
var _ Mailbox = &mailbox{}

// NewMailbox creates an instance of Mailbox
func NewMailbox() Mailbox {
	// create the instance of mailbox and returns it
	return &mailbox{
		mailQueue:    zenq.New[ReceiveContext](4096),
		writeCounter: atomic.NewUint64(0),
		readCounter:  atomic.NewUint64(0),
	}
}

// Start starts the mailbox
func (m *mailbox) Start() {
	// pass
}

// Restart restarts the mailbox
func (m *mailbox) Restart() {
	m.mailQueue.Reset()
}

// Shutdown stops the mailbox
func (m *mailbox) Shutdown() {
	// close the underlying queue
	m.mailQueue.Close()
}

// Post puts a message into the mailbox
func (m *mailbox) Post(messageContext ReceiveContext) {
	// check whether the underlying queue is closed or not
	if !m.mailQueue.IsClosed() {
		// add the message to the mailbox
		m.mailQueue.Write(messageContext)
		// increment the writeCounter
		m.writeCounter.Inc()
	}
}

// Read reads from the underlying queue and returns the data
func (m *mailbox) Read() ReceiveContext {
	// check whether the underlying queue is closed or not
	if !m.mailQueue.IsClosed() {
		// fetch the data from the underlying queue
		if data, open := m.mailQueue.Read(); open {
			// increment the readCounter
			m.readCounter.Inc()
			// return the read data
			return data
		}
	}
	return nil
}

// Size returns the size of the mailbox
func (m *mailbox) Size() uint64 {
	// let us compute the size of the underlying
	size := m.writeCounter.Sub(m.readCounter.Load())
	// return the computed size
	return size
}
