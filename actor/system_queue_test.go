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
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// systemQueueMessage builds a context carrying message for the queue tests.
func systemQueueMessage(message any) *ReceiveContext {
	return &ReceiveContext{message: message}
}

// popMessages pops until the queue reports nothing and returns the messages
// in the order they came out.
func popMessages(queue *systemQueue) []any {
	var out []any
	for ctx := queue.pop(); ctx != nil; ctx = queue.pop() {
		out = append(out, ctx.Message())
	}

	return out
}

// TestSystemQueueSendOrder verifies that messages come out in the order they
// were pushed, both within one drained batch and across pushes that land
// while the consumer still holds part of an earlier batch.
func TestSystemQueueSendOrder(t *testing.T) {
	t.Run("one batch", func(t *testing.T) {
		var queue systemQueue
		require.True(t, queue.isEmpty())

		for i := range 5 {
			queue.push(systemQueueMessage(i))
		}

		require.False(t, queue.isEmpty())
		require.Equal(t, []any{0, 1, 2, 3, 4}, popMessages(&queue))
		require.True(t, queue.isEmpty())
		require.Nil(t, queue.last.Load())
	})

	t.Run("pushes interleaved with pops", func(t *testing.T) {
		var queue systemQueue
		queue.push(systemQueueMessage("a"))
		queue.push(systemQueueMessage("b"))
		require.Equal(t, "a", queue.pop().Message())

		// pushed while the consumer's private batch still holds b
		queue.push(systemQueueMessage("c"))
		require.False(t, queue.isEmpty())
		require.Equal(t, "b", queue.pop().Message())
		require.False(t, queue.isEmpty())
		require.Equal(t, "c", queue.pop().Message())
		require.Nil(t, queue.pop())
		require.True(t, queue.isEmpty())
	})
}

// TestSystemQueueReleaseProtocol verifies that a popped message stays held
// until the next pop, that the next pop releases it, and that a pop returning
// nothing releases the last message so an idle actor holds no context.
func TestSystemQueueReleaseProtocol(t *testing.T) {
	var queue systemQueue
	first := systemQueueMessage("first")
	second := systemQueueMessage("second")
	queue.push(first)
	queue.push(second)

	require.Same(t, first, queue.pop())
	require.Same(t, first, queue.last.Load())
	require.Same(t, second, nextInBatch(queue.last.Load()))

	require.Same(t, second, queue.pop())
	require.Same(t, second, queue.last.Load())
	require.Nil(t, nextInBatch(queue.last.Load()))

	require.Nil(t, queue.pop())
	require.Nil(t, queue.last.Load())
	require.True(t, queue.isEmpty())
}

// TestSystemQueueConcurrentPushes drains the queue while several producers
// push into it and checks that every message arrives exactly once and that
// each producer's messages keep their order.
func TestSystemQueueConcurrentPushes(t *testing.T) {
	const producers, perProducer = 8, 500

	var queue systemQueue
	var wg sync.WaitGroup
	wg.Add(producers)

	for p := range producers {
		go func() {
			defer wg.Done()

			for i := range perProducer {
				queue.push(systemQueueMessage([2]int{p, i}))
			}
		}()
	}

	next := make([]int, producers)
	received := 0

	for received < producers*perProducer {
		ctx := queue.pop()
		if ctx == nil {
			runtime.Gosched()
			continue
		}

		id := ctx.Message().([2]int)
		require.Equal(t, next[id[0]], id[1], "producer %d delivered out of order", id[0])
		next[id[0]]++
		received++
	}

	wg.Wait()
	require.Nil(t, queue.pop())
	require.True(t, queue.isEmpty())
}
