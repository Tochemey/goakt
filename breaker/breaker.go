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
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/internal/locker"
)

// State represents the breaker state.
type State int32

const (
	Closed State = iota
	Open
	HalfOpen
)

func (s State) String() string {
	switch s {
	case Closed:
		return "closed"
	case Open:
		return "open"
	case HalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

var (
	// ErrOpen is returned when the breaker is open and rejects a call.
	ErrOpen = errors.New("circuit-breaker: open")
	// ErrTimeout indicates the context expired while executing the function.
	ErrTimeout = context.DeadlineExceeded
)

// Breaker is a thread-safe circuit breaker implementation.
type Breaker struct {
	_         locker.NoCopy
	state     int32 // atomic
	openUntil int64 // unix nano when Open ends

	opts options

	buckets *bucketWindow
	mu      sync.Mutex // guards transitions & listeners

	// half-open semaphore
	semCh chan struct{}

	lastFailure atomic.Uint64 // unix nano
	lastSuccess atomic.Uint64 // unix nano
}

// New constructs a circuit breaker.
func New(opts ...Option) *Breaker {
	o := defaultOptions()

	for _, fn := range opts {
		fn(&o)
	}

	if o.buckets <= 0 {
		o.buckets = 1
	}
	if o.window <= 0 {
		o.window = time.Second
	}

	bw := newBuckets(o.window, o.buckets, o.clock)
	b := &Breaker{
		state:       int32(Closed),
		openUntil:   0,
		opts:        o,
		buckets:     bw,
		lastFailure: atomic.Uint64{},
		lastSuccess: atomic.Uint64{},
	}
	return b
}

// State returns the current breaker state.
func (b *Breaker) State() State { return State(atomic.LoadInt32(&b.state)) }

// TryAllow returns whether a call is permitted at this moment.
// If it returns false, callers should not proceed.
func (b *Breaker) TryAllow() bool {
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

// OnSuccess records a successful call.
func (b *Breaker) OnSuccess() {
	b.buckets.addSuccess(1)
	b.lastSuccess.Store(uint64(b.opts.clock().UnixNano()))
	if b.State() == HalfOpen {
		// stricter recovery: only close when enough samples collected
		b.evaluateHalfOpen()
	} else {
		b.evaluate()
	}
}

// OnFailure records a failed call.
func (b *Breaker) OnFailure() {
	b.buckets.addFailure(1)
	b.lastFailure.Store(uint64(b.opts.clock().UnixNano()))
	if b.State() == HalfOpen {
		b.evaluateHalfOpen()
	} else {
		b.evaluate()
	}
}

// evaluate checks in Closed state for Open transition.
func (b *Breaker) evaluate() {
	m := b.snapshot()
	if m.Total < uint64(b.opts.minRequests) {
		return
	}
	if m.FailureRate >= b.opts.failureRate {
		b.toOpen()
	}
}

// evaluateHalfOpen handles stricter recovery rules.
func (b *Breaker) evaluateHalfOpen() {
	m := b.snapshot()
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

// Execute runs fn if allowed. If the breaker is open, it optionally calls fallback.
// It propagates ctx cancellation.
func (b *Breaker) Execute(ctx context.Context, fn func(context.Context) (any, error), fallback ...func(context.Context, error) (any, error)) (any, error) {
	var zero any
	allowed := b.TryAllow()
	if !allowed {
		if len(fallback) > 0 {
			return fallback[0](ctx, ErrOpen)
		}
		return zero, ErrOpen
	}
	// ensure semaphore release if half-open
	release := b.acquireRelease()
	defer release()

	resCh := make(chan struct {
		v   any
		err error
	}, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				switch v, ok := r.(error); {
				case ok:
					var pe *gerrors.PanicError
					if errors.As(v, &pe) {
						resCh <- struct {
							v   any
							err error
						}{v, pe}
						return
					}

					// this is a normal error just wrap it with some stack trace
					pc, fn, line, _ := runtime.Caller(2)
					err := gerrors.NewPanicError(
						fmt.Errorf("%w at %s[%s:%d]", v, runtime.FuncForPC(pc).Name(), fn, line),
					)
					resCh <- struct {
						v   any
						err error
					}{v, err}

				default:
					// we have no idea what panic it is. Enrich it with some stack trace
					pc, fn, line, _ := runtime.Caller(2)
					err := gerrors.NewPanicError(
						fmt.Errorf("%#v at %s[%s:%d]", r, runtime.FuncForPC(pc).Name(), fn, line),
					)
					resCh <- struct {
						v   any
						err error
					}{v, err}
				}
			}
		}()

		v, err := fn(ctx)
		resCh <- struct {
			v   any
			err error
		}{v, err}
	}()

	select {
	case <-ctx.Done():
		b.OnFailure()
		if len(fallback) > 0 {
			return fallback[0](ctx, ErrTimeout)
		}
		return zero, ErrTimeout
	case r := <-resCh:
		if r.err != nil {
			b.OnFailure()
			if len(fallback) > 0 {
				return fallback[0](ctx, r.err)
			}
			return zero, r.err
		}
		b.OnSuccess()
		return r.v, nil
	}
}

// acquireRelease returns a no-op if not half-open; otherwise a function that releases the semaphore slot.
func (b *Breaker) acquireRelease() func() {
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

// snapshot builds a Metrics snapshot.
func (b *Breaker) snapshot() Metrics {
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

// MetricsSnapshot returns a copy of current metrics.
func (b *Breaker) MetricsSnapshot() Metrics { return b.snapshot() }

// ensureSem initializes the half-open semaphore lazily.
func (b *Breaker) ensureSem() {
	if b.semCh != nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	max := b.opts.halfOpenMaxCalls
	if max <= 0 {
		max = 1
	}
	b.semCh = make(chan struct{}, max)
}

// resetSemLocked replaces the half-open semaphore with a fresh, empty channel of the given capacity.
// Caller must hold b.mu.
func (b *Breaker) resetSemLocked(newCap int) {
	if newCap <= 0 {
		b.semCh = nil
		return
	}
	b.semCh = make(chan struct{}, newCap)
}

func (b *Breaker) toOpen() {
	if b.State() == Open {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if State(b.state) == Open {
		return
	}

	atomic.StoreInt32(&b.state, int32(Open))
	until := b.opts.clock().Add(b.opts.openTimeout).UnixNano()
	atomic.StoreInt64(&b.openUntil, until)
	// while open, reject everything; ensure half-open semaphore is cleared
	b.resetSemLocked(0)
}

func (b *Breaker) toHalfOpen() {
	if b.State() == HalfOpen {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if State(b.state) == HalfOpen {
		return
	}

	atomic.StoreInt32(&b.state, int32(HalfOpen))
	// reset window so probes evaluate fresh
	b.buckets.reset()
	// reset semaphore to an empty channel with configured capacity
	max := b.opts.halfOpenMaxCalls
	if max <= 0 {
		max = 1
	}
	b.resetSemLocked(max)
}

func (b *Breaker) toClosed() {
	if b.State() == Closed {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if State(b.state) == Closed {
		return
	}

	atomic.StoreInt32(&b.state, int32(Closed))
	b.buckets.reset()
	b.resetSemLocked(0)
}
