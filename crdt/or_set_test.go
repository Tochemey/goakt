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

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestORSet(t *testing.T) {
	t.Run("new set is empty", func(t *testing.T) {
		s := NewORSet()
		require.NotNil(t, s)
		assert.Equal(t, 0, s.Len())
		assert.Empty(t, s.Elements())
	})

	t.Run("add element", func(t *testing.T) {
		s := NewORSet().Add("node-1", "a")
		assert.True(t, s.Contains("a"))
		assert.Equal(t, 1, s.Len())
	})

	t.Run("add multiple elements", func(t *testing.T) {
		s := NewORSet().
			Add("node-1", "a").
			Add("node-1", "b").
			Add("node-2", "c")
		assert.Equal(t, 3, s.Len())
		assert.True(t, s.Contains("a"))
		assert.True(t, s.Contains("b"))
		assert.True(t, s.Contains("c"))
	})

	t.Run("add is immutable", func(t *testing.T) {
		s := NewORSet()
		s2 := s.Add("node-1", "a")
		assert.Equal(t, 0, s.Len())
		assert.Equal(t, 1, s2.Len())
	})

	t.Run("remove element", func(t *testing.T) {
		s := NewORSet().
			Add("node-1", "a").
			Add("node-1", "b").
			Remove("a")
		assert.False(t, s.Contains("a"))
		assert.True(t, s.Contains("b"))
		assert.Equal(t, 1, s.Len())
	})

	t.Run("remove is immutable", func(t *testing.T) {
		s := NewORSet().Add("node-1", "a")
		s2 := s.Remove("a")
		assert.True(t, s.Contains("a"))
		assert.False(t, s2.Contains("a"))
	})

	t.Run("remove nonexistent element returns same set", func(t *testing.T) {
		s := NewORSet().Add("node-1", "a")
		s2 := s.Remove("b")
		assert.True(t, s2.Contains("a"))
		assert.Equal(t, 1, s2.Len())
	})

	t.Run("add-wins semantics on merge", func(t *testing.T) {
		// node-1 adds "a", node-2 concurrently removes "a" (which it observed earlier).
		s1 := NewORSet().Add("node-1", "a")

		// s2 starts from a state where "a" was added then removed.
		s2 := NewORSet().Add("node-1", "a").Remove("a")
		// Now node-1 re-adds "a" concurrently.
		s1 = s1.Add("node-1", "a")

		merged := s1.Merge(s2).(*ORSet)
		assert.True(t, merged.Contains("a"), "add should win over concurrent remove")
	})

	t.Run("merge concurrent adds from different nodes", func(t *testing.T) {
		s1 := NewORSet().Add("node-1", "a")
		s2 := NewORSet().Add("node-2", "b")
		merged := s1.Merge(s2).(*ORSet)
		assert.True(t, merged.Contains("a"))
		assert.True(t, merged.Contains("b"))
		assert.Equal(t, 2, merged.Len())
	})

	t.Run("merge same element from different nodes", func(t *testing.T) {
		s1 := NewORSet().Add("node-1", "a")
		s2 := NewORSet().Add("node-2", "a")
		merged := s1.Merge(s2).(*ORSet)
		assert.True(t, merged.Contains("a"))
		assert.Equal(t, 1, merged.Len())
	})

	t.Run("merge is commutative", func(t *testing.T) {
		s1 := NewORSet().Add("node-1", "a").Add("node-1", "b")
		s2 := NewORSet().Add("node-2", "b").Add("node-2", "c")
		m1 := s1.Merge(s2).(*ORSet)
		m2 := s2.Merge(s1).(*ORSet)
		assert.ElementsMatch(t, m1.Elements(), m2.Elements())
	})

	t.Run("merge is idempotent", func(t *testing.T) {
		s1 := NewORSet().Add("node-1", "a")
		s2 := NewORSet().Add("node-2", "b")
		m1 := s1.Merge(s2).(*ORSet)
		m2 := m1.Merge(s2).(*ORSet)
		assert.ElementsMatch(t, m1.Elements(), m2.Elements())
	})

	t.Run("merge is associative", func(t *testing.T) {
		s1 := NewORSet().Add("node-1", "a")
		s2 := NewORSet().Add("node-2", "b")
		s3 := NewORSet().Add("node-3", "c")
		m1 := s1.Merge(s2).Merge(s3).(*ORSet)
		m2 := s1.Merge(s2.Merge(s3)).(*ORSet)
		assert.ElementsMatch(t, m1.Elements(), m2.Elements())
	})

	t.Run("merge does not modify inputs", func(t *testing.T) {
		s1 := NewORSet().Add("node-1", "a")
		s2 := NewORSet().Add("node-2", "b")
		_ = s1.Merge(s2)
		assert.Equal(t, 1, s1.Len())
		assert.True(t, s1.Contains("a"))
		assert.False(t, s1.Contains("b"))
	})

	t.Run("merge with non-ORSet returns self", func(t *testing.T) {
		s := NewORSet().Add("node-1", "a")
		result := s.Merge(NewGCounter())
		assert.True(t, result.(*ORSet).Contains("a"))
	})

	t.Run("delta tracks added elements", func(t *testing.T) {
		s := NewORSet().Add("node-1", "a").Add("node-1", "b")
		d := s.Delta()
		require.NotNil(t, d)
		ds := d.(*ORSet)
		assert.True(t, ds.Contains("a"))
		assert.True(t, ds.Contains("b"))
	})

	t.Run("delta returns nil when no changes", func(t *testing.T) {
		s := NewORSet()
		assert.Nil(t, s.Delta())
	})

	t.Run("reset delta clears tracked changes", func(t *testing.T) {
		s := NewORSet().Add("node-1", "a")
		s.ResetDelta()
		assert.Nil(t, s.Delta())
	})

	t.Run("delta propagates remove to replica via merge", func(t *testing.T) {
		// node-1 adds "a", syncs full state to replica, then removes "a".
		// The delta after the remove must cause the replica to drop "a".
		s := NewORSet().Add("node-1", "a")

		// Simulate full-state sync: replica = merge of empty with s.
		replica := NewORSet().Merge(s).(*ORSet)
		require.True(t, replica.Contains("a"))

		// Reset delta so subsequent changes are tracked from scratch.
		s.ResetDelta()

		// Remove "a" on the source.
		s = s.Remove("a")
		require.False(t, s.Contains("a"))

		// Produce a delta and merge it into the replica.
		d := s.Delta()
		require.NotNil(t, d, "remove-only delta must not be nil")

		replica = replica.Merge(d).(*ORSet)
		assert.False(t, replica.Contains("a"), "remove must propagate through delta+merge")
		assert.Equal(t, 0, replica.Len())
	})

	t.Run("delta remove does not dominate unrelated higher-counter entries", func(t *testing.T) {
		// node-1 adds "a" (c=1), adds "b" (c=2), syncs to replica, then
		// removes only "a". The delta must remove "a" without disturbing "b".
		s := NewORSet().Add("node-1", "a").Add("node-1", "b")
		replica := NewORSet().Merge(s).(*ORSet)
		require.True(t, replica.Contains("a"))
		require.True(t, replica.Contains("b"))

		s.ResetDelta()
		s = s.Remove("a")

		d := s.Delta()
		require.NotNil(t, d)

		replica = replica.Merge(d).(*ORSet)
		assert.False(t, replica.Contains("a"), "removed element must disappear")
		assert.True(t, replica.Contains("b"), "unrelated element must survive")
	})

	t.Run("clone produces independent copy", func(t *testing.T) {
		s := NewORSet().Add("node-1", "a")
		cloned := s.Clone().(*ORSet)
		assert.True(t, cloned.Contains("a"))
		cloned = cloned.Add("node-1", "b")
		assert.False(t, s.Contains("b"))
		assert.True(t, cloned.Contains("b"))
	})

	t.Run("works with int type", func(t *testing.T) {
		s := NewORSet().Add("node-1", 42).Add("node-1", 99)
		assert.True(t, s.Contains(42))
		assert.True(t, s.Contains(99))
		assert.Equal(t, 2, s.Len())
	})

	t.Run("RawState and ORSetFromRawState round-trip", func(t *testing.T) {
		s := NewORSet().Add("node-1", "a").Add("node-2", "b")
		entries, clock := s.RawState()
		restored := ORSetFromRawState(entries, clock)
		assert.True(t, restored.Contains("a"))
		assert.True(t, restored.Contains("b"))
		assert.Equal(t, s.Len(), restored.Len())
	})

	t.Run("RawState skips entries with empty dots", func(t *testing.T) {
		// Manually construct an ORSet with an empty dot entry to cover the
		// len(dots) == 0 branch in RawState.
		s := ORSetFromRawState([]Entry{
			{Element: "a", Dots: []Dot{{NodeID: "n1", Counter: 1}}},
			{Element: "empty", Dots: nil},
		}, map[string]uint64{"n1": 1})
		entries, _ := s.RawState()
		assert.Equal(t, 1, len(entries))
		assert.Equal(t, "a", entries[0].Element)
	})

	t.Run("clone preserves remove delta", func(t *testing.T) {
		s := NewORSet().Add("node-1", "a").Add("node-1", "b")
		s = s.Remove("a")
		cloned := s.Clone().(*ORSet)
		assert.False(t, cloned.Contains("a"))
		assert.True(t, cloned.Contains("b"))
	})

	t.Run("elements skips entries with empty dots", func(t *testing.T) {
		s := NewORSet().Add("node-1", "a").Add("node-1", "b")
		s = s.Remove("a")
		elems := s.Elements()
		assert.Equal(t, 1, len(elems))
		assert.Contains(t, elems, "b")
	})
}

