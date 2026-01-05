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
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/atomic"

	gerrors "github.com/tochemey/goakt/v3/errors"
)

func TestGrainMailboxEmpty_Unbounded(t *testing.T) {
	mailbox := newGrainMailbox(0) // unbounded
	require.True(t, mailbox.IsEmpty())
	require.EqualValues(t, 0, mailbox.Len())
	require.Nil(t, mailbox.Dequeue())
}

func TestGrainMailboxFIFO_Unbounded(t *testing.T) {
	mailbox := newGrainMailbox(0) // unbounded

	ctx1 := &GrainContext{}
	ctx2 := &GrainContext{}
	ctx3 := &GrainContext{}

	require.NoError(t, mailbox.Enqueue(ctx1))
	require.NoError(t, mailbox.Enqueue(ctx2))
	require.NoError(t, mailbox.Enqueue(ctx3))

	require.EqualValues(t, 3, mailbox.Len())
	require.False(t, mailbox.IsEmpty())

	require.Equal(t, ctx1, mailbox.Dequeue())
	require.Equal(t, ctx2, mailbox.Dequeue())
	require.Equal(t, ctx3, mailbox.Dequeue())

	require.Nil(t, mailbox.Dequeue())
	require.True(t, mailbox.IsEmpty())
	require.EqualValues(t, 0, mailbox.Len())
	require.Zero(t, mailbox.Capacity())
}

func TestGrainMailboxConcurrentEnqueue_Unbounded(t *testing.T) {
	const producers = 16
	const messagesPerProducer = 32
	total := producers * messagesPerProducer

	mailbox := newGrainMailbox(0) // unbounded

	var wg sync.WaitGroup
	wg.Add(producers)

	for i := 0; i < producers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < messagesPerProducer; j++ {
				require.NoError(t, mailbox.Enqueue(&GrainContext{}))
			}
		}()
	}

	wg.Wait()

	require.Eventually(t, func() bool {
		return mailbox.Len() == int64(total)
	}, time.Second, time.Millisecond)

	count := 0
	for {
		ctx := mailbox.Dequeue()
		if ctx == nil {
			break
		}
		count++
	}

	require.Equal(t, total, count)
	require.True(t, mailbox.IsEmpty())
	require.EqualValues(t, 0, mailbox.Len())
}

func TestGrainMailboxBounded_FullReturnsError(t *testing.T) {
	mailbox := newGrainMailbox(2)

	require.NoError(t, mailbox.Enqueue(&GrainContext{}))
	require.NoError(t, mailbox.Enqueue(&GrainContext{}))

	require.EqualValues(t, 2, mailbox.Len())
	require.ErrorIs(t, mailbox.Enqueue(&GrainContext{}), gerrors.ErrMailboxFull)

	// Make sure dequeue frees space and enqueue works again.
	require.NotNil(t, mailbox.Dequeue())
	require.EqualValues(t, 1, mailbox.Len())

	require.NoError(t, mailbox.Enqueue(&GrainContext{}))
	require.EqualValues(t, 2, mailbox.Len())
}

func TestGrainMailboxBounded_DoesNotOvershootCapacity_Concurrent(t *testing.T) {
	const cap = 64
	const producers = 16
	const attemptsPerProducer = 64 // 1024 attempts > cap

	mailbox := newGrainMailbox(cap)

	var wg sync.WaitGroup
	wg.Add(producers)

	var okCount atomic.Int64
	var fullCount atomic.Int64

	for i := 0; i < producers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < attemptsPerProducer; j++ {
				err := mailbox.Enqueue(&GrainContext{})
				if err == nil {
					okCount.Add(1)
					continue
				}
				if err == gerrors.ErrMailboxFull {
					fullCount.Add(1)
					continue
				}
				require.NoError(t, err) // fail on unexpected error
			}
		}()
	}

	wg.Wait()

	// Exactly 'cap' successful enqueues, rest should be full.
	require.EqualValues(t, cap, okCount.Load())
	require.EqualValues(t, producers*attemptsPerProducer-cap, fullCount.Load())

	// Length must never exceed capacity; by the end it should be exactly cap.
	require.EqualValues(t, cap, mailbox.Len())

	// Drain and ensure we got exactly cap messages.
	drained := 0
	for {
		if mailbox.Dequeue() == nil {
			break
		}
		drained++
	}
	require.Equal(t, cap, drained)
	require.True(t, mailbox.IsEmpty())
	require.EqualValues(t, 0, mailbox.Len())
}

func TestGrainMailboxCapacityZeroOrNegative_IsUnbounded(t *testing.T) {
	m0 := newGrainMailbox(0)
	mNeg := newGrainMailbox(-10)

	// Try to enqueue a bunch; should never return ErrMailboxFull.
	for i := 0; i < 1000; i++ {
		require.NoError(t, m0.Enqueue(&GrainContext{}))
		require.NoError(t, mNeg.Enqueue(&GrainContext{}))
	}

	require.EqualValues(t, 1000, m0.Len())
	require.EqualValues(t, 1000, mNeg.Len())
}
