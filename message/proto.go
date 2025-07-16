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
	"encoding/binary"
	"errors"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// Proto is an adapter that allows a proto.Message to satisfy the Message interface.
//
// It enables the use of Protocol Buffers for message serialization in actor systems,
// providing a robust and efficient mechanism for inter-actor communication.
// This is particularly useful when leveraging the efficiency, strong typing,
// schema evolution capabilities, and extensive tooling benefits of Protobuf
// while still conforming to the common Message interface contract.
//
// The adapter handles the serialization of the Protobuf message itself, as well
// as embedding the Protobuf message's fully qualified name within the binary payload.
// This allows the `UnmarshalBinary` method to dynamically create the correct
// Protobuf message type during deserialization, even if the concrete type is not
// known at compile time on the receiving end.
//
// Usage:
//
//	msg := &Proto{Message: &mypb.MyMessage{Field: "value"}}
//	data, err := msg.MarshalBinary()
//	...
//	err = msg.UnmarshalBinary(data)
//
// Note: The embedded proto.Message must be a pointer to a concrete Protobuf message
// type that supports unmarshaling into its existing value.
type Proto struct {
	message proto.Message
}

// Compile-time check that Proto implements the Message interface.
var _ Message = (*Proto)(nil)

// NewProto creates a new Proto instance wrapping the provided proto.Message.
// The provided message must be a pointer to a concrete Protobuf message type.
func NewProto(msg proto.Message) *Proto {
	return &Proto{message: msg}
}

// Message returns the underlying proto.Message wrapped by this Proto adapter.
// It allows access to the deserialized Protobuf message. Callers should
// type assert the returned `proto.Message` to the expected concrete Protobuf type.
func (x *Proto) Message() proto.Message {
	return x.message
}

// MarshalBinary serializes the embedded proto.Message into a custom binary format.
//
// The binary payload has the following structure:
//   - 4 bytes: Total message length (uint32, Big Endian), including this field.
//   - 4 bytes: Length of the Protobuf message name (uint32, Big Endian).
//   - N bytes: Fully qualified Protobuf message name (e.g., "mypb.MyMessage").
//   - M bytes: Protobuf-encoded message data.
//
// This method implements the encoding.BinaryMarshaler interface and is intended
// for transmitting or storing Protobuf messages with their type information.
//
// Returns ErrUnknownMessageType if the message is nil or its type name cannot be determined.
// Returns ErrMarshalBinaryFailed if Protobuf marshaling fails.
func (x *Proto) MarshalBinary() ([]byte, error) {
	if x.message == nil {
		return nil, ErrUnknownMessageType
	}

	// get the message name
	messageName := proto.MessageName(x.message)
	messageNameLength := len(messageName)
	if messageNameLength == 0 {
		return nil, ErrUnknownMessageType
	}

	// calculate the total length of the binary data
	// 4 bytes for the message name length, 4 bytes for the message length,
	// plus the length of the message name and the serialized message itself.
	messageLength := 4 + 4 + messageNameLength + proto.Size(x.message)
	bytea, err := proto.Marshal(x.message)
	if err != nil {
		return nil, errors.Join(ErrMarshalBinaryFailed, err)
	}

	// Use buffer pool to reduce allocations
	buf := pool.Get()
	buf.Reset()
	defer pool.Put(buf)

	// Write data to buffer
	if err := binary.Write(buf, binary.BigEndian, uint32(messageLength)); err != nil {
		return nil, ErrMarshalBinaryFailed
	}

	if err := binary.Write(buf, binary.BigEndian, uint32(messageNameLength)); err != nil {
		return nil, ErrMarshalBinaryFailed
	}

	if _, err := buf.WriteString(string(messageName)); err != nil {
		return nil, ErrMarshalBinaryFailed
	}

	if _, err := buf.Write(bytea); err != nil {
		return nil, ErrMarshalBinaryFailed
	}

	return buf.Bytes(), nil
}

// UnmarshalBinary deserializes data into a new Protobuf message instance.
//
// It parses the custom binary format produced by MarshalBinary, extracts the
// fully qualified message name, and uses the global Protobuf registry to
// construct the appropriate message type. It then unmarshals the Protobuf-encoded
// payload into the new instance.
//
// This method implements the encoding.BinaryUnmarshaler interface.
//
// Returns ErrInvalidMessageLength if the data is too short or malformed.
// Returns ErrUnknownMessageType if the message name is not found in the registry.
// Returns ErrUnmarshalBinaryFailed if Protobuf unmarshalling fails.
func (x *Proto) UnmarshalBinary(data []byte) error {
	if len(data) < 4 {
		return ErrInvalidMessageLength
	}

	// Read the total message length (first 4 bytes)
	messageLength := int(binary.BigEndian.Uint32(data[:4]))
	if len(data) < messageLength {
		return ErrInvalidMessageLength
	}

	// Read the message name length (next 4 bytes)
	messageNameLength := binary.BigEndian.Uint32(data[4:8])
	if int(messageNameLength)+8 > messageLength {
		return ErrInvalidMessageLength
	}

	// Read the message name
	typeName := string(data[8 : 8+messageNameLength])
	message, err := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName(typeName))
	if err != nil {
		return errors.Join(ErrUnknownMessageType, err)
	}

	msg := message.New().Interface()
	if err := proto.Unmarshal(data[8+messageNameLength:messageLength], msg); err != nil {
		return errors.Join(ErrUnmarshalBinaryFailed, err)
	}

	d := &Proto{message: msg}
	*x = *d
	return nil
}
