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
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/internal/pause"
)

// TestEmbeddedGrainMailboxFIFOOrder verifies that messages come out in the
// order they were enqueued, that each dequeue returns the very context that was
// put in, and that Len counts the queued messages by walking the list.
func TestEmbeddedGrainMailboxFIFOOrder(t *testing.T) {
	mailbox := newEmbeddedGrainMailbox()
	require.True(t, mailbox.IsEmpty())
	require.Zero(t, mailbox.Len())

	const count = 64
	in := make([]*GrainContext, count)

	for i := range count {
		in[i] = embeddedGrainMailboxMessage(i)
		mailbox.Enqueue(in[i])
	}

	require.False(t, mailbox.IsEmpty())
	require.EqualValues(t, count, mailbox.Len())

	for i := range count {
		out := mailbox.Dequeue()
		require.Same(t, in[i], out)
		require.Equal(t, i, out.Message())
		require.EqualValues(t, count-i-1, mailbox.Len())
	}

	require.Nil(t, mailbox.Dequeue())
	require.True(t, mailbox.IsEmpty())
}

// TestEmbeddedGrainMailboxReleaseProtocol verifies the sentinel release
// protocol: each dequeue returns the message following the current sentinel and
// promotes it to the new sentinel, so the head advances to the returned context
// while the previous sentinel is reset and recycled. An empty dequeue leaves
// the last delivered context as the sentinel and touches nothing else.
func TestEmbeddedGrainMailboxReleaseProtocol(t *testing.T) {
	mailbox := newEmbeddedGrainMailbox()
	sentinel := embeddedGrainMailboxHead(mailbox)

	first := embeddedGrainMailboxMessage("first")
	second := embeddedGrainMailboxMessage("second")
	mailbox.Enqueue(first)
	mailbox.Enqueue(second)

	// the sentinel still fronts the list until the first dequeue
	require.Same(t, sentinel, embeddedGrainMailboxHead(mailbox))

	require.Same(t, first, mailbox.Dequeue())
	require.Same(t, first, embeddedGrainMailboxHead(mailbox))

	require.Same(t, second, mailbox.Dequeue())
	require.Same(t, second, embeddedGrainMailboxHead(mailbox))

	// the retired sentinel was reset before it went back to the pool
	require.Nil(t, first.Message())
	require.Nil(t, first.next.Load())

	require.Nil(t, mailbox.Dequeue())
	require.Same(t, second, embeddedGrainMailboxHead(mailbox))
	require.True(t, mailbox.IsEmpty())
}

// TestEmbeddedGrainMailboxConcurrentProducers drains the mailbox from a single
// consumer while eight producers enqueue into it, and checks that every message
// arrives exactly once and that each producer's messages keep their order. Run
// under the race detector.
func TestEmbeddedGrainMailboxConcurrentProducers(t *testing.T) {
	const producers, perProducer = 8, 500

	mailbox := newEmbeddedGrainMailbox()
	var wg sync.WaitGroup
	wg.Add(producers)

	for p := range producers {
		go func() {
			defer wg.Done()

			for i := range perProducer {
				mailbox.Enqueue(embeddedGrainMailboxMessage([2]int{p, i}))
			}
		}()
	}

	next := make([]int, producers)
	received := 0

	for received < producers*perProducer {
		gctx := mailbox.Dequeue()
		if gctx == nil {
			runtime.Gosched()
			continue
		}

		id := gctx.Message().([2]int)
		require.Equal(t, next[id[0]], id[1], "producer %d delivered out of order", id[0])
		next[id[0]]++
		received++
	}

	wg.Wait()
	require.Nil(t, mailbox.Dequeue())
	require.True(t, mailbox.IsEmpty())
}

// TestEmbeddedGrainMailboxAwaitsPendingLink drives the spurious-empty guard: a
// producer that swapped the tail but has not linked its node yet must not make
// the consumer report an empty mailbox. The half-finished enqueue is staged by
// hand because a real producer closes that window too fast to observe.
func TestEmbeddedGrainMailboxAwaitsPendingLink(t *testing.T) {
	mailbox := newEmbeddedGrainMailbox()
	sentinel := embeddedGrainMailboxHead(mailbox)

	// the swap half of an enqueue: the tail names the node, nothing links it
	pending := embeddedGrainMailboxMessage("pending")
	mailbox.mailboxTail.Store(pending)

	go func() {
		pause.For(50 * time.Millisecond)
		sentinel.next.Store(pending)
	}()

	require.Same(t, pending, mailbox.Dequeue())
	require.True(t, mailbox.IsEmpty())
}
