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

// ensure ORMap implements ReplicatedData at compile time.
var _ ReplicatedData = (*ORMap[string, *GCounter])(nil)

// ORMap is a map CRDT where keys are managed by an OR-Set and values
// are themselves CRDTs.
//
// The key set uses observed-remove semantics (add-wins on concurrent
// add/remove of the same key). Each value is merged independently using
// its own CRDT merge function. This makes ORMap composable: any CRDT
// type can be used as a value.
//
// Primary use cases: shopping carts, user profiles, distributed config maps.
type ORMap[K comparable, V ReplicatedData] struct {
	keys   *ORSet[K]
	values map[K]V
	dirty  bool
}

// NewORMap creates a new empty ORMap.
func NewORMap[K comparable, V ReplicatedData]() *ORMap[K, V] {
	return &ORMap[K, V]{
		keys:   NewORSet[K](),
		values: make(map[K]V),
	}
}

// Set adds or updates a key-value pair in the map.
// If the key already exists, the value is merged with the existing value.
// If the key is new, it is added to the key set.
// Returns a new ORMap with the updated state.
func (m *ORMap[K, V]) Set(nodeID string, key K, value V) *ORMap[K, V] {
	out := m.cloneInternal()
	out.keys = out.keys.Add(nodeID, key)
	if existing, ok := out.values[key]; ok {
		out.values[key] = existing.Merge(value).(V)
	} else {
		out.values[key] = value.Clone().(V)
	}
	out.dirty = true
	return out
}

// Remove removes a key from the map using observed-remove semantics.
// Returns a new ORMap with the updated state. If the key is not in
// the map, the returned map is unchanged.
func (m *ORMap[K, V]) Remove(key K) *ORMap[K, V] {
	if !m.keys.Contains(key) {
		return m
	}
	out := m.cloneInternal()
	out.keys = out.keys.Remove(key)
	delete(out.values, key)
	out.dirty = true
	return out
}

// Get returns the value associated with the key and whether the key exists.
func (m *ORMap[K, V]) Get(key K) (V, bool) {
	if !m.keys.Contains(key) {
		var zero V
		return zero, false
	}
	v, ok := m.values[key]
	return v, ok
}

// Keys returns all keys currently in the map.
func (m *ORMap[K, V]) Keys() []K {
	return m.keys.Elements()
}

// Len returns the number of entries in the map.
func (m *ORMap[K, V]) Len() int {
	return m.keys.Len()
}

// Entries returns all key-value pairs currently in the map.
func (m *ORMap[K, V]) Entries() map[K]V {
	result := make(map[K]V, m.keys.Len())
	for _, k := range m.keys.Elements() {
		if v, ok := m.values[k]; ok {
			result[k] = v
		}
	}
	return result
}

// Merge combines this ORMap with another.
// Keys are merged using OR-Set semantics. For keys present in both maps,
// values are merged using the value type's CRDT merge function.
// Both inputs are left unchanged.
func (m *ORMap[K, V]) Merge(other ReplicatedData) ReplicatedData {
	o, ok := other.(*ORMap[K, V])
	if !ok {
		return m
	}

	merged := &ORMap[K, V]{
		keys:   m.keys.Merge(o.keys).(*ORSet[K]),
		values: make(map[K]V),
	}

	// For every key in the merged key set, merge the values from both sides.
	for _, key := range merged.keys.Elements() {
		lv, lok := m.values[key]
		rv, rok := o.values[key]
		switch {
		case lok && rok:
			merged.values[key] = lv.Merge(rv).(V)
		case lok:
			merged.values[key] = lv.Clone().(V)
		case rok:
			merged.values[key] = rv.Clone().(V)
		}
	}

	return merged
}

// Delta returns the state changes since the last call to ResetDelta.
// Returns nil if there are no changes.
func (m *ORMap[K, V]) Delta() ReplicatedData {
	if !m.dirty {
		return nil
	}
	return m.Clone()
}

// ResetDelta clears the dirty flag.
func (m *ORMap[K, V]) ResetDelta() {
	m.dirty = false
}

// Clone returns a deep copy of the ORMap.
func (m *ORMap[K, V]) Clone() ReplicatedData {
	return m.cloneInternal()
}

// Compact removes redundant causal dots from the key set and cleans up
// orphaned values whose keys are no longer present after compaction.
// Returns a new ORMap with the compacted state.
func (m *ORMap[K, V]) Compact() *ORMap[K, V] {
	compactedKeys := m.keys.Compact()
	out := &ORMap[K, V]{
		keys:   compactedKeys,
		values: make(map[K]V, compactedKeys.Len()),
	}
	for _, key := range compactedKeys.Elements() {
		if v, ok := m.values[key]; ok {
			out.values[key] = v.Clone().(V)
		}
	}
	return out
}

// CompactData implements Compactable by delegating to Compact.
func (m *ORMap[K, V]) CompactData() ReplicatedData {
	return m.Compact()
}

// ORMapRawState is the exported state of an ORMap for serialization.
type ORMapRawState[K comparable, V ReplicatedData] struct {
	KeyEntries []Entry[K]
	KeyClock   map[string]uint64
	Values     map[K]V
}

// RawState returns the internal state in a serializable format.
func (m *ORMap[K, V]) RawState() ORMapRawState[K, V] {
	entries, clock := m.keys.RawState()
	vals := make(map[K]V, len(m.values))
	for k, v := range m.values {
		vals[k] = v.Clone().(V)
	}
	return ORMapRawState[K, V]{
		KeyEntries: entries,
		KeyClock:   clock,
		Values:     vals,
	}
}

// ORMapFromRawState creates an ORMap from serialized state.
func ORMapFromRawState[K comparable, V ReplicatedData](state ORMapRawState[K, V]) *ORMap[K, V] {
	m := &ORMap[K, V]{
		keys:   ORSetFromRawState(state.KeyEntries, state.KeyClock),
		values: make(map[K]V, len(state.Values)),
	}
	maps.Copy(m.values, state.Values)
	return m
}

// cloneInternal returns a deep copy preserving the concrete type.
func (m *ORMap[K, V]) cloneInternal() *ORMap[K, V] {
	out := &ORMap[K, V]{
		keys:   m.keys.cloneInternal(),
		values: make(map[K]V, len(m.values)),
		dirty:  m.dirty,
	}
	for k, v := range m.values {
		out.values[k] = v.Clone().(V)
	}
	return out
}
