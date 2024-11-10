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
	"testing"

	"github.com/stretchr/testify/assert"
)

// nolint
func TestBehaviorStack(t *testing.T) {
	stack := newBehaviorStack()
	assert.NotNil(t, stack)
	assert.True(t, stack.IsEmpty())

	// push a behavior onto the stack
	behavior := func(ctx *ReceiveContext) {}
	stack.Push(behavior)
	stack.Push(behavior)

	assert.False(t, stack.IsEmpty())
	assert.EqualValues(t, 2, stack.Len())

	peek := stack.Peek()
	assert.NotNil(t, peek)
	assert.EqualValues(t, 2, stack.Len())

	pop := stack.Pop()
	assert.NotNil(t, pop)
	assert.EqualValues(t, 1, stack.Len())

	stack.Reset()
	assert.True(t, stack.IsEmpty())
}
