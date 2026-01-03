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

package reentrancy

import (
	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/internal/validation"
)

// Mode determines how an actor processes other messages while waiting
// for an async response started via Request/RequestName.
//
// Modes:
//   - Off disables async requests for the actor.
//   - AllowAll keeps processing all messages while awaiting a response.
//   - StashNonReentrant stashes user messages until the response arrives.
//
// Design decision:
//   - Off preserves legacy behavior by disabling async requests.
//   - AllowAll favors throughput; state can change while waiting.
//   - StashNonReentrant favors determinism by stashing user messages
//     until the response arrives, preserving mailbox order at the cost of latency.
//
// Production note: prefer AllowAll to avoid deadlocks in call cycles
// (A -> B -> A). Reserve StashNonReentrant for cases that require strict
// ordering, and pair it with bounded in-flight limits and request timeouts.
type Mode int

const (
	// Off disables async requests for the actor.
	Off Mode = iota
	// AllowAll keeps processing all messages while awaiting a response.
	AllowAll
	// StashNonReentrant stashes user messages while awaiting a response.
	StashNonReentrant
)

// Option configures reentrancy behavior.
type Option func(*Reentrancy)

// WithMaxInFlight caps the number of outstanding async requests per actor instance.
//
// A value <= 0 disables the limit. When the cap is reached, Request/RequestName
// return ErrReentrancyInFlightLimit.
//
// Design decision: a value <= 0 means "no limit" to preserve existing behavior
// unless explicitly constrained.
//
// Production note: use a finite cap to bound memory and mailbox pressure.
// Size the limit against downstream latency (p99) and request rate, and pair
// it with per-request timeouts to avoid unbounded in-flight growth under failure.
func WithMaxInFlight(maxInFlight int) Option {
	return func(r *Reentrancy) {
		if maxInFlight <= 0 {
			r.maxInFlight = 0
			return
		}
		r.maxInFlight = maxInFlight
	}
}

// WithMode sets the reentrancy mode.
func WithMode(mode Mode) Option {
	return func(r *Reentrancy) {
		r.mode = mode
	}
}

// Reentrancy configures actor reentrancy behavior.
type Reentrancy struct {
	mode        Mode
	maxInFlight int
}

// ensure Reentrancy implements validation.Validator.
var _ validation.Validator = (*Reentrancy)(nil)

// New creates a new Reentrancy configuration with the provided options.
func New(opts ...Option) *Reentrancy {
	r := new(Reentrancy)
	r.mode = Off
	r.maxInFlight = 0

	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Mode returns the reentrancy mode.
func (r *Reentrancy) Mode() Mode {
	return r.mode
}

// MaxInFlight returns the maximum number of in-flight async requests.
func (r *Reentrancy) MaxInFlight() int {
	return r.maxInFlight
}

// Validate validates the Reentrancy configuration.
func (r *Reentrancy) Validate() error {
	if !IsValidReentrancyMode(r.mode) {
		return gerrors.ErrInvalidReentrancyMode
	}
	return nil
}

// IsValidReentrancyMode guards against unknown enum values.
func IsValidReentrancyMode(mode Mode) bool {
	switch mode {
	case Off, AllowAll, StashNonReentrant:
		return true
	default:
		return false
	}
}
