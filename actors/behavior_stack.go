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

package actors

import "sync"

// Behavior defines an actor behavior
type Behavior func(ctx *ReceiveContext)

type bnode struct {
	value    Behavior
	previous *bnode
}

// behaviorStack defines a stack of Behavior
type behaviorStack struct {
	top    *bnode
	length int
	mutex  *sync.RWMutex
}

// newBehaviorStack creates an instance of behaviorStack
func newBehaviorStack() *behaviorStack {
	return &behaviorStack{
		top:    nil,
		length: 0,
		mutex:  &sync.RWMutex{},
	}
}

// Len returns the length of the stack.
func (bs *behaviorStack) Len() int {
	bs.mutex.RLock()
	length := bs.length
	bs.mutex.RUnlock()
	return length
}

// Peek helps view the top item on the stack
func (bs *behaviorStack) Peek() Behavior {
	bs.mutex.RLock()
	length := bs.length
	bs.mutex.RUnlock()
	if length == 0 {
		return nil
	}

	bs.mutex.RLock()
	value := bs.top.value
	bs.mutex.RUnlock()
	return value
}

// Pop removes and return top element of stack
func (bs *behaviorStack) Pop() Behavior {
	bs.mutex.RLock()
	length := bs.length
	bs.mutex.RUnlock()
	if length == 0 {
		return nil
	}

	bs.mutex.RLock()
	n := bs.top
	bs.top = n.previous
	bs.length--
	value := n.value
	bs.mutex.RUnlock()
	return value
}

// Push a new value onto the stack
func (bs *behaviorStack) Push(behavior Behavior) {
	bs.mutex.Lock()
	n := &bnode{behavior, bs.top}
	bs.top = n
	bs.length++
	bs.mutex.Unlock()
}

// IsEmpty checks if stack is empty
func (bs *behaviorStack) IsEmpty() bool {
	return bs.Len() == 0
}

// Reset empty the stack
func (bs *behaviorStack) Reset() {
	bs.mutex.Lock()
	bs.top = nil
	bs.length = 0
	bs.mutex.Unlock()
}
