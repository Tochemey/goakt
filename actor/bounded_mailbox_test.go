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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/pause"
)

func TestBoundedMailbox(t *testing.T) {
	mailbox := NewBoundedMailbox(20)
	assert.Nil(t, mailbox.Dequeue())
	for range 20 {
		require.NoError(t, mailbox.Enqueue(&ReceiveContext{}))
	}
	assert.False(t, mailbox.IsEmpty())
	dequeue := mailbox.Dequeue()
	require.NotNil(t, dequeue)
	assert.EqualValues(t, 19, mailbox.Len())
	counter := 19
	for counter > 0 {
		mailbox.Dequeue()
		counter--
	}
	assert.True(t, mailbox.IsEmpty())
	mailbox.Dispose()
	err := mailbox.Enqueue(&ReceiveContext{})
	require.Error(t, err)
}

// TestBoundedMailboxEnqueueRemoteMessage pins the one case where Enqueue does
// not block: a message that arrived over remoting is refused with
// ErrMailboxFull when the mailbox is full, queued while there is room, and
// fails once the mailbox is disposed. TestBoundedMailboxEnqueueBlocksLocalSender
// pins that a local sender still blocks.
func TestBoundedMailboxEnqueueRemoteMessage(t *testing.T) {
	remote := func() *ReceiveContext { return &ReceiveContext{remote: true} }

	t.Run("With room", func(t *testing.T) {
		mailbox := NewBoundedMailbox(2)
		require.NoError(t, mailbox.Enqueue(remote()))
		require.NoError(t, mailbox.Enqueue(remote()))
		assert.EqualValues(t, 2, mailbox.Len())
	})

	t.Run("With a full mailbox", func(t *testing.T) {
		mailbox := NewBoundedMailbox(2)
		require.NoError(t, mailbox.Enqueue(&ReceiveContext{}))
		require.NoError(t, mailbox.Enqueue(remote()))

		err := mailbox.Enqueue(remote())
		require.ErrorIs(t, err, gerrors.ErrMailboxFull)
		assert.EqualValues(t, 2, mailbox.Len())

		require.NotNil(t, mailbox.Dequeue())
		require.NoError(t, mailbox.Enqueue(remote()))
		assert.EqualValues(t, 2, mailbox.Len())
	})

	t.Run("With a disposed mailbox", func(t *testing.T) {
		mailbox := NewBoundedMailbox(2)
		mailbox.Dispose()

		err := mailbox.Enqueue(remote())
		require.Error(t, err)
		require.NotErrorIs(t, err, gerrors.ErrMailboxFull)
	})
}

// TestBoundedMailboxEnqueueBlocksLocalSender pins the blocking contract for
// a local sender: an Enqueue into a full mailbox does not return until the
// consumer frees a slot.
func TestBoundedMailboxEnqueueBlocksLocalSender(t *testing.T) {
	mailbox := NewBoundedMailbox(2)
	require.NoError(t, mailbox.Enqueue(&ReceiveContext{}))
	require.NoError(t, mailbox.Enqueue(&ReceiveContext{}))

	returned := make(chan error, 1)

	go func() {
		returned <- mailbox.Enqueue(&ReceiveContext{})
	}()

	pause.For(100 * time.Millisecond)

	select {
	case err := <-returned:
		t.Fatalf("a local Enqueue into a full mailbox must block, returned %v", err)
	default:
	}

	require.NotNil(t, mailbox.Dequeue())

	require.Eventually(t, func() bool {
		select {
		case err := <-returned:
			require.NoError(t, err)
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond, "the blocked Enqueue must return once a slot frees")

	assert.EqualValues(t, 2, mailbox.Len())
}

// TestBoundedMailboxEnqueueRemoteUnderContention pins that a remote message
// is refused only by a full mailbox, never by losing the race for a slot to
// another producer: local producers and a remote one fill a mailbox that
// never reaches capacity, and every remote Enqueue must succeed.
func TestBoundedMailboxEnqueueRemoteUnderContention(t *testing.T) {
	const (
		capacity     = 1 << 16
		localSenders = 3
		perSender    = 10_000
	)

	mailbox := NewBoundedMailbox(capacity)

	var wg sync.WaitGroup
	wg.Add(localSenders)

	for range localSenders {
		go func() {
			defer wg.Done()

			for range perSender {
				_ = mailbox.Enqueue(&ReceiveContext{})
			}
		}()
	}

	refused := 0

	for range perSender {
		if err := mailbox.Enqueue(&ReceiveContext{remote: true}); err != nil {
			refused++
		}
	}

	wg.Wait()

	assert.Zero(t, refused, "a remote message must not be refused while the mailbox has room")
	assert.EqualValues(t, (localSenders+1)*perSender, mailbox.Len())
}
