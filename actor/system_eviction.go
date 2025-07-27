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
	// LRU (Least Recently Used) passivates actors that have not been accessed for the longest time
	// when the number of active actors exceeds the specified limit. This policy is effective
	// for scenarios where older, unused actors are likely to remain idle.
	LRU EvictionPolicy = iota

	// LFU (Least Frequently Used) passivates actors that have been used the least number of times
	// when the number of active actors exceeds the specified limit. This policy is suitable
	// when you want to retain actors that are consistently in use, even if they were accessed
	// a while ago.
	LFU

	// MRU (Most Recently Used) passivates actors that were accessed most recently
	// when the number of active actors exceeds the specified limit. While less common
	// for general resource management, MRU can be useful in specific scenarios
	// where fresh data is prioritized, and older, less volatile data can be evicted.
	MRU
)

// String returns the string representation of the EvictionPolicy (e.g., "LRU", "LFU", "MRU").
// This method makes EvictionPolicy compatible with the fmt.Stringer interface,
// allowing for easy printing and logging.
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

// EvictionStrategy implements a system-configurable actor passivation policy,
// limiting the number of active actors based on their usage patterns.
//
// An EvictionStrategy combines a chosen EvictionPolicy (LRU, LFU, or MRU)
// with a defined limit and a percentage of actors to passivate.
type EvictionStrategy struct {
	limit      uint64
	policy     EvictionPolicy
	percentage int
}

// String returns a human-readable summary of the EvictionStrategy configuration.
// It satisfies both the fmt.Stringer and SystemStrategy interfaces.
func (x *EvictionStrategy) String() string {
	return fmt.Sprintf("EvictionStrategy(limit=%d, policy=%s)", x.limit, x.policy)
}

// NewEvictionStrategy constructs and initializes a new EvictionStrategy instance.
// This function acts as a factory for creating valid eviction strategies.
//
// Parameters:
//   - limit: The maximum number of actors that can remain active. Once this limit
//     is exceeded, the passivation process begins according to the chosen policy.
//     Must be greater than zero.
//   - policy: The EvictionPolicy to apply (LRU, LFU, or MRU). This determines
//     which actors are selected for passivation.
//   - percentage: An integer representing the percentage of actors to passivate
//     when the limit is reached. This value will be clamped
//     between 0 and 100, inclusive.
//
// Returns:
//   - *EvictionStrategy: A pointer to the newly initialized EvictionStrategy instance.
//   - error: An error if the provided 'limit' is zero or if the 'policy' is invalid.
func NewEvictionStrategy(limit uint64, policy EvictionPolicy, percentage int) (*EvictionStrategy, error) {
	if limit == 0 {
		return nil, fmt.Errorf("limit must be greater than zero, got %d", limit)
	}

	// Ensure percentage is within a valid range [0, 100].
	if percentage < 0 {
		percentage = 0
	} else if percentage > 100 {
		percentage = 100
	}

	switch policy {
	case LRU, LFU, MRU:
		// Valid policy, proceed with creation.
	default:
		// ErrInvalidEvictionPolicy should be a pre-defined error constant.
		return nil, ErrInvalidEvictionPolicy
	}

	return &EvictionStrategy{
		limit:      limit,
		policy:     policy,
		percentage: percentage,
	}, nil
}

// Limit returns the maximum number of active actors allowed before passivation is triggered.
func (x *EvictionStrategy) Limit() uint64 {
	return x.limit
}

// Policy returns the specific EvictionPolicy (LRU, LFU, or MRU) configured
// for this strategy instance.
func (x *EvictionStrategy) Policy() EvictionPolicy {
	return x.policy
}

// Percentage returns the percentage of actors that should be passivated
// when the system exceeds the defined active actor limit. This value is guaranteed
// to be between 0 and 100, inclusive.
func (x *EvictionStrategy) Percentage() int {
	return x.percentage
}