func TestORSetCompact(t *testing.T) {
	t.Run("compact deduplicates dots per node", func(t *testing.T) {
		// Manually build an ORSet with duplicate dots for the same element from the same node.
		// This can happen after multiple merges.
		s := NewORSet()
		s = s.Add("node-1", "a")
		s = s.Add("node-1", "a") // second add creates a new dot
		// Before compaction: element "a" has 2 dots from node-1.
		entries, _ := s.RawState()
		require.Len(t, entries, 1)
		assert.Len(t, entries[0].Dots, 2)

		compacted := s.Compact()
		entries2, _ := compacted.RawState()
		require.Len(t, entries2, 1)
		// After compaction: only the highest dot per node remains.
		assert.Len(t, entries2[0].Dots, 1)
		assert.True(t, compacted.Contains("a"))
	})

	t.Run("compact preserves elements from different nodes", func(t *testing.T) {
		s := NewORSet()
		s = s.Add("node-1", "a")
		s = s.Add("node-2", "a")
		compacted := s.Compact()
		entries, _ := compacted.RawState()
		require.Len(t, entries, 1)
		assert.Len(t, entries[0].Dots, 2)
	})

	t.Run("compact on empty set", func(t *testing.T) {
		s := NewORSet()
		compacted := s.Compact()
		assert.Equal(t, 0, compacted.Len())
	})

	t.Run("compact is immutable", func(t *testing.T) {
		s := NewORSet()
		s = s.Add("node-1", "a")
		s = s.Add("node-1", "a")
		compacted := s.Compact()
		assert.NotSame(t, s, compacted)
		// Original still has 2 dots.
		entries, _ := s.RawState()
		assert.Len(t, entries[0].Dots, 2)
	})

	t.Run("compact removes entries with empty dots", func(t *testing.T) {
		// Build set, then manipulate internal state to have empty dots.
		s := &ORSet{
			entries: map[any][]dot{
				"a": {},
				"b": {{nodeID: "node-1", counter: 1}},
			},
			clock: map[string]uint64{"node-1": 1},
			delta: newORSetDelta(),
		}
		compacted := s.Compact()
		assert.Equal(t, 1, compacted.Len())
		assert.True(t, compacted.Contains("b"))
		assert.False(t, compacted.Contains("a"))
	})

	t.Run("CompactData implements Compactable", func(t *testing.T) {
		s := NewORSet()
		s = s.Add("node-1", "a")
		s = s.Add("node-1", "a")
		var c Compactable = s
		result := c.CompactData()
		require.NotNil(t, result)
		compacted := result.(*ORSet)
		assert.True(t, compacted.Contains("a"))
		entries, _ := compacted.RawState()
		require.Len(t, entries, 1)
		assert.Len(t, entries[0].Dots, 1)
	})
}

func TestCloneDots(t *testing.T) {
	t.Run("nil input returns nil", func(t *testing.T) {
		assert.Nil(t, cloneDots(nil))
	})

	t.Run("clones dot slice", func(t *testing.T) {
		dots := []dot{{nodeID: "a", counter: 1}, {nodeID: "b", counter: 2}}
		cloned := cloneDots(dots)
		assert.Equal(t, dots, cloned)
		cloned[0].counter = 99
		assert.Equal(t, uint64(1), dots[0].counter)
	})
}
