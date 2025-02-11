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
	"errors"

	"github.com/tochemey/goakt/v3/internal/bufferpool"
)

const _magic uint64 = 0x5bc2aa5766250562

var ErrInvalidMagic = errors.New("error invalid magic")

// Footer
// 16 + 16 + 8 = 40 bytes
type Footer struct {
	MetaBlock  Block
	IndexBlock Block
	Magic      uint64
}

func (f *Footer) Encode() ([]byte, error) {
	buf := bufferpool.Pool.Get()
	defer bufferpool.Pool.Put(buf)

	if err := binary.Write(buf, binary.LittleEndian, f.MetaBlock.Offset); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.LittleEndian, f.MetaBlock.Length); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.LittleEndian, f.IndexBlock.Offset); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.LittleEndian, f.IndexBlock.Length); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.LittleEndian, f.Magic); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (f *Footer) Decode(footer []byte) error {
	reader := bytes.NewReader(footer)
	var metaOffset uint64
	if err := binary.Read(reader, binary.LittleEndian, &metaOffset); err != nil {
		return err
	}
	var metaLength uint64
	if err := binary.Read(reader, binary.LittleEndian, &metaLength); err != nil {
		return err
	}
	var indexOffset uint64
	if err := binary.Read(reader, binary.LittleEndian, &indexOffset); err != nil {
		return err
	}
	var indexLength uint64
	if err := binary.Read(reader, binary.LittleEndian, &indexLength); err != nil {
		return err
	}
	var magic uint64
	if err := binary.Read(reader, binary.LittleEndian, &magic); err != nil {
		return err
	}
	if magic != _magic {
		return ErrInvalidMagic
	}
	f.Magic = magic
	f.MetaBlock.Offset = metaOffset
	f.MetaBlock.Length = metaLength
	f.IndexBlock.Offset = indexOffset
	f.IndexBlock.Length = indexLength
	return nil
}
