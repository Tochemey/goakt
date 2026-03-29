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

func TestORMap(t *testing.T) {
	t.Run("new map is empty", func(t *testing.T) {
		m := NewORMap[string, *GCounter]()
		require.NotNil(t, m)
		assert.Equal(t, 0, m.Len())
		assert.Empty(t, m.Keys())
		assert.Empty(t, m.Entries())
	})

	t.Run("set and get single entry", func(t *testing.T) {
		m := NewORMap[string, *GCounter]()
		counter := NewGCounter().Increment("node-1", 5)
		m = m.Set("node-1", "requests", counter)
		v, ok := m.Get("requests")
		require.True(t, ok)
		assert.Equal(t, uint64(5), v.Value())
		assert.Equal(t, 1, m.Len())
	})

	t.Run("set is immutable", func(t *testing.T) {
		m := NewORMap[string, *GCounter]()
		counter := NewGCounter().Increment("node-1", 5)
		m2 := m.Set("node-1", "requests", counter)
		assert.Equal(t, 0, m.Len())
		assert.Equal(t, 1, m2.Len())
	})

	t.Run("set merges existing value", func(t *testing.T) {
		m := NewORMap[string, *GCounter]()
		c1 := NewGCounter().Increment("node-1", 5)
		m = m.Set("node-1", "requests", c1)
		c2 := NewGCounter().Increment("node-2", 3)
		m = m.Set("node-1", "requests", c2)
		v, ok := m.Get("requests")
		require.True(t, ok)
		assert.Equal(t, uint64(8), v.Value())
	})

	t.Run("get missing key returns false", func(t *testing.T) {
		m := NewORMap[string, *GCounter]()
		_, ok := m.Get("missing")
		assert.False(t, ok)
	})

	t.Run("remove existing key", func(t *testing.T) {
		m := NewORMap[string, *GCounter]()
		counter := NewGCounter().Increment("node-1", 5)
		m = m.Set("node-1", "requests", counter)
		m = m.Remove("requests")
		_, ok := m.Get("requests")
		assert.False(t, ok)
		assert.Equal(t, 0, m.Len())
	})

	t.Run("remove missing key is noop", func(t *testing.T) {
		m := NewORMap[string, *GCounter]()
		counter := NewGCounter().Increment("node-1", 5)
		m = m.Set("node-1", "requests", counter)
		m2 := m.Remove("missing")
		assert.Same(t, m, m2)
		assert.Equal(t, 1, m2.Len())
	})

	t.Run("remove is immutable", func(t *testing.T) {
		m := NewORMap[string, *GCounter]()
		counter := NewGCounter().Increment("node-1", 5)
		m = m.Set("node-1", "requests", counter)
		m2 := m.Remove("requests")
		assert.Equal(t, 1, m.Len())
		assert.Equal(t, 0, m2.Len())
	})

	t.Run("keys returns all keys", func(t *testing.T) {
		m := NewORMap[string, *GCounter]()
		m = m.Set("node-1", "a", NewGCounter().Increment("node-1", 1))
		m = m.Set("node-1", "b", NewGCounter().Increment("node-1", 2))
		m = m.Set("node-1", "c", NewGCounter().Increment("node-1", 3))
		keys := m.Keys()
		assert.Len(t, keys, 3)
		assert.ElementsMatch(t, []string{"a", "b", "c"}, keys)
	})

	t.Run("entries returns all key-value pairs", func(t *testing.T) {
		m := NewORMap[string, *GCounter]()
		m = m.Set("node-1", "a", NewGCounter().Increment("node-1", 1))
		m = m.Set("node-1", "b", NewGCounter().Increment("node-1", 2))
		entries := m.Entries()
		assert.Len(t, entries, 2)
		assert.Equal(t, uint64(1), entries["a"].Value())
		assert.Equal(t, uint64(2), entries["b"].Value())
	})

	t.Run("merge disjoint maps", func(t *testing.T) {
		m1 := NewORMap[string, *GCounter]()
		m1 = m1.Set("node-1", "a", NewGCounter().Increment("node-1", 5))
		m2 := NewORMap[string, *GCounter]()
		m2 = m2.Set("node-2", "b", NewGCounter().Increment("node-2", 3))
		merged := m1.Merge(m2).(*ORMap[string, *GCounter])
		assert.Equal(t, 2, merged.Len())
		va, _ := merged.Get("a")
		assert.Equal(t, uint64(5), va.Value())
		vb, _ := merged.Get("b")
		assert.Equal(t, uint64(3), vb.Value())
	})

	t.Run("merge overlapping maps merges values", func(t *testing.T) {
		m1 := NewORMap[string, *GCounter]()
		m1 = m1.Set("node-1", "counter", NewGCounter().Increment("node-1", 5))
		m2 := NewORMap[string, *GCounter]()
		m2 = m2.Set("node-2", "counter", NewGCounter().Increment("node-2", 3))
		merged := m1.Merge(m2).(*ORMap[string, *GCounter])
		assert.Equal(t, 1, merged.Len())
		v, ok := merged.Get("counter")
		require.True(t, ok)
		assert.Equal(t, uint64(8), v.Value())
	})

	t.Run("merge is commutative", func(t *testing.T) {
		m1 := NewORMap[string, *GCounter]()
		m1 = m1.Set("node-1", "x", NewGCounter().Increment("node-1", 5))
		m2 := NewORMap[string, *GCounter]()
		m2 = m2.Set("node-2", "x", NewGCounter().Increment("node-2", 3))
		r1 := m1.Merge(m2).(*ORMap[string, *GCounter])
		r2 := m2.Merge(m1).(*ORMap[string, *GCounter])
		v1, _ := r1.Get("x")
		v2, _ := r2.Get("x")
		assert.Equal(t, v1.Value(), v2.Value())
	})

	t.Run("merge is idempotent", func(t *testing.T) {
		m1 := NewORMap[string, *GCounter]()
		m1 = m1.Set("node-1", "x", NewGCounter().Increment("node-1", 5))
		m2 := NewORMap[string, *GCounter]()
		m2 = m2.Set("node-2", "x", NewGCounter().Increment("node-2", 3))
		r1 := m1.Merge(m2).(*ORMap[string, *GCounter])
		r2 := r1.Merge(m2).(*ORMap[string, *GCounter])
		v1, _ := r1.Get("x")
		v2, _ := r2.Get("x")
		assert.Equal(t, v1.Value(), v2.Value())
	})

	t.Run("merge is associative", func(t *testing.T) {
		m1 := NewORMap[string, *GCounter]()
		m1 = m1.Set("node-1", "x", NewGCounter().Increment("node-1", 1))
		m2 := NewORMap[string, *GCounter]()
		m2 = m2.Set("node-2", "x", NewGCounter().Increment("node-2", 2))
		m3 := NewORMap[string, *GCounter]()
		m3 = m3.Set("node-3", "x", NewGCounter().Increment("node-3", 3))
		r1 := m1.Merge(m2).Merge(m3).(*ORMap[string, *GCounter])
		r2 := m1.Merge(m2.Merge(m3)).(*ORMap[string, *GCounter])
		v1, _ := r1.Get("x")
		v2, _ := r2.Get("x")
		assert.Equal(t, v1.Value(), v2.Value())
	})

	t.Run("merge does not modify inputs", func(t *testing.T) {
		m1 := NewORMap[string, *GCounter]()
		m1 = m1.Set("node-1", "x", NewGCounter().Increment("node-1", 5))
		m2 := NewORMap[string, *GCounter]()
		m2 = m2.Set("node-2", "x", NewGCounter().Increment("node-2", 3))
		_ = m1.Merge(m2)
		assert.Equal(t, 1, m1.Len())
		v1, _ := m1.Get("x")
		assert.Equal(t, uint64(5), v1.Value())
	})

	t.Run("merge with non-ORMap returns self", func(t *testing.T) {
		m := NewORMap[string, *GCounter]()
		m = m.Set("node-1", "x", NewGCounter().Increment("node-1", 5))
		result := m.Merge(NewGCounter())
		assert.Equal(t, 1, result.(*ORMap[string, *GCounter]).Len())
	})

	t.Run("merge concurrent add and remove is add-wins", func(t *testing.T) {
		base := NewORMap[string, *GCounter]()
		base = base.Set("node-1", "key", NewGCounter().Increment("node-1", 1))

		// Replica 1 removes the key.
		r1 := base.cloneInternal()
		r1 = r1.Remove("key")

		// Replica 2 concurrently updates the key (add-wins).
		r2 := base.cloneInternal()
		r2 = r2.Set("node-2", "key", NewGCounter().Increment("node-2", 10))

		merged := r1.Merge(r2).(*ORMap[string, *GCounter])
		_, ok := merged.Get("key")
		assert.True(t, ok)
	})

	t.Run("delta tracks changes since last reset", func(t *testing.T) {
		m := NewORMap[string, *GCounter]()
		m = m.Set("node-1", "x", NewGCounter().Increment("node-1", 5))
		d := m.Delta()
		require.NotNil(t, d)
		dm := d.(*ORMap[string, *GCounter])
		assert.Equal(t, 1, dm.Len())
	})

	t.Run("delta returns nil when no changes", func(t *testing.T) {
		m := NewORMap[string, *GCounter]()
		assert.Nil(t, m.Delta())
	})

	t.Run("reset delta clears tracked changes", func(t *testing.T) {
		m := NewORMap[string, *GCounter]()
		m = m.Set("node-1", "x", NewGCounter().Increment("node-1", 5))
		m.ResetDelta()
		assert.Nil(t, m.Delta())
	})

	t.Run("clone produces independent copy", func(t *testing.T) {
		m := NewORMap[string, *GCounter]()
		m = m.Set("node-1", "x", NewGCounter().Increment("node-1", 5))
		cloned := m.Clone().(*ORMap[string, *GCounter])
		cloned = cloned.Set("node-1", "y", NewGCounter().Increment("node-1", 3))
		assert.Equal(t, 1, m.Len())
		assert.Equal(t, 2, cloned.Len())
	})

	t.Run("raw state round trips", func(t *testing.T) {
		m := NewORMap[string, *GCounter]()
		m = m.Set("node-1", "a", NewGCounter().Increment("node-1", 5))
		m = m.Set("node-2", "b", NewGCounter().Increment("node-2", 3))
		state := m.RawState()
		restored := ORMapFromRawState(state)
		assert.Equal(t, m.Len(), restored.Len())
		va, _ := restored.Get("a")
		assert.Equal(t, uint64(5), va.Value())
		vb, _ := restored.Get("b")
		assert.Equal(t, uint64(3), vb.Value())
	})

	t.Run("nested ORMap with PNCounter values", func(t *testing.T) {
		m := NewORMap[string, *PNCounter]()
		m = m.Set("node-1", "score", NewPNCounter().Increment("node-1", 10))
		m = m.Set("node-1", "score", NewPNCounter().Decrement("node-1", 3))
		v, ok := m.Get("score")
		require.True(t, ok)
		// Merge of two PNCounters: inc=10, dec=3 → value=7
		assert.Equal(t, int64(7), v.Value())
	})

	t.Run("merge empty with non-empty", func(t *testing.T) {
		m1 := NewORMap[string, *GCounter]()
		m2 := NewORMap[string, *GCounter]()
		m2 = m2.Set("node-1", "x", NewGCounter().Increment("node-1", 5))
		merged := m1.Merge(m2).(*ORMap[string, *GCounter])
		assert.Equal(t, 1, merged.Len())
		v, _ := merged.Get("x")
		assert.Equal(t, uint64(5), v.Value())
	})
}

