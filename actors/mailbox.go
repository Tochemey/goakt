package actors

import (
	"github.com/pkg/errors"
	"go.uber.org/atomic"
)

var (
	// errEmptyMailbox is returned when the mailbox is empty
	errEmptyMailbox = errors.New("mailbox is empty")
	// errFullMailbox is returned when the mailbox is full
	errFullMailbox = errors.New("mailbox is full")
)

// mailbox is the actor mailbox
type mailbox struct {
	// specifies the number of messages to stash
	capacity int
	counter  *atomic.Uint64
	buffer   chan ReceiveContext
}

// newMailbox creates a mailbox with a fixed capacity
func newMailbox(capacity int) *mailbox {
	return &mailbox{
		capacity: capacity,
		buffer:   make(chan ReceiveContext, capacity),
		counter:  atomic.NewUint64(0),
	}
}

// Push pushes a message into the mailbox. This returns an error
// when the box is full
func (x *mailbox) Push(msg ReceiveContext) error {
	// check whether the buffer is full
	if int(x.Size()) < x.capacity {
		x.buffer <- msg
		x.counter.Inc()
		return nil
	}
	return errFullMailbox
}

// Pop fetches a message from the mailbox
func (x *mailbox) Pop() (msg ReceiveContext, err error) {
	// check whether the buffer is empty
	if x.IsEmpty() {
		return nil, errEmptyMailbox
	}
	// grab the message
	msg = <-x.buffer
	x.counter.Dec()
	return
}

// IsEmpty returns true when the buffer is empty
func (x *mailbox) IsEmpty() bool {
	return x.counter.Load() == 0
}

// Size returns the size of the buffer
func (x *mailbox) Size() uint64 {
	return x.counter.Load()
}
