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

// Uint8 wraps a Go uint8 value to implement the Message interface.
// Its value is immutable after creation.
//
// It serializes a uint8 as a single byte.
type Uint8 struct {
	value uint8 // Unexported to enforce immutability
}

// Compile-time check that Uint8 implements the Message interface.
var _ Message = (*Uint8)(nil)

// NewUint8 creates a new immutable Uint8 instance.
func NewUint8(u uint8) *Uint8 {
	return &Uint8{value: u}
}

// Value returns the underlying Go uint8 value wrapped by this message.
func (u *Uint8) Value() uint8 {
	return u.value
}

// MarshalBinary encodes the uint8 value as a single byte.
// This method satisfies the encoding.BinaryMarshaler interface.
//
// Returns an error if the underlying binary writing fails (though unlikely for a single byte).
func (u *Uint8) MarshalBinary() ([]byte, error) {
	return []byte{u.value}, nil
}

// UnmarshalBinary decodes binary data into the Uint8.
// It expects a single byte. The receiver's internal value will be updated.
// This method satisfies the encoding.BinaryUnmarshaler interface.
//
// Returns ErrInvalidMessageLength if the data length is less than 1 byte.
// Returns an error if the underlying binary reading fails (unlikely for a single byte).
func (u *Uint8) UnmarshalBinary(data []byte) error {
	if len(data) < 1 {
		return errors.Join(ErrInvalidMessageLength, fmt.Errorf("data too short for uint8: expected 1 byte, got %d", len(data)))
	}
	u.value = data[0]
	return nil
}
