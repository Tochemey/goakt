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

package xsync

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestList(t *testing.T) {
	t.Run("With_basic_operations", func(t *testing.T) {
		sl := NewList[int]()

		sl.Append(2)
		sl.Append(4)
		sl.Append(5)

		assert.EqualValues(t, 3, sl.Len())
		assert.NotEmpty(t, sl.Items())
		assert.Len(t, sl.Items(), 3)

		assert.EqualValues(t, 5, sl.Get(2))
		sl.Delete(1)
		assert.EqualValues(t, 2, sl.Len())
		assert.Zero(t, sl.Get(4))

		sl.Reset()
		assert.Zero(t, sl.Len())

		sl.AppendMany(1, 2, 3, 4, 5)
		assert.EqualValues(t, 5, sl.Len())

		sl.Delete(10)
	})

	t.Run("With_deduplication_on_Append", func(t *testing.T) {
		sl := NewList[int]()
		sl.Append(1)
		sl.Append(1)
		sl.Append(2)
		sl.Append(2)
		sl.Append(3)

		require.EqualValues(t, 3, sl.Len())
		assert.Equal(t, []int{1, 2, 3}, sl.Items())
	})

	t.Run("With_deduplication_on_AppendMany", func(t *testing.T) {
		sl := NewList[int]()
		sl.AppendMany(1, 2, 2, 3, 1, 4)

		require.EqualValues(t, 4, sl.Len())
		assert.Equal(t, []int{1, 2, 3, 4}, sl.Items())
	})

	t.Run("With_Contains", func(t *testing.T) {
		sl := NewList[string]()
		sl.Append("foo")
		sl.Append("bar")

		assert.True(t, sl.Contains("foo"))
		assert.True(t, sl.Contains("bar"))
		assert.False(t, sl.Contains("baz"))
	})

	t.Run("With_Get_bounds", func(t *testing.T) {
		sl := NewList[int]()
		assert.Zero(t, sl.Get(0))
		assert.Zero(t, sl.Get(-1))

		sl.Append(42)
		assert.EqualValues(t, 42, sl.Get(0))
		assert.Zero(t, sl.Get(1))
	})

	t.Run("With_Reset_reuses_backing_array", func(t *testing.T) {
		sl := NewList[int]()
		sl.AppendMany(1, 2, 3, 4, 5, 6, 7, 8)
		sl.Reset()

		assert.Zero(t, sl.Len())
		assert.Empty(t, sl.Items())

		sl.Append(99)
		assert.EqualValues(t, 1, sl.Len())
		assert.EqualValues(t, 99, sl.Get(0))
	})

	t.Run("With_concurrent_Append_no_duplicates", func(t *testing.T) {
		sl := NewList[int]()
		var wg sync.WaitGroup

		for i := range 100 {
			wg.Add(1)
			go func(v int) {
				defer wg.Done()
				sl.Append(v % 10)
			}(i)
		}
		wg.Wait()

		assert.LessOrEqual(t, sl.Len(), 10)
		items := sl.Items()
		seen := make(map[int]int)
		for _, v := range items {
			seen[v]++
		}
		for k, count := range seen {
			assert.EqualValues(t, 1, count, "duplicate found for value %d", k)
		}
	})

	t.Run("With_Delete_out_of_range_is_noop", func(t *testing.T) {
		sl := NewList[int]()
		sl.Append(10)
		sl.Delete(-1)
		sl.Delete(5)
		assert.EqualValues(t, 1, sl.Len())
	})
}
