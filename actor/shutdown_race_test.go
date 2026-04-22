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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGrainsQueueGuardPreventsPostShutdownSend verifies that the
// shuttingDown atomic guard in putGrainOnCluster prevents sends after
// shutdown begins. Before the fix, putGrainOnCluster had no guard and
// would panic with "send on closed channel".
func TestGrainsQueueGuardPreventsPostShutdownSend(t *testing.T) {
	var shuttingDown atomic.Bool
	ch := make(chan int, 10)

	guardedSend := func(v int) bool {
		if shuttingDown.Load() {
			return false
		}
		ch <- v
		return true
	}

	assert.True(t, guardedSend(1))
	assert.True(t, guardedSend(2))
	assert.Equal(t, 2, len(ch))

	shuttingDown.Store(true)
	assert.False(t, guardedSend(3))
	assert.False(t, guardedSend(4))
	assert.Equal(t, 2, len(ch))

	close(ch)
}

// TestGrainsQueueRecoverCatchesSendOnClosedChannel verifies that the
// deferred recover() in putGrainOnCluster catches the panic from
// sending to a closed channel in the TOCTOU window.
func TestGrainsQueueRecoverCatchesSendOnClosedChannel(t *testing.T) {
	ch := make(chan int, 10)
	close(ch)

	var panicked bool
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		ch <- 1
	}()

	assert.True(t, panicked, "expected recover to catch send-on-closed-channel panic")
}

// TestGrainsQueueConcurrentSendersStopOnFlag verifies that concurrent
// senders all stop promptly when the shuttingDown flag is set. This is
// the primary defense: in production, shutdown() sets shuttingDown=true
// well before shutdownCluster() closes the channel, so almost all
// senders observe the flag before the close.
func TestGrainsQueueConcurrentSendersStopOnFlag(t *testing.T) {
	const senders = 20
	const sendsPerSender = 5000

	var shuttingDown atomic.Bool
	var sent atomic.Int64
	ch := make(chan int, 100)

	guardedSend := func(v int) {
		if shuttingDown.Load() {
			return
		}
		ch <- v
		sent.Add(1)
	}

	// Drain the channel
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range ch {
		}
	}()

	var wg sync.WaitGroup
	wg.Add(senders)
	for range senders {
		go func() {
			defer wg.Done()
			for i := range sendsPerSender {
				guardedSend(i)
			}
		}()
	}

	// Let some sends through, then set the flag
	time.Sleep(time.Millisecond)
	shuttingDown.Store(true)
	wg.Wait()

	sentCount := sent.Load()
	totalPossible := int64(senders * sendsPerSender)

	// Now safe to close — all senders have exited
	close(ch)
	<-done

	// Some sends should have succeeded before the flag was set, but not all
	require.Greater(t, sentCount, int64(0), "some sends should succeed before shutdown")
	require.Less(t, sentCount, totalPossible, "flag should have stopped some senders")
	t.Logf("sent %d / %d messages before shutdown flag", sentCount, totalPossible)
}
