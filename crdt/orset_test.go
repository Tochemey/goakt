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
		s := NewORSet[string]()
		require.NotNil(t, s)
		assert.Equal(t, 0, s.Len())
		assert.Empty(t, s.Elements())
	})

	t.Run("add element", func(t *testing.T) {
		s := NewORSet[string]().Add("node-1", "a")
		assert.True(t, s.Contains("a"))
		assert.Equal(t, 1, s.Len())
	})

	t.Run("add multiple elements", func(t *testing.T) {
		s := NewORSet[string]().
			Add("node-1", "a").
			Add("node-1", "b").
			Add("node-2", "c")
		assert.Equal(t, 3, s.Len())
		assert.True(t, s.Contains("a"))
		assert.True(t, s.Contains("b"))
		assert.True(t, s.Contains("c"))
	})

	t.Run("add is immutable", func(t *testing.T) {
		s := NewORSet[string]()
		s2 := s.Add("node-1", "a")
		assert.Equal(t, 0, s.Len())
		assert.Equal(t, 1, s2.Len())
	})

	t.Run("remove element", func(t *testing.T) {
		s := NewORSet[string]().
			Add("node-1", "a").
			Add("node-1", "b").
			Remove("a")
		assert.False(t, s.Contains("a"))
		assert.True(t, s.Contains("b"))
		assert.Equal(t, 1, s.Len())
	})

	t.Run("remove is immutable", func(t *testing.T) {
		s := NewORSet[string]().Add("node-1", "a")
		s2 := s.Remove("a")
		assert.True(t, s.Contains("a"))
		assert.False(t, s2.Contains("a"))
	})

	t.Run("remove nonexistent element returns same set", func(t *testing.T) {
		s := NewORSet[string]().Add("node-1", "a")
		s2 := s.Remove("b")
		assert.True(t, s2.Contains("a"))
		assert.Equal(t, 1, s2.Len())
	})

	t.Run("add-wins semantics on merge", func(t *testing.T) {
		// node-1 adds "a", node-2 concurrently removes "a" (which it observed earlier).
		s1 := NewORSet[string]().Add("node-1", "a")

		// s2 starts from a state where "a" was added then removed.
		s2 := NewORSet[string]().Add("node-1", "a").Remove("a")
		// Now node-1 re-adds "a" concurrently.
		s1 = s1.Add("node-1", "a")

		merged := s1.Merge(s2).(*ORSet[string])
		assert.True(t, merged.Contains("a"), "add should win over concurrent remove")
	})

	t.Run("merge concurrent adds from different nodes", func(t *testing.T) {
		s1 := NewORSet[string]().Add("node-1", "a")
		s2 := NewORSet[string]().Add("node-2", "b")
		merged := s1.Merge(s2).(*ORSet[string])
		assert.True(t, merged.Contains("a"))
		assert.True(t, merged.Contains("b"))
		assert.Equal(t, 2, merged.Len())
	})

	t.Run("merge same element from different nodes", func(t *testing.T) {
		s1 := NewORSet[string]().Add("node-1", "a")
		s2 := NewORSet[string]().Add("node-2", "a")
		merged := s1.Merge(s2).(*ORSet[string])
		assert.True(t, merged.Contains("a"))
		assert.Equal(t, 1, merged.Len())
	})

	t.Run("merge is commutative", func(t *testing.T) {
		s1 := NewORSet[string]().Add("node-1", "a").Add("node-1", "b")
		s2 := NewORSet[string]().Add("node-2", "b").Add("node-2", "c")
		m1 := s1.Merge(s2).(*ORSet[string])
		m2 := s2.Merge(s1).(*ORSet[string])
		assert.ElementsMatch(t, m1.Elements(), m2.Elements())
	})

	t.Run("merge is idempotent", func(t *testing.T) {
		s1 := NewORSet[string]().Add("node-1", "a")
		s2 := NewORSet[string]().Add("node-2", "b")
		m1 := s1.Merge(s2).(*ORSet[string])
		m2 := m1.Merge(s2).(*ORSet[string])
		assert.ElementsMatch(t, m1.Elements(), m2.Elements())
	})

	t.Run("merge is associative", func(t *testing.T) {
		s1 := NewORSet[string]().Add("node-1", "a")
		s2 := NewORSet[string]().Add("node-2", "b")
		s3 := NewORSet[string]().Add("node-3", "c")
		m1 := s1.Merge(s2).Merge(s3).(*ORSet[string])
		m2 := s1.Merge(s2.Merge(s3)).(*ORSet[string])
		assert.ElementsMatch(t, m1.Elements(), m2.Elements())
	})

	t.Run("merge does not modify inputs", func(t *testing.T) {
		s1 := NewORSet[string]().Add("node-1", "a")
		s2 := NewORSet[string]().Add("node-2", "b")
		_ = s1.Merge(s2)
		assert.Equal(t, 1, s1.Len())
		assert.True(t, s1.Contains("a"))
		assert.False(t, s1.Contains("b"))
	})

	t.Run("merge with non-ORSet returns self", func(t *testing.T) {
		s := NewORSet[string]().Add("node-1", "a")
		result := s.Merge(NewGCounter())
		assert.True(t, result.(*ORSet[string]).Contains("a"))
	})

	t.Run("delta tracks added elements", func(t *testing.T) {
		s := NewORSet[string]().Add("node-1", "a").Add("node-1", "b")
		d := s.Delta()
		require.NotNil(t, d)
		ds := d.(*ORSet[string])
		assert.True(t, ds.Contains("a"))
		assert.True(t, ds.Contains("b"))
	})

	t.Run("delta returns nil when no changes", func(t *testing.T) {
		s := NewORSet[string]()
		assert.Nil(t, s.Delta())
	})

	t.Run("reset delta clears tracked changes", func(t *testing.T) {
		s := NewORSet[string]().Add("node-1", "a")
		s.ResetDelta()
		assert.Nil(t, s.Delta())
	})

	t.Run("clone produces independent copy", func(t *testing.T) {
		s := NewORSet[string]().Add("node-1", "a")
		cloned := s.Clone().(*ORSet[string])
		assert.True(t, cloned.Contains("a"))
		cloned = cloned.Add("node-1", "b")
		assert.False(t, s.Contains("b"))
		assert.True(t, cloned.Contains("b"))
	})

	t.Run("works with int type", func(t *testing.T) {
		s := NewORSet[int]().Add("node-1", 42).Add("node-1", 99)
		assert.True(t, s.Contains(42))
		assert.True(t, s.Contains(99))
		assert.Equal(t, 2, s.Len())
	})

	t.Run("RawState and ORSetFromRawState round-trip", func(t *testing.T) {
		s := NewORSet[string]().Add("node-1", "a").Add("node-2", "b")
		entries, clock := s.RawState()
		restored := ORSetFromRawState(entries, clock)
		assert.True(t, restored.Contains("a"))
		assert.True(t, restored.Contains("b"))
		assert.Equal(t, s.Len(), restored.Len())
	})

	t.Run("RawState skips entries with empty dots", func(t *testing.T) {
		// Manually construct an ORSet with an empty dot entry to cover the
		// len(dots) == 0 branch in RawState.
		s := ORSetFromRawState([]Entry[string]{
			{Element: "a", Dots: []Dot{{NodeID: "n1", Counter: 1}}},
			{Element: "empty", Dots: nil},
		}, map[string]uint64{"n1": 1})
		entries, _ := s.RawState()
		assert.Equal(t, 1, len(entries))
		assert.Equal(t, "a", entries[0].Element)
	})

	t.Run("clone preserves remove delta", func(t *testing.T) {
		s := NewORSet[string]().Add("node-1", "a").Add("node-1", "b")
		s = s.Remove("a")
		cloned := s.Clone().(*ORSet[string])
		assert.False(t, cloned.Contains("a"))
		assert.True(t, cloned.Contains("b"))
	})

	t.Run("elements skips entries with empty dots", func(t *testing.T) {
		s := NewORSet[string]().Add("node-1", "a").Add("node-1", "b")
		s = s.Remove("a")
		elems := s.Elements()
		assert.Equal(t, 1, len(elems))
		assert.Contains(t, elems, "b")
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

func benchmarkORSetMergeConverged(b *testing.B, elements int) {
	s1 := NewORSet[int]()
	s2 := NewORSet[int]()
	for i := range elements {
		s1 = s1.Add("node-1", i)
		s2 = s2.Add("node-1", i)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		s1.Merge(s2)
	}
}

func BenchmarkORSetMergeConverged_10(b *testing.B)   { benchmarkORSetMergeConverged(b, 10) }
func BenchmarkORSetMergeConverged_100(b *testing.B)  { benchmarkORSetMergeConverged(b, 100) }
func BenchmarkORSetMergeConverged_1000(b *testing.B) { benchmarkORSetMergeConverged(b, 1000) }

func benchmarkORSetMergeDiverged(b *testing.B, elements int) {
	s1 := NewORSet[int]()
	s2 := NewORSet[int]()
	for i := range elements {
		s1 = s1.Add("node-1", i)
		s2 = s2.Add("node-2", i+elements)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		s1.Merge(s2)
	}
}

func BenchmarkORSetMergeDiverged_10(b *testing.B)   { benchmarkORSetMergeDiverged(b, 10) }
func BenchmarkORSetMergeDiverged_100(b *testing.B)  { benchmarkORSetMergeDiverged(b, 100) }
func BenchmarkORSetMergeDiverged_1000(b *testing.B) { benchmarkORSetMergeDiverged(b, 1000) }

func BenchmarkORSetAdd(b *testing.B) {
	s := NewORSet[int]()
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		s = s.Add("node-1", i)
	}
}
