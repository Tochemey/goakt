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

// Int wraps a Go int to implement the Message interface.
// It serializes the int as an 8-byte big-endian signed integer (int64).
// This ensures consistent size across different architectures where `int` size might vary.
type Int struct {
	value int
}

// Compile-time check that Int implements the Message interface.
var _ Message = (*Int)(nil)

// NewInt creates a new Int instance.
func NewInt(i int) *Int {
	return &Int{value: i}
}

// Value returns the underlying int value.
func (i *Int) Value() int {
	return i.value
}

// MarshalBinary encodes the int as an int64 using binary.BigEndian.
// This method satisfies the encoding.BinaryMarshaler interface.
func (i *Int) MarshalBinary() ([]byte, error) {
	buf := new(bytes.Buffer)
	// Cast to int64 to ensure consistent 8-byte serialization regardless of int size
	if err := binary.Write(buf, binary.BigEndian, int64(i.value)); err != nil {
		return nil, errors.Join(ErrMarshalBinaryFailed, fmt.Errorf("writing int64: %w", err))
	}
	return buf.Bytes(), nil
}

// UnmarshalBinary decodes binary data into an Int.
// It expects an 8-byte big-endian signed integer (int64).
// This method satisfies the encoding.BinaryUnmarshaler interface.
func (i *Int) UnmarshalBinary(data []byte) error {
	if len(data) < 8 { // int64 is 8 bytes
		return errors.Join(ErrInvalidMessageLength, fmt.Errorf("data too short for int64: %d bytes", len(data)))
	}

	buf := bytes.NewReader(data)
	var val int64
	if err := binary.Read(buf, binary.BigEndian, &val); err != nil {
		return errors.Join(ErrUnmarshalBinaryFailed, fmt.Errorf("reading int64: %w", err))
	}

	// Cast back to int. Handle potential overflow if int is smaller than int64.
	// For most common cases (int is 32 or 64 bit), this is fine.
	// For strict overflow checking, you'd need more complex logic.
	i.value = int(val)
	return nil
}
