package actors

import (
	"github.com/pkg/errors"
	"go.uber.org/atomic"
)

var (
	ErrBufferEmpty = errors.New("buffer is empty")
	ErrBufferFull  = errors.New("buffer is full")
)

// StashBuffer s a buffer and stashed messages will be kept in memory
// until they are unstashed (or the actor is stopped and garbage collected)
type StashBuffer struct {
	// specifies the number of messages to stash
	capacity int
	counter  *atomic.Uint64
	buffer   chan ReceiveContext
}

// NewStashBuffer creates a stash buffer with a fixed capacity
func NewStashBuffer(capacity int) *StashBuffer {
	return &StashBuffer{
		capacity: capacity,
		buffer:   make(chan ReceiveContext, capacity),
		counter:  atomic.NewUint64(0),
	}
}

// Push pushes a message into the buffer. This returns an error
// when the buffer is full
func (x *StashBuffer) Push(msg ReceiveContext) error {
	// check whether the buffer is full
	if int(x.Size()) < x.capacity {
		x.buffer <- msg
		x.counter.Inc()
		return nil
	}
	return ErrBufferFull
}

// Pop fetches a message from the buffer
func (x *StashBuffer) Pop() (msg ReceiveContext, err error) {
	// check whether the buffer is empty
	if x.IsEmpty() {
		return nil, ErrBufferEmpty
	}
	// grab the message]
	msg = <-x.buffer
	x.counter.Dec()
	return
}

// IsEmpty returns true when the buffer is empty
func (x *StashBuffer) IsEmpty() bool {
	return x.counter.Load() == 0
}

// Size returns the size of the buffer
func (x *StashBuffer) Size() uint64 {
	return x.counter.Load()
}
