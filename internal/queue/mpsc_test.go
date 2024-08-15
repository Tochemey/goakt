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

// TODO: add go routine-based tests
func TestMpscQueue(t *testing.T) {
	t.Run("With Push/Pop", func(t *testing.T) {
		q := NewMpsc[int]()
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

		assert.True(t, q.Len() > 0)
	})
}
