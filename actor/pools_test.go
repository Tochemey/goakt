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
	"errors"
	"sync"
	"testing"
	"time"
)

func TestErrorChannel_CapacityIsOne(t *testing.T) {
	ch := getErrorChannel()
	defer putErrorChannel(ch)

	if cap(ch) != 1 {
		t.Fatalf("expected channel capacity 1, got %d", cap(ch))
	}
}

func TestPutErrorChannel_DrainsChannel(t *testing.T) {
	ch := getErrorChannel()

	// Put a leftover error in the buffer to simulate a previous use.
	want := errors.New("leftover")
	select {
	case ch <- want:
		// ok
	default:
		t.Fatalf("expected to be able to send into buffered channel")
	}

	// Return it to pool; should drain it.
	putErrorChannel(ch)

	// Get a channel back. We can't *guarantee* it's the same channel due to sync.Pool,
	// but we can make a strong check by getting one and verifying it's empty.
	ch2 := getErrorChannel()
	defer putErrorChannel(ch2)

	select {
	case err := <-ch2:
		t.Fatalf("expected channel to be drained/empty, but received: %v", err)
	default:
		// empty as expected
	}
}

func TestErrorChannel_ReusableAfterPutGet(t *testing.T) {
	ch := getErrorChannel()

	// Simulate correct usage: receive path reads a value then we put channel back.
	sentinel := errors.New("sentinel")
	ch <- sentinel

	// Drain it ourselves (as a caller would when consuming the response).
	got := <-ch
	if got != sentinel {
		t.Fatalf("expected %v, got %v", sentinel, got)
	}

	putErrorChannel(ch)

	// Now borrow again and ensure send/receive works.
	ch2 := getErrorChannel()
	defer putErrorChannel(ch2)

	select {
	case ch2 <- sentinel:
		// ok
	default:
		t.Fatalf("expected send to succeed on reusable channel")
	}

	select {
	case err := <-ch2:
		if err != sentinel {
			t.Fatalf("expected %v, got %v", sentinel, err)
		}
	default:
		t.Fatalf("expected to receive value we just sent")
	}
}

func TestErrorChannel_PoolReturnsDrainedChannels_SetEquality(t *testing.T) {
	// This test avoids assuming sync.Pool returns the same object.
	// Instead, we:
	// 1) take numChans channels
	// 2) "dirty" them
	// 3) put them back (should drain)
	// 4) take numChans channels again
	// 5) assert all returned channels are empty
	// 6) and that we got N channels total (no nils, usable)

	numChans := 64

	taken := make([]chan error, 0, numChans)
	for range numChans {
		ch := getErrorChannel()
		// dirty it
		ch <- errors.New("x")
		taken = append(taken, ch)
	}

	for _, ch := range taken {
		putErrorChannel(ch)
	}

	for range numChans {
		ch := getErrorChannel()
		if ch == nil {
			t.Fatalf("got nil channel from pool")
		}
		// must be empty (drained)
		select {
		case err := <-ch:
			t.Fatalf("expected drained/empty channel, got %v", err)
		default:
		}
		putErrorChannel(ch)
	}
}

func TestErrorChannel_ConcurrentBorrowReturn(t *testing.T) {
	// It validates that borrow/use/return does not deadlock and that
	// putErrorChannel properly drains a possibly-filled channel.

	workers := 32
	iters := 500

	var wg sync.WaitGroup
	wg.Add(workers)

	start := make(chan struct{})

	for w := range workers {
		go func(id int) {
			defer wg.Done()
			<-start

			for i := range iters {
				ch := getErrorChannel()

				// Sometimes leave it dirty, sometimes clean.
				if i%2 == 0 {
					// Fill it (non-blocking due to cap=1).
					select {
					case ch <- errors.New("boom"):
					default:
						// If it blocks here, something is very wrong.
						// But for safety we avoid blocking.
					}
				}

				putErrorChannel(ch)
			}
		}(w)
	}

	close(start)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// ok
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout: potential deadlock in concurrent borrow/return")
	}
}
