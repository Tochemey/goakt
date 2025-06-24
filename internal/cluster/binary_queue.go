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

package cluster

import (
	"sync/atomic"
	"unsafe"
)

// node represents a single element in the binaryQueue.
//
// Each node holds a value (a byte slice) and a pointer to the next node in the queue.
// The node struct is used internally to implement the lock-free linked list
// that underpins the binaryQueue.
type node struct {
	value []byte
	next  *node
}

// binaryQueue is a lock-free, multi-producer single-consumer (MPSC) FIFO queue
// for managing peer states as byte slices. It is optimized for high-throughput
// scenarios and is more efficient than Go channels in such cases.
//
// The queue supports concurrent enqueuing by multiple producers and
// single-threaded dequeuing by a consumer. It is not safe for multiple
// consumers to dequeue concurrently.
type binaryQueue struct {
	head, tail *node
	length     int64
}

// newBinaryQueue creates and returns a new, empty binaryQueue instance.
//
// The returned queue is ready for use and can be safely shared among
// multiple producers for enqueuing, with a single consumer for dequeuing.
func newBinaryQueue() *binaryQueue {
	dummy := new(node)
	return &binaryQueue{
		head:   dummy,
		tail:   dummy,
		length: 0,
	}
}

// enqueue adds the given byte slice value to the end of the queue.
//
// This method is safe to call concurrently from multiple goroutines.
// The value is appended to the queue and will be returned in FIFO order
// by subsequent calls to dequeue.
func (m *binaryQueue) enqueue(value []byte) {
	tnode := &node{
		value: value,
	}
	previousHead := (*node)(atomic.SwapPointer((*unsafe.Pointer)(unsafe.Pointer(&m.head)), unsafe.Pointer(tnode)))
	atomic.StorePointer((*unsafe.Pointer)(unsafe.Pointer(&previousHead.next)), unsafe.Pointer(tnode))
	atomic.AddInt64(&m.length, 1)
}

// dequeue removes and returns the next byte slice value from the front of the queue.
//
// If the queue is empty, dequeue returns nil. This method is intended to be
// called by a single consumer goroutine and is not safe for concurrent use
// by multiple consumers.
func (m *binaryQueue) dequeue() []byte {
	next := (*node)(atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(&m.tail.next))))
	if next == nil {
		return nil
	}

	m.tail = next
	value := next.value
	next.value = nil
	atomic.AddInt64(&m.length, -1)
	return value
}

// isEmpty returns true if the queue contains no elements, or false otherwise.
//
// This method is safe to call concurrently.
func (m *binaryQueue) isEmpty() bool {
	return atomic.LoadInt64(&m.length) == 0
}
