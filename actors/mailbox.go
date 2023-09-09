package actors

import (
	"sync"

	"github.com/pkg/errors"
	"go.uber.org/atomic"
)

var (
	// ErrEmptyMailbox is returned when the mailbox is empty
	ErrEmptyMailbox = errors.New("mailbox is empty")
	// ErrFullMailbox is returned when the mailbox is full
	ErrFullMailbox = errors.New("mailbox is full")
)

// Mailbox defines the actor mailbox.
// Any implementation should be a thread-safe FIFO
type Mailbox interface {
	// Push pushes a message into the mailbox. This returns an error
	// when the box is full
	Push(msg ReceiveContext) error
	// Pop fetches a message from the mailbox
	Pop() (msg ReceiveContext, err error)
	// IsEmpty returns true when the mailbox is empty
	IsEmpty() bool
	// IsFull returns true when the mailbox is full
	IsFull() bool
	// Size returns the size of the buffer atomically
	Size() uint64
	// Reset resets the mailbox
	Reset()
	// Clone clones the current mailbox and returns a new Mailbox with reset settings
	Clone() Mailbox
	// Capacity returns the mailbox capacity atomically
	Capacity() uint64
}

// receiveContextBuffer is the actor default inbox
type receiveContextBuffer struct {
	// specifies the number of messages to stash
	capacity *atomic.Uint64
	counter  *atomic.Uint64
	buffer   chan ReceiveContext
	mu       sync.Mutex
}

// newReceiveContextBuffer creates a Mailbox with a fixed capacity
func newReceiveContextBuffer(capacity uint64) Mailbox {
	return &receiveContextBuffer{
		capacity: atomic.NewUint64(capacity),
		buffer:   make(chan ReceiveContext, capacity),
		counter:  atomic.NewUint64(0),
		mu:       sync.Mutex{},
	}
}

// enforce compilation error
var _ Mailbox = &receiveContextBuffer{}

// Push pushes a message into the mailbox. This returns an error
// when the box is full
func (x *receiveContextBuffer) Push(msg ReceiveContext) error {
	// check whether the buffer is full
	if x.Size() < x.capacity.Load() {
		x.mu.Lock()
		x.buffer <- msg
		x.mu.Unlock()
		x.counter.Inc()
		return nil
	}
	return ErrFullMailbox
}

// Pop fetches a message from the mailbox
func (x *receiveContextBuffer) Pop() (msg ReceiveContext, err error) {
	// check whether the buffer is empty
	if x.IsEmpty() {
		return nil, ErrEmptyMailbox
	}
	// grab the message
	x.mu.Lock()
	msg = <-x.buffer
	x.mu.Unlock()
	x.counter.Dec()
	return
}

// IsEmpty returns true when the buffer is empty
func (x *receiveContextBuffer) IsEmpty() bool {
	return x.counter.Load() == 0
}

// Size returns the size of the buffer
func (x *receiveContextBuffer) Size() uint64 {
	return x.counter.Load()
}

// Clone clones the current mailbox and returns a new Mailbox with reset settings
func (x *receiveContextBuffer) Clone() Mailbox {
	return &receiveContextBuffer{
		capacity: x.capacity,
		counter:  atomic.NewUint64(0),
		buffer:   make(chan ReceiveContext, x.capacity.Load()),
		mu:       sync.Mutex{},
	}
}

// Reset resets the mailbox
func (x *receiveContextBuffer) Reset() {
	x.counter.Store(0)
	x.mu.Lock()
	x.buffer = make(chan ReceiveContext, x.capacity.Load())
	x.mu.Unlock()
}

// IsFull returns true when the mailbox is full
func (x *receiveContextBuffer) IsFull() bool {
	return x.Size() >= x.capacity.Load()
}

// Capacity implements Mailbox.
func (x *receiveContextBuffer) Capacity() uint64 {
	return x.capacity.Load()
}
