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
	"github.com/stretchr/testify/require"
)

func TestBoundedMailbox(t *testing.T) {
	mailbox := NewBoundedMailbox(20)
	for i := 0; i < 20; i++ {
		require.NoError(t, mailbox.Enqueue(&ReceiveContext{}))
	}
	assert.False(t, mailbox.IsEmpty())
	err := mailbox.Enqueue(&ReceiveContext{})
	require.Error(t, err)
	assert.EqualError(t, err, ErrFullMailbox.Error())
	dequeue := mailbox.Dequeue()
	require.NotNil(t, dequeue)
	assert.EqualValues(t, 19, mailbox.Len())
	counter := 19
	for counter > 0 {
		mailbox.Dequeue()
		counter--
	}
	assert.True(t, mailbox.IsEmpty())
}
