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
	"fmt"
)

// Duplex DATA/REPLY serializer identifiers carried in the envelope.
const (
	// SerializerIDInternalProto identifies raw protobuf payload bytes whose
	// concrete type is named by the envelope typeRef.
	SerializerIDInternalProto byte = 0

	// SerializerIDPublicProto identifies a self-describing public protobuf
	// frame (legacy ProtoSerializer layout).
	SerializerIDPublicProto byte = 1

	// SerializerIDJSON identifies a JSON payload.
	SerializerIDJSON byte = 2

	// SerializerIDCBOR identifies a CBOR payload.
	SerializerIDCBOR byte = 3

	// SerializerIDCustom marks a custom serializer's verbatim self-describing
	// bytes. typeRef must be empty on the wire.
	SerializerIDCustom byte = 255
)

// DataEnvelope is the hand-parsed payload of a duplex DATA frame.
//
// Wire layout:
//
//	senderRef | receiverRef | typeRef | serializerID (1B) | [metaLen(4B) metadata] | payload
//
// Each ref is a uvarint table ID, or 0 followed by a uvarint length and that
// many bytes of inline literal. Encoders always emit inline literals until
// compression tables land (Milestone 5). Control RPCs use empty Sender and
// Receiver with SerializerIDInternalProto; user messages carry address
// strings and a public/JSON/CBOR/custom serializer ID.
type DataEnvelope struct {
	// Sender is the wire actor address of the origin, or empty for control RPCs.
	Sender string
	// Receiver is the wire actor address of the target, or empty for control RPCs.
	Receiver string
	// TypeName is the typeRef: protobuf full name for internal proto, frame
	// type name for public serializers, or empty for custom (ID 255).
	TypeName string
	// SerializerID selects how Payload is interpreted (see SerializerID* constants).
	SerializerID byte
	// Metadata is the optional binary blob from [Metadata.MarshalBinary].
	// Present on the wire only when the frame has FrameFlagHasMetadata set.
	Metadata []byte
	// Payload is the message body: raw proto bytes for ID 0, or a
	// self-describing serializer frame for IDs 1/2/3/255.
	Payload []byte
}

// ReplyEnvelope is the hand-parsed payload of a duplex REPLY frame.
//
// Wire layout:
//
//	typeRef | serializerID (1B) | [metaLen(4B) metadata] | payload
type ReplyEnvelope struct {
	// TypeName is the typeRef of the response body (empty for custom ID 255).
	TypeName string
	// SerializerID selects how Payload is interpreted (see SerializerID* constants).
	SerializerID byte
	// Metadata is optional reply metadata; rare on the ask path today.
	Metadata []byte
	// Payload is the response body bytes for the chosen serializer.
	Payload []byte
}

// EncodeDataEnvelope serializes env into a DATA frame payload. Metadata is
// written only when non-empty; callers must set FrameFlagHasMetadata on the
// surrounding frame to match. Returns [ErrUnknownSerializerID] or a
// typeRef/serializer mismatch error when env is invalid.
func EncodeDataEnvelope(env DataEnvelope) ([]byte, error) {
	return encodeDataEnvelope(env)
}

// encodeDataEnvelope is the unexported implementation of [EncodeDataEnvelope].
func encodeDataEnvelope(env DataEnvelope) ([]byte, error) {
	if err := validateSerializerID(env.SerializerID, env.TypeName); err != nil {
		return nil, err
	}

	size := refSize(env.Sender) + refSize(env.Receiver) + refSize(env.TypeName) + 1
	if len(env.Metadata) > 0 {
		size += 4 + len(env.Metadata)
	}
	size += len(env.Payload)

	buf := make([]byte, size)
	pos := 0
	pos += putInlineRef(buf[pos:], env.Sender)
	pos += putInlineRef(buf[pos:], env.Receiver)
	pos += putInlineRef(buf[pos:], env.TypeName)
	buf[pos] = env.SerializerID
	pos++

	if len(env.Metadata) > 0 {
		binary.BigEndian.PutUint32(buf[pos:], uint32(len(env.Metadata)))
		pos += 4
		pos += copy(buf[pos:], env.Metadata)
	}

	copy(buf[pos:], env.Payload)
	return buf, nil
}

