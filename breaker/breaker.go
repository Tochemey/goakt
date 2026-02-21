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
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/locker"
)

// executionResult holds the result of a function execution
type executionResult struct {
	value any
	err   error
}

// CircuitBreaker is a thread-safe circuit breaker implementation.
type CircuitBreaker struct {
	_         locker.NoCopy
	state     int32 // atomic
	openUntil int64 // unix nano when Open ends

	opts *options

	buckets *bucketWindow
	mu      sync.Mutex // guards transitions & listeners

	// half-open semaphore
	semCh chan struct{}

	lastFailure atomic.Uint64 // unix nano
	lastSuccess atomic.Uint64 // unix nano
}

// NewCircuitBreaker constructs a circuit breaker.
func NewCircuitBreaker(opts ...Option) *CircuitBreaker {
	o := defaultOptions()

	for _, fn := range opts {
		fn(o)
	}

	// Note: We don't return an error from New to maintain backward compatibility
	// Instead, we apply sensible defaults for invalid values
	o.Sanitize()

	bw := newBuckets(o.window, o.buckets, o.clock)
	b := &CircuitBreaker{
		state:       int32(Closed),
		openUntil:   0,
		opts:        o,
		buckets:     bw,
		lastFailure: atomic.Uint64{},
		lastSuccess: atomic.Uint64{},
	}
	return b
}

// NewCircuitBreakerWithValidation constructs a circuit breaker with validation.
// Returns an error if the provided options are invalid.
func NewCircuitBreakerWithValidation(opts ...Option) (*CircuitBreaker, error) {
	o := defaultOptions()

	for _, fn := range opts {
		fn(o)
	}

	if err := o.Validate(); err != nil {
		return nil, err
	}

	bw := newBuckets(o.window, o.buckets, o.clock)
	b := &CircuitBreaker{
		state:       int32(Closed),
		openUntil:   0,
		opts:        o,
		buckets:     bw,
		lastFailure: atomic.Uint64{},
		lastSuccess: atomic.Uint64{},
	}
	return b, nil
}

// State returns the current breaker state.
func (b *CircuitBreaker) State() State { return State(atomic.LoadInt32(&b.state)) }

// Execute runs fn if allowed. If the breaker is open, it optionally calls fallback.
// It propagates ctx cancellation.
func (b *CircuitBreaker) Execute(ctx context.Context, fn func(context.Context) (any, error), fallback ...func(context.Context, error) (any, error)) (any, error) {
	if !b.tryAllow() {
		return b.handleRejection(ctx, ErrOpen, fallback...)
	}

	release := b.acquireRelease()
	defer release()

	return b.executeWithTimeout(ctx, fn, fallback...)
}

// tryAllow returns whether a call is permitted at this moment.
// If it returns false, callers should not proceed.
func (b *CircuitBreaker) tryAllow() bool {
	now := b.opts.clock()
	s := b.State()
	if s == Closed {
		return true
	}

	if s == Open {
		if now.UnixNano() >= atomic.LoadInt64(&b.openUntil) {
			b.toHalfOpen()
			// fallthrough to half-open handling
		} else {
			return false
		}
	}
	// Half-open: enforce semaphore
	b.ensureSem()
	select {
	case b.semCh <- struct{}{}:
		return true
	default:
		return false
	}
}

// onSuccess records a successful call.
func (b *CircuitBreaker) onSuccess() {
	b.buckets.addSuccess(1)
	b.lastSuccess.Store(uint64(b.opts.clock().UnixNano()))
	if b.State() == HalfOpen {
		// stricter recovery: only close when enough samples collected
		b.evaluateHalfOpen()
	} else {
		b.evaluate()
	}
}

// onFailure records a failed call.
func (b *CircuitBreaker) onFailure() {
	b.buckets.addFailure(1)
	b.lastFailure.Store(uint64(b.opts.clock().UnixNano()))
	if b.State() == HalfOpen {
		b.evaluateHalfOpen()
	} else {
		b.evaluate()
	}
}

// evaluate checks in Closed state for Open transition.
func (b *CircuitBreaker) evaluate() {
	m := b.Metrics()
	if m.Total < uint64(b.opts.minRequests) {
		return
	}
	if m.FailureRate >= b.opts.failureRate {
		b.toOpen()
	}
}

// evaluateHalfOpen handles stricter recovery rules.
func (b *CircuitBreaker) evaluateHalfOpen() {
	m := b.Metrics()
	if m.Total < uint64(b.opts.minRequests) {
		// stay in HalfOpen until enough samples collected
		return
	}
	if m.FailureRate >= b.opts.failureRate {
		b.toOpen()
		return
	}
	// only now safe to close
	b.toClosed()
}

// handleRejection handles the case when the breaker rejects a call
func (b *CircuitBreaker) handleRejection(ctx context.Context, err error, fallback ...func(context.Context, error) (any, error)) (any, error) {
	if len(fallback) > 0 {
		return fallback[0](ctx, err)
	}
	return nil, err
}

