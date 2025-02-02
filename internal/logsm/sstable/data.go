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

	"github.com/tochemey/goakt/v2/internal/bufferpool"
	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/internal/logsm/types"
	"github.com/tochemey/goakt/v2/internal/logsm/util"
)

// Data Block
type Data struct {
	Entries []*internalpb.Entry
}

func (d *Data) Search(key types.Key) (*internalpb.Entry, bool) {
	low, high := 0, len(d.Entries)-1
	for low <= high {
		mid := low + ((high - low) >> 1)
		if d.Entries[mid].GetKey() < key {
			low = mid + 1
		} else if d.Entries[mid].GetKey() > key {
			high = mid - 1
		} else {
			return d.Entries[mid], true
		}
	}
	return nil, false
}

func (d *Data) Scan(start, end types.Key) []*internalpb.Entry {
	var res []*internalpb.Entry
	var found bool
	low, high := 0, len(d.Entries)-1

	// find the first key >= start
	var mid int
	for low <= high {
		mid = low + ((high - low) >> 1)
		if d.Entries[mid].GetKey() >= start {
			if mid == 0 || d.Entries[mid-1].GetKey() < start {
				// used as return
				found = true
				break
			}
			high = mid - 1
		} else {
			low = mid + 1
		}
	}

	for i := mid; i < len(d.Entries) && d.Entries[i].GetKey() < end && found; i++ {
		res = append(res, d.Entries[i])
	}

	return res
}

func (d *Data) Encode() ([]byte, error) {
	buf := bufferpool.Pool.Get()
	defer bufferpool.Pool.Put(buf)

	var prevKey string
	for _, entry := range d.Entries {
		lcp := util.CommonPrefixLength(entry.GetKey(), prevKey)
		suffix := entry.GetKey()[lcp:]

		// lcp
		if err := binary.Write(buf, binary.LittleEndian, uint16(lcp)); err != nil {
			return nil, err
		}

		// suffix length
		if err := binary.Write(buf, binary.LittleEndian, uint16(len(suffix))); err != nil {
			return nil, err
		}
		// suffix
		if err := binary.Write(buf, binary.LittleEndian, []byte(suffix)); err != nil {
			return nil, err
		}

		// value length
		if err := binary.Write(buf, binary.LittleEndian, uint16(len(entry.Value))); err != nil {
			return nil, err
		}
		// value
		if err := binary.Write(buf, binary.LittleEndian, entry.Value); err != nil {
			return nil, err
		}

		// tombstone
		tombstone := uint8(0)
		if entry.Tombstone {
			tombstone = 1
		}
		if err := binary.Write(buf, binary.LittleEndian, tombstone); err != nil {
			return nil, err
		}

		prevKey = entry.Key
	}

	compressed := bufferpool.Pool.Get()
	defer bufferpool.Pool.Put(compressed)

	if err := util.Compress(buf, compressed); err != nil {
		return nil, err
	}
	return compressed.Bytes(), nil
}

func (d *Data) Decode(data []byte) error {
	buf := bufferpool.Pool.Get()
	defer bufferpool.Pool.Put(buf)

	if err := util.Decompress(bytes.NewReader(data), buf); err != nil {
		return err
	}

	reader := bytes.NewReader(buf.Bytes())
	var prevKey string
	for reader.Len() > 0 {
		// lcp
		var lcp uint16
		if err := binary.Read(reader, binary.LittleEndian, &lcp); err != nil {
			return err
		}

		// suffix length
		var suffixLen uint16
		if err := binary.Read(reader, binary.LittleEndian, &suffixLen); err != nil {
			return err
		}
		// suffix
		suffix := make([]byte, suffixLen)
		if err := binary.Read(reader, binary.LittleEndian, &suffix); err != nil {
			return err
		}

		// value length
		var valueLen uint16
		if err := binary.Read(reader, binary.LittleEndian, &valueLen); err != nil {
			return err
		}
		// value
		value := make([]byte, valueLen)
		if err := binary.Read(reader, binary.LittleEndian, &value); err != nil {
			return err
		}

		var tombstone uint8
		if err := binary.Read(reader, binary.LittleEndian, &tombstone); err != nil {
			return err
		}

		key := prevKey[:lcp] + string(suffix)
		d.Entries = append(d.Entries, &internalpb.Entry{
			Key:       key,
			Value:     value,
			Tombstone: tombstone == 1,
		})

		prevKey = key
	}
	return nil
}
