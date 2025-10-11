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

// nolint
func TestNewBreakerWithInvalidOptions(t *testing.T) {
	t.Run("With valid options", func(t *testing.T) {
		b, err := NewCircuitBreakerWithValidation(
			WithFailureRate(0.5),
			WithMinRequests(2),
			WithOpenTimeout(50*time.Millisecond),
			WithWindow(100*time.Millisecond, 2),
			WithHalfOpenMaxCalls(0), // Invalid
		)
		require.Error(t, err)
		require.Nil(t, b)
	})
	t.Run("With invalid options", func(t *testing.T) {
		b, err := NewCircuitBreakerWithValidation(
			WithFailureRate(0.5),
			WithMinRequests(2),
			WithOpenTimeout(50*time.Millisecond),
			WithWindow(100*time.Millisecond, 2),
			WithHalfOpenMaxCalls(1),
		)
		require.NoError(t, err)
		require.NotNil(t, b)
	})
}

// nolint
func TestNewBreaker_WithSanitization(t *testing.T) {
	b := NewCircuitBreaker(
		WithFailureRate(0.5),
		WithMinRequests(2),
		WithOpenTimeout(50*time.Millisecond),
		WithWindow(100*time.Millisecond, 2),
		WithHalfOpenMaxCalls(0), // Invalid
	)

	require.NotNil(t, b)
}

// nolint
func TestBreakerAllowsAndBlocks(t *testing.T) {
	b := NewCircuitBreaker(
		WithFailureRate(0.5),
		WithMinRequests(2),
		WithOpenTimeout(50*time.Millisecond),
		WithWindow(100*time.Millisecond, 2),
		WithHalfOpenMaxCalls(1),
	)

	// Initially closed: should allow
	require.True(t, b.tryAllow())

	// Record 2 failures -> exceeds failure rate
	b.onFailure()
	b.onFailure()
	require.Equal(t, Open, b.State())
	require.False(t, b.tryAllow())

	// Wait for open timeout to expire
	pause.For(60 * time.Millisecond)
	require.True(t, b.tryAllow())
	require.Equal(t, HalfOpen, b.State())

	// Success alone is not enough (minRequests=2)
	b.onSuccess()
	require.Equal(t, HalfOpen, b.State())

	// Add another success to meet MinRequests
	b.onSuccess()
	require.Equal(t, Closed, b.State())
}

// nolint
func TestBreakerExecuteSuccess(t *testing.T) {
	b := NewCircuitBreaker(WithClock(func() time.Time {
		return time.Now()
	}))
	ctx := context.Background()

	res, err := b.Execute(ctx, func(_ context.Context) (any, error) {
		return "ok", nil
	})
	require.NoError(t, err)
	require.Equal(t, "ok", res.(string))
	require.Equal(t, Closed, b.State())
}

