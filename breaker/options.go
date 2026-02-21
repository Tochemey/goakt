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
	"fmt"
	"time"

	"github.com/tochemey/goakt/v4/internal/validation"
)

// options configures the breaker.
type options struct {
	failureRate      float64       // e.g. 0.5 for 50%
	minRequests      int           // minimum samples before evaluating failureRate
	openTimeout      time.Duration // how long to stay open before moving to half-open
	window           time.Duration // total rolling window
	buckets          int           // number of buckets in the rolling window
	halfOpenMaxCalls int           // concurrent calls permitted in half-open
	clock            func() time.Time
}

var _ validation.Validator = (*options)(nil)

// Validate checks if the options are valid and returns an error if not
func (o *options) Validate() error {
	if o.failureRate < 0 || o.failureRate > 1 {
		return fmt.Errorf("failureRate must be between 0.0 and 1.0, got %f", o.failureRate)
	}
	if o.minRequests < 1 {
		return fmt.Errorf("minRequests must be at least 1, got %d", o.minRequests)
	}
	if o.openTimeout <= 0 {
		return fmt.Errorf("openTimeout must be positive, got %v", o.openTimeout)
	}
	if o.window <= 0 {
		return fmt.Errorf("window must be positive, got %v", o.window)
	}
	if o.buckets < 1 {
		return fmt.Errorf("buckets must be at least 1, got %d", o.buckets)
	}
	if o.halfOpenMaxCalls < 1 {
		return fmt.Errorf("halfOpenMaxCalls must be at least 1, got %d", o.halfOpenMaxCalls)
	}
	if o.clock == nil {
		return fmt.Errorf("clock function cannot be nil")
	}
	// Ensure bucket duration is reasonable
	bucketDur := o.window / time.Duration(o.buckets)
	if bucketDur < time.Millisecond {
		return fmt.Errorf("bucket duration too small (%v), consider reducing buckets or increasing window", bucketDur)
	}
	return nil
}

func defaultOptions() *options {
	return &options{
		failureRate:      0.5,
		minRequests:      10,
		openTimeout:      30 * time.Second,
		window:           60 * time.Second,
		buckets:          12,
		halfOpenMaxCalls: 1,
		clock:            time.Now,
	}
}

// Sanitize adjusts invalid options to their default values.
func (o *options) Sanitize() {
	if o.buckets <= 0 {
		o.buckets = 1
	}
	if o.window <= 0 {
		o.window = time.Second
	}
	if o.failureRate < 0 || o.failureRate > 1 {
		o.failureRate = 0.5
	}
	if o.minRequests < 1 {
		o.minRequests = 1
	}
	if o.openTimeout <= 0 {
		o.openTimeout = 30 * time.Second
	}
	if o.halfOpenMaxCalls < 1 {
		o.halfOpenMaxCalls = 1
	}
	if o.clock == nil {
		o.clock = time.Now
	}
}

// Option functional option.
type Option func(*options)

// WithFailureRate sets the failure rate threshold for the circuit breaker.
// The value should be between 0.0 and 1.0, representing the percentage of failed requests
// (e.g., 0.5 for 50%). When the failure rate exceeds this threshold, the breaker opens.
func WithFailureRate(r float64) Option { return func(o *options) { o.failureRate = r } }

// WithMinRequests sets the minimum number of requests required before the circuit breaker
// evaluates the failure rate. This prevents the breaker from opening prematurely due to
// insufficient sample size.
func WithMinRequests(n int) Option { return func(o *options) { o.minRequests = n } }

// WithOpenTimeout sets the duration the circuit breaker remains open before transitioning
// to the half-open state. This controls how long requests are blocked after the breaker opens.
func WithOpenTimeout(d time.Duration) Option { return func(o *options) { o.openTimeout = d } }

// WithWindow sets the total rolling window duration and the number of buckets used for
// statistical sampling. The window determines how far back in time to consider requests
// when calculating failure rates, and buckets control the granularity of sampling.
func WithWindow(d time.Duration, buckets int) Option {
	return func(o *options) { o.window, o.buckets = d, buckets }
}

// WithHalfOpenMaxCalls sets the maximum number of concurrent calls permitted when the
// circuit breaker is in the half-open state. This limits the risk of overload during recovery.
func WithHalfOpenMaxCalls(n int) Option { return func(o *options) { o.halfOpenMaxCalls = n } }

// WithClock sets a custom clock function for retrieving the current time.
// Useful for testing or overriding time behavior.
func WithClock(c func() time.Time) Option { return func(o *options) { o.clock = c } }
