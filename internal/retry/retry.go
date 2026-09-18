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

// Package retry runs an operation until it succeeds, with exponential backoff between attempts.
package retry

import (
	"context"
	"time"

	"github.com/cenkalti/backoff/v7"

	"github.com/tochemey/goakt/v4/internal/types"
)

const (
	// DefaultMaxTries is the number of attempts used when NewRetrier is given a non-positive count.
	DefaultMaxTries = 5
	// DefaultInitialDelay is the first delay used when NewRetrier is given a non-positive initial delay.
	DefaultInitialDelay = 200 * time.Millisecond
	// DefaultMaxDelay is the delay cap used when NewRetrier is given a non-positive maximum delay.
	DefaultMaxDelay = time.Second
	// backoffMultiplier doubles the delay after each failed attempt.
	backoffMultiplier = 2.0
)

// Retrier runs an operation with a bounded number of attempts and exponential backoff
// between them. A Retrier holds only its configuration, so it may be shared and used
// concurrently.
type Retrier struct {
	maxTries     int
	initialDelay time.Duration
	maxDelay     time.Duration
}

// NewRetrier returns a Retrier that makes at most maxTries attempts, the first attempt
// included, waiting between attempts with a delay that starts at initialDelay, doubles
// each time with jitter and never exceeds maxDelay. A non-positive value falls back to
// the matching default.
func NewRetrier(maxTries int, initialDelay, maxDelay time.Duration) *Retrier {
	if maxTries <= 0 {
		maxTries = DefaultMaxTries
	}

	if initialDelay <= 0 {
		initialDelay = DefaultInitialDelay
	}

	if maxDelay <= 0 {
		maxDelay = DefaultMaxDelay
	}

	return &Retrier{
		maxTries:     maxTries,
		initialDelay: initialDelay,
		maxDelay:     maxDelay,
	}
}

// Run runs funcToRetry with a background context. See RunContext.
func (x *Retrier) Run(funcToRetry func() error) error {
	return x.RunContext(context.Background(), func(context.Context) error {
		return funcToRetry()
	})
}

// RunContext calls funcToRetry with ctx until it returns nil, returns an error marked
// with Stop, the attempts are exhausted, or ctx ends. It always returns the last error
// funcToRetry produced, with the Stop marker removed, and never an error type of the
// backoff library. When ctx ends, the last operation error is returned rather than the
// context error, so callers see what actually failed.
func (x *Retrier) RunContext(ctx context.Context, funcToRetry func(context.Context) error) error {
	policy := backoff.NewExponentialBackOff()
	policy.InitialInterval = x.initialDelay
	policy.MaxInterval = x.maxDelay
	policy.Multiplier = backoffMultiplier

	_, err := backoff.Retry(ctx, func() (types.Unit, error) {
		return types.Unit{}, funcToRetry(ctx)
	}, backoff.WithBackOff(policy), backoff.WithMaxTries(uint(x.maxTries)), backoff.WithMaxElapsedTime(0))
	if err == nil {
		return nil
	}

	if retryErr := backoff.AsRetryError(err); retryErr != nil {
		return retryErr.LastErr
	}

	return err
}

// Stop marks err as terminal: RunContext returns err at once instead of retrying. The
// returned error unwraps to err, so errors.Is and errors.As see through it. Stop(nil)
// returns nil.
func Stop(err error) error {
	return backoff.Permanent(err)
}
