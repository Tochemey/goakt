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

package logsm

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/logsm/types"
	"github.com/tochemey/goakt/v3/internal/util"
	"github.com/tochemey/goakt/v3/log"
)

func TestOpen(t *testing.T) {
	dir := t.TempDir()

	db, err := Open(dir,
		WithSkipListMaxLevel(4),
		WithProbability(0.5),
		WithDataBlockByteThreshold(4096),
		WithL0TargetNum(4),
		WithLogger(log.DiscardLogger),
		WithMemTableSizeThreshold(1024))

	require.NoError(t, err)
	require.NotNil(t, db)
	require.Equal(t, StateInitialized, db.State())
	util.Pause(time.Second * 1)
	require.Equal(t, StateOpened, db.State())
	db.Close()
}

func TestClose(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir,
		WithSkipListMaxLevel(4),
		WithProbability(0.5),
		WithDataBlockByteThreshold(4096),
		WithL0TargetNum(4),
		WithMemTableSizeThreshold(1024))

	require.NoError(t, err)
	require.NotNil(t, db)

	db.Close()
	assert.Equal(t, StateClosed, db.State())
}

func TestSetAndGet(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir,
		WithSkipListMaxLevel(4),
		WithProbability(0.5),
		WithDataBlockByteThreshold(4096),
		WithL0TargetNum(4),
		WithMemTableSizeThreshold(1024))
	require.NoError(t, err)
	require.NotNil(t, db)

	defer db.Close()

	key := "key1"
	value := []byte("value1")

	err = db.Set(key, value)
	require.NoError(t, err)
	result, found := db.Get(key)
	require.True(t, found)
	assert.Equal(t, value, result)
}

func TestScan(t *testing.T) {
	dir := t.TempDir()

	db, err := Open(dir,
		WithSkipListMaxLevel(4),
		WithProbability(0.5),
		WithDataBlockByteThreshold(10),
		WithLevelRatio(2),
		WithL0TargetNum(1),
		WithMemTableSizeThreshold(50))

	require.NoError(t, err)
	require.NotNil(t, db)

	defer db.Close()

	// Insert test data
	entries := []*internalpb.Entry{
		{Key: "key1", Value: []byte("value1"), Tombstone: false},
		{Key: "key2", Value: []byte("value2"), Tombstone: false},
		{Key: "key3", Value: []byte("value3"), Tombstone: false},
		{Key: "key4", Value: []byte("value4"), Tombstone: false},
		{Key: "key5", Value: []byte("value5"), Tombstone: false},
	}

	for _, entry := range entries {
		err = db.Set(entry.Key, entry.Value)
		require.NoError(t, err)
	}

	tests := []struct {
		start    string
		end      string
		expected []types.KV
	}{
		{"key1", "key3", []types.KV{
			{Key: "key1", Value: []byte("value1")},
			{Key: "key2", Value: []byte("value2")},
		}},
		{"key2", "key5", []types.KV{
			{Key: "key2", Value: []byte("value2")},
			{Key: "key3", Value: []byte("value3")},
			{Key: "key4", Value: []byte("value4")},
		}},
		{"key3", "key6", []types.KV{
			{Key: "key3", Value: []byte("value3")},
			{Key: "key4", Value: []byte("value4")},
			{Key: "key5", Value: []byte("value5")},
		}},
		{"key0", "key6", []types.KV{
			{Key: "key1", Value: []byte("value1")},
			{Key: "key2", Value: []byte("value2")},
			{Key: "key3", Value: []byte("value3")},
			{Key: "key4", Value: []byte("value4")},
			{Key: "key5", Value: []byte("value5")},
		}},
		{"key6", "key7", nil},
	}

	for _, tt := range tests {
		result := db.Scan(tt.start, tt.end)
		require.Equal(t, tt.expected, result)
	}
}

func TestDelete(t *testing.T) {
	dir := t.TempDir()

	db, err := Open(dir,
		WithSkipListMaxLevel(4),
		WithProbability(0.5),
		WithDataBlockByteThreshold(4096),
		WithLevelRatio(10),
		WithL0TargetNum(4),
		WithMemTableSizeThreshold(1024))

	require.NoError(t, err)
	require.NotNil(t, db)

	defer db.Close()

	key := "key1"
	value := []byte("value1")

	err = db.Set(key, value)
	require.NoError(t, err)
	err = db.Delete(key)
	require.NoError(t, err)
	result, found := db.Get(key)
	assert.False(t, found)
	assert.Nil(t, result)
}
