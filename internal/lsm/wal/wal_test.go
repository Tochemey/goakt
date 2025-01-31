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

package wal

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tochemey/goakt/v2/internal/internalpb"
)

func TestCreateAndDelete(t *testing.T) {
	dir := t.TempDir()
	wal, err := Create(dir)
	assert.NoError(t, err)
	assert.NotNil(t, wal)

	err = wal.Close()
	assert.NoError(t, err)

	err = wal.Delete()
	assert.NoError(t, err)

	_, err = os.Stat(wal.path)
	assert.True(t, os.IsNotExist(err))
}

func TestOpen(t *testing.T) {
	dir := t.TempDir()
	wal, err := Create(dir)
	assert.NoError(t, err)
	assert.NotNil(t, wal)
	err = wal.file.Close()
	assert.NotNil(t, wal)

	wal2, err := Open(wal.path)
	assert.NoError(t, err)

	err = wal2.Delete()
	assert.NoError(t, err)

	_, err = os.Stat(wal.path)
	assert.True(t, os.IsNotExist(err))
}

func TestWriteAndRead(t *testing.T) {
	dir := t.TempDir()
	wal, err := Create(dir)
	assert.NoError(t, err)
	assert.NotNil(t, wal)

	entries := []*internalpb.Entry{
		{
			Key:       "hello",
			Value:     []byte("world"),
			Tombstone: false,
		},
		{
			Key:       "foo",
			Value:     []byte("bar"),
			Tombstone: true,
		},
		{
			Key:       "foiver",
			Value:     []byte("originium"),
			Tombstone: false,
		},
	}

	err = wal.Write(entries...)
	assert.NoError(t, err)

	readEntries, err := wal.Read()
	assert.NoError(t, err)
	assert.Equal(t, entries, readEntries)

	err = wal.Delete()
	assert.NoError(t, err)
}
