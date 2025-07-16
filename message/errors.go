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

import "errors"

var (
	// ErrInvalidProtoMessage indicates that a `Proto` adapter was created or used
	// with a `proto.Message` that is not a pointer to a concrete Protobuf message type.
	// For instance, providing a nil pointer, an interface, or a non-pointer struct
	// will result in this error.
	ErrInvalidProtoMessage = errors.New("invalid proto message: must be a pointer to a concrete Protobuf message type")

	// ErrUnknownMessageType signifies that the `Proto` adapter encountered a message
	// type that it cannot identify or handle during marshaling or unmarshaling.
	// This can occur if the embedded `proto.Message` is nil during marshaling,
	// or if the message name embedded in the binary payload during unmarshaling
	// does not correspond to a known Protobuf message type in the global registry.
	ErrUnknownMessageType = errors.New("unknown message type: cannot (un)marshal into nil or unsupported type")

	// ErrMarshalBinaryFailed indicates a failure during the marshaling process
	// of a `Proto` message into its binary representation. This often wraps
	// an underlying error from the Protobuf library (e.g., if the message
	// itself is invalid or self-referential) or a binary writing error.
	ErrMarshalBinaryFailed = errors.New("failed to marshal message to binary format")

	// ErrUnmarshalBinaryFailed indicates a failure during the unmarshaling process
	// of binary data back into a `Proto` message. This typically wraps an
	// underlying error from the Protobuf library (e.g., if the binary data
	// is malformed or corrupted) or a binary reading error.
	ErrUnmarshalBinaryFailed = errors.New("failed to unmarshal binary data into message")

	// ErrInvalidMessageLength indicates that the provided binary data has an
	// unexpected or insufficient length, preventing successful unmarshaling.
	// This can mean the total length field, message name length field, or the
	// overall data size does not conform to the expected binary message format.
	ErrInvalidMessageLength = errors.New("invalid message length: must be greater than zero")
)
