// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package net

import (
	"encoding/binary"
	"errors"
	"unsafe"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// ProtoSerializer serializes and deserializes [proto.Message] values using a
// self-describing binary frame format. The frame embeds the message's fully
// qualified type name so the receiver can resolve the concrete Go type at
// runtime via [protoregistry.GlobalTypes].
//
// Frame layout (all integers are big-endian uint32):
//
//	┌──────────┬──────────┬────────────┬──────────────┐
//	│ totalLen │ nameLen  │ type name  │ proto bytes  │
//	│ 4 bytes  │ 4 bytes  │ N bytes    │ M bytes      │
//	└──────────┴──────────┴────────────┴──────────────┘
//
//	totalLen = 4 + 4 + N + M   (covers the entire frame including itself)
//
// The serializer is stateless and safe for concurrent use.
type ProtoSerializer struct{}

// NewProtoSerializer returns a ready-to-use serializer.
func NewProtoSerializer() *ProtoSerializer {
	return &ProtoSerializer{}
}

// MarshalBinary encodes message into a single frame. It pre-computes the
// frame size via [proto.Size], allocates a single []byte of exactly that
// size, writes the two uint32 headers and the type name in-place, then
// appends the proto wire bytes with [proto.MarshalOptions.MarshalAppend]
// — one allocation, no intermediate buffers.
//
// Returns [ErrUnknownMessageType] if message is nil or has no registered
// type name. Returns [ErrMarshalBinaryFailed] wrapping the proto error on
// marshal failure.
func (x *ProtoSerializer) MarshalBinary(message proto.Message) ([]byte, error) {
	if message == nil {
		return nil, ErrUnknownMessageType
	}

	messageName := proto.MessageName(message)
	nameLen := len(messageName)
	if nameLen == 0 {
		return nil, ErrUnknownMessageType
	}

	protoSize := proto.Size(message)
	totalLen := 4 + 4 + nameLen + protoSize

	// Single allocation: exact-size output frame.
	out := make([]byte, 0, totalLen)

	// Write the two uint32 header fields into a stack-allocated array,
	// then append. No reflection, no interface boxing.
	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[0:4], uint32(totalLen))
	binary.BigEndian.PutUint32(hdr[4:8], uint32(nameLen))
	out = append(out, hdr[:]...)

	// Append the fully-qualified type name.
	out = append(out, messageName...)

	// Marshal the proto payload directly into the tail of out.
	out, err := proto.MarshalOptions{}.MarshalAppend(out, message)
	if err != nil {
		return nil, errors.Join(ErrMarshalBinaryFailed, err)
	}

	return out, nil
}

// UnmarshalBinary decodes a frame produced by [MarshalBinary]. It extracts
// the type name, resolves the concrete [proto.Message] type from the global
// registry, and unmarshals the proto payload into a new instance.
//
// The returned [protoreflect.FullName] is the message's fully-qualified type
// name as read from the frame header. Callers that need the type name for
// dispatch can use it directly instead of calling [proto.MessageName] again.
//
// The type-name extraction uses an unsafe []byte→string conversion to avoid
// a heap allocation; the resulting string is only used for the registry
// lookup and the returned FullName copies from the frame's own storage.
//
// Returns [ErrInvalidMessageLength] for truncated or malformed frames.
// Returns [ErrUnknownMessageType] if the type name is not in the registry.
// Returns [ErrUnmarshalBinaryFailed] wrapping the proto error on decode failure.
func (x *ProtoSerializer) UnmarshalBinary(data []byte) (proto.Message, protoreflect.FullName, error) {
	if len(data) < 8 {
		return nil, "", ErrInvalidMessageLength
	}

	messageLength := int(binary.BigEndian.Uint32(data[:4]))
	if len(data) < messageLength || messageLength < 8 {
		return nil, "", ErrInvalidMessageLength
	}

	nameLen := int(binary.BigEndian.Uint32(data[4:8]))
	if 8+nameLen > messageLength {
		return nil, "", ErrInvalidMessageLength
	}

	// Zero-copy string from the frame bytes. Safe because the string is only
	// used for the registry map lookup below and for the returned FullName
	// (which is a plain string type, so Go retains it independently of data).
	typeName := protoreflect.FullName(unsafe.String(unsafe.SliceData(data[8:8+nameLen]), nameLen))

	msgType, err := protoregistry.GlobalTypes.FindMessageByName(typeName)
	if err != nil {
		return nil, "", errors.Join(ErrUnknownMessageType, err)
	}

	msg := msgType.New().Interface()
	if err := proto.Unmarshal(data[8+nameLen:messageLength], msg); err != nil {
		return nil, "", errors.Join(ErrUnmarshalBinaryFailed, err)
	}

	return msg, typeName, nil
}

