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
	"encoding/binary"

	"github.com/tochemey/goakt/v3/internal/bufferpool"
	"github.com/tochemey/goakt/v3/internal/logsm/types"
	"github.com/tochemey/goakt/v3/internal/logsm/util"
)

// IndexEntry include index of a sstable data block
type IndexEntry struct {
	// StartKey of each Data block
	StartKey string
	// EndKey of each Data block
	EndKey string
	// offset and length of each data block
	DataHandle Block
}

// Index block stores the first and last key of each Data Block,
type Index struct {
	// Block of all data blocks of this sstable
	DataBlock Block
	Entries   []IndexEntry
}

// Search data block included the key
func (i *Index) Search(key types.Key) (Block, bool) {
	n := len(i.Entries)
	if n == 0 {
		return Block{}, false
	}

	// check if the key is beyond this sstable
	if key > i.Entries[n-1].EndKey {
		return Block{}, false
	}

	low, high := 0, n-1
	for low <= high {
		mid := low + ((high - low) >> 1)
		if i.Entries[mid].StartKey > key {
			high = mid - 1
		} else {
			if mid == n-1 || i.Entries[mid+1].StartKey > key {
				return i.Entries[mid].DataHandle, true
			}
			low = mid + 1
		}
	}
	return Block{}, false
}

func (i *Index) Scan(start, end types.Key) []Block {
	var res []Block
	for _, entry := range i.Entries {
		if entry.EndKey >= start && entry.StartKey <= end {
			res = append(res, entry.DataHandle)
		}
	}
	return res
}

func (i *Index) Encode() ([]byte, error) {
	buf := bufferpool.Pool.Get()
	defer bufferpool.Pool.Put(buf)

	if err := binary.Write(buf, binary.LittleEndian, i.DataBlock.Offset); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.LittleEndian, i.DataBlock.Length); err != nil {
		return nil, err
	}

	for _, entry := range i.Entries {
		if err := binary.Write(buf, binary.LittleEndian, uint16(len(entry.StartKey))); err != nil {
			return nil, err
		}
		if err := binary.Write(buf, binary.LittleEndian, []byte(entry.StartKey)); err != nil {
			return nil, err
		}
		if err := binary.Write(buf, binary.LittleEndian, uint16(len(entry.EndKey))); err != nil {
			return nil, err
		}
		if err := binary.Write(buf, binary.LittleEndian, []byte(entry.EndKey)); err != nil {
			return nil, err
		}
		if err := binary.Write(buf, binary.LittleEndian, entry.DataHandle.Offset); err != nil {
			return nil, err
		}
		if err := binary.Write(buf, binary.LittleEndian, entry.DataHandle.Length); err != nil {
			return nil, err
		}
	}

	compressed := bufferpool.Pool.Get()
	defer bufferpool.Pool.Put(compressed)

	if err := util.Compress(buf, compressed); err != nil {
		return nil, err
	}
	return compressed.Bytes(), nil
}

func (i *Index) Decode(index []byte) error {
	buf := bufferpool.Pool.Get()
	defer bufferpool.Pool.Put(buf)

	if err := util.Decompress(bytes.NewReader(index), buf); err != nil {
		return err
	}

	reader := bytes.NewReader(buf.Bytes())

	if err := binary.Read(reader, binary.LittleEndian, &i.DataBlock.Offset); err != nil {
		return err
	}
	if err := binary.Read(reader, binary.LittleEndian, &i.DataBlock.Length); err != nil {
		return err
	}

	for reader.Len() > 0 {
		var startKeyLen uint16
		if err := binary.Read(reader, binary.LittleEndian, &startKeyLen); err != nil {
			return err
		}
		startKey := make([]byte, startKeyLen)
		if err := binary.Read(reader, binary.LittleEndian, &startKey); err != nil {
			return err
		}
		var endKeyLen uint16
		if err := binary.Read(reader, binary.LittleEndian, &endKeyLen); err != nil {
			return err
		}
		endKey := make([]byte, endKeyLen)
		if err := binary.Read(reader, binary.LittleEndian, &endKey); err != nil {
			return err
		}
		var offset uint64
		if err := binary.Read(reader, binary.LittleEndian, &offset); err != nil {
			return err
		}
		var length uint64
		if err := binary.Read(reader, binary.LittleEndian, &length); err != nil {
			return err
		}
		i.Entries = append(i.Entries, IndexEntry{
			StartKey: string(startKey),
			EndKey:   string(endKey),
			DataHandle: Block{
				Offset: offset,
				Length: length,
			},
		})
	}
	return nil
}
