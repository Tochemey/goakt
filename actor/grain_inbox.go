/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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

package actor

import (
	"sync/atomic"
	"unsafe"
)

type grainInboxNode struct {
	value *GrainRequest
	next  *grainInboxNode
}

type grainInbox struct {
	head, tail *grainInboxNode
	length     int64
}

func newGrainInxbox() *grainInbox {
	item := new(grainInboxNode)
	return &grainInbox{
		head:   item,
		tail:   item,
		length: 0,
	}
}

// Enqueue places the given value in the mailbox
func (m *grainInbox) Enqueue(value *GrainRequest) error {
	tnode := &grainInboxNode{
		value: value,
	}
	previousHead := (*node)(atomic.SwapPointer((*unsafe.Pointer)(unsafe.Pointer(&m.head)), unsafe.Pointer(tnode)))
	atomic.StorePointer((*unsafe.Pointer)(unsafe.Pointer(&previousHead.next)), unsafe.Pointer(tnode))
	atomic.AddInt64(&m.length, 1)
	return nil
}

// Dequeue takes the mail from the mailbox
// Returns nil if the mailbox is empty. Can be used in a single consumer (goroutine) only.
func (m *grainInbox) Dequeue() *GrainRequest {
	next := (*grainInboxNode)(atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(&m.tail.next))))
	if next == nil {
		return nil
	}

	m.tail = next
	value := next.value
	next.value = nil
	atomic.AddInt64(&m.length, -1)
	return value
}

// Len returns mailbox length
func (m *grainInbox) Len() int64 {
	return atomic.LoadInt64(&m.length)
}

// IsEmpty returns true when the mailbox is empty
func (m *grainInbox) IsEmpty() bool {
	return atomic.LoadInt64(&m.length) == 0
}
