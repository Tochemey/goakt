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

// ensure PNCounter implements ReplicatedData at compile time.
var _ ReplicatedData = (*PNCounter)(nil)

// PNCounter is a positive-negative counter CRDT.
//
// It is composed of two GCounters: one for increments and one for decrements.
// The counter value is the difference between the two. Concurrent increments
// and decrements on different nodes never conflict because each operation
// targets a separate GCounter, and each GCounter merges independently.
type PNCounter struct {
	increments *GCounter
	decrements *GCounter
}

// NewPNCounter creates a new PNCounter with a zero value.
func NewPNCounter() *PNCounter {
	return &PNCounter{
		increments: NewGCounter(),
		decrements: NewGCounter(),
	}
}

// Increment adds a positive value on behalf of the given node.
// Returns a new PNCounter with the updated state.
func (c *PNCounter) Increment(nodeID string, value uint64) *PNCounter {
	return &PNCounter{
		increments: c.increments.Increment(nodeID, value),
		decrements: c.decrements.Clone().(*GCounter),
	}
}

// Decrement adds a negative value on behalf of the given node.
// Returns a new PNCounter with the updated state.
func (c *PNCounter) Decrement(nodeID string, value uint64) *PNCounter {
	return &PNCounter{
		increments: c.increments.Clone().(*GCounter),
		decrements: c.decrements.Increment(nodeID, value),
	}
}

// State returns copies of the internal increment and decrement counter slots.
// Used for serialization.
func (c *PNCounter) State() (increments, decrements map[string]uint64) {
	return c.increments.State(), c.decrements.State()
}

// PNCounterFromState creates a PNCounter from serialized increment and decrement state maps.
func PNCounterFromState(increments, decrements map[string]uint64) *PNCounter {
	return &PNCounter{
		increments: GCounterFromState(increments),
		decrements: GCounterFromState(decrements),
	}
}

// Value returns the current counter value (increments - decrements).
func (c *PNCounter) Value() int64 {
	return int64(c.increments.Value()) - int64(c.decrements.Value())
}

// Merge combines this PNCounter with another by merging each GCounter independently.
// Both inputs are left unchanged.
func (c *PNCounter) Merge(other ReplicatedData) ReplicatedData {
	o, ok := other.(*PNCounter)
	if !ok {
		return c
	}
	return &PNCounter{
		increments: c.increments.Merge(o.increments).(*GCounter),
		decrements: c.decrements.Merge(o.decrements).(*GCounter),
	}
}

// Delta returns the state changes since the last call to ResetDelta.
// Returns nil if neither GCounter has changes.
func (c *PNCounter) Delta() ReplicatedData {
	incDelta := c.increments.Delta()
	decDelta := c.decrements.Delta()
	if incDelta == nil && decDelta == nil {
		return nil
	}

	d := &PNCounter{
		increments: NewGCounter(),
		decrements: NewGCounter(),
	}
	if incDelta != nil {
		d.increments = incDelta.(*GCounter)
	}
	if decDelta != nil {
		d.decrements = decDelta.(*GCounter)
	}
	return d
}

// ResetDelta clears the accumulated delta state on both GCounters.
func (c *PNCounter) ResetDelta() {
	c.increments.ResetDelta()
	c.decrements.ResetDelta()
}

// Clone returns a deep copy of the PNCounter.
func (c *PNCounter) Clone() ReplicatedData {
	return &PNCounter{
		increments: c.increments.Clone().(*GCounter),
		decrements: c.decrements.Clone().(*GCounter),
	}
}
