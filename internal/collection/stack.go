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
	"sync/atomic"
	"unsafe"
)

// Stack implements lock-free freelist based stack.
// code has been lifted as it is from https://github.com/golang-design/lockfree
type Stack struct {
	top unsafe.Pointer
	len uint64
}

// NewStack creates a new lock-free queue.
func NewStack() *Stack {
	return &Stack{}
}

// Pop pops value from the top of the stack.
func (s *Stack) Pop() interface{} {
	var top, next unsafe.Pointer
	var item *directItem
	for {
		top = atomic.LoadPointer(&s.top)
		if top == nil {
			return nil
		}
		item = (*directItem)(top)
		next = atomic.LoadPointer(&item.next)
		if atomic.CompareAndSwapPointer(&s.top, top, next) {
			atomic.AddUint64(&s.len, ^uint64(0))
			return item.v
		}
	}
}

// Push pushes a value on top of the stack.
func (s *Stack) Push(v interface{}) {
	item := directItem{v: v}
	var top unsafe.Pointer
	for {
		top = atomic.LoadPointer(&s.top)
		item.next = top
		if atomic.CompareAndSwapPointer(&s.top, top, unsafe.Pointer(&item)) {
			atomic.AddUint64(&s.len, 1)
			return
		}
	}
}

// Peek helps view the top item on the stack
func (s *Stack) Peek() interface{} {
	top := atomic.LoadPointer(&s.top)
	if top == nil {
		return nil
	}
	item := (*directItem)(top)
	return item.v
}

// Length returns the length of the Stack.
func (s *Stack) Length() uint64 {
	return atomic.LoadUint64(&s.len)
}
