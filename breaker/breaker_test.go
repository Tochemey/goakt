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

package breaker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gerrors "github.com/tochemey/goakt/v4/errors"
)

// fakeClock is a deterministic clock for tests.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(1_700_000_000, 0)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func TestNewCircuitBreakerWithValidation(t *testing.T) {
	t.Run("invalid options are rejected", func(t *testing.T) {
		b, err := NewCircuitBreakerWithValidation(WithHalfOpenMaxCalls(0))
		require.Error(t, err)
		require.Nil(t, b)
	})
	t.Run("valid options are accepted", func(t *testing.T) {
		b, err := NewCircuitBreakerWithValidation(
			WithFailureRate(0.5),
			WithMinRequests(2),
			WithOpenTimeout(50*time.Millisecond),
			WithWindow(100*time.Millisecond, 2),
			WithHalfOpenMaxCalls(1),
		)
		require.NoError(t, err)
		require.NotNil(t, b)
		require.Equal(t, Closed, b.State())
	})
}

func TestNewCircuitBreakerSanitizesInvalidOptions(t *testing.T) {
	b := NewCircuitBreaker(
		WithFailureRate(-1),
		WithMinRequests(0),
		WithOpenTimeout(0),
		WithWindow(0, 0),
		WithHalfOpenMaxCalls(0),
		WithClock(nil),
	)

	require.NotNil(t, b)
	d := defaultOptions()
	require.Equal(t, d.failureRate, b.opts.failureRate)
	require.Equal(t, d.minRequests, b.opts.minRequests)
	require.Equal(t, d.openTimeout, b.opts.openTimeout)
	require.Equal(t, d.window, b.opts.window)
	require.Equal(t, d.buckets, b.opts.buckets)
	require.Equal(t, d.halfOpenMaxCalls, cap(b.semCh))
	require.NotNil(t, b.opts.clock)
}

func TestExecuteSuccess(t *testing.T) {
	b := NewCircuitBreaker()
	res, err := b.Execute(context.Background(), func(context.Context) (any, error) {
		return "ok", nil
	})

	require.NoError(t, err)
	require.Equal(t, "ok", res)
	require.Equal(t, Closed, b.State())

	m := b.Metrics()
	require.Equal(t, uint64(1), m.Successes)
	require.Equal(t, uint64(0), m.Failures)
}

func TestExecuteFailureOpensBreaker(t *testing.T) {
	b := NewCircuitBreaker(WithMinRequests(1), WithFailureRate(0.5))
	boom := errors.New("boom")

	_, err := b.Execute(context.Background(), func(context.Context) (any, error) {
		return nil, boom
	})

	require.ErrorIs(t, err, boom)
	require.Equal(t, Open, b.State())
}

func TestExecuteOpenWithoutFallback(t *testing.T) {
	b := NewCircuitBreaker(WithMinRequests(1), WithFailureRate(0.0))
	_, err := b.Execute(context.Background(), func(context.Context) (any, error) {
		return nil, errors.New("boom")
	})
	require.Error(t, err)
	require.Equal(t, Open, b.State())

	_, err = b.Execute(context.Background(), func(context.Context) (any, error) {
		t.Fatal("must not run while open")
		return nil, nil
	})

	require.ErrorIs(t, err, ErrOpen)
}

func TestExecuteOpenInvokesFallbackWithErrOpen(t *testing.T) {
	b := NewCircuitBreaker(WithMinRequests(1), WithFailureRate(0.0))
	_, _ = b.Execute(context.Background(), func(context.Context) (any, error) {
		return nil, errors.New("boom")
	})
	require.Equal(t, Open, b.State())

	var got error
	val, err := b.Execute(context.Background(),
		func(context.Context) (any, error) { return nil, nil },
		func(_ context.Context, cause error) (any, error) {
			got = cause
			return "fallback", nil
		},
	)

	require.NoError(t, err)
	require.Equal(t, "fallback", val)
	require.ErrorIs(t, got, ErrOpen)
}

