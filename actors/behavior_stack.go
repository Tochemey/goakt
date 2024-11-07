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
	"sync/atomic"
	"unsafe"
)

// Behavior defines an actor behavior
type Behavior func(ctx *ReceiveContext)

type bnode struct {
	value Behavior
	next  unsafe.Pointer
}

// behaviorStack defines a stack of Behavior
type behaviorStack struct {
	top    unsafe.Pointer
	length uint64
}

// newBehaviorStack creates an instance of behaviorStack
func newBehaviorStack() *behaviorStack {
	return &behaviorStack{
		top:    nil,
		length: 0,
	}
}

// Len returns the length of the stack.
func (bs *behaviorStack) Len() int {
	return int(atomic.LoadUint64(&bs.length))
}

// Peek helps view the top item on the stack
func (bs *behaviorStack) Peek() Behavior {
	top := atomic.LoadPointer(&bs.top)
	if top == nil {
		return nil
	}
	item := (*bnode)(top)
	return item.value
}

// Pop removes and return top element of stack
func (bs *behaviorStack) Pop() Behavior {
	var top, next unsafe.Pointer
	var item *bnode
	for {
		top = atomic.LoadPointer(&bs.top)
		if top == nil {
			return nil
		}
		item = (*bnode)(top)
		next = atomic.LoadPointer(&item.next)
		if atomic.CompareAndSwapPointer(&bs.top, top, next) {
			atomic.AddUint64(&bs.length, ^uint64(0))
			return item.value
		}
	}
}

// Push a new value onto the stack
func (bs *behaviorStack) Push(behavior Behavior) {
	node := bnode{value: behavior}
	var top unsafe.Pointer
	for {
		top = atomic.LoadPointer(&bs.top)
		node.next = top
		if atomic.CompareAndSwapPointer(&bs.top, top, unsafe.Pointer(&node)) {
			atomic.AddUint64(&bs.length, 1)
			return
		}
	}
}

// IsEmpty checks if stack is empty
func (bs *behaviorStack) IsEmpty() bool {
	return bs.Len() == 0
}

// Reset empty the stack
func (bs *behaviorStack) Reset() {
	atomic.StorePointer(&bs.top, nil)
	atomic.StoreUint64(&bs.length, 0)
}
