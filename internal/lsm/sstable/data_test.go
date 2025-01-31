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
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tochemey/goakt/v2/internal/internalpb"
)

func TestDataEncodeDecode(t *testing.T) {
	data := Data{
		Entries: []*internalpb.Entry{
			{Key: "key1", Value: []byte("value1"), Tombstone: false},
			{Key: "key2", Value: []byte("value2"), Tombstone: true},
		},
	}

	// Test Encode
	encoded, err := data.Encode()
	assert.NoError(t, err)
	assert.NotNil(t, encoded)

	// Test Decode
	var decodedData Data
	err = decodedData.Decode(encoded)
	assert.NoError(t, err)
	assert.Equal(t, data, decodedData)
}

func TestSearch(t *testing.T) {
	data := Data{
		Entries: []*internalpb.Entry{
			{Key: "key1", Value: []byte("value1"), Tombstone: false},
			{Key: "key2", Value: []byte("value2"), Tombstone: true},
			{Key: "key3", Value: []byte("value3"), Tombstone: false},
		},
	}

	tests := []struct {
		key      string
		expected *internalpb.Entry
		found    bool
	}{
		{"key1", &internalpb.Entry{Key: "key1", Value: []byte("value1"), Tombstone: false}, true},
		{"key2", &internalpb.Entry{Key: "key2", Value: []byte("value2"), Tombstone: true}, true},
		{"key3", &internalpb.Entry{Key: "key3", Value: []byte("value3"), Tombstone: false}, true},
		{"key4", nil, false},
	}

	for _, tt := range tests {
		entry, found := data.Search(tt.key)
		assert.Equal(t, tt.found, found)
		assert.Equal(t, tt.expected, entry)
	}
}

func TestDataEncodeDecodeMultiple(t *testing.T) {
	entries := []*internalpb.Entry{
		{Key: "asy1", Value: []byte("value1"), Tombstone: false},
		{Key: "kssdy2", Value: []byte("value2"), Tombstone: true},
		{Key: "keyiiwadc", Value: []byte("value3"), Tombstone: false},
		{Key: "y4", Value: []byte{}, Tombstone: true},
		{Key: "sdasey1", Value: []byte("value1"), Tombstone: false},
		{Key: "ooiney2", Value: []byte("value2"), Tombstone: true},
		{Key: "iinnisaksady3", Value: []byte("value3"), Tombstone: false},
		{Key: "kiiwadc", Value: []byte("value3"), Tombstone: false},
		{Key: "4", Value: []byte{}, Tombstone: true},
		{Key: "asy1", Value: []byte("value1"), Tombstone: false},
		{Key: "ooey2", Value: []byte("value2"), Tombstone: true},
		{Key: "iiissady3", Value: []byte("value3"), Tombstone: false},
	}

	// Create multiple Data objects
	dataList := []Data{
		{Entries: entries[:1]},
		{Entries: entries[1:6]},
		{Entries: entries[6:]},
	}

	var buf bytes.Buffer
	// Encode each Data object separately
	for _, data := range dataList {
		encoded, err := data.Encode()
		assert.NoError(t, err)
		assert.NotNil(t, encoded)
		buf.Write(encoded)
	}

	var data Data
	err := data.Decode(buf.Bytes())
	assert.NoError(t, err)
	assert.Equal(t, entries, data.Entries)
}

func TestScan(t *testing.T) {
	data := Data{
		Entries: []*internalpb.Entry{
			{Key: "key1", Value: []byte("value1"), Tombstone: false},
			{Key: "key2", Value: []byte("value2"), Tombstone: true},
			{Key: "key3", Value: []byte("value3"), Tombstone: false},
			{Key: "key4", Value: []byte("value4"), Tombstone: false},
			{Key: "key5", Value: []byte("value5"), Tombstone: true},
		},
	}

	tests := []struct {
		start    string
		end      string
		expected []*internalpb.Entry
	}{
		{"key1", "key3", []*internalpb.Entry{
			{Key: "key1", Value: []byte("value1"), Tombstone: false},
			{Key: "key2", Value: []byte("value2"), Tombstone: true},
		}},
		{"key2", "key5", []*internalpb.Entry{
			{Key: "key2", Value: []byte("value2"), Tombstone: true},
			{Key: "key3", Value: []byte("value3"), Tombstone: false},
			{Key: "key4", Value: []byte("value4"), Tombstone: false},
		}},
		{"key3", "key6", []*internalpb.Entry{
			{Key: "key3", Value: []byte("value3"), Tombstone: false},
			{Key: "key4", Value: []byte("value4"), Tombstone: false},
			{Key: "key5", Value: []byte("value5"), Tombstone: true},
		}},
		{"key0", "key6", []*internalpb.Entry{
			{Key: "key1", Value: []byte("value1"), Tombstone: false},
			{Key: "key2", Value: []byte("value2"), Tombstone: true},
			{Key: "key3", Value: []byte("value3"), Tombstone: false},
			{Key: "key4", Value: []byte("value4"), Tombstone: false},
			{Key: "key5", Value: []byte("value5"), Tombstone: true},
		}},
		{"key6", "key7", nil},
	}

	for _, tt := range tests {
		result := data.Scan(tt.start, tt.end)
		assert.Equal(t, tt.expected, result)
	}
}