// DecodeDataEnvelope parses a DATA frame payload. hasMetadata must match the
// surrounding frame's FrameFlagHasMetadata bit. A nonzero table ID is a
// capability violation at revision 1 and returns [ErrTableRefUnsupported].
func DecodeDataEnvelope(src []byte, hasMetadata bool) (DataEnvelope, error) {
	return decodeDataEnvelope(src, hasMetadata)
}

// decodeDataEnvelope is the unexported implementation of [DecodeDataEnvelope].
func decodeDataEnvelope(src []byte, hasMetadata bool) (DataEnvelope, error) {
	var env DataEnvelope
	pos := 0

	sender, n, err := readRef(src[pos:])
	if err != nil {
		return DataEnvelope{}, fmt.Errorf("tcp: data envelope sender: %w", err)
	}
	env.Sender = sender
	pos += n

	receiver, n, err := readRef(src[pos:])
	if err != nil {
		return DataEnvelope{}, fmt.Errorf("tcp: data envelope receiver: %w", err)
	}
	env.Receiver = receiver
	pos += n

	typeName, n, err := readRef(src[pos:])
	if err != nil {
		return DataEnvelope{}, fmt.Errorf("tcp: data envelope type: %w", err)
	}
	env.TypeName = typeName
	pos += n

	if pos >= len(src) {
		return DataEnvelope{}, fmt.Errorf("tcp: data envelope truncated: missing serializer id")
	}
	env.SerializerID = src[pos]
	pos++

	if err := validateSerializerID(env.SerializerID, env.TypeName); err != nil {
		return DataEnvelope{}, err
	}

	if hasMetadata {
		if pos+4 > len(src) {
			return DataEnvelope{}, fmt.Errorf("tcp: data envelope truncated: missing metadata length")
		}
		metaLen := int(binary.BigEndian.Uint32(src[pos:]))
		pos += 4
		if metaLen < 0 || pos+metaLen > len(src) {
			return DataEnvelope{}, fmt.Errorf("tcp: data envelope metadata length %d out of range", metaLen)
		}
		// Alias the frame payload slice: ReadFrame allocates uniquely per
		// frame and dispatch retains env until the handler returns.
		env.Metadata = src[pos : pos+metaLen]
		pos += metaLen
	}

	env.Payload = src[pos:]
	return env, nil
}

// EncodeReplyEnvelope serializes env into a REPLY frame payload. Metadata is
// written only when non-empty; callers must set FrameFlagHasMetadata on the
// surrounding frame to match.
func EncodeReplyEnvelope(env ReplyEnvelope) ([]byte, error) {
	return encodeReplyEnvelope(env)
}

// encodeReplyEnvelope is the unexported implementation of [EncodeReplyEnvelope].
func encodeReplyEnvelope(env ReplyEnvelope) ([]byte, error) {
	if err := validateSerializerID(env.SerializerID, env.TypeName); err != nil {
		return nil, err
	}

	size := refSize(env.TypeName) + 1
	if len(env.Metadata) > 0 {
		size += 4 + len(env.Metadata)
	}
	size += len(env.Payload)

	buf := make([]byte, size)
	pos := 0
	pos += putInlineRef(buf[pos:], env.TypeName)
	buf[pos] = env.SerializerID
	pos++

	if len(env.Metadata) > 0 {
		binary.BigEndian.PutUint32(buf[pos:], uint32(len(env.Metadata)))
		pos += 4
		pos += copy(buf[pos:], env.Metadata)
	}

	copy(buf[pos:], env.Payload)
	return buf, nil
}

