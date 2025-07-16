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

// String wraps a Go string to implement the Message interface.
// It serializes the string by first encoding its length as a uint32 (big-endian),
// followed by the UTF-8 bytes of the string itself.
type String struct {
	value string
}

// Compile-time check that String implements the Message interface.
var _ Message = (*String)(nil)

// NewString creates a new String instance.
func NewString(s string) *String {
	return &String{value: s}
}

// Value returns the underlying string value.
func (s *String) Value() string {
	return s.value
}

// MarshalBinary encodes the string's length (uint32) and then its UTF-8 bytes.
// This method satisfies the encoding.BinaryMarshaler interface.
func (s *String) MarshalBinary() ([]byte, error) {
	strBytes := []byte(s.value)
	length := uint32(len(strBytes))

	buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.BigEndian, length); err != nil {
		return nil, errors.Join(ErrMarshalBinaryFailed, fmt.Errorf("writing length: %w", err))
	}
	if _, err := buf.Write(strBytes); err != nil {
		return nil, errors.Join(ErrMarshalBinaryFailed, fmt.Errorf("writing string bytes: %w", err))
	}
	return buf.Bytes(), nil
}

// UnmarshalBinary decodes binary data into a String.
// It first reads a uint32 length prefix, then reads that many bytes
// to reconstruct the string.
// This method satisfies the encoding.BinaryUnmarshaler interface.
func (s *String) UnmarshalBinary(data []byte) error {
	if len(data) < 4 { // Minimum 4 bytes for length prefix
		return errors.Join(ErrInvalidMessageLength, fmt.Errorf("data too short for string length prefix: %d bytes", len(data)))
	}

	buf := bytes.NewReader(data)
	var length uint32
	if err := binary.Read(buf, binary.BigEndian, &length); err != nil {
		return errors.Join(ErrUnmarshalBinaryFailed, fmt.Errorf("reading string length: %w", err))
	}

	if int(length) > buf.Len() {
		return errors.Join(ErrInvalidMessageLength, fmt.Errorf("declared string length %d exceeds available data %d", length, buf.Len()))
	}

	strBytes := make([]byte, length)
	if _, err := buf.Read(strBytes); err != nil {
		return errors.Join(ErrUnmarshalBinaryFailed, fmt.Errorf("reading string bytes: %w", err))
	}

	s.value = string(strBytes)
	return nil
}
