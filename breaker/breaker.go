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
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/locker"
)

// CircuitBreaker is a thread-safe circuit breaker implementation.
type CircuitBreaker struct {
	_ locker.NoCopy

	state     atomic.Int32 // current State
	openUntil atomic.Int64 // unix nano when the Open state ends

	lastFailure atomic.Int64 // unix nano of the most recent failure
	lastSuccess atomic.Int64 // unix nano of the most recent success

	opts    *options
	buckets *bucketWindow

	// semCh bounds concurrent half-open probes. It is created once with capacity
	// halfOpenMaxCalls and never replaced, so every acquired token is released on
	// the same channel and accounting stays balanced across state transitions.
	semCh chan struct{}

	mu sync.Mutex // serializes state transitions
}

// NewCircuitBreaker constructs a circuit breaker. Invalid option values are
// replaced with their defaults; use NewCircuitBreakerWithValidation to reject
// them instead.
func NewCircuitBreaker(opts ...Option) *CircuitBreaker {
	o := defaultOptions()

	for _, fn := range opts {
		fn(o)
	}

	o.Sanitize()
	return newCircuitBreaker(o)
}

// NewCircuitBreakerWithValidation constructs a circuit breaker and returns an
// error if the provided options are invalid.
func NewCircuitBreakerWithValidation(opts ...Option) (*CircuitBreaker, error) {
	o := defaultOptions()

	for _, fn := range opts {
		fn(o)
	}

	if err := o.Validate(); err != nil {
		return nil, err
	}

	return newCircuitBreaker(o), nil
}

// newCircuitBreaker builds a breaker from sanitized or validated options.
func newCircuitBreaker(o *options) *CircuitBreaker {
	b := &CircuitBreaker{
		opts:    o,
		buckets: newBuckets(o.window, o.buckets, o.clock),
		semCh:   make(chan struct{}, o.halfOpenMaxCalls),
	}

	b.state.Store(int32(Closed))
	return b
}

// State returns the current breaker state.
func (b *CircuitBreaker) State() State { return State(b.state.Load()) }

// Execute runs fn if the breaker allows it, invoking the optional fallback with
// the causing error otherwise. fn runs synchronously in the calling goroutine
// and is expected to honor ctx cancellation. A panic in fn is recovered,
// recorded as a failure and returned as an *Error of type ErrorTypePanic.
//
// Failures are recorded for every error returned by fn except when ctx was
// canceled by the caller: cancellation says nothing about the health of the
// protected resource, so it neither trips nor heals the breaker. A deadline
// expiry counts as a failure.
func (b *CircuitBreaker) Execute(ctx context.Context, fn func(context.Context) (any, error), fallback ...func(context.Context, error) (any, error)) (any, error) {
	if err := ctx.Err(); err != nil {
		return b.withFallback(ctx, contextError(b.State(), err), fallback...)
	}

	allowed, acquired := b.tryAcquire()
	if !allowed {
		return b.withFallback(ctx, ErrOpen, fallback...)
	}

	if acquired {
		defer b.release()
	}

	value, err := b.invoke(ctx, fn)

	switch {
	case err == nil:
		b.record(true)
		return value, nil
	case ctx.Err() == context.Canceled:
		return b.withFallback(ctx, err, fallback...)
	default:
		b.record(false)
		return b.withFallback(ctx, err, fallback...)
	}
}

// Metrics builds a snapshot of the rolling counts and state.
func (b *CircuitBreaker) Metrics() Metrics {
	succ, fail, start, end := b.buckets.snapshot()
	m := Metrics{
		State:       b.State(),
		Successes:   succ,
		Failures:    fail,
		Total:       succ + fail,
		Window:      b.opts.window,
		WindowStart: start,
		WindowEnd:   end,
	}

	if m.Total > 0 {
		m.FailureRate = float64(m.Failures) / float64(m.Total)
	}

	if lf := b.lastFailure.Load(); lf > 0 {
		m.LastFailure = time.Unix(0, lf)
	}

	if ls := b.lastSuccess.Load(); ls > 0 {
		m.LastSuccess = time.Unix(0, ls)
	}

	return m
}

