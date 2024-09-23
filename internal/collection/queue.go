/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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

package collection

import (
	"sync"
	"sync/atomic"
	"unsafe"
)

// Queue defines a lock-free Queue
// code has been lifted as it is from https://github.com/golang-design/lockfree
type Queue struct {
	head unsafe.Pointer
	tail unsafe.Pointer
	len  uint64
	pool sync.Pool
}

// NewQueue creates a new lock-free queue.
func NewQueue() *Queue {
	head := item{next: nil, v: nil} // allocate a free item
	return &Queue{
		tail: unsafe.Pointer(&head), // both head and tail points
		head: unsafe.Pointer(&head), // to the free item
		pool: sync.Pool{
			New: func() interface{} {
				return &item{}
			},
		},
	}
}

// Enqueue puts the given value v at the tail of the queue.
func (q *Queue) Enqueue(v interface{}) {
	i := q.pool.Get().(*item)
	i.next = nil
	i.v = v

	var last, lastnext *item
	for {
		last = loaditem(&q.tail)
		lastnext = loaditem(&last.next)
		if loaditem(&q.tail) == last { // are tail and next consistent?
			if lastnext == nil { // was tail pointing to the last node?
				if casitem(&last.next, lastnext, i) { // try to link item at the end of linked list
					casitem(&q.tail, last, i) // enqueue is done. try swing tail to the inserted node
					atomic.AddUint64(&q.len, 1)
					return
				}
			} else { // tail was not pointing to the last node
				casitem(&q.tail, last, lastnext) // try swing tail to the next node
			}
		}
	}
}

// Dequeue removes and returns the value at the head of the queue.
// It returns nil if the queue is empty.
func (q *Queue) Dequeue() interface{} {
	var first, last, firstnext *item
	for {
		first = loaditem(&q.head)
		last = loaditem(&q.tail)
		firstnext = loaditem(&first.next)
		if first == loaditem(&q.head) { // are head, tail and next consistent?
			if first == last { // is queue empty?
				if firstnext == nil { // queue is empty, couldn't dequeue
					return nil
				}
				casitem(&q.tail, last, firstnext) // tail is falling behind, try to advance it
			} else { // read value before cas, otherwise another dequeue might free the next node
				v := firstnext.v
				if casitem(&q.head, first, firstnext) { // try to swing head to the next node
					atomic.AddUint64(&q.len, ^uint64(0))
					q.pool.Put(first)
					return v // queue was not empty and dequeue finished.
				}
			}
		}
	}
}

// Length returns the length of the queue.
func (q *Queue) Length() uint64 {
	return atomic.LoadUint64(&q.len)
}

type item struct {
	next unsafe.Pointer
	v    interface{}
}

func loaditem(p *unsafe.Pointer) *item {
	return (*item)(atomic.LoadPointer(p))
}
func casitem(p *unsafe.Pointer, old, new *item) bool {
	return atomic.CompareAndSwapPointer(p, unsafe.Pointer(old), unsafe.Pointer(new))
}
