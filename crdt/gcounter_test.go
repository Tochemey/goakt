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

func TestGCounter(t *testing.T) {
	t.Run("new counter is zero", func(t *testing.T) {
		c := NewGCounter()
		require.NotNil(t, c)
		assert.Equal(t, uint64(0), c.Value())
	})

	t.Run("increment single node", func(t *testing.T) {
		c := NewGCounter()
		c = c.Increment("node-1", 5)
		assert.Equal(t, uint64(5), c.Value())
		c = c.Increment("node-1", 3)
		assert.Equal(t, uint64(8), c.Value())
	})

	t.Run("increment multiple nodes", func(t *testing.T) {
		c := NewGCounter()
		c = c.Increment("node-1", 5)
		c = c.Increment("node-2", 10)
		c = c.Increment("node-3", 3)
		assert.Equal(t, uint64(18), c.Value())
	})

	t.Run("increment is immutable", func(t *testing.T) {
		c := NewGCounter()
		c2 := c.Increment("node-1", 5)
		assert.Equal(t, uint64(0), c.Value())
		assert.Equal(t, uint64(5), c2.Value())
	})

	t.Run("merge with empty counter", func(t *testing.T) {
		c1 := NewGCounter().Increment("node-1", 5)
		c2 := NewGCounter()
		merged := c1.Merge(c2).(*GCounter)
		assert.Equal(t, uint64(5), merged.Value())
	})

	t.Run("merge takes per-node max", func(t *testing.T) {
		c1 := NewGCounter().Increment("node-1", 5).Increment("node-2", 3)
		c2 := NewGCounter().Increment("node-1", 3).Increment("node-2", 7)
		merged := c1.Merge(c2).(*GCounter)
		assert.Equal(t, uint64(12), merged.Value())
	})

	t.Run("merge is commutative", func(t *testing.T) {
		c1 := NewGCounter().Increment("node-1", 5)
		c2 := NewGCounter().Increment("node-2", 3)
		m1 := c1.Merge(c2).(*GCounter)
		m2 := c2.Merge(c1).(*GCounter)
		assert.Equal(t, m1.Value(), m2.Value())
	})

	t.Run("merge is idempotent", func(t *testing.T) {
		c1 := NewGCounter().Increment("node-1", 5)
		c2 := NewGCounter().Increment("node-2", 3)
		m1 := c1.Merge(c2).(*GCounter)
		m2 := m1.Merge(c2).(*GCounter)
		assert.Equal(t, m1.Value(), m2.Value())
	})

	t.Run("merge is associative", func(t *testing.T) {
		c1 := NewGCounter().Increment("node-1", 5)
		c2 := NewGCounter().Increment("node-2", 3)
		c3 := NewGCounter().Increment("node-3", 7)
		m1 := c1.Merge(c2).Merge(c3).(*GCounter)
		m2 := c1.Merge(c2.Merge(c3)).(*GCounter)
		assert.Equal(t, m1.Value(), m2.Value())
	})

	t.Run("merge does not modify inputs", func(t *testing.T) {
		c1 := NewGCounter().Increment("node-1", 5)
		c2 := NewGCounter().Increment("node-1", 10)
		_ = c1.Merge(c2)
		assert.Equal(t, uint64(5), c1.Value())
		assert.Equal(t, uint64(10), c2.Value())
	})

	t.Run("merge with non-GCounter returns self", func(t *testing.T) {
		c := NewGCounter().Increment("node-1", 5)
		result := c.Merge(NewPNCounter())
		assert.Equal(t, uint64(5), result.(*GCounter).Value())
	})

	t.Run("delta tracks changes since last reset", func(t *testing.T) {
		c := NewGCounter()
		c = c.Increment("node-1", 5)
		d := c.Delta()
		require.NotNil(t, d)
		assert.Equal(t, uint64(5), d.(*GCounter).Value())
	})

	t.Run("delta returns nil when no changes", func(t *testing.T) {
		c := NewGCounter()
		assert.Nil(t, c.Delta())
	})

	t.Run("reset delta clears tracked changes", func(t *testing.T) {
		c := NewGCounter()
		c = c.Increment("node-1", 5)
		c.ResetDelta()
		assert.Nil(t, c.Delta())
	})

	t.Run("delta contains full state for changed nodes since last reset", func(t *testing.T) {
		c := NewGCounter()
		c = c.Increment("node-1", 5)
		c.ResetDelta()
		c = c.Increment("node-1", 3)
		d := c.Delta()
		require.NotNil(t, d)
		// delta returns the full accumulated state (8) for changed nodes,
		// not just the increment (3). This is correct for GCounter merge
		// which takes per-node max.
		assert.Equal(t, uint64(8), d.(*GCounter).Value())
	})

	t.Run("state returns per-node slots", func(t *testing.T) {
		c := NewGCounter().Increment("node-1", 5).Increment("node-2", 3)
		state := c.State()
		assert.Equal(t, uint64(5), state["node-1"])
		assert.Equal(t, uint64(3), state["node-2"])
		// modifying returned state does not affect the counter
		state["node-1"] = 999
		assert.Equal(t, uint64(5), c.State()["node-1"])
	})

	t.Run("GCounterFromState round-trips", func(t *testing.T) {
		c := NewGCounter().Increment("node-1", 5).Increment("node-2", 3)
		restored := GCounterFromState(c.State())
		assert.Equal(t, c.Value(), restored.Value())
	})

	t.Run("clone produces independent copy", func(t *testing.T) {
		c := NewGCounter().Increment("node-1", 5)
		cloned := c.Clone().(*GCounter)
		assert.Equal(t, c.Value(), cloned.Value())
		cloned = cloned.Increment("node-1", 10)
		assert.Equal(t, uint64(5), c.Value())
		assert.Equal(t, uint64(15), cloned.Value())
	})
}
