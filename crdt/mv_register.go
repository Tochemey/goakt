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

// ensure MVRegister implements ReplicatedData at compile time.
var _ ReplicatedData = (*MVRegister[string])(nil)

// mvEntry holds a value tagged with its causal dot.
type mvEntry[T any] struct {
	value T
	dot   dot
}

// MVRegister is a multi-value register CRDT.
//
// Unlike LWWRegister which picks a single winner, MVRegister preserves
// all concurrent writes. When two nodes write different values without
// seeing each other's writes, both values are retained after merge.
// This allows the application to detect conflicts and resolve them
// (e.g., show both values to a user, pick one, or merge them).
//
// Each write is tagged with a causal dot (nodeID + monotonic counter).
// On merge, values whose dots are dominated by the other replica's
// clock are discarded (they have been superseded). Values with
// undominated dots are kept (they are concurrent).
type MVRegister[T any] struct {
	entries []mvEntry[T]
	clock   map[string]uint64
	dirty   bool
}

// NewMVRegister creates a new empty MVRegister.
func NewMVRegister[T any]() *MVRegister[T] {
	return &MVRegister[T]{
		clock: make(map[string]uint64),
	}
}

// Set writes a value to the register on behalf of the given node.
// This supersedes all values previously observed by this register.
// Returns a new MVRegister with the updated state.
func (r *MVRegister[T]) Set(nodeID string, value T) *MVRegister[T] {
	out := r.cloneInternal()
	out.clock[nodeID]++
	d := dot{nodeID: nodeID, counter: out.clock[nodeID]}
	out.entries = []mvEntry[T]{{value: value, dot: d}}
	out.dirty = true
	return out
}

// Values returns all currently held values.
// A single value means no conflicts; multiple values indicate concurrent writes.
func (r *MVRegister[T]) Values() []T {
	result := make([]T, len(r.entries))
	for i, e := range r.entries {
		result[i] = e.value
	}
	return result
}

// Merge combines this MVRegister with another.
// Values whose dots are dominated by the other replica's clock are discarded.
// Remaining values (concurrent writes) are kept. Both inputs are left unchanged.
func (r *MVRegister[T]) Merge(other ReplicatedData) ReplicatedData {
	o, ok := other.(*MVRegister[T])
	if !ok {
		return r
	}

	merged := &MVRegister[T]{
		clock: make(map[string]uint64, len(r.clock)+len(o.clock)),
	}

	// Merge clocks: take the max of each node's counter.
	maps.Copy(merged.clock, r.clock)
	for nodeID, counter := range o.clock {
		if counter > merged.clock[nodeID] {
			merged.clock[nodeID] = counter
		}
	}

	// Keep entries from r that are not dominated by o's clock,
	// or that also exist in o (both sides observed the same write).
	for _, e := range r.entries {
		if !isDominated(e.dot, o.clock) || containsMVDot(o.entries, e.dot) {
			merged.entries = appendMVEntryUnique(merged.entries, e)
		}
	}

	// Keep entries from o that are not dominated by r's clock,
	// or that also exist in r.
	for _, e := range o.entries {
		if !isDominated(e.dot, r.clock) || containsMVDot(r.entries, e.dot) {
			merged.entries = appendMVEntryUnique(merged.entries, e)
		}
	}

	return merged
}

// Delta returns the register state if it has changed since the last ResetDelta.
// Returns nil if there are no changes.
func (r *MVRegister[T]) Delta() ReplicatedData {
	if !r.dirty {
		return nil
	}
	return r.Clone()
}

// ResetDelta clears the dirty flag.
func (r *MVRegister[T]) ResetDelta() {
	r.dirty = false
}

// MVEntry is an exported representation of an MVRegister entry for serialization.
type MVEntry[T any] struct {
	Value T
	Dot   Dot
}

// RawState returns the internal state in a serializable format.
func (r *MVRegister[T]) RawState() (entries []MVEntry[T], clock map[string]uint64) {
	result := make([]MVEntry[T], len(r.entries))
	for i, e := range r.entries {
		result[i] = MVEntry[T]{
			Value: e.value,
			Dot:   Dot{NodeID: e.dot.nodeID, Counter: e.dot.counter},
		}
	}
	clk := make(map[string]uint64, len(r.clock))
	maps.Copy(clk, r.clock)
	return result, clk
}

// MVRegisterFromRawState creates an MVRegister from serialized state.
func MVRegisterFromRawState[T any](entries []MVEntry[T], clock map[string]uint64) *MVRegister[T] {
	r := &MVRegister[T]{
		entries: make([]mvEntry[T], len(entries)),
		clock:   make(map[string]uint64, len(clock)),
	}
	for i, e := range entries {
		r.entries[i] = mvEntry[T]{
			value: e.Value,
			dot:   dot{nodeID: e.Dot.NodeID, counter: e.Dot.Counter},
		}
	}
	maps.Copy(r.clock, clock)
	return r
}

// Clone returns a deep copy of the MVRegister.
func (r *MVRegister[T]) Clone() ReplicatedData {
	return r.cloneInternal()
}

// cloneInternal returns a deep copy preserving the concrete type.
func (r *MVRegister[T]) cloneInternal() *MVRegister[T] {
	out := &MVRegister[T]{
		entries: make([]mvEntry[T], len(r.entries)),
		clock:   make(map[string]uint64, len(r.clock)),
		dirty:   r.dirty,
	}
	copy(out.entries, r.entries)
	maps.Copy(out.clock, r.clock)
	return out
}

// containsMVDot returns true if any entry has a matching dot.
func containsMVDot[T any](entries []mvEntry[T], d dot) bool {
	for _, e := range entries {
		if e.dot.nodeID == d.nodeID && e.dot.counter == d.counter {
			return true
		}
	}
	return false
}

// appendMVEntryUnique appends an entry only if no entry with the same dot exists.
func appendMVEntryUnique[T any](entries []mvEntry[T], e mvEntry[T]) []mvEntry[T] {
	if containsMVDot(entries, e.dot) {
		return entries
	}
	return append(entries, e)
}
