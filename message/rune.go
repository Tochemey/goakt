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

// Rune wraps a Go rune (int32) to implement the Message interface.
// Its value is immutable after creation.
//
// It serializes a rune as a 4-byte big-endian signed integer (int32).
// This provides a fixed-size, architecture-independent binary representation.
type Rune struct {
	value rune // Unexported to enforce immutability
}

// Compile-time check that Rune implements the Message interface.
var _ Message = (*Rune)(nil)

// NewRune creates a new immutable Rune instance.
func NewRune(r rune) *Rune {
	return &Rune{value: r}
}

// Value returns the underlying Go rune value wrapped by this message.
func (r *Rune) Value() rune {
	return r.value
}

// MarshalBinary encodes the rune as a 4-byte big-endian int32.
//
// This method satisfies the encoding.BinaryMarshaler interface.
//
// Returns an error if the underlying binary writing fails.
func (r *Rune) MarshalBinary() ([]byte, error) {
	buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.BigEndian, int32(r.value)); err != nil {
		return nil, errors.Join(ErrMarshalBinaryFailed, fmt.Errorf("writing rune (int32): %w", err))
	}
	return buf.Bytes(), nil
}

// UnmarshalBinary decodes binary data into the Rune.
//
// It expects the input `data` to be 4 bytes long, representing a
// big-endian int32. The receiver's internal value will be updated.
//
// This method satisfies the encoding.BinaryUnmarshaler interface.
//
// Returns ErrInvalidMessageLength if the data length is less than 4 bytes.
// Returns an error if the underlying binary reading fails.
func (r *Rune) UnmarshalBinary(data []byte) error {
	if len(data) < 4 { // int32 is 4 bytes
		return errors.Join(ErrInvalidMessageLength, fmt.Errorf("data too short for rune (int32): expected 4 bytes, got %d", len(data)))
	}

	buf := bytes.NewReader(data)
	var val int32
	if err := binary.Read(buf, binary.BigEndian, &val); err != nil {
		return errors.Join(ErrUnmarshalBinaryFailed, fmt.Errorf("reading rune (int32): %w", err))
	}

	r.value = rune(val)
	return nil
}
