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

// Boolean wraps a Go bool to implement the Message interface.
// It serializes a boolean as a single byte: 0 for false, 1 for true.
type Boolean struct {
	value bool
}

// Compile-time check that Boolean implements the Message interface.
var _ Message = (*Boolean)(nil)

// NewBoolean creates a new Boolean instance.
func NewBoolean(b bool) *Boolean {
	return &Boolean{value: b}
}

// Value returns the underlying boolean value.
func (b *Boolean) Value() bool {
	return b.value
}

// MarshalBinary encodes the boolean as a single byte (0 or 1).
// This method satisfies the encoding.BinaryMarshaler interface.
func (b *Boolean) MarshalBinary() ([]byte, error) {
	val := byte(0)
	if b.value {
		val = 1
	}
	return []byte{val}, nil
}

// UnmarshalBinary decodes binary data into a Boolean.
// It expects a single byte: 0 for false, any other non-zero value for true.
// This method satisfies the encoding.BinaryUnmarshaler interface.
func (b *Boolean) UnmarshalBinary(data []byte) error {
	if len(data) < 1 {
		return errors.Join(ErrInvalidMessageLength, fmt.Errorf("data too short for boolean: %d bytes", len(data)))
	}
	b.value = data[0] != 0
	return nil
}
