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

package crdt

import "maps"

// ensure GCounter implements ReplicatedData at compile time.
var _ ReplicatedData = (*GCounter)(nil)

// GCounter is a grow-only counter CRDT.
//
// Each node in the cluster maintains its own increment slot identified by
// a node ID. The counter value is the sum of all slots. Concurrent
// increments on different nodes never conflict because each node only
// writes to its own slot and the merge function takes the maximum of
// each slot.
type GCounter struct {
	state map[string]uint64
	delta map[string]uint64
}

// NewGCounter creates a new GCounter with an empty state.
func NewGCounter() *GCounter {
	return &GCounter{
		state: make(map[string]uint64),
		delta: make(map[string]uint64),
	}
}

// Increment adds a positive value to the counter on behalf of the given node.
// Returns a new GCounter with the updated state.
func (c *GCounter) Increment(nodeID string, value uint64) *GCounter {
	out := c.Clone().(*GCounter)
	out.state[nodeID] += value
	out.delta[nodeID] += value
	return out
}

// State returns a copy of the internal per-node counter slots.
// Used for serialization.
func (c *GCounter) State() map[string]uint64 {
	out := make(map[string]uint64, len(c.state))
	maps.Copy(out, c.state)
	return out
}

// GCounterFromState creates a GCounter from a serialized state map.
func GCounterFromState(state map[string]uint64) *GCounter {
	s := make(map[string]uint64, len(state))
	maps.Copy(s, state)
	return &GCounter{
		state: s,
		delta: make(map[string]uint64),
	}
}

// Value returns the current counter value (sum of all node slots).
func (c *GCounter) Value() uint64 {
	var total uint64
	for _, v := range c.state {
		total += v
	}
	return total
}

// Merge combines this GCounter with another by taking the per-node maximum.
// Both inputs are left unchanged.
func (c *GCounter) Merge(other ReplicatedData) ReplicatedData {
	o, ok := other.(*GCounter)
	if !ok {
		return c
	}

	merged := c.Clone().(*GCounter)
	for nodeID, remoteVal := range o.state {
		if localVal, exists := merged.state[nodeID]; !exists || remoteVal > localVal {
			merged.state[nodeID] = remoteVal
		}
	}
	return merged
}

// Delta returns the state changes since the last call to ResetDelta.
// Returns nil if there are no changes. The returned delta contains the
// full accumulated state for each node that changed, not just the increment.
// This ensures that merge (which takes per-node max) produces the correct result.
func (c *GCounter) Delta() ReplicatedData {
	if len(c.delta) == 0 {
		return nil
	}
	d := &GCounter{
		state: make(map[string]uint64, len(c.delta)),
		delta: make(map[string]uint64),
	}
	for nodeID := range c.delta {
		d.state[nodeID] = c.state[nodeID]
	}
	return d
}

// ResetDelta clears the accumulated delta state.
func (c *GCounter) ResetDelta() {
	clear(c.delta)
}

// Clone returns a deep copy of the GCounter.
func (c *GCounter) Clone() ReplicatedData {
	out := &GCounter{
		state: make(map[string]uint64, len(c.state)),
		delta: make(map[string]uint64, len(c.delta)),
	}
	maps.Copy(out.state, c.state)
	maps.Copy(out.delta, c.delta)
	return out
}
