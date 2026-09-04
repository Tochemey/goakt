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
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
)

// newEmbeddedMailbox builds a standalone embedded mailbox seeded exactly as
// newPID seeds a default actor's: one shared sentinel node on both ends of an
// otherwise blank PID, reinterpreted through (*embeddedMailbox).
func newEmbeddedMailbox() *embeddedMailbox {
	pid := &PID{}
	sentinel := new(ReceiveContext)
	pid.mailboxHead = unsafe.Pointer(sentinel)
	pid.mailboxTail = unsafe.Pointer(sentinel)
	return (*embeddedMailbox)(pid)
}

// embeddedMailboxMessage builds a context carrying message for the mailbox tests.
func embeddedMailboxMessage(message any) *ReceiveContext {
	return &ReceiveContext{message: message}
}

// embeddedMailboxHead atomically reads the mailbox's current head, the node the
// release protocol keeps as the sentinel.
func embeddedMailboxHead(mailbox *embeddedMailbox) *ReceiveContext {
	return (*ReceiveContext)(atomic.LoadPointer(&mailbox.mailboxHead))
}

// TestEmbeddedMailboxFIFOOrder verifies that messages come out in the order they
// were enqueued and that each dequeue returns the very context that was put in.
func TestEmbeddedMailboxFIFOOrder(t *testing.T) {
	mailbox := newEmbeddedMailbox()
	require.True(t, mailbox.IsEmpty())

	const count = 64
	in := make([]*ReceiveContext, count)
	for i := range count {
		in[i] = embeddedMailboxMessage(i)
		require.NoError(t, mailbox.Enqueue(in[i]))
	}

	require.False(t, mailbox.IsEmpty())
	require.Equal(t, int64(count), mailbox.Len())

	for i := range count {
		out := mailbox.Dequeue()
		require.Same(t, in[i], out)
		require.Equal(t, i, out.Message())
	}

	require.Nil(t, mailbox.Dequeue())
	require.True(t, mailbox.IsEmpty())
	mailbox.Dispose()
}

// TestEmbeddedMailboxReleaseProtocol verifies the sentinel release protocol:
// each dequeue returns the message following the current sentinel and promotes
// it to the new sentinel, so the mailbox head advances to the returned context
// and the previous head is recycled. An empty dequeue leaves the last delivered
// context as the sentinel and touches nothing else.
func TestEmbeddedMailboxReleaseProtocol(t *testing.T) {
	mailbox := newEmbeddedMailbox()
	sentinel := embeddedMailboxHead(mailbox)

	first := embeddedMailboxMessage("first")
	second := embeddedMailboxMessage("second")
	require.NoError(t, mailbox.Enqueue(first))
	require.NoError(t, mailbox.Enqueue(second))

	// the sentinel still fronts the list until the first dequeue
	require.Same(t, sentinel, embeddedMailboxHead(mailbox))

	require.Same(t, first, mailbox.Dequeue())
	require.Same(t, first, embeddedMailboxHead(mailbox))

	require.Same(t, second, mailbox.Dequeue())
	require.Same(t, second, embeddedMailboxHead(mailbox))

	require.Nil(t, mailbox.Dequeue())
	require.Same(t, second, embeddedMailboxHead(mailbox))
	require.True(t, mailbox.IsEmpty())
}

// TestEmbeddedMailboxConcurrentProducers drains the mailbox from a single
// consumer while eight producers enqueue into it, and checks that every message
// arrives exactly once and that each producer's messages keep their order. Run
// under the race detector.
func TestEmbeddedMailboxConcurrentProducers(t *testing.T) {
	const producers, perProducer = 8, 500

	mailbox := newEmbeddedMailbox()
	var wg sync.WaitGroup
	wg.Add(producers)

	for p := range producers {
		go func() {
			defer wg.Done()

			for i := range perProducer {
				require.NoError(t, mailbox.Enqueue(embeddedMailboxMessage([2]int{p, i})))
			}
		}()
	}

	next := make([]int, producers)
	received := 0

	for received < producers*perProducer {
		ctx := mailbox.Dequeue()
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
	require.Nil(t, mailbox.Dequeue())
	require.True(t, mailbox.IsEmpty())
}

// TestEmbeddedMailboxDefaultSpawnUsesIt verifies that a default-spawned actor
// runs on the embedded mailbox, its Mailbox pointing straight into the PID's own
// head and tail words, while an actor given a custom mailbox leaves those words
// nil and dispatches through the custom box instead.
func TestEmbeddedMailboxDefaultSpawnUsesIt(t *testing.T) {
	ctx := context.TODO()
	sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))

	pause.For(time.Second)

	t.Cleanup(func() {
		require.NoError(t, sys.Stop(ctx))
	})

	defaultPID, err := sys.Spawn(ctx, "Default", NewMockActor())
	require.NoError(t, err)
	require.NotNil(t, defaultPID.mailbox)
	// the consumer turn advances mailboxHead atomically on a live actor, so the
	// test reads both ends atomically too.
	require.NotNil(t, (*ReceiveContext)(atomic.LoadPointer(&defaultPID.mailboxHead)))
	require.NotNil(t, (*ReceiveContext)(atomic.LoadPointer(&defaultPID.mailboxTail)))

	box, ok := defaultPID.mailbox.(*embeddedMailbox)
	require.True(t, ok, "default spawn must use the embedded mailbox")
	require.Same(t, (*embeddedMailbox)(defaultPID), box)

	custom := NewUnboundedMailbox()
	customPID, err := sys.Spawn(ctx, "Custom", NewMockActor(), WithMailbox(custom))
	require.NoError(t, err)
	require.Same(t, custom, customPID.mailbox)
	require.Nil(t, (*ReceiveContext)(atomic.LoadPointer(&customPID.mailboxHead)))
	require.Nil(t, (*ReceiveContext)(atomic.LoadPointer(&customPID.mailboxTail)))
}