// MarshalBinaryWithMetadata encodes a message with optional metadata into a
// single frame. The extended frame format includes a 4-byte metadata length
// field after the type name, followed by the serialized metadata bytes.
//
// Extended frame layout (all integers are big-endian):
//
//	┌──────────┬──────────┬────────────┬──────────┬────────────┬──────────────┐
//	│ totalLen │ nameLen  │ type name  │ metaLen  │ metadata   │ proto bytes  │
//	│ 4 bytes  │ 4 bytes  │ N bytes    │ 4 bytes  │ K bytes    │ M bytes      │
//	└──────────┴──────────┴────────────┴──────────┴────────────┴──────────────┘
//
//	totalLen = 4 + 4 + N + 4 + K + M   (covers the entire frame including itself)
//
// Like [MarshalBinary], this uses a single allocation by pre-computing the
// exact frame size. If md is nil, metaLen is written as 0 with no metadata
// bytes, maintaining wire-format compatibility.
//
// Returns [ErrUnknownMessageType] if message is nil or has no registered
// type name. Returns [ErrMarshalBinaryFailed] wrapping the proto error on
// marshal failure.
func (x *ProtoSerializer) MarshalBinaryWithMetadata(message proto.Message, md *Metadata) ([]byte, error) {
	if message == nil {
		return nil, ErrUnknownMessageType
	}

	messageName := proto.MessageName(message)
	nameLen := len(messageName)
	if nameLen == 0 {
		return nil, ErrUnknownMessageType
	}

	// Marshal metadata once if present (reuses the allocation from Marshal).
	var metaBytes []byte
	if md != nil {
		metaBytes = md.MarshalBinary()
	}
	metaLen := len(metaBytes)

	protoSize := proto.Size(message)
	totalLen := 4 + 4 + nameLen + 4 + metaLen + protoSize

	// Single allocation: exact-size output frame.
	out := make([]byte, 0, totalLen)

	// Write the three fixed header fields (totalLen, nameLen, metaLen)
	// into a stack-allocated array, then append.
	var hdr [12]byte
	binary.BigEndian.PutUint32(hdr[0:4], uint32(totalLen))
	binary.BigEndian.PutUint32(hdr[4:8], uint32(nameLen))
	binary.BigEndian.PutUint32(hdr[8:12], uint32(metaLen))
	out = append(out, hdr[:]...)

	// Append the fully-qualified type name.
	out = append(out, messageName...)

	// Append the metadata bytes (if any).
	if metaLen > 0 {
		out = append(out, metaBytes...)
	}

	// Marshal the proto payload directly into the tail of out.
	out, err := proto.MarshalOptions{}.MarshalAppend(out, message)
	if err != nil {
		return nil, errors.Join(ErrMarshalBinaryFailed, err)
	}

	return out, nil
}

// UnmarshalBinaryWithMetadata decodes a frame produced by
// [MarshalBinaryWithMetadata]. It extracts the type name, metadata (if
// present), resolves the concrete [proto.Message] type from the global
// registry, and unmarshals the proto payload into a new instance.
//
// The returned [protoreflect.FullName] is the message's fully-qualified type
// name as read from the frame header. The returned [*Metadata] is nil if
// metaLen was 0 in the frame.
//
// The type-name extraction uses an unsafe []byte→string conversion to avoid
// a heap allocation (same as [UnmarshalBinary]). The metadata is parsed
// in-place from the frame bytes using [Metadata.UnmarshalBinary], which performs
// zero-copy string extraction for header keys and values via unsafe pointers.
//
// Returns [ErrInvalidMessageLength] for truncated or malformed frames.
// Returns [ErrUnknownMessageType] if the type name is not in the registry.
// Returns [ErrInvalidMetadata] if the metadata section is malformed.
// Returns [ErrUnmarshalBinaryFailed] wrapping the proto error on decode failure.
func (x *ProtoSerializer) UnmarshalBinaryWithMetadata(data []byte) (proto.Message, *Metadata, protoreflect.FullName, error) {
	if len(data) < 12 {
		return nil, nil, "", ErrInvalidMessageLength
	}

	messageLength := int(binary.BigEndian.Uint32(data[:4]))
	if len(data) < messageLength || messageLength < 12 {
		return nil, nil, "", ErrInvalidMessageLength
	}

	nameLen := int(binary.BigEndian.Uint32(data[4:8]))
	metaLen := int(binary.BigEndian.Uint32(data[8:12]))

	if 12+nameLen+metaLen > messageLength {
		return nil, nil, "", ErrInvalidMessageLength
	}

	// Zero-copy string from the frame bytes (same technique as UnmarshalBinary).
	typeName := protoreflect.FullName(unsafe.String(unsafe.SliceData(data[12:12+nameLen]), nameLen))

	msgType, err := protoregistry.GlobalTypes.FindMessageByName(typeName)
	if err != nil {
		return nil, nil, "", errors.Join(ErrUnknownMessageType, err)
	}

	// Parse metadata if present.
	var md *Metadata
	if metaLen > 0 {
		md = NewMetadata()
		metaStart := 12 + nameLen
		metaEnd := metaStart + metaLen
		if err := md.UnmarshalBinary(data[metaStart:metaEnd]); err != nil {
			return nil, nil, "", err
		}
	}

	// Unmarshal the proto payload.
	protoStart := 12 + nameLen + metaLen
	msg := msgType.New().Interface()
	if err := proto.Unmarshal(data[protoStart:messageLength], msg); err != nil {
		return nil, nil, "", errors.Join(ErrUnmarshalBinaryFailed, err)
	}

	return msg, md, typeName, nil
}
