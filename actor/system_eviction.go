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

package actor

import "fmt"

// EvictionPolicy defines a strategy for passivating (deactivating) actors
// based on their usage patterns. It is used to manage memory or resource constraints
// by limiting the number of concurrently active actors.
type EvictionPolicy int

const (
	// LRU (Least Recently Used) passivates actors that have not been accessed for the longest time when the number of active actors passes the specified limit.
	LRU EvictionPolicy = iota

	// LFU (Least Frequently Used) passivates actors that have been used the least number of times when the number of active actors passes the specified limit
	LFU

	// MRU (Most Recently Used) passivates actors that were accessed most recently when the number of active actors passes the specified limit.
	MRU
)

// String returns the string representation of the EvictionPolicy.
// It satisfies the fmt.Stringer interface.
func (x EvictionPolicy) String() string {
	switch x {
	case LRU:
		return "LRU"
	case LFU:
		return "LFU"
	case MRU:
		return "MRU"
	default:
		return "Unknown"
	}
}

// EvictionStrategy implements a system configurable actor passivation policy,
// limiting the number of active actors based on usage patterns.
//
// The strategy consists of a chosen eviction policy (LRU, LFU, MRU) and
// a limit, which specifies the maximum number of actors that can remain active.
type EvictionStrategy struct {
	limit  uint64
	policy EvictionPolicy
}

// String returns a human-readable summary of the EvictionStrategy configuration.
// It satisfies both the fmt.Stringer and SystemStrategy interfaces.
func (x *EvictionStrategy) String() string {
	return fmt.Sprintf("EvictionStrategy(limit=%d, policy=%s)", x.limit, x.policy)
}

// NewEvictionStrategy constructs a new EvictionStrategy instance using the provided
// actor limit and eviction policy.
//
// Parameters:
//   - limit: The number of active actors limit before passivation begins.
//   - policy: The eviction policy to apply (LRU, LFU, or MRU).
//
// Returns:
//   - A pointer to the initialized EvictionStrategy.
//   - An error if the limit is non-positive or the policy is invalid.
func NewEvictionStrategy(limit uint64, policy EvictionPolicy) (*EvictionStrategy, error) {
	if limit == 0 {
		return nil, fmt.Errorf("limit must be greater than zero, got %d", limit)
	}

	switch policy {
	case LRU, LFU, MRU:
		// Valid policy
	default:
		return nil, ErrInvalidEvictionPolicy
	}

	return &EvictionStrategy{
		limit:  limit,
		policy: policy,
	}, nil
}

// Limit returns the number of active actors limit configured for this EvictionStrategy.
func (x *EvictionStrategy) Limit() uint64 {
	return x.limit
}

// Policy returns the configured EvictionPolicy (LRU, LFU, or MRU) used
// by this strategy instance.
func (x *EvictionStrategy) Policy() EvictionPolicy {
	return x.policy
}
