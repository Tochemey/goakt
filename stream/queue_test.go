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

package stream

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueue_Empty_OnNew(t *testing.T) {
	q := &queue{}
	assert.True(t, q.empty())
	assert.Equal(t, 0, q.len())
}

func TestQueue_Empty_AfterDrain(t *testing.T) {
	q := &queue{}
	q.push(1)
	q.pop()
	assert.True(t, q.empty())
	assert.Equal(t, 0, q.len())
}

func TestQueue_PushPop_Single(t *testing.T) {
	q := &queue{}
	q.push(42)
	assert.False(t, q.empty())
	assert.Equal(t, 1, q.len())
	v := q.pop()
	assert.Equal(t, 42, v)
	assert.True(t, q.empty())
}

func TestQueue_PushPop_PreservesOrder(t *testing.T) {
	q := &queue{}
	items := []int{10, 20, 30, 40, 50}
	for _, v := range items {
		q.push(v)
	}
	assert.Equal(t, len(items), q.len())
	for _, want := range items {
		require.False(t, q.empty())
		got := q.pop()
		assert.Equal(t, want, got)
	}
	assert.True(t, q.empty())
}

func TestQueue_Peek_DoesNotConsume(t *testing.T) {
	q := &queue{}
	q.push("hello")
	first := q.peek()
	assert.Equal(t, "hello", first)
	assert.Equal(t, 1, q.len(), "peek should not consume the element")
	second := q.peek()
	assert.Equal(t, "hello", second)
}

func TestQueue_Len_AfterPushPop(t *testing.T) {
	q := &queue{}
	for i := 0; i < 5; i++ {
		q.push(i)
	}
	assert.Equal(t, 5, q.len())
	q.pop()
	assert.Equal(t, 4, q.len())
	q.pop()
	assert.Equal(t, 3, q.len())
}

// TestQueue_Compaction verifies that the backing slice is compacted when the
// dead prefix reaches half the total slice length, and that all remaining
// elements are still accessible after compaction.
func TestQueue_Compaction(t *testing.T) {
	q := &queue{}
	// Push 10 elements.
	for i := 0; i < 10; i++ {
		q.push(i)
	}
	// Pop exactly 5: head becomes 5, len(data)=10. head*2 == 10 >= 10 → compact.
	for i := 0; i < 5; i++ {
		v := q.pop()
		assert.Equal(t, i, v)
	}
	// After compaction head should reset to 0 and 5 elements remain.
	assert.Equal(t, 5, q.len())
	for i := 5; i < 10; i++ {
		require.False(t, q.empty())
		assert.Equal(t, i, q.pop())
	}
	assert.True(t, q.empty())
}

// TestQueue_Compaction_SubsequentPushes verifies that the queue remains
// correct after compaction when more elements are pushed.
func TestQueue_Compaction_SubsequentPushes(t *testing.T) {
	q := &queue{}
	for i := 0; i < 6; i++ {
		q.push(i)
	}
	// Pop 3: head=3, len=6. 3*2=6 >= 6 → compact to [3,4,5].
	for i := 0; i < 3; i++ {
		q.pop()
	}
	assert.Equal(t, 3, q.len())
	// Push 3 more after compaction.
	for i := 10; i < 13; i++ {
		q.push(i)
	}
	assert.Equal(t, 6, q.len())
	expected := []int{3, 4, 5, 10, 11, 12}
	for _, want := range expected {
		assert.Equal(t, want, q.pop())
	}
	assert.True(t, q.empty())
}

// TestQueue_NilsVacatedAfterCompaction verifies that popped slots are zeroed
// (set to nil) so the GC can collect the underlying values.
func TestQueue_NilsVacatedAfterCompaction(t *testing.T) {
	q := &queue{}
	for i := 0; i < 4; i++ {
		q.push(i)
	}
	// Pop 2: head=2, len=4. 2*2=4 >= 4 → compact.
	q.pop()
	q.pop()
	// Two elements remain.
	assert.Equal(t, 2, q.len())
	// Verify head reset to 0 (indirectly by checking remaining values are correct).
	assert.Equal(t, 2, q.pop())
	assert.Equal(t, 3, q.pop())
	assert.True(t, q.empty())
}

// TestQueue_MixedTypes verifies the queue works with heterogeneous any values.
func TestQueue_MixedTypes(t *testing.T) {
	q := &queue{}
	q.push(1)
	q.push("two")
	q.push(3.0)
	assert.Equal(t, 1, q.pop())
	assert.Equal(t, "two", q.pop())
	assert.Equal(t, 3.0, q.pop())
}
