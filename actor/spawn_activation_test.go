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
	"errors"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v4/internal/pause"
)

func TestSpawnActivationGroup(t *testing.T) {
	t.Run("runs the spawn on the caller's goroutine", func(t *testing.T) {
		var group spawnActivationGroup
		want := &PID{}
		inline := false
		pid, shared, err := group.run(context.Background(), "key", func() (*PID, error) {
			// the test function is on the stack only when fn runs inline
			inline = strings.Contains(string(debug.Stack()), "TestSpawnActivationGroup")
			return want, nil
		})
		require.NoError(t, err)
		require.Same(t, want, pid)
		require.False(t, shared)
		require.True(t, inline)
		require.Empty(t, group.calls, "the call is removed once it has returned")
	})

	t.Run("returns the spawn's error", func(t *testing.T) {
		var group spawnActivationGroup
		want := errors.New("spawn failed")
		pid, shared, err := group.run(context.Background(), "key", func() (*PID, error) {
			return nil, want
		})
		require.ErrorIs(t, err, want)
		require.Nil(t, pid)
		require.False(t, shared)
	})

	t.Run("shares an in-flight result with the callers that join it", func(t *testing.T) {
		var group spawnActivationGroup
		want := &PID{}
		entered := make(chan struct{})
		release := make(chan struct{})
		runs := atomic.NewInt32(0)

		type result struct {
			pid    *PID
			shared bool
			err    error
		}

		winner := make(chan result, 1)
		go func() {
			pid, shared, err := group.run(context.Background(), "key", func() (*PID, error) {
				runs.Inc()
				close(entered)
				<-release
				return want, nil
			})
			winner <- result{pid, shared, err}
		}()
		<-entered

		waiter := make(chan result, 1)
		go func() {
			pid, shared, err := group.run(context.Background(), "key", func() (*PID, error) {
				runs.Inc()
				return nil, errors.New("the waiter's spawn must not run")
			})
			waiter <- result{pid, shared, err}
		}()

		// the waiter joins the in-flight call instead of starting its own
		require.Eventually(t, func() bool {
			group.mu.Lock()
			defer group.mu.Unlock()
			return group.calls["key"].waiters == 1
		}, time.Second, 5*time.Millisecond)

		close(release)
		got := <-winner
		require.NoError(t, got.err)
		require.Same(t, want, got.pid)
		require.True(t, got.shared, "the winner learns its result was shared")

		got = <-waiter
		require.NoError(t, got.err)
		require.Same(t, want, got.pid)
		require.True(t, got.shared)
		require.Equal(t, int32(1), runs.Load())
	})

	t.Run("a waiter leaves when its context ends", func(t *testing.T) {
		var group spawnActivationGroup
		entered := make(chan struct{})
		release := make(chan struct{})
		winnerDone := make(chan struct{})
		go func() {
			defer close(winnerDone)
			_, _, _ = group.run(context.Background(), "key", func() (*PID, error) {
				close(entered)
				<-release
				return &PID{}, nil
			})
		}()
		<-entered

		ctx, cancel := context.WithCancel(context.Background())
		waiterErr := make(chan error, 1)
		go func() {
			_, _, err := group.run(ctx, "key", func() (*PID, error) {
				return nil, errors.New("the waiter's spawn must not run")
			})
			waiterErr <- err
		}()
		pause.For(50 * time.Millisecond) // let the waiter join the call
		cancel()
		require.ErrorIs(t, <-waiterErr, context.Canceled)

		// the call it left is still in flight for its winner
		close(release)
		<-winnerDone
	})

	t.Run("runs the spawn again once the earlier call has returned", func(t *testing.T) {
		var group spawnActivationGroup
		runs := 0
		for range 2 {
			_, shared, err := group.run(context.Background(), "key", func() (*PID, error) {
				runs++
				return &PID{}, nil
			})
			require.NoError(t, err)
			require.False(t, shared)
		}
		require.Equal(t, 2, runs)
	})

	t.Run("a panic in the spawn fails the waiters and reaches the caller", func(t *testing.T) {
		var group spawnActivationGroup
		entered := make(chan struct{})
		release := make(chan struct{})
		recovered := make(chan any, 1)
		go func() {
			defer func() { recovered <- recover() }()
			_, _, _ = group.run(context.Background(), "key", func() (*PID, error) {
				close(entered)
				<-release
				panic("spawn panicked")
			})
		}()
		<-entered

		waiterErr := make(chan error, 1)
		go func() {
			_, _, err := group.run(context.Background(), "key", func() (*PID, error) {
				return nil, errors.New("the waiter's spawn must not run")
			})
			waiterErr <- err
		}()
		pause.For(50 * time.Millisecond) // let the waiter join the call

		close(release)
		require.Equal(t, "spawn panicked", <-recovered)
		require.ErrorContains(t, <-waiterErr, "spawn activation of key panicked")
		require.Empty(t, group.calls)
	})
}
