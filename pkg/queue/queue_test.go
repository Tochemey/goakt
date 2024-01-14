/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package queue

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnboundedQueue(t *testing.T) {
	t.Run("With Push/Pop", func(t *testing.T) {
		q := NewUnbounded[int]()
		require.True(t, q.IsEmpty())
		for j := 0; j < 100; j++ {
			if q.Len() != 0 {
				t.Fatal("expected no elements")
			} else if _, ok := q.Pop(); ok {
				t.Fatal("expected no elements")
			}

			for i := 0; i < j; i++ {
				q.Push(i)
			}

			for i := 0; i < j; i++ {
				if x, ok := q.Pop(); !ok {
					t.Fatal("expected an element")
				} else if x != i {
					t.Fatalf("expected %d got %d", i, x)
				}
			}
		}

		a := 0
		r := 0
		for j := 0; j < 100; j++ {
			for i := 0; i < 4; i++ {
				q.Push(a)
				a++
			}

			for i := 0; i < 2; i++ {
				if x, ok := q.Pop(); !ok {
					t.Fatal("expected an element")
				} else if x != r {
					t.Fatalf("expected %d got %d", r, x)
				}
				r++
			}
		}

		if q.Len() != 200 {
			t.Fatalf("expected 200 elements have %d", q.Len())
		}

		assert.False(t, q.IsClosed())
		assert.True(t, q.Cap() > 0)

		// close the queue
		q.Close()
		assert.True(t, q.IsClosed())
		assert.True(t, q.IsEmpty())
		assert.Zero(t, q.Len())
		assert.Zero(t, q.Cap())
	})
	t.Run("With Wait", func(t *testing.T) {
		q := NewUnbounded[int]()
		assert.True(t, q.Push(1))
		assert.True(t, q.Push(2))
		assert.True(t, q.Push(3))
		assert.True(t, q.Push(4))
		assert.EqualValues(t, 4, q.Len())

		wait, ok := q.Wait()
		assert.True(t, ok)
		assert.EqualValues(t, 1, wait)
		// close the queue
		q.Close()
		assert.True(t, q.IsClosed())
		assert.Zero(t, q.Len())
		assert.Zero(t, q.Cap())
	})
	t.Run("With Close remaining", func(t *testing.T) {
		q := NewUnbounded[int]()
		for j := 0; j < 100; j++ {
			q.Push(j)
		}
		ret := q.CloseRemaining()
		assert.NotEmpty(t, ret)
		assert.Len(t, ret, 100)
		assert.True(t, q.IsClosed())
		assert.True(t, q.IsEmpty())
		assert.Zero(t, q.Len())
		assert.Zero(t, q.Cap())
	})
}

func BenchmarkUnbounded_Push(b *testing.B) {
	q := NewUnbounded[int]()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q.Push(i)
	}
}

func BenchmarkUnbounded_Pop(b *testing.B) {
	q := NewUnbounded[int]()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q.Push(i)
		if q.Len() > 10 {
			q.Pop()
		}
	}

	//for i := 0; i < b.N; i++ {
	//	q.Pop()
	//}
}
