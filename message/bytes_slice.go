/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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

package message

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// BytesSlice wraps a Go []byte to implement the Message interface.
// It serializes the byte slice by first encoding its length as a uint32 (big-endian),
// followed by the raw bytes of the slice itself.
type BytesSlice struct {
	value []byte
}

// Compile-time check that BytesSlice implements the Message interface.
var _ Message = (*BytesSlice)(nil)

// NewByteSliceMessage creates a new BytesSlice instance.
func NewByteSliceMessage(b []byte) *BytesSlice {
	return &BytesSlice{value: b}
}

// Value returns the underlying byte slice value.
func (b *BytesSlice) Value() []byte {
	return b.value
}

// MarshalBinary encodes the byte slice's length (uint32) and then its raw bytes.
// This method satisfies the encoding.BinaryMarshaler interface.
func (b *BytesSlice) MarshalBinary() ([]byte, error) {
	length := uint32(len(b.value))

	buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.BigEndian, length); err != nil {
		return nil, errors.Join(ErrMarshalBinaryFailed, fmt.Errorf("writing length: %w", err))
	}
	if _, err := buf.Write(b.value); err != nil {
		return nil, errors.Join(ErrMarshalBinaryFailed, fmt.Errorf("writing byte slice: %w", err))
	}
	return buf.Bytes(), nil
}

// UnmarshalBinary decodes binary data into a BytesSlice.
// It first reads a uint32 length prefix, then reads that many bytes
// to reconstruct the slice.
// This method satisfies the encoding.BinaryUnmarshaler interface.
func (b *BytesSlice) UnmarshalBinary(data []byte) error {
	if len(data) < 4 { // Minimum 4 bytes for length prefix
		return errors.Join(ErrInvalidMessageLength, fmt.Errorf("data too short for byte slice length prefix: %d bytes", len(data)))
	}

	buf := bytes.NewReader(data)
	var length uint32
	if err := binary.Read(buf, binary.BigEndian, &length); err != nil {
		return errors.Join(ErrUnmarshalBinaryFailed, fmt.Errorf("reading byte slice length: %w", err))
	}

	if int(length) > buf.Len() {
		return errors.Join(ErrInvalidMessageLength, fmt.Errorf("declared byte slice length %d exceeds available data %d", length, buf.Len()))
	}

	b.value = make([]byte, length)
	if _, err := buf.Read(b.value); err != nil {
		return errors.Join(ErrUnmarshalBinaryFailed, fmt.Errorf("reading byte slice: %w", err))
	}

	return nil
}
