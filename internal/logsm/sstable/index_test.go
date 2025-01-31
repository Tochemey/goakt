/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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

package sstable

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIndexSearch(t *testing.T) {
	index := Index{
		Entries: []IndexEntry{
			{
				StartKey: "b",
				DataHandle: Block{
					Offset: 2,
					Length: 1,
				},
			},
			{
				StartKey: "c",
				DataHandle: Block{
					Offset: 3,
					Length: 1,
				},
			},
			{
				StartKey: "d",
				DataHandle: Block{
					Offset: 4,
					Length: 1,
				},
			},
			{
				StartKey: "f",
				EndKey:   "h",
				DataHandle: Block{
					Offset: 6,
					Length: 1,
				},
			},
		},
	}

	dataH, found := index.Search("b")
	assert.True(t, found)
	assert.Equal(t, uint64(2), dataH.Offset)

	dataH, found = index.Search("e")
	assert.True(t, found)
	assert.Equal(t, uint64(4), dataH.Offset)

	dataH, found = index.Search("a")
	assert.False(t, found)
	assert.Equal(t, uint64(0), dataH.Offset)

	dataH, found = index.Search("f")
	assert.True(t, found)
	assert.Equal(t, uint64(6), dataH.Offset)

	dataH, found = index.Search("g")
	assert.True(t, found)
	assert.Equal(t, uint64(6), dataH.Offset)

	dataH, found = index.Search("i")
	assert.False(t, found)
	assert.Equal(t, uint64(0), dataH.Offset)
}

func TestIndexEncodeDecode(t *testing.T) {
	index := Index{
		DataBlock: Block{
			Offset: 0,
			Length: 100,
		},
		Entries: []IndexEntry{
			{
				StartKey: "a",
				EndKey:   "q",
				DataHandle: Block{
					Offset: 1,
					Length: 1,
				},
			},
			{
				StartKey: "b",
				EndKey:   "w",
				DataHandle: Block{
					Offset: 2,
					Length: 1,
				},
			},
			{
				StartKey: "c",
				EndKey:   "e",
				DataHandle: Block{
					Offset: 3,
					Length: 1,
				},
			},
		},
	}

	encoded, err := index.Encode()
	assert.NoError(t, err)
	assert.NotNil(t, encoded)

	var decodedIndex Index
	err = decodedIndex.Decode(encoded)
	assert.NoError(t, err)
	assert.Equal(t, index, decodedIndex)
}
