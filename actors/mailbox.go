/*
 * MIT License
 *
 * Copyright (c) 2022-2023 Tochemey
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package actors

import (
	"sync"
	"sync/atomic"

	"github.com/tochemey/goakt/pkg/slices"
	"github.com/tochemey/goakt/pkg/types"
)

type MailboxHandler func(ReceiveContext)

// Mailbox defines the actor mailbox.
// It is responsible for storing messages that are to be processed by the actor and is an
// actor itself. It is also the policy for how messages are enqueued and dequeued from the mailbox.
// Any implementation should be a thread-safe FIFO
type Mailbox interface {
	// Send pushes a message into the mailbox. This returns an error
	// when the mailbox is closed
	Send(msg ReceiveContext) error
	// Next fetches a message from the mailbox. This returns an error
	// when the mailbox is closed and empty
	Next() (msg ReceiveContext, err error)
	// Iterator returns a channel that can be used to iterate the mailbox
	Iterator() <-chan ReceiveContext
	// Close sends a signal to the mailbox to close. This will stop the mailbox
	// from receiving new messages and close the Iterator channel once the mailbox
	// is empty
	Close()
	// IsClosed returns true when the mailbox is closed and no messages can be received
	IsClosed() bool
}

// mailbox is the actor default inbox
type mailbox struct {
	sendC, receiveC chan ReceiveContext
	close           chan types.Unit
	buffer          *slices.ConcurrentSlice[ReceiveContext]
	isClosed        *atomic.Bool
	capacity        *atomic.Int32
	mu              sync.Mutex
}

// mailbox creates a Mailbox with an unbounded buffer
func newMailbox(opts ...mailboxOption) Mailbox {
	m := mailbox{
		capacity: &atomic.Int32{},
		isClosed: &atomic.Bool{},
		mu:       sync.Mutex{},
	}

	// set the default capacity to -1 (unbounded)
	m.capacity.Store(-1)
	m.isClosed.Store(false)

	// set the custom options to override the default values
	for _, opt := range opts {
		opt(&m)
	}

	// create the mailbox buffer channels
	if m.capacity.Load() > 0 {
		m.sendC = make(chan ReceiveContext, m.capacity.Load())
		m.receiveC = m.sendC
	} else {
		m.sendC = make(chan ReceiveContext, defaultMailboxBufferSize)
		m.receiveC = make(chan ReceiveContext, defaultMailboxBufferSize)
		m.close = make(chan types.Unit)
		m.buffer = slices.NewConcurrentSlice[ReceiveContext](0, defaultMailboxBufferSize)
		go m.process()
	}

	return &m
}

// enforce compilation error
var _ Mailbox = &mailbox{}

// Send pushes a message into the mailbox. This returns an error
// when the mailbox is closed
func (m *mailbox) Send(msg ReceiveContext) error {
	if m.isClosed.Load() {
		return ErrClosedMailbox
	}
	select {
	case m.sendC <- msg:
		return nil
	default:
		return ErrFullMailbox
	}
}

// Pop fetches a message from the mailbox. This returns an error
// when the mailbox is closed and empty
func (m *mailbox) Next() (msg ReceiveContext, err error) {
	msg, ok := <-m.receiveC
	if !ok {
		return nil, ErrClosedMailbox
	}
	return msg, nil
}

// Iterator returns a channel that can be used to iterate the mailbox
func (m *mailbox) Iterator() <-chan ReceiveContext {
	return m.receiveC
}

// Close closes the mailbox
func (m *mailbox) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.isClosed.Load() {
		return
	}

	if m.capacity.Load() > 0 {
		close(m.sendC)
		m.isClosed.Store(true)
	} else {
		m.close <- types.Unit{}
	}
}

// IsClosed returns true when the mailbox is closed and no messages can be received
func (m *mailbox) IsClosed() bool {
	return m.isClosed.Load()
}

// terminate terminates the unbounded channel's processing loop
// and makes sure all unprocessed elements are consumed if there is
// a pending receiver.
func (m *mailbox) terminate() {
	m.mu.Lock()
	defer m.mu.Unlock()

	close(m.sendC)
	for msg := range m.sendC {
		m.buffer.Append(msg)
	}
	for m.buffer.Len() > 0 {
		select {
		case m.receiveC <- m.buffer.Get(0).(ReceiveContext):
			m.buffer.Delete(0)
		default:
		}
	}
	close(m.receiveC)
	close(m.close)

	m.isClosed.Store(true)
}

// process is a processing loop that implements unbounded
// channel semantics.
func (m *mailbox) process() {
	for {
		// wait for a message to be sent to the mailbox
		select {
		case msg, ok := <-m.sendC:
			if !ok {
				panic("send-only channel sendC closed unexpectedly")
			}
			m.buffer.Append(msg)
		case <-m.close:
			m.terminate()
			return
		}

		// process the mailbox
		for m.buffer.Len() > 0 {
			select {
			case m.receiveC <- m.buffer.Get(0).(ReceiveContext):
				m.buffer.Delete(0)
			case msg, ok := <-m.sendC:
				if !ok {
					panic("send-only channel sendC closed unexpectedly")
				}
				m.buffer.Append(msg)
			case <-m.close:
				m.terminate()
				return
			}
		}

		// reset the buffer if it's too small
		if m.buffer.Cap() < defaultMailboxBufferSize/2 {
			m.buffer = slices.NewConcurrentSlice[ReceiveContext](0, defaultMailboxBufferSize)
		}
	}
}
