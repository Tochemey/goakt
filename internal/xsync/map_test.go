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
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAndSet(t *testing.T) {
	sm := NewMap[int, string]()
	sm.Set(1, "one")
	sm.Set(2, "two")
	assert.Exactly(t, 2, sm.Len())
}

func TestGet(t *testing.T) {
	sm := NewMap[int, string]()
	sm.Set(1, "one")

	val, ok := sm.Get(1)
	require.True(t, ok)
	require.Equal(t, "one", val)

	_, ok = sm.Get(2)
	require.False(t, ok)
}

func TestDelete(t *testing.T) {
	sm := NewMap[int, string]()
	sm.Set(1, "one")
	sm.Delete(1)
	_, ok := sm.Get(1)
	require.False(t, ok)
	sm.Delete(2) // just make sure this doesn't panic
}

func TestLoadAndDelete(t *testing.T) {
	t.Run("returns the stored value and removes it", func(t *testing.T) {
		sm := NewMap[int, string]()
		sm.Set(1, "one")

		value, ok := sm.LoadAndDelete(1)
		require.True(t, ok)
		require.Equal(t, "one", value)

		_, ok = sm.Get(1)
		require.False(t, ok)
	})

	t.Run("reports a missing key", func(t *testing.T) {
		sm := NewMap[int, string]()

		value, ok := sm.LoadAndDelete(1)
		require.False(t, ok)
		require.Empty(t, value)
	})

	t.Run("exactly one racing caller observes the value", func(t *testing.T) {
		const racers = 8

		for range 200 {
			sm := NewMap[int, string]()
			sm.Set(1, "one")

			var wg sync.WaitGroup
			winners := make(chan string, racers)

			wg.Add(racers)
			for range racers {
				go func() {
					defer wg.Done()
					if value, ok := sm.LoadAndDelete(1); ok {
						winners <- value
					}
				}()
			}

			wg.Wait()
			close(winners)
			require.Len(t, collect(winners), 1)
		}
	})
}

// collect drains a closed channel into a slice.
func collect(ch <-chan string) []string {
	var out []string
	for v := range ch {
		out = append(out, v)
	}
	return out
}

func TestLen(t *testing.T) {
	sm := NewMap[int, string]()
	sm.Set(1, "one")
	sm.Set(2, "two")
	sm.Set(3, "three")
	sm.Delete(2)
	assert.Exactly(t, 2, sm.Len())
}

func TestForEach(t *testing.T) {
	sm := NewMap[int, string]()
	sm.Set(1, "one")
	sm.Set(2, "two")

	keys := make([]int, 0)
	sm.Range(func(k int, v string) { // nolint
		keys = append(keys, k)
	})

	require.Exactly(t, 2, len(keys))
	// Check if keys 1 and 2 are present
	if !slices.Contains(keys, 1) || !slices.Contains(keys, 2) {
		t.Errorf("Expected keys 1 and 2, got %v", keys)
	}
}

func TestValues(t *testing.T) {
	sm := NewMap[int, string]()
	sm.Set(1, "one")
	sm.Set(2, "two")

	values := sm.Values()
	require.NotEmpty(t, values)
	require.Len(t, values, 2)
	require.ElementsMatch(t, []string{"one", "two"}, values)
}

func TestKeys(t *testing.T) {
	sm := NewMap[int, string]()
	sm.Set(1, "one")
	sm.Set(2, "two")

	keys := sm.Keys()
	require.NotEmpty(t, keys)
	require.Len(t, keys, 2)
	require.ElementsMatch(t, []int{1, 2}, keys)
}