func TestExecuteFallbackReceivesFunctionError(t *testing.T) {
	b := NewCircuitBreaker()
	boom := errors.New("boom")

	val, err := b.Execute(context.Background(),
		func(context.Context) (any, error) { return nil, boom },
		func(_ context.Context, cause error) (any, error) { return nil, cause },
	)

	require.Nil(t, val)
	require.ErrorIs(t, err, boom)
}

func TestExecuteFallbackErrorPropagates(t *testing.T) {
	b := NewCircuitBreaker(WithMinRequests(1), WithFailureRate(0.0))
	_, _ = b.Execute(context.Background(), func(context.Context) (any, error) {
		return nil, errors.New("boom")
	})

	_, err := b.Execute(context.Background(),
		func(context.Context) (any, error) { return "ok", nil },
		func(context.Context, error) (any, error) { return nil, errors.New("fallback failed") },
	)

	assert.EqualError(t, err, "fallback failed")
}

func TestExecuteContextDoneBeforeExecution(t *testing.T) {
	t.Run("canceled context", func(t *testing.T) {
		b := NewCircuitBreaker()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := b.Execute(ctx, func(context.Context) (any, error) {
			t.Fatal("must not run")
			return nil, nil
		})

		require.ErrorIs(t, err, ErrTimeout)
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, uint64(0), b.Metrics().Total, "nothing should be recorded")
	})
	t.Run("expired deadline", func(t *testing.T) {
		b := NewCircuitBreaker()
		ctx, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
		defer cancel()

		_, err := b.Execute(ctx, func(context.Context) (any, error) {
			t.Fatal("must not run")
			return nil, nil
		})

		require.ErrorIs(t, err, ErrTimeout)
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})
	t.Run("with fallback", func(t *testing.T) {
		b := NewCircuitBreaker()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		val, err := b.Execute(ctx,
			func(context.Context) (any, error) { return nil, nil },
			func(context.Context, error) (any, error) { return "fallback", nil },
		)

		require.NoError(t, err)
		require.Equal(t, "fallback", val)
	})
}

func TestExecuteCallerCancellationIsNotRecorded(t *testing.T) {
	b := NewCircuitBreaker(WithMinRequests(1), WithFailureRate(0.0))
	ctx, cancel := context.WithCancel(context.Background())

	_, err := b.Execute(ctx, func(ctx context.Context) (any, error) {
		cancel()
		<-ctx.Done()
		return nil, ctx.Err()
	})

	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, Closed, b.State(), "caller cancellation must not trip the breaker")
	require.Equal(t, uint64(0), b.Metrics().Total)
}

func TestExecuteDeadlineExpiryIsRecordedAsFailure(t *testing.T) {
	b := NewCircuitBreaker(WithMinRequests(1), WithFailureRate(0.5))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := b.Execute(ctx, func(ctx context.Context) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, Open, b.State(), "deadline expiry must count as a failure")
}

func TestExecutePanicHandledAsFailure(t *testing.T) {
	assertPanicError := func(t *testing.T, err error, b *CircuitBreaker) {
		t.Helper()

		var be *Error
		require.ErrorAs(t, err, &be)
		require.Equal(t, ErrorTypePanic, be.Type)

		var pe *gerrors.PanicError
		require.ErrorAs(t, err, &pe)
		require.Equal(t, Open, b.State())
	}

	t.Run("string panic", func(t *testing.T) {
		b := NewCircuitBreaker(WithMinRequests(1))
		_, err := b.Execute(context.Background(), func(context.Context) (any, error) {
			panic("boom")
		})
		assertPanicError(t, err, b)
	})
	t.Run("error panic", func(t *testing.T) {
		b := NewCircuitBreaker(WithMinRequests(1))
		_, err := b.Execute(context.Background(), func(context.Context) (any, error) {
			panic(errors.New("boom"))
		})
		assertPanicError(t, err, b)
	})
	t.Run("goakt PanicError panic", func(t *testing.T) {
		b := NewCircuitBreaker(WithMinRequests(1))
		_, err := b.Execute(context.Background(), func(context.Context) (any, error) {
			panic(gerrors.NewPanicError(errors.New("boom")))
		})
		assertPanicError(t, err, b)
	})
}

