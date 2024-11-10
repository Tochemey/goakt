/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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
)

// BoundedMailbox defines a bounded mailbox using ring buffer queue
// This mailbox is thread-safe
type BoundedMailbox struct {
	buffer     []*ReceiveContext
	head, tail int
	len, cap   int
	full       bool
	lock       *sync.Mutex
}

// enforce compilation error
var _ Mailbox = (*BoundedMailbox)(nil)

// NewBoundedMailbox creates a new instance BoundedMailbox
func NewBoundedMailbox(cap int) *BoundedMailbox {
	return &BoundedMailbox{
		buffer: make([]*ReceiveContext, cap),
		head:   0,
		tail:   0,
		len:    0,
		cap:    cap,
		lock:   new(sync.Mutex),
	}
}

// Enqueue places the given value in the mailbox
// This will return an error when the mailbox is full
func (mailbox *BoundedMailbox) Enqueue(msg *ReceiveContext) error {
	if mailbox.isFull() {
		return ErrFullMailbox
	}

	mailbox.lock.Lock()
	mailbox.buffer[mailbox.tail] = msg
	mailbox.tail = (mailbox.tail + 1) % mailbox.cap
	mailbox.len++
	mailbox.full = mailbox.head == mailbox.tail
	mailbox.lock.Unlock()
	return nil
}

// Dequeue takes the mail from the mailbox
// It returns nil when the mailbox is empty
func (mailbox *BoundedMailbox) Dequeue() (msg *ReceiveContext) {
	if mailbox.IsEmpty() {
		return nil
	}

	mailbox.lock.Lock()
	item := mailbox.buffer[mailbox.head]
	mailbox.full = false
	mailbox.head = (mailbox.head + 1) % mailbox.cap
	mailbox.len--
	mailbox.lock.Unlock()
	return item
}

// IsEmpty returns true when the mailbox is empty
func (mailbox *BoundedMailbox) IsEmpty() bool {
	mailbox.lock.Lock()
	empty := mailbox.head == mailbox.tail && !mailbox.full
	mailbox.lock.Unlock()
	return empty
}

// Len returns queue length
func (mailbox *BoundedMailbox) Len() int64 {
	mailbox.lock.Lock()
	length := mailbox.len
	mailbox.lock.Unlock()
	return int64(length)
}

func (mailbox *BoundedMailbox) isFull() bool {
	mailbox.lock.Lock()
	full := mailbox.full
	mailbox.lock.Unlock()
	return full
}
