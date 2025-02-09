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

package skiplist

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tochemey/goakt/v3/internal/internalpb"
)

func TestNew(t *testing.T) {
	sl := New(4, 0.5)
	assert.NotNil(t, sl)
	assert.Equal(t, 4, sl.maxLevel)
	assert.Equal(t, 0.5, sl.probability)
	assert.Equal(t, 1, sl.level)
	assert.Equal(t, 0, sl.size)
	assert.NotNil(t, sl.head)
	assert.Equal(t, _head, sl.head.Key)
}

func TestSetAndGet(t *testing.T) {
	sl := New(4, 0.5)
	entry := &internalpb.Entry{Key: "key1", Value: []byte("value1"), Tombstone: false}
	sl.Set(entry)

	result, found := sl.Get("key1")
	assert.True(t, found)
	assert.Equal(t, entry, result)

	// Test updating the entry
	entry.Value = []byte("value2")
	sl.Set(entry)
	result, found = sl.Get("key1")
	assert.True(t, found)
	assert.Equal(t, entry, result)
}

func TestRange(t *testing.T) {
	sl := New(4, 0.5)
	entries := []*internalpb.Entry{
		{Key: "key1", Value: []byte("value1"), Tombstone: false},
		{Key: "key2", Value: []byte("value2"), Tombstone: false},
		{Key: "key3", Value: []byte("value3"), Tombstone: false},
		{Key: "key4", Value: []byte("value4"), Tombstone: false},
	}

	for _, entry := range entries {
		sl.Set(entry)
	}

	tests := []struct {
		start, end string
		expected   []*internalpb.Entry
	}{
		{"key1", "key3", entries[:2]},
		{"key2", "key4", entries[1:3]},
		{"key1", "key5", entries},
		{"key3", "key3", nil},
		{"key0", "key1", nil},
	}

	for _, tt := range tests {
		result := sl.Scan(tt.start, tt.end)
		assert.Equal(t, tt.expected, result)
	}
}

func TestGetNonExistent(t *testing.T) {
	sl := New(4, 0.5)
	result, found := sl.Get("nonexistent")
	assert.False(t, found)
	assert.Nil(t, result)
}

func TestDelete(t *testing.T) {
	sl := New(4, 0.5)
	entry1 := &internalpb.Entry{Key: "key1", Value: []byte("value1"), Tombstone: false}
	entry2 := &internalpb.Entry{Key: "key2", Value: []byte("value2"), Tombstone: false}
	sl.Set(entry1)
	sl.Set(entry2)

	// Delete an existing entry
	deleted := sl.Delete("key1")
	assert.True(t, deleted)

	// Verify the entry is deleted
	_, found := sl.Get("key1")
	assert.False(t, found)

	// Verify the other entry still exists
	result, found := sl.Get("key2")
	assert.True(t, found)
	assert.Equal(t, entry2, result)

	// Try to delete a non-existent entry
	deleted = sl.Delete("nonexistent")
	assert.False(t, deleted)
}

func TestAll(t *testing.T) {
	sl := New(4, 0.5)
	entries := []*internalpb.Entry{
		{Key: "key1", Value: []byte("value1"), Tombstone: false},
		{Key: "key2", Value: nil, Tombstone: true},
		{Key: "key3", Value: []byte("value3"), Tombstone: false},
	}

	for _, entry := range entries {
		sl.Set(entry)
	}

	allEntries := sl.All()
	assert.Equal(t, len(entries), len(allEntries))
	for i, entry := range entries {
		assert.Equal(t, entry, allEntries[i])
	}
}

func TestReset(t *testing.T) {
	sl := New(4, 0.5)
	entry := &internalpb.Entry{Key: "key1", Value: []byte("value1"), Tombstone: false}
	sl.Set(entry)

	sl = sl.Reset()
	assert.Equal(t, 0, sl.size)
	assert.Equal(t, 1, sl.level)
	assert.Nil(t, sl.head.next[0])
}
