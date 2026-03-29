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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLWWRegister(t *testing.T) {
	now := time.Now()

	t.Run("new register has zero value", func(t *testing.T) {
		r := NewLWWRegister()
		require.NotNil(t, r)
		assert.Nil(t, r.Value())
		assert.Equal(t, int64(0), r.Timestamp())
		assert.Equal(t, "", r.NodeID())
	})

	t.Run("set value", func(t *testing.T) {
		r := NewLWWRegister().Set("hello", now, "node-1")
		assert.Equal(t, "hello", r.Value())
		assert.Equal(t, now.UnixNano(), r.Timestamp())
		assert.Equal(t, "node-1", r.NodeID())
	})

	t.Run("set is immutable", func(t *testing.T) {
		r := NewLWWRegister()
		r2 := r.Set("hello", now, "node-1")
		assert.Nil(t, r.Value())
		assert.Equal(t, "hello", r2.Value())
	})

	t.Run("merge higher timestamp wins", func(t *testing.T) {
		r1 := NewLWWRegister().Set("old", now, "node-1")
		r2 := NewLWWRegister().Set("new", now.Add(time.Second), "node-2")
		merged := r1.Merge(r2).(*LWWRegister)
		assert.Equal(t, "new", merged.Value())
	})

	t.Run("merge lower timestamp loses", func(t *testing.T) {
		r1 := NewLWWRegister().Set("new", now.Add(time.Second), "node-1")
		r2 := NewLWWRegister().Set("old", now, "node-2")
		merged := r1.Merge(r2).(*LWWRegister)
		assert.Equal(t, "new", merged.Value())
	})

	t.Run("merge equal timestamp higher node ID wins", func(t *testing.T) {
		r1 := NewLWWRegister().Set("from-A", now, "node-A")
		r2 := NewLWWRegister().Set("from-B", now, "node-B")
		merged := r1.Merge(r2).(*LWWRegister)
		assert.Equal(t, "from-B", merged.Value())
	})

	t.Run("merge is commutative", func(t *testing.T) {
		r1 := NewLWWRegister().Set("v1", now, "node-1")
		r2 := NewLWWRegister().Set("v2", now.Add(time.Second), "node-2")
		m1 := r1.Merge(r2).(*LWWRegister)
		m2 := r2.Merge(r1).(*LWWRegister)
		assert.Equal(t, m1.Value(), m2.Value())
	})

	t.Run("merge is idempotent", func(t *testing.T) {
		r1 := NewLWWRegister().Set("v1", now, "node-1")
		r2 := NewLWWRegister().Set("v2", now.Add(time.Second), "node-2")
		m1 := r1.Merge(r2).(*LWWRegister)
		m2 := m1.Merge(r2).(*LWWRegister)
		assert.Equal(t, m1.Value(), m2.Value())
	})

	t.Run("merge does not modify inputs", func(t *testing.T) {
		r1 := NewLWWRegister().Set("v1", now, "node-1")
		r2 := NewLWWRegister().Set("v2", now.Add(time.Second), "node-2")
		_ = r1.Merge(r2)
		assert.Equal(t, "v1", r1.Value())
		assert.Equal(t, "v2", r2.Value())
	})

	t.Run("merge with non-LWWRegister returns self", func(t *testing.T) {
		r := NewLWWRegister().Set("v1", now, "node-1")
		result := r.Merge(NewGCounter())
		assert.Equal(t, "v1", result.(*LWWRegister).Value())
	})

	t.Run("delta returns state when dirty", func(t *testing.T) {
		r := NewLWWRegister().Set("hello", now, "node-1")
		d := r.Delta()
		require.NotNil(t, d)
		assert.Equal(t, "hello", d.(*LWWRegister).Value())
	})

	t.Run("delta returns nil when not dirty", func(t *testing.T) {
		r := NewLWWRegister()
		assert.Nil(t, r.Delta())
	})

	t.Run("reset delta clears dirty flag", func(t *testing.T) {
		r := NewLWWRegister().Set("hello", now, "node-1")
		r.ResetDelta()
		assert.Nil(t, r.Delta())
	})

	t.Run("clone produces independent copy", func(t *testing.T) {
		r := NewLWWRegister().Set("hello", now, "node-1")
		cloned := r.Clone().(*LWWRegister)
		assert.Equal(t, r.Value(), cloned.Value())
		assert.Equal(t, r.Timestamp(), cloned.Timestamp())
	})

	t.Run("works with int type", func(t *testing.T) {
		r := NewLWWRegister().Set(42, now, "node-1")
		assert.Equal(t, 42, r.Value())
	})

	t.Run("works with bool type", func(t *testing.T) {
		r := NewLWWRegister().Set(true, now, "node-1")
		assert.Equal(t, true, r.Value())
	})
}
