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

// Int16 wraps a Go int16 value to implement the Message interface.
// Its value is immutable after creation.
//
// It serializes an int16 as a 2-byte big-endian signed integer.
type Int16 struct {
	value int16 // Unexported to enforce immutability
}

// Compile-time check that Int16 implements the Message interface.
var _ Message = (*Int16)(nil)

// NewInt16 creates a new immutable Int16 instance.
func NewInt16(i int16) *Int16 {
	return &Int16{value: i}
}

// Value returns the underlying Go int16 value wrapped by this message.
func (i *Int16) Value() int16 {
	return i.value
}

// MarshalBinary encodes the int16 value as a 2-byte big-endian signed integer.
// This method satisfies the encoding.BinaryMarshaler interface.
//
// Returns an error if the underlying binary writing fails.
func (i *Int16) MarshalBinary() ([]byte, error) {
	buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.BigEndian, i.value); err != nil {
		return nil, errors.Join(ErrMarshalBinaryFailed, fmt.Errorf("writing int16: %w", err))
	}
	return buf.Bytes(), nil
}

// UnmarshalBinary decodes binary data into the Int16.
// It expects 2 bytes representing a big-endian int16. The receiver's internal value will be updated.
// This method satisfies the encoding.BinaryUnmarshaler interface.
//
// Returns ErrInvalidMessageLength if the data length is less than 2 bytes.
// Returns an error if the underlying binary reading fails.
func (i *Int16) UnmarshalBinary(data []byte) error {
	if len(data) < 2 {
		return errors.Join(ErrInvalidMessageLength, fmt.Errorf("data too short for int16: expected 2 bytes, got %d", len(data)))
	}

	buf := bytes.NewReader(data)
	var val int16
	if err := binary.Read(buf, binary.BigEndian, &val); err != nil {
		return errors.Join(ErrUnmarshalBinaryFailed, fmt.Errorf("reading int16: %w", err))
	}

	i.value = val
	return nil
}
