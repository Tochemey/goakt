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

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/test/data/testpb"
)

func TestUnboundedPriorityMailBox(t *testing.T) {
	t.Run("With highest priority mailbox", func(t *testing.T) {
		priorityFunc := func(msg1, msg2 proto.Message) bool {
			p1 := msg1.(*testpb.PriorityMessage)
			p2 := msg2.(*testpb.PriorityMessage)
			return p1.Priority > p2.Priority
		}

		// create an instance of the mailbox
		mailbox := NewUnboundedPriorityMailBox(priorityFunc)
		msg1 := &ReceiveContext{message: &testpb.PriorityMessage{Priority: 1}}
		msg2 := &ReceiveContext{message: &testpb.PriorityMessage{Priority: 5}}
		msg3 := &ReceiveContext{message: &testpb.PriorityMessage{Priority: 2}}

		require.NoError(t, mailbox.Enqueue(msg1))
		require.NoError(t, mailbox.Enqueue(msg2))
		require.NoError(t, mailbox.Enqueue(msg3))

		// let us start dequeuing the mailbox
		actual := mailbox.Dequeue()
		msg, ok := actual.Message().(*testpb.PriorityMessage)
		require.True(t, ok)
		require.EqualValues(t, 5, msg.GetPriority())
		require.EqualValues(t, 2, mailbox.Len())

		actual = mailbox.Dequeue()
		msg, ok = actual.Message().(*testpb.PriorityMessage)
		require.True(t, ok)
		require.EqualValues(t, 2, msg.GetPriority())
		require.EqualValues(t, 1, mailbox.Len())

		actual = mailbox.Dequeue()
		msg, ok = actual.Message().(*testpb.PriorityMessage)
		require.True(t, ok)
		require.EqualValues(t, 1, msg.GetPriority())
		require.True(t, mailbox.IsEmpty())
	})
	t.Run("With lowest priority mailbox", func(t *testing.T) {
		priorityFunc := func(msg1, msg2 proto.Message) bool {
			p1 := msg1.(*testpb.PriorityMessage)
			p2 := msg2.(*testpb.PriorityMessage)
			return p1.Priority <= p2.Priority
		}

		// create an instance of the mailbox
		mailbox := NewUnboundedPriorityMailBox(priorityFunc)
		msg1 := &ReceiveContext{message: &testpb.PriorityMessage{Priority: 1}}
		msg2 := &ReceiveContext{message: &testpb.PriorityMessage{Priority: 5}}
		msg3 := &ReceiveContext{message: &testpb.PriorityMessage{Priority: 2}}

		require.NoError(t, mailbox.Enqueue(msg1))
		require.NoError(t, mailbox.Enqueue(msg2))
		require.NoError(t, mailbox.Enqueue(msg3))

		// let us start dequeuing the mailbox
		actual := mailbox.Dequeue()
		msg, ok := actual.Message().(*testpb.PriorityMessage)
		require.True(t, ok)
		require.EqualValues(t, 1, msg.GetPriority())
		require.EqualValues(t, 2, mailbox.Len())

		actual = mailbox.Dequeue()
		msg, ok = actual.Message().(*testpb.PriorityMessage)
		require.True(t, ok)
		require.EqualValues(t, 2, msg.GetPriority())
		require.EqualValues(t, 1, mailbox.Len())

		actual = mailbox.Dequeue()
		msg, ok = actual.Message().(*testpb.PriorityMessage)
		require.True(t, ok)
		require.EqualValues(t, 5, msg.GetPriority())
		require.True(t, mailbox.IsEmpty())
	})
	t.Run("With dequeue when queue is empty", func(t *testing.T) {
		priorityFunc := func(msg1, msg2 proto.Message) bool {
			p1 := msg1.(*testpb.PriorityMessage)
			p2 := msg2.(*testpb.PriorityMessage)
			return p1.Priority <= p2.Priority
		}

		// create an instance of the mailbox
		mailbox := NewUnboundedPriorityMailBox(priorityFunc)
		msg1 := &ReceiveContext{message: &testpb.PriorityMessage{Priority: 1}}
		msg2 := &ReceiveContext{message: &testpb.PriorityMessage{Priority: 5}}
		msg3 := &ReceiveContext{message: &testpb.PriorityMessage{Priority: 2}}

		require.NoError(t, mailbox.Enqueue(msg1))
		require.NoError(t, mailbox.Enqueue(msg2))
		require.NoError(t, mailbox.Enqueue(msg3))

		actual := mailbox.Dequeue()
		require.NotNil(t, actual)
		actual = mailbox.Dequeue()
		require.NotNil(t, actual)
		actual = mailbox.Dequeue()
		require.NotNil(t, actual)
		require.True(t, mailbox.IsEmpty())
		actual = mailbox.Dequeue()
		require.Nil(t, actual)
	})
}
