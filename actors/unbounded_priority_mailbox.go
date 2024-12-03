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
	hp "container/heap"
	"sync"
	"sync/atomic"

	"google.golang.org/protobuf/proto"
)

// PriorityFunc defines the priority function that will help
// determines the priority of two messages
type PriorityFunc func(msg1, msg2 proto.Message) bool

// heap implements the standard heap.Interface
type heap struct {
	items        []*ReceiveContext
	priorityFunc PriorityFunc
}

// enforce compilation error
var _ hp.Interface = (*heap)(nil)

func (h *heap) Len() int {
	return len(h.items)
}

func (h *heap) Push(x any) {
	h.items = append(h.items, x.(*ReceiveContext))
}

func (h *heap) Less(i, j int) bool {
	return h.priorityFunc(
		h.items[i].Message(),
		h.items[j].Message(),
	)
}

// Pop is called after the first element is swapped with the last
// so return the last element and resize the slice
func (h *heap) Pop() any {
	last := len(h.items) - 1
	element := h.items[last]
	h.items = h.items[:last]
	return element
}

func (h *heap) Swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
}

// UnboundedPriorityMailBox is a Priority Queue (FIFO)
// It implements a binary heap (using the standard library container/heap)
type UnboundedPriorityMailBox struct {
	heap   *heap
	lock   *sync.RWMutex
	length int64
}

// enforce compilation error
var _ Mailbox = (*UnboundedPriorityMailBox)(nil)

// NewUnboundedPriorityMailBox creates an instance of UnboundedPriorityMailBox
func NewUnboundedPriorityMailBox(priorityFunc PriorityFunc) *UnboundedPriorityMailBox {
	h := &heap{
		items:        make([]*ReceiveContext, 0),
		priorityFunc: priorityFunc,
	}

	hp.Init(h)

	return &UnboundedPriorityMailBox{
		heap: h,
		lock: &sync.RWMutex{},
	}
}

// Enqueue places the given value in the mailbox
// The given message must be a priority message otherwise an error will be returned
func (q *UnboundedPriorityMailBox) Enqueue(msg *ReceiveContext) error {
	q.lock.Lock()
	hp.Push(q.heap, msg)
	q.lock.Unlock()
	atomic.AddInt64(&q.length, 1)
	return nil
}

// Dequeue takes the mail from the mailbox based upon the priority function
// defined when initializing the mailbox
func (q *UnboundedPriorityMailBox) Dequeue() (msg *ReceiveContext) {
	// to avoid overflow
	if q.IsEmpty() {
		return nil
	}
	q.lock.Lock()
	msg = hp.Pop(q.heap).(*ReceiveContext)
	q.lock.Unlock()
	atomic.AddInt64(&q.length, -1)
	return msg
}

// IsEmpty returns true when the mailbox is empty
func (q *UnboundedPriorityMailBox) IsEmpty() bool {
	return q.Len() == 0
}

// Len returns mailbox length
func (q *UnboundedPriorityMailBox) Len() int64 {
	return atomic.LoadInt64(&q.length)
}
