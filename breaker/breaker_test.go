/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package breaker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/internal/pause"
)

func TestBreaker_AllowsAndBlocks(t *testing.T) {
	b := New(
		WithFailureRate(0.5),
		WithMinRequests(2),
		WithOpenTimeout(50*time.Millisecond),
		WithWindow(100*time.Millisecond, 2),
		WithHalfOpenMaxCalls(1),
	)

	// Initially closed: should allow
	require.True(t, b.TryAllow())

	// Record 2 failures -> exceeds failure rate
	b.OnFailure()
	b.OnFailure()
	require.Equal(t, Open, b.State())
	require.False(t, b.TryAllow())

	// Wait for open timeout to expire
	pause.For(60 * time.Millisecond)
	require.True(t, b.TryAllow())
	require.Equal(t, HalfOpen, b.State())

	// Success alone is not enough (minRequests=2)
	b.OnSuccess()
	require.Equal(t, HalfOpen, b.State())

	// Add another success to meet MinRequests
	b.OnSuccess()
	require.Equal(t, Closed, b.State())
}

func TestBreaker_ExecuteSuccess(t *testing.T) {
	b := New()
	ctx := context.Background()

	res, err := b.Execute(ctx, func(ctx context.Context) (any, error) {
		return "ok", nil
	})
	require.NoError(t, err)
	require.Equal(t, "ok", res.(string))
	require.Equal(t, Closed, b.State())
}

func TestBreaker_ExecuteFailureAndFallback(t *testing.T) {
	b := New(WithMinRequests(1), WithFailureRate(0.5))
	ctx := context.Background()

	// First call fails
	_, err := b.Execute(ctx, func(ctx context.Context) (any, error) {
		return "", errors.New("boom")
	})
	require.Error(t, err)
	require.Equal(t, Open, b.State())

	// Should reject and trigger fallback
	val, err := b.Execute(ctx, func(ctx context.Context) (any, error) {
		panic("should not run")
	}, func(ctx context.Context, cause error) (any, error) {
		return "fallback", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "fallback", val)
}

func TestBreaker_ContextCancellation(t *testing.T) {
	b := New()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	res, err := b.Execute(ctx, func(ctx context.Context) (any, error) {
		pause.For(30 * time.Millisecond)
		return "late", nil
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Nil(t, res)
}

func TestMetricsSnapshot(t *testing.T) {
	b := New()
	b.OnSuccess()
	b.OnFailure()
	metrics := b.MetricsSnapshot()
	require.Equal(t, uint64(1), metrics.Successes)
	require.Equal(t, uint64(1), metrics.Failures)
	require.Equal(t, uint64(2), metrics.Total)
	require.InDelta(t, 0.5, metrics.FailureRate, 0.0001)
}

func TestStateTransitions(t *testing.T) {
	b := New(WithFailureRate(0.5), WithMinRequests(2))

	b.OnFailure()
	require.Equal(t, Closed, b.State())
	b.OnFailure()
	require.Equal(t, Open, b.State())

	// Manually force half-open
	b.toHalfOpen()
	require.Equal(t, HalfOpen, b.State())

	b.OnSuccess()
	require.Equal(t, HalfOpen, b.State())
	b.OnSuccess()
	require.Equal(t, Closed, b.State())
}

func TestSemaphoreHalfOpen(t *testing.T) {
	b := New(WithHalfOpenMaxCalls(2))
	b.toHalfOpen()
	require.Equal(t, HalfOpen, b.State())

	allowed1 := b.TryAllow()
	allowed2 := b.TryAllow()
	allowed3 := b.TryAllow()

	require.True(t, allowed1)
	require.True(t, allowed2)
	require.False(t, allowed3)

	// Release a permit
	release := b.acquireRelease()
	release()

	allowedAgain := b.TryAllow()
	require.True(t, allowedAgain)
}

func TestOpenTimeoutMovesToHalfOpen(t *testing.T) {
	b := New(WithFailureRate(0.5), WithMinRequests(2), WithOpenTimeout(20*time.Millisecond))

	b.OnFailure()
	b.OnFailure()
	require.Equal(t, Open, b.State())
	pause.For(25 * time.Millisecond)
	// Now TryAllow should move breaker to half-open
	require.True(t, b.TryAllow())
	require.Equal(t, HalfOpen, b.State())
}

func TestHardResetAfterIdle(t *testing.T) {
	b := New(WithWindow(50*time.Millisecond, 5))
	b.OnFailure()
	before := b.MetricsSnapshot()
	require.Equal(t, uint64(1), before.Failures)

	// Wait beyond full window
	pause.For(120 * time.Millisecond)
	b.OnSuccess()
	after := b.MetricsSnapshot()
	// Old failures should have been cleared
	require.Equal(t, uint64(0), after.Failures)
	require.Equal(t, uint64(1), after.Successes)
}

func TestBreaker_ExecuteOpenWithoutFallback(t *testing.T) {
	b := New(WithMinRequests(1), WithFailureRate(0.0))
	b.OnFailure() // forces Open
	_, err := b.Execute(context.Background(), func(ctx context.Context) (any, error) {
		return "ok", nil
	})
	require.ErrorIs(t, err, ErrOpen)
}

func TestBreaker_HalfOpenRejectsExtraProbes(t *testing.T) {
	b := New(WithHalfOpenMaxCalls(1))
	b.toHalfOpen()
	require.True(t, b.TryAllow())
	require.False(t, b.TryAllow(), "should reject second probe in HalfOpen")
}

func TestBreaker_PanicHandledAsFailure(t *testing.T) {
	t.Run("With normal panic", func(t *testing.T) {
		b := New(WithMinRequests(1))
		_, err := b.Execute(context.Background(), func(ctx context.Context) (any, error) {
			panic("boom")
		})
		require.Error(t, err)
		require.Equal(t, Open, b.State())
	})
	t.Run("With general error panic", func(t *testing.T) {
		b := New(WithMinRequests(1))
		_, err := b.Execute(context.Background(), func(ctx context.Context) (any, error) {
			panic(errors.New("boom"))
		})
		require.Error(t, err)
		require.Equal(t, Open, b.State())
	})
	t.Run("With goakt panicError", func(t *testing.T) {
		b := New(WithMinRequests(1))
		_, err := b.Execute(context.Background(), func(ctx context.Context) (any, error) {
			panic(gerrors.NewPanicError(errors.New("boom")))
		})
		require.Error(t, err)
		require.Equal(t, Open, b.State())
	})
}

func TestBreaker_EmptyMetricsSnapshot(t *testing.T) {
	b := New()
	m := b.MetricsSnapshot()
	assert.Equal(t, uint64(0), m.Total)
	assert.Equal(t, 0.0, m.FailureRate)
}

func TestBreaker_ContextCancelledBeforeExecute(t *testing.T) {
	b := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := b.Execute(ctx, func(ctx context.Context) (any, error) {
		return "never", nil
	})
	assert.ErrorIs(t, err, ErrTimeout)
}

func TestBreaker_FallbackErrorPropagates(t *testing.T) {
	b := New(WithMinRequests(1))
	b.OnFailure()
	_, err := b.Execute(context.Background(),
		func(ctx context.Context) (any, error) { return "ok", nil },
		func(ctx context.Context, cause error) (any, error) { return "", errors.New("fallback failed") },
	)
	assert.EqualError(t, err, "fallback failed")
}

func TestBreaker_TryAllowClosedAlwaysTrue(t *testing.T) {
	b := New()
	assert.True(t, b.TryAllow())
}
