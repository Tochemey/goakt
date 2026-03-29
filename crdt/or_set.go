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

// dot represents a causal event: a (nodeID, counter) pair that uniquely
// identifies a single add operation.
type dot struct {
	nodeID  string
	counter uint64
}

// ORSet is an observed-remove set CRDT.
//
// It supports concurrent add and remove operations without conflict.
// Each add operation is tagged with a unique causal dot (nodeID + monotonic
// counter). Remove operations record the dots they have observed, so that
// a concurrent add on another node is not affected by a remove that has
// not observed it. This gives add-wins semantics: if an add and a remove
// happen concurrently, the element remains in the set.
type ORSet[T comparable] struct {
	entries map[T][]dot
	clock   map[string]uint64
	delta   *orSetDelta[T]
}

// ensure ORSet implements ReplicatedData at compile time.
var _ ReplicatedData = (*ORSet[string])(nil)

// orSetDelta tracks the changes made since the last ResetDelta.
type orSetDelta[T comparable] struct {
	added   map[T][]dot
	removed map[T][]dot
}

// NewORSet creates a new empty ORSet.
func NewORSet[T comparable]() *ORSet[T] {
	return &ORSet[T]{
		entries: make(map[T][]dot),
		clock:   make(map[string]uint64),
		delta:   newORSetDelta[T](),
	}
}

// Add inserts an element into the set, tagged with the given node's causal dot.
// Returns a new ORSet with the updated state.
func (s *ORSet[T]) Add(nodeID string, element T) *ORSet[T] {
	out := s.shallowCopy()
	out.clock[nodeID]++
	d := dot{nodeID: nodeID, counter: out.clock[nodeID]}
	// clone only the affected entry's dot slice to avoid aliasing
	existing := out.entries[element]
	newDots := make([]dot, len(existing)+1)
	copy(newDots, existing)
	newDots[len(existing)] = d
	out.entries[element] = newDots
	out.delta.added[element] = append(out.delta.added[element], d)
	return out
}

// Remove removes an element from the set by recording all observed dots.
// Returns a new ORSet with the updated state. If the element is not in
// the set, the returned set is unchanged.
func (s *ORSet[T]) Remove(element T) *ORSet[T] {
	dots, exists := s.entries[element]
	if !exists {
		return s
	}
	out := s.shallowCopy()
	out.delta.removed[element] = append(out.delta.removed[element], dots...)
	delete(out.entries, element)
	return out
}

// Contains returns true if the element is in the set.
func (s *ORSet[T]) Contains(element T) bool {
	dots, exists := s.entries[element]
	return exists && len(dots) > 0
}

// Elements returns all elements currently in the set.
// The returned slice is a snapshot; modifications to it do not affect the set.
func (s *ORSet[T]) Elements() []T {
	result := make([]T, 0, len(s.entries))
	for elem, dots := range s.entries {
		if len(dots) > 0 {
			result = append(result, elem)
		}
	}
	return result
}

// Len returns the number of elements in the set.
func (s *ORSet[T]) Len() int {
	count := 0
	for _, dots := range s.entries {
		if len(dots) > 0 {
			count++
		}
	}
	return count
}

// Merge combines this ORSet with another using observed-remove semantics.
// An element is present in the merged set if it has at least one dot that
// is not dominated by the other set's clock. Both inputs are left unchanged.
func (s *ORSet[T]) Merge(other ReplicatedData) ReplicatedData {
	o, ok := other.(*ORSet[T])
	if !ok {
		return s
	}

	merged := &ORSet[T]{
		entries: make(map[T][]dot),
		clock:   make(map[string]uint64, len(s.clock)+len(o.clock)),
		delta:   newORSetDelta[T](),
	}

	// Merge clocks: take the max of each node's counter.
	maps.Copy(merged.clock, s.clock)
	for nodeID, counter := range o.clock {
		if counter > merged.clock[nodeID] {
			merged.clock[nodeID] = counter
		}
	}

	// Collect all elements from both sets.
	allElements := make(map[T]struct{}, len(s.entries)+len(o.entries))
	for elem := range s.entries {
		allElements[elem] = struct{}{}
	}
	for elem := range o.entries {
		allElements[elem] = struct{}{}
	}

	// For each element, keep dots that are not dominated by the other's clock.
	for elem := range allElements {
		var kept []dot
		for _, d := range s.entries[elem] {
			if !isDominated(d, o.clock) || containsDot(o.entries[elem], d) {
				kept = appendDotUnique(kept, d)
			}
		}
		for _, d := range o.entries[elem] {
			if !isDominated(d, s.clock) || containsDot(s.entries[elem], d) {
				kept = appendDotUnique(kept, d)
			}
		}
		if len(kept) > 0 {
			merged.entries[elem] = kept
		}
	}

	return merged
}

