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

package retry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	// testInitialDelay keeps the wait between attempts short enough for the tests.
	testInitialDelay = time.Millisecond
	// testMaxDelay caps the wait between attempts in the tests.
	testMaxDelay = 2 * time.Millisecond
	// testContextKey is the key carried by the context handed to RunContext.
	testContextKey contextKey = "retry-test-key"
	// testContextValue is the value carried by the context handed to RunContext.
	testContextValue = "retry-test-value"
)

// contextKey types the context key the tests use, so the value cannot collide
// with another package's key.
type contextKey string

// TestNewRetrierDefaults checks that non-positive arguments fall back to the defaults
// and that valid arguments are kept.
func TestNewRetrierDefaults(t *testing.T) {
	t.Run("With zero values", func(t *testing.T) {
		retrier := NewRetrier(0, 0, 0)
		require.Equal(t, DefaultMaxTries, retrier.maxTries)
		require.Equal(t, DefaultInitialDelay, retrier.initialDelay)
		require.Equal(t, DefaultMaxDelay, retrier.maxDelay)
	})

	t.Run("With negative values", func(t *testing.T) {
		retrier := NewRetrier(-1, -time.Second, -time.Second)
		require.Equal(t, DefaultMaxTries, retrier.maxTries)
		require.Equal(t, DefaultInitialDelay, retrier.initialDelay)
		require.Equal(t, DefaultMaxDelay, retrier.maxDelay)
	})

	t.Run("With valid values", func(t *testing.T) {
		retrier := NewRetrier(3, time.Millisecond, 2*time.Millisecond)
		require.Equal(t, 3, retrier.maxTries)
		require.Equal(t, time.Millisecond, retrier.initialDelay)
		require.Equal(t, 2*time.Millisecond, retrier.maxDelay)
	})
}

// TestRunContextSucceedsOnFirstAttempt checks that an operation that succeeds is run once.
func TestRunContextSucceedsOnFirstAttempt(t *testing.T) {
	attempts := 0
	retrier := NewRetrier(3, testInitialDelay, testMaxDelay)

	err := retrier.RunContext(context.Background(), func(context.Context) error {
		attempts++
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, 1, attempts)
}

// TestRunContextRetriesUntilSuccess checks that a failing operation is retried until it succeeds.
func TestRunContextRetriesUntilSuccess(t *testing.T) {
	attempts := 0
	retrier := NewRetrier(5, testInitialDelay, testMaxDelay)

	err := retrier.RunContext(context.Background(), func(context.Context) error {
		attempts++
		if attempts < 3 {
			return errors.New("attempt failed")
		}

		return nil
	})

	require.NoError(t, err)
	require.Equal(t, 3, attempts)
}

// TestRunContextReturnsLastErrorWhenExhausted checks that the operation error is returned
// unchanged once the attempts are exhausted.
func TestRunContextReturnsLastErrorWhenExhausted(t *testing.T) {
	sentinel := errors.New("operation failed")
	attempts := 0
	retrier := NewRetrier(3, testInitialDelay, testMaxDelay)

	err := retrier.RunContext(context.Background(), func(context.Context) error {
		attempts++
		return sentinel
	})

	require.Equal(t, sentinel, err)
	require.Equal(t, 3, attempts)
}

// TestRunContextStopsOnTerminalError checks that an error marked with Stop ends the run at once.
func TestRunContextStopsOnTerminalError(t *testing.T) {
	sentinel := errors.New("terminal failure")
	attempts := 0
	retrier := NewRetrier(5, testInitialDelay, testMaxDelay)

	err := retrier.RunContext(context.Background(), func(context.Context) error {
		attempts++
		return Stop(sentinel)
	})

	require.Equal(t, sentinel, err)
	require.Equal(t, 1, attempts)
}

// TestRunContextTreatsStopNilAsSuccess checks that Stop(nil) is a success rather than a failure.
func TestRunContextTreatsStopNilAsSuccess(t *testing.T) {
	attempts := 0
	retrier := NewRetrier(5, testInitialDelay, testMaxDelay)

	err := retrier.RunContext(context.Background(), func(context.Context) error {
		attempts++
		return Stop(nil)
	})

	require.NoError(t, err)
	require.Equal(t, 1, attempts)
}

// TestRunContextReturnsLastErrorWhenContextEnds checks that a context that ends during the
// wait yields the operation error instead of the context error.
func TestRunContextReturnsLastErrorWhenContextEnds(t *testing.T) {
	sentinel := errors.New("operation failed")
	attempts := 0
	retrier := NewRetrier(5, time.Second, 2*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := retrier.RunContext(ctx, func(context.Context) error {
		attempts++
		return sentinel
	})

	require.Equal(t, sentinel, err)
	require.False(t, errors.Is(err, context.DeadlineExceeded))
	require.Equal(t, 1, attempts)
}

// TestRunContextPassesTheContext checks that the caller context reaches the operation.
func TestRunContextPassesTheContext(t *testing.T) {
	var received context.Context
	retrier := NewRetrier(3, testInitialDelay, testMaxDelay)
	ctx := context.WithValue(context.Background(), testContextKey, testContextValue)

	err := retrier.RunContext(ctx, func(opCtx context.Context) error {
		received = opCtx
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, ctx, received)
	require.Equal(t, testContextValue, received.Value(testContextKey))
}

// TestRunUsesABackgroundContext checks that Run retries the operation without a caller context.
func TestRunUsesABackgroundContext(t *testing.T) {
	attempts := 0
	retrier := NewRetrier(3, testInitialDelay, testMaxDelay)

	err := retrier.Run(func() error {
		attempts++
		if attempts < 2 {
			return errors.New("attempt failed")
		}

		return nil
	})

	require.NoError(t, err)
	require.Equal(t, 2, attempts)
}

// TestStop checks that the terminal marker keeps the message and unwraps to the original error.
func TestStop(t *testing.T) {
	sentinel := errors.New("terminal failure")

	require.Equal(t, sentinel.Error(), Stop(sentinel).Error())
	require.ErrorIs(t, Stop(sentinel), sentinel)
	require.NoError(t, Stop(nil))
}
