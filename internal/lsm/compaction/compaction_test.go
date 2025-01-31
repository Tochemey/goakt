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

package compaction

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tochemey/goakt/v2/internal/internalpb"
)

func TestSearch(t *testing.T) {
	dir := t.TempDir()
	lm := New(dir, 4, 10, 4096)

	kvs := []*internalpb.Entry{
		{Key: "key1", Value: []byte("value1")},
		{Key: "key2", Value: []byte("value2")},
		{Key: "key3", Value: []byte("value3")},
		{Key: "key4", Value: []byte("value4")},
		{Key: "key5", Value: []byte("value5"), Tombstone: true},
		{Key: "key6", Value: []byte("value6")},
	}

	err := lm.FlushToL0(kvs)
	assert.NoError(t, err)

	entry, found := lm.Search("key1")
	assert.True(t, found)
	assert.Equal(t, "key1", entry.Key)
	assert.Equal(t, []byte("value1"), entry.Value)

	entry, found = lm.Search("key5")
	assert.True(t, found)
	assert.Equal(t, "key5", entry.Key)
	assert.Equal(t, []byte("value5"), entry.Value)
	assert.True(t, entry.Tombstone)

	entry, found = lm.Search("key7")
	assert.False(t, found)
	assert.Nil(t, entry)
}

func TestManagerScan(t *testing.T) {
	dir := t.TempDir()
	lm := New(dir, 4, 10, 4096)

	kvs := []*internalpb.Entry{
		{Key: "key1", Value: []byte("value1")},
		{Key: "key2", Value: []byte("value2")},
		{Key: "key3", Value: []byte("value3")},
		{Key: "key4", Value: []byte("value4")},
		{Key: "key5", Value: []byte("value5")},
		{Key: "key6", Value: []byte("value6")},
	}

	err := lm.FlushToL0(kvs)
	assert.NoError(t, err)

	// Perform scan
	entries := lm.Scan("key2", "key5")
	expectedEntries := []*internalpb.Entry{
		{Key: "key2", Value: []byte("value2")},
		{Key: "key3", Value: []byte("value3")},
		{Key: "key4", Value: []byte("value4")},
	}

	assert.Equal(t, expectedEntries, entries)

	// Test scan with no results
	entries = lm.Scan("key7", "key8")
	assert.Empty(t, entries)
}

func TestCompact(t *testing.T) {
	lm := New(t.TempDir(), 1, 2, 500)

	// First flush: key100-key200
	kvs1 := make([]*internalpb.Entry, 0)
	for i := 100; i <= 200; i++ {
		kvs1 = append(kvs1, &internalpb.Entry{
			Key:   fmt.Sprintf("key%d", i),
			Value: []byte(fmt.Sprintf("value%d", i)),
		})
	}
	err := lm.FlushToL0(kvs1)
	assert.NoError(t, err)

	// Perform compaction
	lm.CheckAndCompact()

	// Second flush: key150-key300
	kvs2 := make([]*internalpb.Entry, 0)
	for i := 150; i <= 300; i++ {
		kvs2 = append(kvs2, &internalpb.Entry{
			Key:   fmt.Sprintf("key%d", i),
			Value: []byte(fmt.Sprintf("value%d", i)),
		})
	}
	err = lm.FlushToL0(kvs2)
	assert.NoError(t, err)

	// Perform compaction
	lm.CheckAndCompact()

	// Third flush: key250-key400
	kvs3 := make([]*internalpb.Entry, 0)
	for i := 250; i <= 400; i++ {
		kvs3 = append(kvs3, &internalpb.Entry{
			Key:   fmt.Sprintf("key%d", i),
			Value: []byte(fmt.Sprintf("value%d", i)),
		})
	}
	err = lm.FlushToL0(kvs3)
	assert.NoError(t, err)

	// Perform compaction
	lm.CheckAndCompact()

	kvs4 := make([]*internalpb.Entry, 0)
	for i := 500; i <= 600; i++ {
		kvs4 = append(kvs4, &internalpb.Entry{
			Key:   fmt.Sprintf("key%d", i),
			Value: []byte(fmt.Sprintf("value%d", i)),
		})
	}
	err = lm.FlushToL0(kvs4)
	assert.NoError(t, err)

	// Perform compaction
	lm.CheckAndCompact()

	kvs5 := make([]*internalpb.Entry, 0)
	for i := 700; i <= 800; i++ {
		kvs5 = append(kvs5, &internalpb.Entry{
			Key:   fmt.Sprintf("key%d", i),
			Value: []byte(fmt.Sprintf("value%d", i)),
		})
	}
	err = lm.FlushToL0(kvs5)
	assert.NoError(t, err)

	// Perform compaction
	lm.CheckAndCompact()

	kvs6 := make([]*internalpb.Entry, 0)
	for i := 900; i <= 1000; i++ {
		kvs6 = append(kvs6, &internalpb.Entry{
			Key:   fmt.Sprintf("key%d", i),
			Value: []byte(fmt.Sprintf("value%d", i)),
		})
	}
	err = lm.FlushToL0(kvs6)
	assert.NoError(t, err)

	// Perform compaction
	lm.CheckAndCompact()

	// Verify the compaction result
	for i := 100; i <= 400; i++ {
		entry, found := lm.Search(fmt.Sprintf("key%d", i))
		assert.True(t, found)
		assert.Equal(t, fmt.Sprintf("key%d", i), entry.Key)
		assert.Equal(t, []byte(fmt.Sprintf("value%d", i)), entry.Value)
	}
}
