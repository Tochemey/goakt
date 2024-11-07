/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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

package slice

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChunk(t *testing.T) {
	t.Run("With empty slice", func(t *testing.T) {
		var items []int
		chunkSize := 2
		chunks := Chunk(items, chunkSize)
		require.Empty(t, chunks)
	})
	t.Run("With chunk size a dividend of total number of items", func(t *testing.T) {
		items := []int{2, 7, 9, 4, 6, 10}
		chunkSize := 2
		chunks := Chunk(items, chunkSize)
		expected := [][]int{
			{2, 7},
			{9, 4},
			{6, 10},
		}

		require.EqualValues(t, len(expected), len(chunks))
		require.ElementsMatch(t, expected[0], chunks[0])
		require.ElementsMatch(t, expected[1], chunks[1])
		assert.ElementsMatch(t, expected[2], chunks[2])
	})
	t.Run("With chunk size not a dividend of total number of items", func(t *testing.T) {
		items := []int{2, 7, 9, 4, 6, 10, 11}
		chunkSize := 2
		chunks := Chunk(items, chunkSize)
		expected := [][]int{
			{2, 7},
			{9, 4},
			{6, 10},
			{11},
		}

		require.EqualValues(t, len(expected), len(chunks))
		require.ElementsMatch(t, expected[0], chunks[0])
		require.ElementsMatch(t, expected[1], chunks[1])
		require.ElementsMatch(t, expected[2], chunks[2])
		assert.ElementsMatch(t, expected[3], chunks[3])
	})
}
