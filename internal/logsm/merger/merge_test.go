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

package merger

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tochemey/goakt/v3/internal/internalpb"
)

func TestMerge(t *testing.T) {
	list1 := []*internalpb.Entry{
		{Key: "a", Value: []byte("1")},
		{Key: "c", Value: []byte("3")},
	}
	list2 := []*internalpb.Entry{
		{Key: "b", Value: []byte("2")},
		{Key: "d", Value: []byte("4")},
	}

	expected := []*internalpb.Entry{
		{Key: "a", Value: []byte("1")},
		{Key: "b", Value: []byte("2")},
		{Key: "c", Value: []byte("3")},
		{Key: "d", Value: []byte("4")},
	}

	result := Merge(list1, list2)
	assert.Equal(t, expected, result)
}

func TestMergeDuplicate(t *testing.T) {
	list1 := []*internalpb.Entry{
		{Key: "a", Value: []byte("10")},
		{Key: "b", Value: []byte("2")},
		{Key: "c", Value: []byte("10")},
		{Key: "d", Value: []byte("4")},
	}
	list2 := []*internalpb.Entry{
		{Key: "a", Value: []byte("1")},
		{Key: "c", Value: []byte("3")},
	}

	expected := []*internalpb.Entry{
		{Key: "a", Value: []byte("1")},
		{Key: "b", Value: []byte("2")},
		{Key: "c", Value: []byte("3")},
		{Key: "d", Value: []byte("4")},
	}

	result := Merge(list1, list2)
	assert.Equal(t, expected, result)
}

func TestMergeTombstone(t *testing.T) {
	list1 := []*internalpb.Entry{
		{Key: "a", Value: []byte("10")},
		{Key: "b", Value: []byte("2")},
		{Key: "c", Value: []byte("10")},
		{Key: "d", Value: []byte("4")},
	}
	list2 := []*internalpb.Entry{
		{Key: "a", Value: []byte("1"), Tombstone: true},
		{Key: "c", Value: []byte("3"), Tombstone: true},
	}

	expected := []*internalpb.Entry{
		{Key: "b", Value: []byte("2")},
		{Key: "d", Value: []byte("4")},
	}

	result := Merge(list1, list2)
	assert.Equal(t, expected, result)
}
