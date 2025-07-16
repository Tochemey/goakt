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

// Float32 wraps a Go float32 value to implement the Message interface.
// Its value is immutable after creation.
//
// It serializes a float32 as a 4-byte big-endian floating-point number.
// This provides a fixed-size, architecture-independent binary representation.
type Float32 struct {
	value float32 // Unexported to enforce immutability
}

// Compile-time check that Float32 implements the Message interface.
var _ Message = (*Float32)(nil)

// NewFloat32 creates a new immutable Float32 instance.
func NewFloat32(f float32) *Float32 {
	return &Float32{value: f}
}

// Value returns the underlying Go float32 value wrapped by this message.
func (f *Float32) Value() float32 {
	return f.value
}

// MarshalBinary encodes the float32 value into a 4-byte big-endian binary format.
//
// This method satisfies the encoding.BinaryMarshaler interface.
//
// Returns an error if the underlying binary writing fails.
func (f *Float32) MarshalBinary() ([]byte, error) {
	buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.BigEndian, f.value); err != nil {
		return nil, errors.Join(ErrMarshalBinaryFailed, fmt.Errorf("writing float32: %w", err))
	}
	return buf.Bytes(), nil
}

// UnmarshalBinary decodes binary data into the Float32.
//
// It expects the input `data` to be 4 bytes long, representing a
// big-endian float32. The receiver's internal value will be updated.
//
// This method satisfies the encoding.BinaryUnmarshaler interface.
//
// Returns ErrInvalidMessageLength if the data length is less than 4 bytes.
// Returns an error if the underlying binary reading fails.
func (f *Float32) UnmarshalBinary(data []byte) error {
	if len(data) < 4 { // float32 is 4 bytes
		return errors.Join(ErrInvalidMessageLength, fmt.Errorf("data too short for float32: expected 4 bytes, got %d", len(data)))
	}

	buf := bytes.NewReader(data)
	var val float32
	if err := binary.Read(buf, binary.BigEndian, &val); err != nil {
		return errors.Join(ErrUnmarshalBinaryFailed, fmt.Errorf("reading float32: %w", err))
	}

	f.value = val
	return nil
}
