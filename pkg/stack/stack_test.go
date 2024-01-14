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

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStack(t *testing.T) {
	// create an instance of stack
	stack := New[int]()
	assert.NotNil(t, stack)
	assert.True(t, stack.IsEmpty())

	// assert push
	stack.Push(1)
	stack.Push(2)
	stack.Push(3)

	assert.False(t, stack.IsEmpty())
	assert.EqualValues(t, 3, stack.Len())

	// assert peek
	peek, ok := stack.Peek()
	assert.True(t, ok)
	assert.NotNil(t, peek)
	assert.EqualValues(t, 3, peek)
	assert.EqualValues(t, 3, stack.Len())

	// assert pop
	pop, ok := stack.Pop()
	assert.True(t, ok)
	assert.NotNil(t, pop)
	assert.EqualValues(t, 2, stack.Len())
	assert.EqualValues(t, 3, pop)
	peek, ok = stack.Peek()
	assert.True(t, ok)
	assert.NotNil(t, peek)
	assert.EqualValues(t, 2, peek)

	// assert clear
	stack.Clear()
	assert.True(t, stack.IsEmpty())
	stack.Clear()
}
