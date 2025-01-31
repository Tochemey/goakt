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
	"time"

	"github.com/tochemey/goakt/v2/internal/bufferpool"
	"github.com/tochemey/goakt/v2/internal/internalpb"
)

type SSTable struct {
	DataBlocks []Data
	MetaBlock  Meta
	IndexBlock Index
	Footer     Footer
}

type Block struct {
	Offset uint64
	Length uint64
}

func Build(entries []*internalpb.Entry, dataBlockSize, level int) (Index, []byte) {
	buf := bufferpool.Pool.Get()
	defer bufferpool.Pool.Put(buf)

	// build data blocks
	var dataBlocks []Data
	var currSize int
	var data Data
	for _, entry := range entries {
		if currSize > dataBlockSize {
			dataBlocks = append(dataBlocks, data)
			// reset
			data = Data{}
			currSize = 0
		}
		// key, value, tombstone byte sizes
		entrySize := len(entry.Key) + len(entry.Value) + 1
		currSize += entrySize
		data.Entries = append(data.Entries, entry)
	}
	if len(data.Entries) > 0 {
		dataBlocks = append(dataBlocks, data)
	}

	// build index block
	var indexBlock Index
	var offset uint64
	for _, block := range dataBlocks {
		dataBytes, err := block.Encode()
		if err != nil {
			panic(err)
		}
		length := uint64(len(dataBytes))
		indexBlock.Entries = append(indexBlock.Entries, IndexEntry{
			StartKey: block.Entries[0].Key,
			EndKey:   block.Entries[len(block.Entries)-1].Key,
			DataHandle: Block{
				Offset: offset,
				Length: length,
			},
		})
		offset += length

		// write data blocks
		if _, err = buf.Write(dataBytes); err != nil {
			panic(err)
		}
	}
	indexBlock.DataBlock = Block{
		Offset: 0,
		Length: offset,
	}

	// build meta block
	metaBlock := Meta{
		CreatedUnix: time.Now().Unix(),
		Level:       uint64(level),
	}
	metaBytes, err := metaBlock.Encode()
	if err != nil {
		panic(err)
	}
	metaOffset := offset
	metaLength := uint64(len(metaBytes))

	// write meta block
	if _, err = buf.Write(metaBytes); err != nil {
		panic(err)
	}

	// build footer
	indexBytes, err := indexBlock.Encode()
	if err != nil {
		panic(err)
	}
	indexOffset := metaOffset + metaLength
	indexLength := uint64(len(indexBytes))

	// write index block
	if _, err = buf.Write(indexBytes); err != nil {
		panic(err)
	}

	footer := Footer{
		MetaBlock: Block{
			Offset: metaOffset,
			Length: metaLength,
		},
		IndexBlock: Block{
			Offset: indexOffset,
			Length: indexLength,
		},
		Magic: _magic,
	}
	footerBytes, err := footer.Encode()
	if err != nil {
		panic(err)
	}

	// write footer
	if _, err = buf.Write(footerBytes); err != nil {
		panic(err)
	}

	return indexBlock, buf.Bytes()
}
