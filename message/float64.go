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

// Float64 wraps a Go float64 to implement the Message interface.
// It serializes the float64 using binary.BigEndian.
type Float64 struct {
	value float64
}

// Compile-time check that Float64 implements the Message interface.
var _ Message = (*Float64)(nil)

// NewFloat64 creates a new Float64 instance.
func NewFloat64(f float64) *Float64 {
	return &Float64{value: f}
}

// Value returns the underlying float64 value.
func (f *Float64) Value() float64 {
	return f.value
}

// MarshalBinary encodes the float64 using binary.BigEndian.
// This method satisfies the encoding.BinaryMarshaler interface.
func (f *Float64) MarshalBinary() ([]byte, error) {
	buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.BigEndian, f.value); err != nil {
		return nil, errors.Join(ErrMarshalBinaryFailed, fmt.Errorf("writing float64: %w", err))
	}
	return buf.Bytes(), nil
}

// UnmarshalBinary decodes binary data into a Float64.
// It expects an 8-byte big-endian float64.
// This method satisfies the encoding.BinaryUnmarshaler interface.
func (f *Float64) UnmarshalBinary(data []byte) error {
	if len(data) < 8 { // float64 is 8 bytes
		return errors.Join(ErrInvalidMessageLength, fmt.Errorf("data too short for float64: %d bytes", len(data)))
	}

	buf := bytes.NewReader(data)
	var val float64
	if err := binary.Read(buf, binary.BigEndian, &val); err != nil {
		return errors.Join(ErrUnmarshalBinaryFailed, fmt.Errorf("reading float64: %w", err))
	}

	f.value = val
	return nil
}
