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
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

func TestNonBlockingBoundedMailbox(t *testing.T) {
	t.Run("With capacity rounded up to a power of two", func(t *testing.T) {
		// 20 rounds up to 32
		mailbox := NewNonBlockingBoundedMailbox(20)
		var count int
		for {
			if err := mailbox.Enqueue(&ReceiveContext{}); err != nil {
				require.ErrorIs(t, err, gerrors.ErrMailboxFull)
				break
			}
			count++
		}
		require.Equal(t, 32, count)
		require.EqualValues(t, 32, mailbox.Len())
	})

	t.Run("With FIFO ordering", func(t *testing.T) {
		mailbox := NewNonBlockingBoundedMailbox(4)
		for i := range 4 {
			require.NoError(t, mailbox.Enqueue(&ReceiveContext{message: testpb.TestMessage_builder{Priority: int64(i)}.Build()}))
		}

		for i := range 4 {
			actual := mailbox.Dequeue()
			require.NotNil(t, actual)
			msg, ok := actual.Message().(*testpb.TestMessage)
			require.True(t, ok)
			require.EqualValues(t, i, msg.GetPriority())
		}
	})

	t.Run("With overflow returning ErrMailboxFull", func(t *testing.T) {
		mailbox := NewNonBlockingBoundedMailbox(2)
		require.NoError(t, mailbox.Enqueue(&ReceiveContext{}))
		require.NoError(t, mailbox.Enqueue(&ReceiveContext{}))
		err := mailbox.Enqueue(&ReceiveContext{})
		require.ErrorIs(t, err, gerrors.ErrMailboxFull)
	})

	t.Run("With empty mailbox returning nil", func(t *testing.T) {
		mailbox := NewNonBlockingBoundedMailbox(2)
		require.True(t, mailbox.IsEmpty())
		require.Nil(t, mailbox.Dequeue())
		require.Zero(t, mailbox.Len())
	})

	t.Run("With enqueue after draining", func(t *testing.T) {
		mailbox := NewNonBlockingBoundedMailbox(2)
		require.NoError(t, mailbox.Enqueue(&ReceiveContext{}))
		require.NoError(t, mailbox.Enqueue(&ReceiveContext{}))
		require.ErrorIs(t, mailbox.Enqueue(&ReceiveContext{}), gerrors.ErrMailboxFull)
		require.NotNil(t, mailbox.Dequeue())
		require.NoError(t, mailbox.Enqueue(&ReceiveContext{}))
		require.False(t, mailbox.IsEmpty())
		mailbox.Dispose()
	})

	t.Run("With concurrent producers and a single consumer", func(t *testing.T) {
		const (
			producers   = 8
			perProducer = 500
		)

		mailbox := NewNonBlockingBoundedMailbox(1024)
		var enqueued atomic.Int64
		var dequeued atomic.Int64
		var wg sync.WaitGroup

		producersDone := make(chan struct{})
		done := make(chan struct{})

		// a single consumer, as the Mailbox contract requires
		go func() {
			for {
				if msg := mailbox.Dequeue(); msg != nil {
					dequeued.Add(1)
					continue
				}

				select {
				case <-producersDone:
					// producers have finished, so a nil dequeue means drained
					close(done)
					return
				default:
				}
			}
		}()

		wg.Add(producers)
		for range producers {
			go func() {
				defer wg.Done()
				for range perProducer {
					if err := mailbox.Enqueue(&ReceiveContext{}); err == nil {
						enqueued.Add(1)
					}
				}
			}()
		}

		wg.Wait()
		close(producersDone)
		<-done

		require.Equal(t, enqueued.Load(), dequeued.Load())
	})
}

func BenchmarkNonBlockingBoundedMailbox(b *testing.B) {
	benchmarkMailboxThroughput(b, NewNonBlockingBoundedMailbox(1<<16))
}

func TestNextPowerOfTwo(t *testing.T) {
	cases := map[int]uint64{
		-5: 2,
		0:  2,
		1:  2,
		2:  2,
		3:  4,
		5:  8,
		16: 16,
		17: 32,
	}
	for in, want := range cases {
		assert.Equalf(t, want, nextPowerOfTwo(in), "nextPowerOfTwo(%d)", in)
	}
}
