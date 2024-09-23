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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStackPopEmpty(t *testing.T) {
	s := NewStack()
	require.NotNil(t, s.Pop())
	s.Push(1)
	s.Push(2)
	s.Push(3)
	assert.EqualValues(t, 3, s.Length())
}

func TestStackPushPop(t *testing.T) {
	s := NewStack()
	s.Push(4)
	s.Push(6)
	s.Push(8)
	assert.EqualValues(t, 3, s.Length())
	peek := s.Peek()
	require.NotNil(t, peek)
	assert.EqualValues(t, 3, s.Length())
	assert.EqualValues(t, 8, peek.(int))
	popped := s.Pop()
	require.NotNil(t, popped)
	assert.EqualValues(t, 2, s.Length())
	assert.EqualValues(t, 8, popped.(int))
	popped = s.Pop()
	require.NotNil(t, popped)
	assert.EqualValues(t, 1, s.Length())
	assert.EqualValues(t, 6, popped.(int))
}
