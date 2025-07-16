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

// Complex128 wraps a Go complex128 value to implement the Message interface.
//
// It serializes a complex128 by encoding its real and imaginary parts
// as two consecutive 8-byte big-endian float64 values. This provides a
// fixed-size, architecture-independent binary representation for complex numbers.
type Complex128 struct {
	value complex128
}

// Compile-time check that Complex128 implements the Message interface.
var _ Message = (*Complex128)(nil)

// NewComplex128 creates a new Complex128 message instance.
func NewComplex128(c complex128) *Complex128 {
	return &Complex128{value: c}
}

// Value returns the underlying Go complex128 value wrapped by this message.
func (c *Complex128) Value() complex128 {
	return c.value
}

// MarshalBinary encodes the complex128 value into a binary format.
//
// The complex number's real part is marshaled first as an 8-byte float64,
// followed by its imaginary part as another 8-byte float64. Both are
// encoded using big-endian byte order.
//
// This method satisfies the encoding.BinaryMarshaler interface.
//
// Returns an error if the underlying binary writing fails.
func (c *Complex128) MarshalBinary() ([]byte, error) {
	buf := new(bytes.Buffer)
	// Marshal real and imaginary parts as two float64s
	if err := binary.Write(buf, binary.BigEndian, real(c.value)); err != nil {
		return nil, errors.Join(ErrMarshalBinaryFailed, fmt.Errorf("writing real part: %w", err))
	}
	if err := binary.Write(buf, binary.BigEndian, imag(c.value)); err != nil {
		return nil, errors.Join(ErrMarshalBinaryFailed, fmt.Errorf("writing imaginary part: %w", err))
	}
	return buf.Bytes(), nil
}

// UnmarshalBinary decodes binary data into the Complex128 message.
//
// It expects the input `data` to be 16 bytes long, representing two
// consecutive big-endian float64 values: the real part followed by the
// imaginary part.
//
// This method satisfies the encoding.BinaryUnmarshaler interface.
//
// Returns ErrInvalidMessageLength if the data length is less than 16 bytes.
// Returns an error if the underlying binary reading fails.
func (c *Complex128) UnmarshalBinary(data []byte) error {
	if len(data) < 16 { // Two float64s = 16 bytes
		return errors.Join(ErrInvalidMessageLength, fmt.Errorf("data too short for complex128: expected 16 bytes, got %d", len(data)))
	}

	buf := bytes.NewReader(data)
	var realPart, imagPart float64
	if err := binary.Read(buf, binary.BigEndian, &realPart); err != nil {
		return errors.Join(ErrUnmarshalBinaryFailed, fmt.Errorf("reading real part: %w", err))
	}
	if err := binary.Read(buf, binary.BigEndian, &imagPart); err != nil {
		return errors.Join(ErrUnmarshalBinaryFailed, fmt.Errorf("reading imaginary part: %w", err))
	}

	c.value = complex(realPart, imagPart)
	return nil
}

// Complex64 wraps a Go complex64 value to implement the Message interface.
//
// It serializes a complex64 by encoding its real and imaginary parts
// as two consecutive 4-byte big-endian float32 values. This provides a
// fixed-size, architecture-independent binary representation for complex numbers.
type Complex64 struct {
	value complex64
}

// Compile-time check that Complex64 implements the Message interface.
var _ Message = (*Complex64)(nil)

// NewComplex64 creates a new Complex64 message instance.
func NewComplex64(c complex64) *Complex64 {
	return &Complex64{value: c}
}

// Value returns the underlying Go complex64 value wrapped by this message.
func (c *Complex64) Value() complex64 {
	return c.value
}

// MarshalBinary encodes the complex64 value into a binary format.
//
// The complex number's real part is marshaled first as a 4-byte float32,
// followed by its imaginary part as another 4-byte float32. Both are
// encoded using big-endian byte order.
//
// This method satisfies the encoding.BinaryMarshaler interface.
//
// Returns an error if the underlying binary writing fails.
func (c *Complex64) MarshalBinary() ([]byte, error) {
	buf := new(bytes.Buffer)
	// Marshal real and imaginary parts as two float32s
	if err := binary.Write(buf, binary.BigEndian, real(c.value)); err != nil {
		return nil, errors.Join(ErrMarshalBinaryFailed, fmt.Errorf("writing real part: %w", err))
	}
	if err := binary.Write(buf, binary.BigEndian, imag(c.value)); err != nil {
		return nil, errors.Join(ErrMarshalBinaryFailed, fmt.Errorf("writing imaginary part: %w", err))
	}
	return buf.Bytes(), nil
}

// UnmarshalBinary decodes binary data into the Complex64 message.
//
// It expects the input `data` to be 8 bytes long, representing two
// consecutive big-endian float32 values: the real part followed by the
// imaginary part.
//
// This method satisfies the encoding.BinaryUnmarshaler interface.
//
// Returns ErrInvalidMessageLength if the data length is less than 8 bytes.
// Returns an error if the underlying binary reading fails.
func (c *Complex64) UnmarshalBinary(data []byte) error {
	if len(data) < 8 { // Two float32s = 8 bytes
		return errors.Join(ErrInvalidMessageLength, fmt.Errorf("data too short for complex64: expected 8 bytes, got %d", len(data)))
	}

	buf := bytes.NewReader(data)
	var realPart, imagPart float32
	if err := binary.Read(buf, binary.BigEndian, &realPart); err != nil {
		return errors.Join(ErrUnmarshalBinaryFailed, fmt.Errorf("reading real part: %w", err))
	}
	if err := binary.Read(buf, binary.BigEndian, &imagPart); err != nil {
		return errors.Join(ErrUnmarshalBinaryFailed, fmt.Errorf("reading imaginary part: %w", err))
	}

	c.value = complex(realPart, imagPart)
	return nil
}