// DecodeReplyEnvelope parses a REPLY frame payload. hasMetadata must match the
// surrounding frame's FrameFlagHasMetadata bit.
func DecodeReplyEnvelope(src []byte, hasMetadata bool) (ReplyEnvelope, error) {
	return decodeReplyEnvelope(src, hasMetadata)
}

// decodeReplyEnvelope is the unexported implementation of [DecodeReplyEnvelope].
func decodeReplyEnvelope(src []byte, hasMetadata bool) (ReplyEnvelope, error) {
	var env ReplyEnvelope
	pos := 0

	typeName, n, err := readRef(src[pos:])
	if err != nil {
		return ReplyEnvelope{}, fmt.Errorf("tcp: reply envelope type: %w", err)
	}
	env.TypeName = typeName
	pos += n

	if pos >= len(src) {
		return ReplyEnvelope{}, fmt.Errorf("tcp: reply envelope truncated: missing serializer id")
	}
	env.SerializerID = src[pos]
	pos++

	if err := validateSerializerID(env.SerializerID, env.TypeName); err != nil {
		return ReplyEnvelope{}, err
	}

	if hasMetadata {
		if pos+4 > len(src) {
			return ReplyEnvelope{}, fmt.Errorf("tcp: reply envelope truncated: missing metadata length")
		}
		metaLen := int(binary.BigEndian.Uint32(src[pos:]))
		pos += 4
		if metaLen < 0 || pos+metaLen > len(src) {
			return ReplyEnvelope{}, fmt.Errorf("tcp: reply envelope metadata length %d out of range", metaLen)
		}
		// Alias the frame payload slice: ReadFrame allocates uniquely per
		// frame and callers retain env only while the frame remains live.
		env.Metadata = src[pos : pos+metaLen]
		pos += metaLen
	}

	env.Payload = src[pos:]
	return env, nil
}

// refSize returns the encoded size of an inline-literal ref for s.
func refSize(s string) int {
	return uvarintSize(0) + uvarintSize(uint64(len(s))) + len(s)
}

// putInlineRef writes an inline-literal ref for s into dst and returns the
// number of bytes written. dst must have capacity of at least [refSize](s).
func putInlineRef(dst []byte, s string) int {
	n := binary.PutUvarint(dst, 0)
	n += binary.PutUvarint(dst[n:], uint64(len(s)))
	n += copy(dst[n:], s)
	return n
}

// readRef decodes one envelope ref from src. A nonzero table ID returns
// [ErrTableRefUnsupported].
func readRef(src []byte) (string, int, error) {
	id, n := binary.Uvarint(src)
	if n <= 0 {
		return "", 0, fmt.Errorf("truncated table id")
	}
	if id != 0 {
		return "", 0, fmt.Errorf("%w: id %d", ErrTableRefUnsupported, id)
	}

	length, m := binary.Uvarint(src[n:])
	if m <= 0 {
		return "", 0, fmt.Errorf("truncated inline length")
	}
	n += m
	if length > uint64(len(src)-n) {
		return "", 0, fmt.Errorf("inline length %d exceeds remaining %d", length, len(src)-n)
	}

	lit := string(src[n : n+int(length)])
	return lit, n + int(length), nil
}

// validateSerializerID enforces the wire rules for serializer ID and typeRef.
func validateSerializerID(id byte, typeName string) error {
	switch id {
	case SerializerIDInternalProto, SerializerIDPublicProto, SerializerIDJSON, SerializerIDCBOR:
		if typeName == "" {
			return fmt.Errorf("tcp: serializer id 0x%02x requires a nonempty type name", id)
		}
		return nil
	case SerializerIDCustom:
		if typeName != "" {
			return fmt.Errorf("tcp: custom serializer requires an empty type name")
		}
		return nil
	default:
		return fmt.Errorf("%w: 0x%02x", ErrUnknownSerializerID, id)
	}
}

// uvarintSize returns the number of bytes needed to encode x as a uvarint.
func uvarintSize(x uint64) int {
	var buf [binary.MaxVarintLen64]byte
	return binary.PutUvarint(buf[:], x)
}
