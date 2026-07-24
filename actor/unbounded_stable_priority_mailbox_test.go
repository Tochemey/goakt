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
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// highestPriority orders messages so that a larger Priority value is served
// first.
func highestPriority(msg1, msg2 any) bool {
	p1 := msg1.(*testpb.TestMessage)
	p2 := msg2.(*testpb.TestMessage)
	return p1.GetPriority() > p2.GetPriority()
}

func TestUnboundedStablePriorityMailbox(t *testing.T) {
	t.Run("With priority ordering", func(t *testing.T) {
		mailbox := NewUnboundedStablePriorityMailbox(highestPriority)
		require.NoError(t, mailbox.Enqueue(&ReceiveContext{message: &testpb.TestMessage{Priority: 1}}))
		require.NoError(t, mailbox.Enqueue(&ReceiveContext{message: &testpb.TestMessage{Priority: 5}}))
		require.NoError(t, mailbox.Enqueue(&ReceiveContext{message: &testpb.TestMessage{Priority: 2}}))
		require.EqualValues(t, 3, mailbox.Len())

		require.EqualValues(t, 5, mailbox.Dequeue().Message().(*testpb.TestMessage).GetPriority())
		require.EqualValues(t, 2, mailbox.Dequeue().Message().(*testpb.TestMessage).GetPriority())
		require.EqualValues(t, 1, mailbox.Dequeue().Message().(*testpb.TestMessage).GetPriority())
		require.True(t, mailbox.IsEmpty())
	})

	t.Run("With FIFO ordering among equal priorities", func(t *testing.T) {
		mailbox := NewUnboundedStablePriorityMailbox(highestPriority)

		low1 := &ReceiveContext{message: &testpb.TestMessage{Priority: 1}}
		low2 := &ReceiveContext{message: &testpb.TestMessage{Priority: 1}}
		low3 := &ReceiveContext{message: &testpb.TestMessage{Priority: 1}}
		high1 := &ReceiveContext{message: &testpb.TestMessage{Priority: 5}}
		high2 := &ReceiveContext{message: &testpb.TestMessage{Priority: 5}}

		require.NoError(t, mailbox.Enqueue(low1))
		require.NoError(t, mailbox.Enqueue(low2))
		require.NoError(t, mailbox.Enqueue(high1))
		require.NoError(t, mailbox.Enqueue(low3))
		require.NoError(t, mailbox.Enqueue(high2))

		// higher priority first, arrival order preserved within each priority
		require.Same(t, high1, mailbox.Dequeue())
		require.Same(t, high2, mailbox.Dequeue())
		require.Same(t, low1, mailbox.Dequeue())
		require.Same(t, low2, mailbox.Dequeue())
		require.Same(t, low3, mailbox.Dequeue())
		require.True(t, mailbox.IsEmpty())
	})

	t.Run("With empty mailbox returning nil", func(t *testing.T) {
		mailbox := NewUnboundedStablePriorityMailbox(highestPriority)
		require.Nil(t, mailbox.Dequeue())
		require.Zero(t, mailbox.Len())
		mailbox.Dispose()
	})

	t.Run("With concurrent producers losing no message", func(t *testing.T) {
		mailbox := NewUnboundedStablePriorityMailbox(highestPriority)
		const (
			producers   = 8
			perProducer = 1000
		)

		var wg sync.WaitGroup
		wg.Add(producers)
		for range producers {
			go func() {
				defer wg.Done()
				for range perProducer {
					require.NoError(t, mailbox.Enqueue(&ReceiveContext{message: &testpb.TestMessage{Priority: 1}}))
				}
			}()
		}
		wg.Wait()

		require.EqualValues(t, producers*perProducer, mailbox.Len())

		count := 0
		for mailbox.Dequeue() != nil {
			count++
		}

		require.Equal(t, producers*perProducer, count)
		require.True(t, mailbox.IsEmpty())
	})
}

func BenchmarkUnboundedStablePriorityMailbox(b *testing.B) {
	benchmarkMailboxThroughput(b, NewUnboundedStablePriorityMailbox(highestPriority))
}
