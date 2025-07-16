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

// Uint64 wraps a Go uint64 value to implement the Message interface.
// Its value is immutable after creation.
//
// It serializes a uint64 as an 8-byte big-endian unsigned integer.
type Uint64 struct {
	value uint64 // Unexported to enforce immutability
}

// Compile-time check that Uint64 implements the Message interface.
var _ Message = (*Uint64)(nil)

// NewUint64Message creates a new immutable Uint64 instance.
func NewUint64Message(u uint64) *Uint64 {
	return &Uint64{value: u}
}

// Value returns the underlying Go uint64 value wrapped by this message.
func (u *Uint64) Value() uint64 {
	return u.value
}

// MarshalBinary encodes the uint64 value as an 8-byte big-endian unsigned integer.
// This method satisfies the encoding.BinaryMarshaler interface.
//
// Returns an error if the underlying binary writing fails.
func (u *Uint64) MarshalBinary() ([]byte, error) {
	buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.BigEndian, u.value); err != nil {
		return nil, errors.Join(ErrMarshalBinaryFailed, fmt.Errorf("writing uint64: %w", err))
	}
	return buf.Bytes(), nil
}

// UnmarshalBinary decodes binary data into the Uint64.
// It expects 8 bytes representing a big-endian uint64. The receiver's internal value will be updated.
// This method satisfies the encoding.BinaryUnmarshaler interface.
//
// Returns ErrInvalidMessageLength if the data length is less than 8 bytes.
// Returns an error if the underlying binary reading fails.
func (u *Uint64) UnmarshalBinary(data []byte) error {
	if len(data) < 8 {
		return errors.Join(ErrInvalidMessageLength, fmt.Errorf("data too short for uint64: expected 8 bytes, got %d", len(data)))
	}

	buf := bytes.NewReader(data)
	var val uint64
	if err := binary.Read(buf, binary.BigEndian, &val); err != nil {
		return errors.Join(ErrUnmarshalBinaryFailed, fmt.Errorf("reading uint64: %w", err))
	}

	u.value = val
	return nil
}