func TestORMapCompact(t *testing.T) {
	t.Run("compact empty map", func(t *testing.T) {
		m := NewORMap[string, *GCounter]()
		compacted := m.Compact()
		require.NotNil(t, compacted)
		assert.Equal(t, 0, compacted.Len())
	})

	t.Run("compact preserves live entries", func(t *testing.T) {
		m := NewORMap[string, *GCounter]()
		m = m.Set("node-1", "a", NewGCounter().Increment("node-1", 5))
		m = m.Set("node-1", "b", NewGCounter().Increment("node-1", 3))
		compacted := m.Compact()
		assert.Equal(t, 2, compacted.Len())
		va, ok := compacted.Get("a")
		require.True(t, ok)
		assert.Equal(t, uint64(5), va.Value())
		vb, ok := compacted.Get("b")
		require.True(t, ok)
		assert.Equal(t, uint64(3), vb.Value())
	})

	t.Run("compact removes orphaned values", func(t *testing.T) {
		m := NewORMap[string, *GCounter]()
		m = m.Set("node-1", "a", NewGCounter().Increment("node-1", 5))
		m = m.Set("node-1", "b", NewGCounter().Increment("node-1", 3))
		m = m.Remove("a")
		compacted := m.Compact()
		assert.Equal(t, 1, compacted.Len())
		_, ok := compacted.Get("a")
		assert.False(t, ok)
		vb, ok := compacted.Get("b")
		require.True(t, ok)
		assert.Equal(t, uint64(3), vb.Value())
	})

	t.Run("compact returns new instance", func(t *testing.T) {
		m := NewORMap[string, *GCounter]()
		m = m.Set("node-1", "a", NewGCounter().Increment("node-1", 5))
		compacted := m.Compact()
		assert.NotSame(t, m, compacted)
		assert.Equal(t, m.Len(), compacted.Len())
	})

	t.Run("compact reduces redundant dots via ORSet compaction", func(t *testing.T) {
		m := NewORMap[string, *GCounter]()
		// Add the same key multiple times from the same node to accumulate dots.
		// GCounter.Merge takes max per node, so final value is max(1,2,3) = 3.
		m = m.Set("node-1", "a", NewGCounter().Increment("node-1", 1))
		m = m.Set("node-1", "a", NewGCounter().Increment("node-1", 2))
		m = m.Set("node-1", "a", NewGCounter().Increment("node-1", 3))
		compacted := m.Compact()
		assert.Equal(t, 1, compacted.Len())
		va, ok := compacted.Get("a")
		require.True(t, ok)
		assert.Equal(t, uint64(3), va.Value())
	})

	t.Run("CompactData implements Compactable", func(t *testing.T) {
		m := NewORMap[string, *GCounter]()
		m = m.Set("node-1", "x", NewGCounter().Increment("node-1", 5))
		var c Compactable = m
		result := c.CompactData()
		require.NotNil(t, result)
		compacted := result.(*ORMap[string, *GCounter])
		assert.Equal(t, 1, compacted.Len())
	})
}
