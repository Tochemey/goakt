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
var _ ReplicatedData = (*ORMap)(nil)

// ORMap is a map CRDT where keys are managed by an OR-Set and values
// are themselves CRDTs.
//
// The key set uses observed-remove semantics (add-wins on concurrent
// add/remove of the same key). Each value is merged independently using
// its own CRDT merge function. This makes ORMap composable: any CRDT
// type can be used as a value.
//
// Keys must be comparable at runtime (usable as map keys).
//
// Primary use cases: shopping carts, user profiles, distributed config maps.
type ORMap struct {
	keys   *ORSet
	values map[any]ReplicatedData
	dirty  bool
}

// NewORMap creates a new empty ORMap.
func NewORMap() *ORMap {
	return &ORMap{
		keys:   NewORSet(),
		values: make(map[any]ReplicatedData),
	}
}

// Set adds or updates a key-value pair in the map.
// If the key already exists, the value is merged with the existing value.
// If the key is new, it is added to the key set.
// Returns a new ORMap with the updated state.
func (m *ORMap) Set(nodeID string, key any, value ReplicatedData) *ORMap {
	out := m.cloneInternal()
	out.keys = out.keys.Add(nodeID, key)
	if existing, ok := out.values[key]; ok {
		out.values[key] = existing.Merge(value)
	} else {
		out.values[key] = value.Clone()
	}
	out.dirty = true
	return out
}

// Remove removes a key from the map using observed-remove semantics.
// Returns a new ORMap with the updated state. If the key is not in
// the map, the returned map is unchanged.
func (m *ORMap) Remove(key any) *ORMap {
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
func (m *ORMap) Get(key any) (ReplicatedData, bool) {
	if !m.keys.Contains(key) {
		return nil, false
	}
	v, ok := m.values[key]
	return v, ok
}

// Keys returns all keys currently in the map.
func (m *ORMap) Keys() []any {
	return m.keys.Elements()
}

// Len returns the number of entries in the map.
func (m *ORMap) Len() int {
	return m.keys.Len()
}

// Entries returns all key-value pairs currently in the map.
func (m *ORMap) Entries() map[any]ReplicatedData {
	result := make(map[any]ReplicatedData, m.keys.Len())
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
func (m *ORMap) Merge(other ReplicatedData) ReplicatedData {
	o, ok := other.(*ORMap)
	if !ok {
		return m
	}

	merged := &ORMap{
		keys:   m.keys.Merge(o.keys).(*ORSet),
		values: make(map[any]ReplicatedData),
	}

	// For every key in the merged key set, merge the values from both sides.
	for _, key := range merged.keys.Elements() {
		lv, lok := m.values[key]
		rv, rok := o.values[key]
		switch {
		case lok && rok:
			merged.values[key] = lv.Merge(rv)
		case lok:
			merged.values[key] = lv.Clone()
		case rok:
			merged.values[key] = rv.Clone()
		}
	}

	return merged
}

// Delta returns the state changes since the last call to ResetDelta.
// Returns nil if there are no changes.
func (m *ORMap) Delta() ReplicatedData {
	if !m.dirty {
		return nil
	}
	return m.Clone()
}

// ResetDelta clears the dirty flag and resets the embedded key set's
// delta to prevent unbounded accumulation of causal dots.
func (m *ORMap) ResetDelta() {
	m.dirty = false
	m.keys.ResetDelta()
}

// Clone returns a deep copy of the ORMap.
func (m *ORMap) Clone() ReplicatedData {
	return m.cloneInternal()
}

// Compact removes redundant causal dots from the key set and cleans up
// orphaned values whose keys are no longer present after compaction.
// Returns a new ORMap with the compacted state.
func (m *ORMap) Compact() *ORMap {
	compactedKeys := m.keys.Compact()
	out := &ORMap{
		keys:   compactedKeys,
		values: make(map[any]ReplicatedData, compactedKeys.Len()),
	}
	for _, key := range compactedKeys.Elements() {
		if v, ok := m.values[key]; ok {
			out.values[key] = v.Clone()
		}
	}
	return out
}

// CompactData implements Compactable by delegating to Compact.
func (m *ORMap) CompactData() ReplicatedData {
	return m.Compact()
}

// ORMapRawState is the exported state of an ORMap for serialization.
type ORMapRawState struct {
	KeyEntries []Entry
	KeyClock   map[string]uint64
	Values     map[any]ReplicatedData
}

// RawState returns the internal state in a serializable format.
func (m *ORMap) RawState() ORMapRawState {
	entries, clock := m.keys.RawState()
	vals := make(map[any]ReplicatedData, len(m.values))
	for k, v := range m.values {
		vals[k] = v.Clone()
	}
	return ORMapRawState{
		KeyEntries: entries,
		KeyClock:   clock,
		Values:     vals,
	}
}

// ORMapFromRawState creates an ORMap from serialized state.
func ORMapFromRawState(state ORMapRawState) *ORMap {
	m := &ORMap{
		keys:   ORSetFromRawState(state.KeyEntries, state.KeyClock),
		values: make(map[any]ReplicatedData, len(state.Values)),
	}
	maps.Copy(m.values, state.Values)
	return m
}

// cloneInternal returns a deep copy preserving the concrete type.
func (m *ORMap) cloneInternal() *ORMap {
	out := &ORMap{
		keys:   m.keys.cloneInternal(),
		values: make(map[any]ReplicatedData, len(m.values)),
		dirty:  m.dirty,
	}
	for k, v := range m.values {
		out.values[k] = v.Clone()
	}
	return out
}
