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
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/internal/pause"
)

// TestAskTimer verifies the Ask timeout: it delivers askTimedOut into the
// Ask's own reply channel when it expires, delivers nothing once stopped, and
// never blocks on or replaces a reply that is already there.
func TestAskTimer(t *testing.T) {
	t.Run("delivers the timeout into the reply channel", func(t *testing.T) {
		reply := make(chan any, 1)
		timer := startAskTimer(reply, time.Millisecond)

		select {
		case value := <-reply:
			require.IsType(t, askTimedOut{}, value)
		case <-time.After(time.Second):
			t.Fatal("the timeout was never delivered")
		}

		// stopping a timer that already fired is allowed, does nothing and
		// reports that it came too late
		require.False(t, timer.stop())
	})

	t.Run("delivers nothing once stopped", func(t *testing.T) {
		reply := make(chan any, 1)
		timer := startAskTimer(reply, 20*time.Millisecond)
		require.True(t, timer.stop(), "a timer stopped before it fired reports so")

		select {
		case value := <-reply:
			t.Fatalf("a stopped timer delivered %v", value)
		case <-time.After(60 * time.Millisecond):
		}
	})

	t.Run("a timer reused after a stop times the new Ask only", func(t *testing.T) {
		first := make(chan any, 1)
		startAskTimer(first, time.Hour).stop()

		second := make(chan any, 1)
		timer := startAskTimer(second, time.Millisecond)

		select {
		case value := <-second:
			require.IsType(t, askTimedOut{}, value)
		case <-time.After(time.Second):
			t.Fatal("the timeout was never delivered to the second Ask")
		}

		require.Empty(t, first, "the first Ask must not be timed out")
		timer.stop()
	})

	t.Run("leaves a reply that is already there", func(t *testing.T) {
		reply := make(chan any, 1)
		reply <- "answer"

		timer := &askTimer{}
		timer.reply.Store(reply)
		timer.fire()

		require.Equal(t, "answer", <-reply)
		require.Empty(t, reply)
	})
}

func TestAckTimer(t *testing.T) {
	t.Run("delivers the timeout into the acknowledgment channel", func(t *testing.T) {
		ack := make(chan error, 1)
		timer := startAckTimer(ack, time.Millisecond)

		select {
		case err := <-ack:
			require.ErrorIs(t, err, errAckTimedOut)
		case <-time.After(time.Second):
			t.Fatal("the timeout was never delivered")
		}

		require.False(t, timer.stop())
	})

	t.Run("delivers nothing once stopped", func(t *testing.T) {
		ack := make(chan error, 1)
		timer := startAckTimer(ack, 20*time.Millisecond)
		require.True(t, timer.stop())

		select {
		case err := <-ack:
			t.Fatalf("a stopped timer delivered %v", err)
		case <-time.After(60 * time.Millisecond):
		}
	})

	t.Run("does not overwrite an acknowledgment already there", func(t *testing.T) {
		ack := make(chan error, 1)
		ack <- nil
		timer := startAckTimer(ack, time.Millisecond)
		pause.For(20 * time.Millisecond)
		require.NoError(t, <-ack)
		require.False(t, timer.stop())
	})
}
