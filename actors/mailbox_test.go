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
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMailbox(t *testing.T) {
	t.Run("With happy path Send->Next", func(t *testing.T) {
		mailbox := newMailbox()
		defer mailbox.Close()

		assert.NoError(t, mailbox.Send(new(receiveContext)))
		assert.NoError(t, mailbox.Send(new(receiveContext)))
		assert.NoError(t, mailbox.Send(new(receiveContext)))

		popped, err := mailbox.Next()
		assert.NoError(t, err)
		assert.NotNil(t, popped)

		popped, err = mailbox.Next()
		assert.NoError(t, err)
		assert.NotNil(t, popped)

		popped, err = mailbox.Next()
		assert.NoError(t, err)
		assert.NotNil(t, popped)
	})
	t.Run("With happy path Iterator", func(t *testing.T) {
		mailbox := newMailbox()
		defer mailbox.Close()

		assert.NoError(t, mailbox.Send(new(receiveContext)))
		assert.NoError(t, mailbox.Send(new(receiveContext)))
		assert.NoError(t, mailbox.Send(new(receiveContext)))

		go func() {
			counter := 0
			for msg := range mailbox.Iterator() {
				assert.NotNil(t, msg)
				counter++
			}
			assert.EqualValues(t, 3, counter)
		}()

		// wait for the mailbox to be drained
		time.Sleep(time.Second)
	})
	t.Run("With Send to closed mailbox", func(t *testing.T) {
		mailbox := newMailbox()

		assert.NoError(t, mailbox.Send(new(receiveContext)))

		mailbox.Close()

		assert.Eventually(t, func() bool {
			return mailbox.IsClosed()
		}, time.Second, 100*time.Millisecond)

		assert.Error(t, mailbox.Send(new(receiveContext)))
	})
	t.Run("With Next from closed mailbox", func(t *testing.T) {
		mailbox := newMailbox()

		assert.NoError(t, mailbox.Send(new(receiveContext)))

		mailbox.Close()

		assert.Eventually(t, func() bool {
			return mailbox.IsClosed()
		}, time.Second, 100*time.Millisecond)

		popped, err := mailbox.Next()
		assert.NoError(t, err)
		assert.NotNil(t, popped)

		// mailbox is empty
		popped, err = mailbox.Next()
		assert.Error(t, err)
		assert.Nil(t, popped)
	})
	t.Run("With Next from empty closed mailbox", func(t *testing.T) {
		mailbox := newMailbox()

		mailbox.Close()

		assert.Eventually(t, func() bool {
			return mailbox.IsClosed()
		}, time.Second, 100*time.Millisecond)

		popped, err := mailbox.Next()
		assert.Error(t, err)
		assert.Nil(t, popped)
	})
	t.Run("With Send to full mailbox", func(t *testing.T) {
		mailbox := newMailbox(withCapacity(1))

		assert.NoError(t, mailbox.Send(new(receiveContext)))
		assert.Error(t, mailbox.Send(new(receiveContext)))
	})
}
