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

import "github.com/tochemey/goakt/v3/internal/internalpb"

// ReentrancyMode determines how an actor processes other messages while waiting
// for an async response started via Request/RequestName.
//
// Modes:
//   - ReentrancyOff disables async requests for the actor.
//   - ReentrancyAllowAll keeps processing all messages while awaiting a response.
//   - ReentrancyStashNonReentrant stashes user messages until the response arrives.
//
// Design decision:
//   - ReentrancyOff preserves legacy behavior by disabling async requests.
//   - ReentrancyAllowAll favors throughput; state can change while waiting.
//   - ReentrancyStashNonReentrant favors determinism by stashing user messages
//     until the response arrives, preserving mailbox order at the cost of latency.
//
// Production note: prefer ReentrancyAllowAll to avoid deadlocks in call cycles
// (A -> B -> A). Reserve ReentrancyStashNonReentrant for cases that require strict
// ordering, and pair it with bounded in-flight limits and request timeouts.
type ReentrancyMode int

const (
	// ReentrancyOff disables async requests for the actor.
	ReentrancyOff ReentrancyMode = iota
	// ReentrancyAllowAll keeps processing all messages while awaiting a response.
	ReentrancyAllowAll
	// ReentrancyStashNonReentrant stashes user messages while awaiting a response.
	ReentrancyStashNonReentrant
)

// ReentrancyOption configures actor-level reentrancy behavior.
//
// Design decision: actor-level options establish defaults while RequestOption
// handles per-call overrides.
type ReentrancyOption func(*reentrancyConfig)

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
func WithMaxInFlight(max int) ReentrancyOption {
	return func(config *reentrancyConfig) {
		if config == nil {
			return
		}
		if max <= 0 {
			config.maxInFlight = 0
			return
		}
		config.maxInFlight = max
	}
}

// WithReentrancy enables async requests for an actor and sets the default policy.
//
// Request/RequestName are disabled unless reentrancy is configured on the actor.
// Per-call overrides are available via WithReentrancyMode.
//
// Design decision: reentrancy is opt-in to preserve backward compatibility for
// existing actors. Enabling it makes Request/RequestName available; per-call
// overrides via WithReentrancyMode can tighten or relax behavior for a single
// request.
//
// Production note: prefer ReentrancyAllowAll for throughput and to avoid
// deadlocks in call cycles (A -> B -> A). Use ReentrancyStashNonReentrant only
// when strict message ordering is required, and always set Request timeouts to
// prevent indefinite stashing if a dependency slows or fails.
func WithReentrancy(mode ReentrancyMode, opts ...ReentrancyOption) SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		reentrancy := &reentrancyConfig{mode: mode}
		for _, opt := range opts {
			opt(reentrancy)
		}
		config.reentrancy = reentrancy
	})
}

type reentrancyConfig struct {
	mode        ReentrancyMode
	maxInFlight int
}

// valid validates the configured reentrancy mode.
func (c *reentrancyConfig) valid() bool {
	return isValidReentrancyMode(c.mode)
}

// toProto converts the config into its wire representation.
func (c *reentrancyConfig) toProto() *internalpb.ReentrancyConfig {
	if c == nil {
		return nil
	}
	return &internalpb.ReentrancyConfig{
		Mode:        toInternalReentrancyMode(c.mode),
		MaxInFlight: uint32(c.maxInFlight),
	}
}

// reentrancyConfigFromProto restores the config from its wire representation.
func reentrancyConfigFromProto(cfg *internalpb.ReentrancyConfig) *reentrancyConfig {
	if cfg == nil {
		return nil
	}
	return &reentrancyConfig{
		mode:        fromInternalReentrancyMode(cfg.GetMode()),
		maxInFlight: int(cfg.GetMaxInFlight()),
	}
}

// toInternalReentrancyMode maps local modes to protobuf enums.
//
// Design decision: unknown values default to OFF for safety.
func toInternalReentrancyMode(mode ReentrancyMode) internalpb.ReentrancyMode {
	switch mode {
	case ReentrancyAllowAll:
		return internalpb.ReentrancyMode_REENTRANCY_MODE_ALLOW_ALL
	case ReentrancyStashNonReentrant:
		return internalpb.ReentrancyMode_REENTRANCY_MODE_STASH_NON_REENTRANT
	default:
		return internalpb.ReentrancyMode_REENTRANCY_MODE_OFF
	}
}

// fromInternalReentrancyMode maps protobuf enums to local modes.
//
// Design decision: unknown values default to OFF for safety.
func fromInternalReentrancyMode(mode internalpb.ReentrancyMode) ReentrancyMode {
	switch mode {
	case internalpb.ReentrancyMode_REENTRANCY_MODE_ALLOW_ALL:
		return ReentrancyAllowAll
	case internalpb.ReentrancyMode_REENTRANCY_MODE_STASH_NON_REENTRANT:
		return ReentrancyStashNonReentrant
	default:
		return ReentrancyOff
	}
}

// isValidReentrancyMode guards against unknown enum values.
//
// Design decision: validation remains strict even though mapping defaults to OFF.
func isValidReentrancyMode(mode ReentrancyMode) bool {
	switch mode {
	case ReentrancyOff, ReentrancyAllowAll, ReentrancyStashNonReentrant:
		return true
	default:
		return false
	}
}