// Delta returns the state changes since the last call to ResetDelta.
// Returns nil if there are no changes. The returned delta can be used
// as a ReplicatedData and merged into a peer's ORSet.
//
// The delta carries only the newly-added entries and a clock scoped to
// the nodes that produced those dots. This prevents a peer from treating
// the full clock as evidence that unseen dots have been removed.
func (s *ORSet[T]) Delta() ReplicatedData {
	if len(s.delta.added) == 0 && len(s.delta.removed) == 0 {
		return nil
	}
	// Build a minimal ORSet representing just the delta.
	d := &ORSet[T]{
		entries: make(map[T][]dot, len(s.delta.added)),
		clock:   make(map[string]uint64),
		delta:   newORSetDelta[T](),
	}
	for elem, dots := range s.delta.added {
		cloned := cloneDots(dots)
		d.entries[elem] = cloned
		// Include only clock entries for nodes that produced new dots.
		for _, dt := range cloned {
			if c, ok := s.clock[dt.nodeID]; ok {
				if c > d.clock[dt.nodeID] {
					d.clock[dt.nodeID] = c
				}
			}
		}
	}
	// Include clock entries for removed dots so that peers will see
	// these dots as dominated and drop them during merge. We use each
	// dot's own counter (not s.clock) to avoid over-claiming causality
	// which could accidentally dominate unrelated higher-counter entries.
	for _, dots := range s.delta.removed {
		for _, dt := range dots {
			if dt.counter > d.clock[dt.nodeID] {
				d.clock[dt.nodeID] = dt.counter
			}
		}
	}
	return d
}

// ResetDelta clears the accumulated delta state.
func (s *ORSet[T]) ResetDelta() {
	s.delta = newORSetDelta[T]()
}

// Dot is an exported representation of a causal dot for serialization.
type Dot struct {
	NodeID  string
	Counter uint64
}

// Entry is an exported representation of an ORSet entry for serialization.
type Entry[T comparable] struct {
	Element T
	Dots    []Dot
}

// RawState returns the internal state in a serializable format.
// Used by the codec layer for protobuf encoding.
func (s *ORSet[T]) RawState() (entries []Entry[T], clock map[string]uint64) {
	result := make([]Entry[T], 0, len(s.entries))
	for elem, dots := range s.entries {
		if len(dots) == 0 {
			continue
		}
		exported := make([]Dot, len(dots))
		for i, d := range dots {
			exported[i] = Dot{NodeID: d.nodeID, Counter: d.counter}
		}
		result = append(result, Entry[T]{Element: elem, Dots: exported})
	}
	clk := make(map[string]uint64, len(s.clock))
	maps.Copy(clk, s.clock)
	return result, clk
}

// ORSetFromRawState creates an ORSet from serialized state.
func ORSetFromRawState[T comparable](entries []Entry[T], clock map[string]uint64) *ORSet[T] {
	s := &ORSet[T]{
		entries: make(map[T][]dot, len(entries)),
		clock:   make(map[string]uint64, len(clock)),
		delta:   newORSetDelta[T](),
	}
	for _, e := range entries {
		dots := make([]dot, len(e.Dots))
		for i, d := range e.Dots {
			dots[i] = dot{nodeID: d.NodeID, counter: d.Counter}
		}
		s.entries[e.Element] = dots
	}
	maps.Copy(s.clock, clock)
	return s
}