// nolint
func TestBreakerExecuteFailureAndFallback(t *testing.T) {
	b := NewCircuitBreaker(WithMinRequests(1), WithFailureRate(0.5))
	ctx := context.Background()

	// First call fails
	_, err := b.Execute(ctx, func(_ context.Context) (any, error) {
		return "", errors.New("boom")
	})
	require.Error(t, err)
	require.Equal(t, Open, b.State())

	// Should reject and trigger fallback
	val, err := b.Execute(ctx, func(_ context.Context) (any, error) {
		panic("should not run")
	}, func(_ context.Context, cause error) (any, error) {
		return "fallback", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "fallback", val)
}

// nolint
func TestBreakerContextCancellation(t *testing.T) {
	b := NewCircuitBreaker()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	res, err := b.Execute(ctx, func(_ context.Context) (any, error) {
		pause.For(30 * time.Millisecond)
		return "late", nil
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Nil(t, res)
}

// nolint
func TestMetricsSnapshot(t *testing.T) {
	b := NewCircuitBreaker()
	b.onSuccess()
	b.onFailure()
	metrics := b.Metrics()
	require.Equal(t, uint64(1), metrics.Successes)
	require.Equal(t, uint64(1), metrics.Failures)
	require.Equal(t, uint64(2), metrics.Total)
	require.InDelta(t, 0.5, metrics.FailureRate, 0.0001)
}

// nolint
func TestStateTransitions(t *testing.T) {
	b := NewCircuitBreaker(WithFailureRate(0.5), WithMinRequests(2))

	b.onFailure()
	require.Equal(t, Closed, b.State())
	b.onFailure()
	require.Equal(t, Open, b.State())

	// Manually force half-open
	b.toHalfOpen()
	require.Equal(t, HalfOpen, b.State())

	b.onSuccess()
	require.Equal(t, HalfOpen, b.State())
	b.onSuccess()
	require.Equal(t, Closed, b.State())
}

// nolint
func TestSemaphoreHalfOpen(t *testing.T) {
	b := NewCircuitBreaker(WithHalfOpenMaxCalls(2))
	b.toHalfOpen()
	require.Equal(t, HalfOpen, b.State())

	allowed1 := b.tryAllow()
	allowed2 := b.tryAllow()
	allowed3 := b.tryAllow()

	require.True(t, allowed1)
	require.True(t, allowed2)
	require.False(t, allowed3)

	// Release a permit
	release := b.acquireRelease()
	release()

	allowedAgain := b.tryAllow()
	require.True(t, allowedAgain)
}

// nolint
func TestOpenTimeoutMovesToHalfOpen(t *testing.T) {
	b := NewCircuitBreaker(WithFailureRate(0.5), WithMinRequests(2), WithOpenTimeout(20*time.Millisecond))

	b.onFailure()
	b.onFailure()
	require.Equal(t, Open, b.State())
	pause.For(25 * time.Millisecond)
	// Now TryAllow should move breaker to half-open
	require.True(t, b.tryAllow())
	require.Equal(t, HalfOpen, b.State())
}

// nolint
func TestHardResetAfterIdle(t *testing.T) {
	b := NewCircuitBreaker(WithWindow(50*time.Millisecond, 5))
	b.onFailure()
	before := b.Metrics()
	require.Equal(t, uint64(1), before.Failures)

	// Wait beyond full window
	pause.For(120 * time.Millisecond)
	b.onSuccess()
	after := b.Metrics()
	// Old failures should have been cleared
	require.Equal(t, uint64(0), after.Failures)
	require.Equal(t, uint64(1), after.Successes)
}

// nolint
func TestBreakerExecuteOpenWithoutFallback(t *testing.T) {
	b := NewCircuitBreaker(WithMinRequests(1), WithFailureRate(0.0))
	b.onFailure() // forces Open
	_, err := b.Execute(context.Background(), func(_ context.Context) (any, error) {
		return "ok", nil
	})
	require.ErrorIs(t, err, ErrOpen)
}

// nolint
func TestBreakerHalfOpenRejectsExtraProbes(t *testing.T) {
	b := NewCircuitBreaker(WithHalfOpenMaxCalls(1))
	b.toHalfOpen()
	require.True(t, b.tryAllow())
	require.False(t, b.tryAllow(), "should reject second probe in HalfOpen")
}

// nolint
func TestBreakerPanicHandledAsFailure(t *testing.T) {
	t.Run("With normal panic", func(t *testing.T) {
		b := NewCircuitBreaker(WithMinRequests(1))
		_, err := b.Execute(context.Background(), func(ctx context.Context) (any, error) {
			panic("boom")
		})
		require.Error(t, err)
		require.Equal(t, Open, b.State())
	})
	t.Run("With general error panic", func(t *testing.T) {
		b := NewCircuitBreaker(WithMinRequests(1))
		_, err := b.Execute(context.Background(), func(ctx context.Context) (any, error) {
			panic(errors.New("boom"))
		})
		require.Error(t, err)
		require.Equal(t, Open, b.State())
	})
	t.Run("With goakt panicError", func(t *testing.T) {
		b := NewCircuitBreaker(WithMinRequests(1))
		_, err := b.Execute(context.Background(), func(ctx context.Context) (any, error) {
			panic(gerrors.NewPanicError(errors.New("boom")))
		})
		require.Error(t, err)
		require.Equal(t, Open, b.State())
	})
}

// nolint
func TestBreakerEmptyMetricsSnapshot(t *testing.T) {
	b := NewCircuitBreaker()
	m := b.Metrics()
	assert.Equal(t, uint64(0), m.Total)
	assert.Equal(t, 0.0, m.FailureRate)
}

// nolint
func TestBreakerContextCancelledBeforeExecute(t *testing.T) {
	b := NewCircuitBreaker()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := b.Execute(ctx, func(ctx context.Context) (any, error) {
		return "never", nil
	})
	assert.ErrorIs(t, err, ErrTimeout)
}

// nolint
func TestBreakerFallbackErrorPropagates(t *testing.T) {
	b := NewCircuitBreaker(WithMinRequests(1))
	b.onFailure()
	_, err := b.Execute(context.Background(),
		func(ctx context.Context) (any, error) { return "ok", nil },
		func(ctx context.Context, cause error) (any, error) { return "", errors.New("fallback failed") },
	)
	assert.EqualError(t, err, "fallback failed")
}

// nolint
func TestBreakerTryAllowClosedAlwaysTrue(t *testing.T) {
	b := NewCircuitBreaker()
	assert.True(t, b.tryAllow())
}

// nolint
func TestHalfOpenFailureReopensBreaker(t *testing.T) {
	b := NewCircuitBreaker(WithMinRequests(1), WithFailureRate(0.5))
	b.toHalfOpen()
	require.Equal(t, HalfOpen, b.State())

	b.onFailure()

	require.Equal(t, Open, b.State())
}

// nolint
func TestEnsureSemInitializesWhenUnset(t *testing.T) {
	b := NewCircuitBreaker()
	b.semCh = nil
	// force invalid configuration to take default path
	b.opts.halfOpenMaxCalls = 0

	b.ensureSem()

	require.NotNil(t, b.semCh)
	assert.Equal(t, 1, cap(b.semCh))
}

// nolint
func TestTransitionToReturnsFalseForSameState(t *testing.T) {
	b := NewCircuitBreaker()

	changed := b.transitionTo(Closed)

	require.False(t, changed)
	require.Equal(t, Closed, b.State())
}

// nolint
func TestTransitionToHalfOpenDefaultsMaxCalls(t *testing.T) {
	b := NewCircuitBreaker()
	b.opts.halfOpenMaxCalls = 0

	b.transitionTo(HalfOpen)

	require.Equal(t, HalfOpen, b.State())
	assert.NotNil(t, b.semCh)
	assert.Equal(t, 1, cap(b.semCh))
}