// tryAcquire reports whether a call may proceed and whether it holds a
// half-open token that must be released once the call completes.
func (b *CircuitBreaker) tryAcquire() (allowed, acquired bool) {
	state := b.State()
	if state == Closed {
		return true, false
	}

	if state == Open {
		if b.opts.clock().UnixNano() < b.openUntil.Load() {
			return false, false
		}

		b.toHalfOpen()
	}

	select {
	case b.semCh <- struct{}{}:
		return true, true
	default:
		return false, false
	}
}

// release returns a half-open token. It never blocks because only callers that
// acquired a token release one, on a channel that is never replaced.
func (b *CircuitBreaker) release() {
	<-b.semCh
}

// invoke runs fn, converting a panic into an error.
func (b *CircuitBreaker) invoke(ctx context.Context, fn func(context.Context) (any, error)) (value any, err error) {
	defer func() {
		if r := recover(); r != nil {
			value, err = nil, b.panicError(r)
		}
	}()

	return fn(ctx)
}

// record adds an outcome to the rolling window and re-evaluates the state using
// the totals observed under the same lock acquisition.
func (b *CircuitBreaker) record(success bool) {
	now := b.opts.clock().UnixNano()
	succ, fail := b.buckets.add(now, success)

	if success {
		b.lastSuccess.Store(now)
	} else {
		b.lastFailure.Store(now)
	}

	total := succ + fail
	if total < uint64(b.opts.minRequests) {
		return
	}

	if float64(fail)/float64(total) >= b.opts.failureRate {
		b.toOpen()
		return
	}

	// enough samples with an acceptable failure rate: recover if probing
	if b.State() == HalfOpen {
		b.toClosed()
	}
}

// withFallback invokes the fallback with err if one is provided, otherwise
// returns err.
func (b *CircuitBreaker) withFallback(ctx context.Context, err error, fallback ...func(context.Context, error) (any, error)) (any, error) {
	if len(fallback) > 0 {
		return fallback[0](ctx, err)
	}

	return nil, err
}

// panicError converts a recovered panic value into a structured error.
func (b *CircuitBreaker) panicError(r any) *Error {
	var cause error

	switch v := r.(type) {
	case error:
		if _, ok := errors.AsType[*gerrors.PanicError](v); ok {
			cause = v
		} else {
			cause = gerrors.NewPanicError(v)
		}
	default:
		cause = gerrors.NewPanicError(fmt.Errorf("%v", r))
	}

	return &Error{
		Type:    ErrorTypePanic,
		State:   b.State(),
		Message: "panic during execution",
		Cause:   cause,
	}
}

// contextError wraps a context termination cause into a structured error.
func contextError(state State, cause error) *Error {
	return &Error{
		Type:    ErrorTypeTimeout,
		State:   state,
		Message: "context done before execution",
		Cause:   cause,
	}
}

// transitionTo moves the breaker to the target state, returning false if it is
// already there.
func (b *CircuitBreaker) transitionTo(target State) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if State(b.state.Load()) == target {
		return false
	}

	switch target {
	case Open:
		b.openUntil.Store(b.opts.clock().Add(b.opts.openTimeout).UnixNano())
	case HalfOpen, Closed:
		// reset the window so probing and recovery evaluate fresh samples
		b.buckets.reset()
	}

	b.state.Store(int32(target))
	return true
}

func (b *CircuitBreaker) toOpen() {
	b.transitionTo(Open)
}

func (b *CircuitBreaker) toHalfOpen() {
	b.transitionTo(HalfOpen)
}

func (b *CircuitBreaker) toClosed() {
	b.transitionTo(Closed)
}