// executeWithTimeout executes the function with timeout handling
func (b *CircuitBreaker) executeWithTimeout(ctx context.Context, fn func(context.Context) (any, error), fallback ...func(context.Context, error) (any, error)) (any, error) {
	resultCh := make(chan executionResult, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				err := b.handlePanic(r)
				resultCh <- executionResult{err: err}
			}
		}()

		value, err := fn(ctx)
		resultCh <- executionResult{value: value, err: err}
	}()

	select {
	case <-ctx.Done():
		b.onFailure()
		return b.handleRejection(ctx, ErrTimeout, fallback...)
	case result := <-resultCh:
		if result.err != nil {
			b.onFailure()
			return b.handleRejection(ctx, result.err, fallback...)
		}
		b.onSuccess()
		return result.value, nil
	}
}

// handlePanic converts a panic into a structured error
func (b *CircuitBreaker) handlePanic(r any) error {
	switch v := r.(type) {
	case error:
		var pe *gerrors.PanicError
		if errors.As(v, &pe) {
			return &Error{
				Type:    ErrorTypePanic,
				State:   b.State(),
				Message: "panic during execution",
				Cause:   v,
			}
		}
		// Normal error - wrap with stack trace
		pc, fn, line, _ := runtime.Caller(3)
		panicErr := gerrors.NewPanicError(
			fmt.Errorf("%w at %s[%s:%d]", v, runtime.FuncForPC(pc).Name(), fn, line),
		)
		return &Error{
			Type:    ErrorTypePanic,
			State:   b.State(),
			Message: "panic during execution",
			Cause:   panicErr,
		}
	default:
		// Unknown panic type - enrich with stack trace
		pc, fn, line, _ := runtime.Caller(3)
		panicErr := gerrors.NewPanicError(
			fmt.Errorf("%#v at %s[%s:%d]", r, runtime.FuncForPC(pc).Name(), fn, line),
		)
		return &Error{
			Type:    ErrorTypePanic,
			State:   b.State(),
			Message: "panic during execution",
			Cause:   panicErr,
		}
	}
}

// acquireRelease returns a no-op if not half-open; otherwise a function that releases the semaphore slot.
func (b *CircuitBreaker) acquireRelease() func() {
	return func() {
		// Always try to release one permit if present; non-blocking and safe.
		if b.semCh != nil {
			select {
			case <-b.semCh:
			default:
			}
		}
	}
}

// Metrics builds Metrics.
func (b *CircuitBreaker) Metrics() Metrics {
	wins, wine := b.buckets.windowBounds()
	succ, fail := b.buckets.totals()
	m := Metrics{
		State:       b.State(),
		Successes:   succ,
		Failures:    fail,
		Total:       succ + fail,
		FailureRate: 0,
		Window:      b.opts.window,
		WindowStart: wins,
		WindowEnd:   wine,
	}
	if m.Total > 0 {
		m.FailureRate = float64(m.Failures) / float64(m.Total)
	}
	if lf := b.lastFailure.Load(); lf > 0 {
		m.LastFailure = time.Unix(0, int64(lf))
	}
	if ls := b.lastSuccess.Load(); ls > 0 {
		m.LastSuccess = time.Unix(0, int64(ls))
	}
	return m
}

// ensureSem initializes the half-open semaphore lazily.
func (b *CircuitBreaker) ensureSem() {
	if b.semCh != nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	maxCalls := b.opts.halfOpenMaxCalls
	if maxCalls <= 0 {
		maxCalls = 1
	}
	b.semCh = make(chan struct{}, maxCalls)
}

// resetSemLocked replaces the half-open semaphore with a fresh, empty channel of the given capacity.
// Caller must hold b.mu.
func (b *CircuitBreaker) resetSemLocked(newCap int) {
	if newCap <= 0 {
		b.semCh = nil
		return
	}
	b.semCh = make(chan struct{}, newCap)
}

// transitionTo attempts to transition from the current state to the target state
// Returns true if the transition was successful, false if already in target state
func (b *CircuitBreaker) transitionTo(targetState State) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	currentState := State(atomic.LoadInt32(&b.state))
	if currentState == targetState {
		return false
	}

	// Perform state-specific actions
	switch targetState {
	case Open:
		b.transitionToOpenLocked()
	case HalfOpen:
		b.transitionToHalfOpenLocked()
	case Closed:
		b.transitionToClosedLocked()
	}

	atomic.StoreInt32(&b.state, int32(targetState))
	return true
}

func (b *CircuitBreaker) transitionToOpenLocked() {
	until := b.opts.clock().Add(b.opts.openTimeout).UnixNano()
	atomic.StoreInt64(&b.openUntil, until)
	// while open, reject everything; ensure half-open semaphore is cleared
	b.resetSemLocked(0)
}

func (b *CircuitBreaker) transitionToHalfOpenLocked() {
	// reset window so probes evaluate fresh
	b.buckets.reset()
	// reset semaphore to an empty channel with configured capacity
	maxCalls := b.opts.halfOpenMaxCalls
	if maxCalls <= 0 {
		maxCalls = 1
	}
	b.resetSemLocked(maxCalls)
}

func (b *CircuitBreaker) transitionToClosedLocked() {
	b.buckets.reset()
	b.resetSemLocked(0)
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