func TestTryAcquireClosedNeedsNoToken(t *testing.T) {
	b := NewCircuitBreaker()
	allowed, acquired := b.tryAcquire()

	require.True(t, allowed)
	require.False(t, acquired)
	require.Empty(t, b.semCh)
}

func TestOpenRejectsUntilTimeoutThenRecovers(t *testing.T) {
	clock := newFakeClock()
	b := NewCircuitBreaker(
		WithClock(clock.Now),
		WithMinRequests(1),
		WithFailureRate(0.5),
		WithOpenTimeout(time.Second),
		WithWindow(10*time.Second, 5),
	)

	_, err := b.Execute(context.Background(), func(context.Context) (any, error) {
		return nil, errors.New("boom")
	})
	require.Error(t, err)
	require.Equal(t, Open, b.State())

	allowed, acquired := b.tryAcquire()
	require.False(t, allowed)
	require.False(t, acquired)

	clock.Advance(1500 * time.Millisecond)

	res, err := b.Execute(context.Background(), func(context.Context) (any, error) {
		return "ok", nil
	})

	require.NoError(t, err)
	require.Equal(t, "ok", res)
	require.Equal(t, Closed, b.State(), "successful probe with minRequests=1 must close the breaker")
}

func TestOpenTimeoutMovesToHalfOpen(t *testing.T) {
	clock := newFakeClock()
	b := NewCircuitBreaker(
		WithClock(clock.Now),
		WithMinRequests(1),
		WithFailureRate(0.5),
		WithOpenTimeout(time.Second),
	)

	b.record(false)
	require.Equal(t, Open, b.State())

	clock.Advance(2 * time.Second)

	allowed, acquired := b.tryAcquire()
	require.True(t, allowed)
	require.True(t, acquired)
	require.Equal(t, HalfOpen, b.State())
	b.release()
}

func TestHalfOpenCapsConcurrentProbes(t *testing.T) {
	clock := newFakeClock()
	b := NewCircuitBreaker(
		WithClock(clock.Now),
		WithMinRequests(2),
		WithFailureRate(0.5),
		WithOpenTimeout(time.Second),
		WithHalfOpenMaxCalls(2),
	)

	b.record(false)
	b.record(false)
	require.Equal(t, Open, b.State())
	clock.Advance(2 * time.Second)

	unblock := make(chan struct{})
	started := make(chan struct{}, 2)
	results := make(chan error, 2)

	for range 2 {
		go func() {
			_, err := b.Execute(context.Background(), func(context.Context) (any, error) {
				started <- struct{}{}
				<-unblock
				return nil, nil
			})
			results <- err
		}()
	}

	<-started
	<-started
	require.Equal(t, HalfOpen, b.State())

	// both permits are held by the in-flight probes: further calls are rejected
	_, err := b.Execute(context.Background(), func(context.Context) (any, error) {
		t.Error("must not run beyond halfOpenMaxCalls")
		return nil, nil
	})
	require.ErrorIs(t, err, ErrOpen)

	close(unblock)
	require.NoError(t, <-results)
	require.NoError(t, <-results)
	require.Equal(t, Closed, b.State(), "two successful probes meet minRequests and close the breaker")
	require.Empty(t, b.semCh, "all tokens must be released")
}

func TestHalfOpenFailureReopensBreaker(t *testing.T) {
	clock := newFakeClock()
	b := NewCircuitBreaker(
		WithClock(clock.Now),
		WithMinRequests(1),
		WithFailureRate(0.5),
		WithOpenTimeout(time.Second),
	)

	b.record(false)
	require.Equal(t, Open, b.State())
	clock.Advance(2 * time.Second)

	_, err := b.Execute(context.Background(), func(context.Context) (any, error) {
		return nil, errors.New("still failing")
	})

	require.Error(t, err)
	require.Equal(t, Open, b.State())

	// the open timeout must be re-armed
	allowed, _ := b.tryAcquire()
	require.False(t, allowed)
	require.Empty(t, b.semCh, "the probe token must be released after reopening")
}

