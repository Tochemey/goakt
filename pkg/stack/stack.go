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

package stack

import "sync"

// Stack is a last-in-first-out data structure
type Stack[T any] struct {
	mutex sync.Mutex
	items []T
}

// New creates a new stack
func New[T any]() *Stack[T] {
	return &Stack[T]{
		mutex: sync.Mutex{},
		items: make([]T, 0),
	}
}

// Peek helps view the top item on the stack
func (s *Stack[T]) Peek() (item T, ok bool) {
	// acquire the lock
	s.mutex.Lock()
	// release the lock
	defer s.mutex.Unlock()
	if length := len(s.items); length > 0 {
		ok = true
		item = s.items[length-1]
	}
	return
}

// Pop removes and return top element of stack. Return false if stack is empty.
func (s *Stack[T]) Pop() (item T, ok bool) {
	// acquire the lock
	s.mutex.Lock()
	// release the lock
	defer s.mutex.Unlock()
	if length := len(s.items); length > 0 {
		// get the index of the top most element.
		length--
		ok = true
		// index into the slice and obtain the element.
		item = s.items[length]
		// remove it from the stack by slicing it off.
		s.items = s.items[:length]
	}
	return
}

// Push a new value onto the stack
func (s *Stack[T]) Push(item T) {
	// acquire the lock
	s.mutex.Lock()
	// release the lock
	defer s.mutex.Unlock()
	s.items = append(s.items, item)
}

// Len returns the length of the stack.
func (s *Stack[T]) Len() int {
	// acquire the lock
	s.mutex.Lock()
	// release the lock
	defer s.mutex.Unlock()
	return len(s.items)
}

// IsEmpty checks if stack is empty
func (s *Stack[T]) IsEmpty() bool {
	return s.Len() == 0
}

// Clear empty the stack
func (s *Stack[T]) Clear() {
	// acquire the lock
	s.mutex.Lock()
	// release the lock
	defer s.mutex.Unlock()
	if len(s.items) == 0 {
		return
	}

	s.items = []T{}
}
