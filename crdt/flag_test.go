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

func TestFlag(t *testing.T) {
	t.Run("new flag is disabled", func(t *testing.T) {
		f := NewFlag()
		require.NotNil(t, f)
		assert.False(t, f.Enabled())
	})

	t.Run("enable sets flag to true", func(t *testing.T) {
		f := NewFlag()
		f = f.Enable()
		assert.True(t, f.Enabled())
	})

	t.Run("enable is immutable", func(t *testing.T) {
		f := NewFlag()
		f2 := f.Enable()
		assert.False(t, f.Enabled())
		assert.True(t, f2.Enabled())
	})

	t.Run("enable on already enabled flag returns clone", func(t *testing.T) {
		f := NewFlag().Enable()
		f2 := f.Enable()
		assert.True(t, f2.Enabled())
		assert.NotSame(t, f, f2)
	})

	t.Run("merge false with false", func(t *testing.T) {
		f1 := NewFlag()
		f2 := NewFlag()
		merged := f1.Merge(f2).(*Flag)
		assert.False(t, merged.Enabled())
	})

	t.Run("merge true with false", func(t *testing.T) {
		f1 := NewFlag().Enable()
		f2 := NewFlag()
		merged := f1.Merge(f2).(*Flag)
		assert.True(t, merged.Enabled())
	})

	t.Run("merge false with true", func(t *testing.T) {
		f1 := NewFlag()
		f2 := NewFlag().Enable()
		merged := f1.Merge(f2).(*Flag)
		assert.True(t, merged.Enabled())
	})

	t.Run("merge true with true", func(t *testing.T) {
		f1 := NewFlag().Enable()
		f2 := NewFlag().Enable()
		merged := f1.Merge(f2).(*Flag)
		assert.True(t, merged.Enabled())
	})

	t.Run("merge is commutative", func(t *testing.T) {
		f1 := NewFlag().Enable()
		f2 := NewFlag()
		m1 := f1.Merge(f2).(*Flag)
		m2 := f2.Merge(f1).(*Flag)
		assert.Equal(t, m1.Enabled(), m2.Enabled())
	})

	t.Run("merge is idempotent", func(t *testing.T) {
		f1 := NewFlag().Enable()
		f2 := NewFlag()
		m1 := f1.Merge(f2).(*Flag)
		m2 := m1.Merge(f2).(*Flag)
		assert.Equal(t, m1.Enabled(), m2.Enabled())
	})

	t.Run("merge is associative", func(t *testing.T) {
		f1 := NewFlag().Enable()
		f2 := NewFlag()
		f3 := NewFlag().Enable()
		m1 := f1.Merge(f2).Merge(f3).(*Flag)
		m2 := f1.Merge(f2.Merge(f3)).(*Flag)
		assert.Equal(t, m1.Enabled(), m2.Enabled())
	})

	t.Run("merge does not modify inputs", func(t *testing.T) {
		f1 := NewFlag()
		f2 := NewFlag().Enable()
		_ = f1.Merge(f2)
		assert.False(t, f1.Enabled())
		assert.True(t, f2.Enabled())
	})

	t.Run("merge with non-Flag returns self", func(t *testing.T) {
		f := NewFlag().Enable()
		result := f.Merge(NewGCounter())
		assert.True(t, result.(*Flag).Enabled())
	})

	t.Run("delta tracks changes since last reset", func(t *testing.T) {
		f := NewFlag()
		f = f.Enable()
		d := f.Delta()
		require.NotNil(t, d)
		assert.True(t, d.(*Flag).Enabled())
	})

	t.Run("delta returns nil when no changes", func(t *testing.T) {
		f := NewFlag()
		assert.Nil(t, f.Delta())
	})

	t.Run("reset delta clears tracked changes", func(t *testing.T) {
		f := NewFlag()
		f = f.Enable()
		f.ResetDelta()
		assert.Nil(t, f.Delta())
	})

	t.Run("FlagFromState round-trips", func(t *testing.T) {
		f := NewFlag().Enable()
		restored := FlagFromState(f.Enabled())
		assert.Equal(t, f.Enabled(), restored.Enabled())

		f2 := NewFlag()
		restored2 := FlagFromState(f2.Enabled())
		assert.Equal(t, f2.Enabled(), restored2.Enabled())
	})

	t.Run("clone produces independent copy", func(t *testing.T) {
		f := NewFlag().Enable()
		cloned := f.Clone().(*Flag)
		assert.Equal(t, f.Enabled(), cloned.Enabled())
		assert.NotSame(t, f, cloned)
	})
}
