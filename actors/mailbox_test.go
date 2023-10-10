/*
 * MIT License
 *
 * Copyright (c) 2022-2023 Tochemey
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

func TestMailbox(t *testing.T) {
	t.Run("With happy path Push", func(t *testing.T) {
		mailbox := newReceiveContextBuffer(10)
		assert.True(t, mailbox.IsEmpty())
		err := mailbox.Push(new(receiveContext))
		assert.NoError(t, err)
		assert.EqualValues(t, 1, mailbox.Size())
		assert.EqualValues(t, 10, mailbox.Capacity())
	})
	t.Run("With Push when buffer is full", func(t *testing.T) {
		mailbox := newReceiveContextBuffer(10)
		assert.True(t, mailbox.IsEmpty())
		for i := 0; i < 10; i++ {
			err := mailbox.Push(new(receiveContext))
			assert.NoError(t, err)
		}
		assert.EqualValues(t, 10, mailbox.Size())
		// push another message to the buffer
		err := mailbox.Push(new(receiveContext))
		assert.Error(t, err)
		assert.EqualError(t, err, ErrFullMailbox.Error())
		assert.False(t, mailbox.IsEmpty())
		assert.True(t, mailbox.IsFull())
	})
	t.Run("With happy path Pop", func(t *testing.T) {
		mailbox := newReceiveContextBuffer(10)
		assert.True(t, mailbox.IsEmpty())
		assert.NoError(t, mailbox.Push(new(receiveContext)))
		assert.NoError(t, mailbox.Push(new(receiveContext)))
		assert.NoError(t, mailbox.Push(new(receiveContext)))
		assert.NoError(t, mailbox.Push(new(receiveContext)))

		popped, err := mailbox.Pop()
		assert.NoError(t, err)
		assert.NotNil(t, popped)
		assert.EqualValues(t, 3, mailbox.Size())
	})

	t.Run("With Clone", func(t *testing.T) {
		mailbox := newReceiveContextBuffer(10)
		assert.True(t, mailbox.IsEmpty())
		assert.NoError(t, mailbox.Push(new(receiveContext)))
		assert.NoError(t, mailbox.Push(new(receiveContext)))
		assert.NoError(t, mailbox.Push(new(receiveContext)))
		assert.NoError(t, mailbox.Push(new(receiveContext)))

		popped, err := mailbox.Pop()
		assert.NoError(t, err)
		assert.NotNil(t, popped)
		assert.EqualValues(t, 3, mailbox.Size())

		cloned := mailbox.Clone()
		assert.True(t, cloned.IsEmpty())
		assert.False(t, cloned.IsFull())
	})
	t.Run("With Pop when buffer is empty", func(t *testing.T) {
		mailbox := newReceiveContextBuffer(10)
		assert.True(t, mailbox.IsEmpty())

		popped, err := mailbox.Pop()
		assert.Error(t, err)
		assert.EqualError(t, err, ErrEmptyMailbox.Error())
		assert.Nil(t, popped)
	})
	t.Run("With Reset", func(t *testing.T) {
		mailbox := newReceiveContextBuffer(10)
		assert.True(t, mailbox.IsEmpty())
		assert.NoError(t, mailbox.Push(new(receiveContext)))
		assert.NoError(t, mailbox.Push(new(receiveContext)))
		assert.NoError(t, mailbox.Push(new(receiveContext)))
		assert.NoError(t, mailbox.Push(new(receiveContext)))

		popped, err := mailbox.Pop()
		assert.NoError(t, err)
		assert.NotNil(t, popped)
		assert.EqualValues(t, 3, mailbox.Size())
		assert.False(t, mailbox.IsEmpty())

		mailbox.Reset()
		assert.True(t, mailbox.IsEmpty())
		assert.False(t, mailbox.IsFull())
	})
}
