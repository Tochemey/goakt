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

func TestMVRegister(t *testing.T) {
	t.Run("new register is empty", func(t *testing.T) {
		r := NewMVRegister()
		require.NotNil(t, r)
		assert.Empty(t, r.Values())
	})

	t.Run("set single value", func(t *testing.T) {
		r := NewMVRegister()
		r = r.Set("node-1", "hello")
		values := r.Values()
		require.Len(t, values, 1)
		assert.Equal(t, "hello", values[0])
	})

	t.Run("set is immutable", func(t *testing.T) {
		r := NewMVRegister()
		r2 := r.Set("node-1", "hello")
		assert.Empty(t, r.Values())
		assert.Len(t, r2.Values(), 1)
	})

	t.Run("sequential writes on same node keep latest", func(t *testing.T) {
		r := NewMVRegister()
		r = r.Set("node-1", "first")
		r = r.Set("node-1", "second")
		values := r.Values()
		require.Len(t, values, 1)
		assert.Equal(t, "second", values[0])
	})

	t.Run("concurrent writes on different nodes are preserved", func(t *testing.T) {
		r1 := NewMVRegister()
		r2 := NewMVRegister()
		r1 = r1.Set("node-1", "alice")
		r2 = r2.Set("node-2", "bob")
		merged := r1.Merge(r2).(*MVRegister)
		values := merged.Values()
		require.Len(t, values, 2)
		assert.Contains(t, values, "alice")
		assert.Contains(t, values, "bob")
	})

	t.Run("merge supersedes old value seen by both", func(t *testing.T) {
		r := NewMVRegister()
		r = r.Set("node-1", "old")

		// Both replicas start from the same state.
		r1 := r.cloneInternal()
		r2 := r.cloneInternal()

		// node-1 updates to "new" — this supersedes "old" on r1.
		r1 = r1.Set("node-1", "new")

		// r2 still has "old". Merge should keep only "new" because
		// r1's clock dominates the dot for "old".
		merged := r1.Merge(r2).(*MVRegister)
		values := merged.Values()
		require.Len(t, values, 1)
		assert.Equal(t, "new", values[0])
	})

	t.Run("merge is commutative", func(t *testing.T) {
		r1 := NewMVRegister().Set("node-1", "a")
		r2 := NewMVRegister().Set("node-2", "b")
		m1 := r1.Merge(r2).(*MVRegister)
		m2 := r2.Merge(r1).(*MVRegister)
		assert.ElementsMatch(t, m1.Values(), m2.Values())
	})

	t.Run("merge is idempotent", func(t *testing.T) {
		r1 := NewMVRegister().Set("node-1", "a")
		r2 := NewMVRegister().Set("node-2", "b")
		m1 := r1.Merge(r2).(*MVRegister)
		m2 := m1.Merge(r2).(*MVRegister)
		assert.ElementsMatch(t, m1.Values(), m2.Values())
	})

	t.Run("merge is associative", func(t *testing.T) {
		r1 := NewMVRegister().Set("node-1", "a")
		r2 := NewMVRegister().Set("node-2", "b")
		r3 := NewMVRegister().Set("node-3", "c")
		m1 := r1.Merge(r2).Merge(r3).(*MVRegister)
		m2 := r1.Merge(r2.Merge(r3)).(*MVRegister)
		assert.ElementsMatch(t, m1.Values(), m2.Values())
	})

	t.Run("merge does not modify inputs", func(t *testing.T) {
		r1 := NewMVRegister().Set("node-1", "a")
		r2 := NewMVRegister().Set("node-2", "b")
		_ = r1.Merge(r2)
		assert.Len(t, r1.Values(), 1)
		assert.Len(t, r2.Values(), 1)
	})

	t.Run("merge with non-MVRegister returns self", func(t *testing.T) {
		r := NewMVRegister().Set("node-1", "a")
		result := r.Merge(NewGCounter())
		assert.Len(t, result.(*MVRegister).Values(), 1)
	})

	t.Run("merge empty with non-empty", func(t *testing.T) {
		r1 := NewMVRegister()
		r2 := NewMVRegister().Set("node-1", "a")
		merged := r1.Merge(r2).(*MVRegister)
		values := merged.Values()
		require.Len(t, values, 1)
		assert.Equal(t, "a", values[0])
	})

	t.Run("delta tracks changes since last reset", func(t *testing.T) {
		r := NewMVRegister()
		r = r.Set("node-1", "hello")
		d := r.Delta()
		require.NotNil(t, d)
		assert.Len(t, d.(*MVRegister).Values(), 1)
	})

	t.Run("delta returns nil when no changes", func(t *testing.T) {
		r := NewMVRegister()
		assert.Nil(t, r.Delta())
	})

	t.Run("reset delta clears tracked changes", func(t *testing.T) {
		r := NewMVRegister()
		r = r.Set("node-1", "hello")
		r.ResetDelta()
		assert.Nil(t, r.Delta())
	})

	t.Run("clone produces independent copy", func(t *testing.T) {
		r := NewMVRegister().Set("node-1", "hello")
		cloned := r.Clone().(*MVRegister)
		assert.Equal(t, r.Values(), cloned.Values())
		cloned = cloned.Set("node-1", "world")
		assert.Equal(t, "hello", r.Values()[0])
		assert.Equal(t, "world", cloned.Values()[0])
	})

	t.Run("raw state round trips", func(t *testing.T) {
		r1 := NewMVRegister().Set("node-1", "hello")
		r2 := NewMVRegister().Set("node-2", "world")
		merged := r1.Merge(r2).(*MVRegister)
		entries, clock := merged.RawState()
		restored := MVRegisterFromRawState(entries, clock)
		assert.ElementsMatch(t, merged.Values(), restored.Values())
	})

	t.Run("integer values", func(t *testing.T) {
		r := NewMVRegister()
		r = r.Set("node-1", 42)
		values := r.Values()
		require.Len(t, values, 1)
		assert.Equal(t, 42, values[0])
	})
}
