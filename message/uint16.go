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

// Uint16 wraps a Go uint16 value to implement the Message interface.
// Its value is immutable after creation.
//
// It serializes a uint16 as a 2-byte big-endian unsigned integer.
type Uint16 struct {
	value uint16 // Unexported to enforce immutability
}

// Compile-time check that Uint16 implements the Message interface.
var _ Message = (*Uint16)(nil)

// NewUint16 creates a new immutable Uint16 instance.
func NewUint16(u uint16) *Uint16 {
	return &Uint16{value: u}
}

// Value returns the underlying Go uint16 value wrapped by this message.
func (u *Uint16) Value() uint16 {
	return u.value
}

// MarshalBinary encodes the uint16 value as a 2-byte big-endian unsigned integer.
// This method satisfies the encoding.BinaryMarshaler interface.
//
// Returns an error if the underlying binary writing fails.
func (u *Uint16) MarshalBinary() ([]byte, error) {
	buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.BigEndian, u.value); err != nil {
		return nil, errors.Join(ErrMarshalBinaryFailed, fmt.Errorf("writing uint16: %w", err))
	}
	return buf.Bytes(), nil
}

// UnmarshalBinary decodes binary data into the Uint16.
// It expects 2 bytes representing a big-endian uint16. The receiver's internal value will be updated.
// This method satisfies the encoding.BinaryUnmarshaler interface.
//
// Returns ErrInvalidMessageLength if the data length is less than 2 bytes.
// Returns an error if the underlying binary reading fails.
func (u *Uint16) UnmarshalBinary(data []byte) error {
	if len(data) < 2 {
		return errors.Join(ErrInvalidMessageLength, fmt.Errorf("data too short for uint16: expected 2 bytes, got %d", len(data)))
	}

	buf := bytes.NewReader(data)
	var val uint16
	if err := binary.Read(buf, binary.BigEndian, &val); err != nil {
		return errors.Join(ErrUnmarshalBinaryFailed, fmt.Errorf("reading uint16: %w", err))
	}

	u.value = val
	return nil
}
