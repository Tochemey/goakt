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
	"time"

	"github.com/tochemey/goakt/v3/breaker"
)

// pipeConfig defines how the outcome of a long-running task should be delivered
// ("piped") as a message to a target actor.
//
// It allows controlling the delivery semantics with exactly one of:
//
//   - Timeout: abort delivery if the task result is not ready within the given
//     duration.
//   - CircuitBreaker: prevent delivery attempts if the circuit is open due to
//     repeated failures.
//
// Only one of these options may be configured at a time. Attempting to set
// multiple options will return an error.
type pipeConfig struct {
	// timeout is the maximum duration to wait for the task outcome before
	// giving up. Only one option (timeout or circuitBreaker) can be set.
	timeout *time.Duration

	// circuitBreaker guards message delivery with a breaker. If the breaker
	// is open, the outcome will not be delivered. Only one option (timeout
	// or circuitBreaker) can be set.
	circuitBreaker *breaker.CircuitBreaker
}

// newPipeConfig constructs a new pipeConfig configuration using the provided options.
//
// It enforces that only one option (timeout or circuitBreaker) may be set.
// If multiple options are applied, newPipeConfig returns ErrOnlyOneOptionAllowed.
func newPipeConfig(opts ...PipeOption) *pipeConfig {
	config := new(pipeConfig)
	for _, opt := range opts {
		opt(config)
	}
	return config
}

// PipeOption configures a pipeConfig instance.
//
// Options are mutually exclusive; attempting to set more than one will
// return ErrOnlyOneOptionAllowed.
type PipeOption func(config *pipeConfig)

// WithTimeout configures PipeTo with a maximum duration for waiting on the
// task outcome. If the result is not available within this duration, the
// message will not be delivered.
func WithTimeout(timeout time.Duration) PipeOption {
	return func(config *pipeConfig) {
		config.timeout = &timeout
	}
}

// WithCircuitBreaker configures PipeTo with a circuit breaker that controls
// whether task outcomes are delivered. If the breaker is open due to repeated
// failures, outcomes will be dropped instead of being sent.
func WithCircuitBreaker(cb *breaker.CircuitBreaker) PipeOption {
	return func(config *pipeConfig) {
		config.circuitBreaker = cb
	}
}
