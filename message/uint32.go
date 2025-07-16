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

// Uint32 wraps a Go uint32 value to implement the Message interface.
// Its value is immutable after creation.
//
// It serializes a uint32 as a 4-byte big-endian unsigned integer.
type Uint32 struct {
	value uint32 // Unexported to enforce immutability
}

// Compile-time check that Uint32 implements the Message interface.
var _ Message = (*Uint32)(nil)

// NewUint32 creates a new immutable Uint32 instance.
func NewUint32(u uint32) *Uint32 {
	return &Uint32{value: u}
}

// Value returns the underlying Go uint32 value wrapped by this message.
func (u *Uint32) Value() uint32 {
	return u.value
}

// MarshalBinary encodes the uint32 value as a 4-byte big-endian unsigned integer.
// This method satisfies the encoding.BinaryMarshaler interface.
//
// Returns an error if the underlying binary writing fails.
func (u *Uint32) MarshalBinary() ([]byte, error) {
	buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.BigEndian, u.value); err != nil {
		return nil, errors.Join(ErrMarshalBinaryFailed, fmt.Errorf("writing uint32: %w", err))
	}
	return buf.Bytes(), nil
}

// UnmarshalBinary decodes binary data into the Uint32.
// It expects 4 bytes representing a big-endian uint32. The receiver's internal value will be updated.
// This method satisfies the encoding.BinaryUnmarshaler interface.
//
// Returns ErrInvalidMessageLength if the data length is less than 4 bytes.
// Returns an error if the underlying binary reading fails.
func (u *Uint32) UnmarshalBinary(data []byte) error {
	if len(data) < 4 {
		return errors.Join(ErrInvalidMessageLength, fmt.Errorf("data too short for uint32: expected 4 bytes, got %d", len(data)))
	}

	buf := bytes.NewReader(data)
	var val uint32
	if err := binary.Read(buf, binary.BigEndian, &val); err != nil {
		return errors.Join(ErrUnmarshalBinaryFailed, fmt.Errorf("reading uint32: %w", err))
	}

	u.value = val
	return nil
}