// Compact removes redundant causal dots from the set.
// A dot is redundant if the element has multiple dots and the dot is
// dominated by the vector clock. After compaction, each element retains
// only the single highest dot per node. This reduces memory overhead
// from repeated add operations on the same element.
// Returns a new ORSet with the compacted state.
func (s *ORSet[T]) Compact() *ORSet[T] {
	out := &ORSet[T]{
		entries: make(map[T][]dot, len(s.entries)),
		clock:   make(map[string]uint64, len(s.clock)),
		delta:   newORSetDelta[T](),
	}
	maps.Copy(out.clock, s.clock)

	for elem, dots := range s.entries {
		if len(dots) == 0 {
			continue
		}
		// Keep only the highest-counter dot per node.
		highest := make(map[string]dot, len(dots))
		for _, d := range dots {
			if existing, ok := highest[d.nodeID]; !ok || d.counter > existing.counter {
				highest[d.nodeID] = d
			}
		}
		compacted := make([]dot, 0, len(highest))
		for _, d := range highest {
			compacted = append(compacted, d)
		}
		out.entries[elem] = compacted
	}

	return out
}

// CompactData implements Compactable by delegating to Compact.
func (s *ORSet[T]) CompactData() ReplicatedData {
	return s.Compact()
}

// Clone returns a deep copy of the ORSet.
func (s *ORSet[T]) Clone() ReplicatedData {
	return s.cloneInternal()
}

// shallowCopy returns a copy that shares dot slices with the original.
// The entries map and clock map are copied so that inserts/deletes on
// the copy do not affect the original, but the underlying dot slices
// are shared. Callers that modify a specific element's dots must clone
// that slice before mutating it.
func (s *ORSet[T]) shallowCopy() *ORSet[T] {
	out := &ORSet[T]{
		entries: make(map[T][]dot, len(s.entries)),
		clock:   make(map[string]uint64, len(s.clock)),
		delta:   s.delta.clone(),
	}
	for elem, dots := range s.entries {
		out.entries[elem] = dots // shared, not cloned
	}
	maps.Copy(out.clock, s.clock)
	return out
}

// cloneInternal returns a deep copy preserving the concrete type.
func (s *ORSet[T]) cloneInternal() *ORSet[T] {
	out := &ORSet[T]{
		entries: make(map[T][]dot, len(s.entries)),
		clock:   make(map[string]uint64, len(s.clock)),
		delta:   s.delta.clone(),
	}
	for elem, dots := range s.entries {
		out.entries[elem] = cloneDots(dots)
	}
	maps.Copy(out.clock, s.clock)
	return out
}

// newORSetDelta creates an empty delta tracker.
func newORSetDelta[T comparable]() *orSetDelta[T] {
	return &orSetDelta[T]{
		added:   make(map[T][]dot),
		removed: make(map[T][]dot),
	}
}

// clone returns a deep copy of the delta tracker.
func (d *orSetDelta[T]) clone() *orSetDelta[T] {
	out := newORSetDelta[T]()
	for elem, dots := range d.added {
		out.added[elem] = cloneDots(dots)
	}
	for elem, dots := range d.removed {
		out.removed[elem] = cloneDots(dots)
	}
	return out
}

// isDominated returns true if the dot is dominated by the given clock.
func isDominated(d dot, clock map[string]uint64) bool {
	return d.counter <= clock[d.nodeID]
}

// containsDot returns true if the dot slice contains the given dot.
func containsDot(dots []dot, target dot) bool {
	for _, d := range dots {
		if d.nodeID == target.nodeID && d.counter == target.counter {
			return true
		}
	}
	return false
}

// appendDotUnique appends a dot to the slice only if it's not already present.
func appendDotUnique(dots []dot, d dot) []dot {
	if containsDot(dots, d) {
		return dots
	}
	return append(dots, d)
}

// cloneDots returns a deep copy of a dot slice.
func cloneDots(dots []dot) []dot {
	if dots == nil {
		return nil
	}
	out := make([]dot, len(dots))
	copy(out, dots)
	return out
}
