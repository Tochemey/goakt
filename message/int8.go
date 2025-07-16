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
	"errors"
	"fmt"
)

// Int8 wraps a Go int8 value to implement the Message interface.
// Its value is immutable after creation.
//
// It serializes an int8 as a single byte.
type Int8 struct {
	value int8 // Unexported to enforce immutability
}

// Compile-time check that Int8 implements the Message interface.
var _ Message = (*Int8)(nil)

// NewInt8 creates a new immutable Int8 instance.
func NewInt8(i int8) *Int8 {
	return &Int8{value: i}
}

// Value returns the underlying Go int8 value wrapped by this message.
func (i *Int8) Value() int8 {
	return i.value
}

// MarshalBinary encodes the int8 value as a single byte.
// This method satisfies the encoding.BinaryMarshaler interface.
//
// Returns an error if the underlying binary writing fails (though unlikely for a single byte).
func (i *Int8) MarshalBinary() ([]byte, error) {
	return []byte{byte(i.value)}, nil
}

// UnmarshalBinary decodes binary data into the Int8.
// It expects a single byte. The receiver's internal value will be updated.
// This method satisfies the encoding.BinaryUnmarshaler interface.
//
// Returns ErrInvalidMessageLength if the data length is less than 1 byte.
// Returns an error if the underlying binary reading fails (unlikely for a single byte).
func (i *Int8) UnmarshalBinary(data []byte) error {
	if len(data) < 1 {
		return errors.Join(ErrInvalidMessageLength, fmt.Errorf("data too short for int8: expected 1 byte, got %d", len(data)))
	}
	i.value = int8(data[0])
	return nil
}
