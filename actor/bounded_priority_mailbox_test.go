// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package actor

import (
	"testing"

	"github.com/stretchr/testify/require"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

func TestBoundedPriorityMailbox(t *testing.T) {
	t.Run("With priority ordering", func(t *testing.T) {
		mailbox := NewBoundedPriorityMailbox(8, highestPriority)
		require.NoError(t, mailbox.Enqueue(&ReceiveContext{message: testpb.TestMessage_builder{Priority: 1}.Build()}))
		require.NoError(t, mailbox.Enqueue(&ReceiveContext{message: testpb.TestMessage_builder{Priority: 5}.Build()}))
		require.NoError(t, mailbox.Enqueue(&ReceiveContext{message: testpb.TestMessage_builder{Priority: 2}.Build()}))

		require.EqualValues(t, 5, mailbox.Dequeue().Message().(*testpb.TestMessage).GetPriority())
		require.EqualValues(t, 2, mailbox.Dequeue().Message().(*testpb.TestMessage).GetPriority())
		require.EqualValues(t, 1, mailbox.Dequeue().Message().(*testpb.TestMessage).GetPriority())
		require.True(t, mailbox.IsEmpty())
	})

	t.Run("With overflow returning ErrMailboxFull", func(t *testing.T) {
		mailbox := NewBoundedPriorityMailbox(2, highestPriority)
		require.NoError(t, mailbox.Enqueue(&ReceiveContext{message: testpb.TestMessage_builder{Priority: 1}.Build()}))
		require.NoError(t, mailbox.Enqueue(&ReceiveContext{message: testpb.TestMessage_builder{Priority: 2}.Build()}))
		require.ErrorIs(t, mailbox.Enqueue(&ReceiveContext{message: testpb.TestMessage_builder{Priority: 3}.Build()}), gerrors.ErrMailboxFull)
		require.EqualValues(t, 2, mailbox.Len())

		// draining frees a slot for a new message
		require.NotNil(t, mailbox.Dequeue())
		require.NoError(t, mailbox.Enqueue(&ReceiveContext{message: testpb.TestMessage_builder{Priority: 3}.Build()}))
	})

	t.Run("With empty mailbox returning nil", func(t *testing.T) {
		mailbox := NewBoundedPriorityMailbox(2, highestPriority)
		require.Nil(t, mailbox.Dequeue())
		require.Zero(t, mailbox.Len())
		mailbox.Dispose()
	})
}

func BenchmarkBoundedPriorityMailbox(b *testing.B) {
	benchmarkMailboxThroughput(b, NewBoundedPriorityMailbox(1<<16, highestPriority))
}