func TestHalfOpenStaysUntilEnoughSamples(t *testing.T) {
	clock := newFakeClock()
	b := NewCircuitBreaker(
		WithClock(clock.Now),
		WithMinRequests(3),
		WithFailureRate(0.5),
		WithOpenTimeout(time.Second),
		WithHalfOpenMaxCalls(3),
	)

	b.record(false)
	b.record(false)
	b.record(false)
	require.Equal(t, Open, b.State())
	clock.Advance(2 * time.Second)

	execute := func() {
		_, err := b.Execute(context.Background(), func(context.Context) (any, error) {
			return nil, nil
		})
		require.NoError(t, err)
	}

	execute()
	require.Equal(t, HalfOpen, b.State())
	execute()
	require.Equal(t, HalfOpen, b.State())
	execute()
	require.Equal(t, Closed, b.State())
}

func TestTransitionToSameStateReturnsFalse(t *testing.T) {
	b := NewCircuitBreaker()
	require.False(t, b.transitionTo(Closed))
	require.True(t, b.transitionTo(Open))
	require.False(t, b.transitionTo(Open))
}

func TestMetricsSnapshot(t *testing.T) {
	clock := newFakeClock()
	b := NewCircuitBreaker(
		WithClock(clock.Now),
		WithWindow(time.Minute, 6),
	)

	b.record(true)
	clock.Advance(time.Second)
	b.record(false)

	m := b.Metrics()
	require.Equal(t, Closed, m.State)
	require.Equal(t, uint64(1), m.Successes)
	require.Equal(t, uint64(1), m.Failures)
	require.Equal(t, uint64(2), m.Total)
	require.InDelta(t, 0.5, m.FailureRate, 0.0001)
	require.Equal(t, time.Minute, m.Window)
	require.Equal(t, clock.Now(), m.WindowEnd)
	require.Equal(t, clock.Now().Add(-time.Minute), m.WindowStart)
	require.Equal(t, clock.Now(), m.LastFailure)
	require.Equal(t, clock.Now().Add(-time.Second), m.LastSuccess)
}

func TestMetricsEmptySnapshot(t *testing.T) {
	b := NewCircuitBreaker()
	m := b.Metrics()

	assert.Equal(t, uint64(0), m.Total)
	assert.Equal(t, 0.0, m.FailureRate)
	assert.True(t, m.LastFailure.IsZero())
	assert.True(t, m.LastSuccess.IsZero())
}

func TestHardResetAfterIdle(t *testing.T) {
	clock := newFakeClock()
	b := NewCircuitBreaker(
		WithClock(clock.Now),
		WithWindow(50*time.Millisecond, 5),
	)

	b.record(false)
	require.Equal(t, uint64(1), b.Metrics().Failures)

	clock.Advance(120 * time.Millisecond)
	b.record(true)

	m := b.Metrics()
	require.Equal(t, uint64(0), m.Failures, "counts older than the window must be dropped")
	require.Equal(t, uint64(1), m.Successes)
}

func TestExecuteSuccessPathDoesNotAllocate(t *testing.T) {
	b := NewCircuitBreaker()
	ctx := context.Background()
	fn := func(context.Context) (any, error) { return nil, nil }

	allocs := testing.AllocsPerRun(100, func() {
		if _, err := b.Execute(ctx, fn); err != nil {
			t.Fatal(err)
		}
	})

	require.Zero(t, allocs, "closed-state success path must not allocate")
}

func TestConcurrentExecuteAcrossTransitions(t *testing.T) {
	b := NewCircuitBreaker(
		WithFailureRate(0.5),
		WithMinRequests(1),
		WithOpenTimeout(time.Millisecond),
		WithWindow(10*time.Millisecond, 2),
		WithHalfOpenMaxCalls(2),
	)

	ctx := context.Background()
	boom := errors.New("boom")
	var wg sync.WaitGroup

	for i := range 16 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			for j := range 500 {
				fail := (i+j)%2 == 0
				_, _ = b.Execute(ctx, func(context.Context) (any, error) {
					if fail {
						return nil, boom
					}
					return nil, nil
				})
			}
		}(i)
	}

	wg.Wait()
	require.Contains(t, []State{Closed, Open, HalfOpen}, b.State())
	require.LessOrEqual(t, len(b.semCh), cap(b.semCh))
}
