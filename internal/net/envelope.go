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
// many bytes of inline literal. Encoders currently always emit inline
// literals (table IDs are reserved for negotiated compression tables).
// Control RPCs use empty Sender and Receiver with SerializerIDInternalProto;
// user messages carry address strings and a public/JSON/CBOR/custom
// serializer ID.
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
	// SenderHandle is an opaque actor-layer cache populated on a sender-ref
	// table hit (typically a *PID). It is never serialized.
	SenderHandle any
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

// EncodeDataEnvelope serializes env into a DATA frame payload using inline
// refs only. Metadata is written only when non-empty; callers must set
// FrameFlagHasMetadata on the surrounding frame to match. Returns
// [ErrUnknownSerializerID] or a typeRef/serializer mismatch error when env is
// invalid.
func EncodeDataEnvelope(env DataEnvelope) ([]byte, error) {
	return encodeDataEnvelope(env)
}

// InlineDataEnvelopeSize returns the byte length of env encoded with inline
// refs only, without allocating the payload buffer. Callers use it for
// conservative lane selection before table registration.
func InlineDataEnvelopeSize(env DataEnvelope) int {
	size := encodedRefSize(0, env.Sender) +
		encodedRefSize(0, env.Receiver) +
		encodedRefSize(0, env.TypeName) + 1

	if len(env.Metadata) > 0 {
		size += 4 + len(env.Metadata)
	}

	return size + len(env.Payload)
}

// encodeDataEnvelope is the inline-only encoder used by tests and the exported
// [EncodeDataEnvelope] helper.
func encodeDataEnvelope(env DataEnvelope) ([]byte, error) {
	return encodeDataEnvelopeWithTables(env, 0, 0, 0)
}

// EncodeDataEnvelopeWithTables serializes env, encoding a table ref when the
// corresponding ID is nonzero and an inline literal otherwise.
func EncodeDataEnvelopeWithTables(env DataEnvelope, senderID, receiverID, typeID uint64) ([]byte, error) {
	return encodeDataEnvelopeWithTables(env, senderID, receiverID, typeID)
}

// encodeDataEnvelopeWithTables is the unexported implementation of the DATA
// envelope encoders.
func encodeDataEnvelopeWithTables(env DataEnvelope, senderID, receiverID, typeID uint64) ([]byte, error) {
	if err := validateSerializerID(env.SerializerID, env.TypeName); err != nil {
		return nil, err
	}

	size := encodedRefSize(senderID, env.Sender) +
		encodedRefSize(receiverID, env.Receiver) +
		encodedRefSize(typeID, env.TypeName) + 1

	if len(env.Metadata) > 0 {
		size += 4 + len(env.Metadata)
	}

	size += len(env.Payload)

	buf := make([]byte, size)
	pos := 0
	pos += putEncodedRef(buf[pos:], senderID, env.Sender)
	pos += putEncodedRef(buf[pos:], receiverID, env.Receiver)
	pos += putEncodedRef(buf[pos:], typeID, env.TypeName)
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
// surrounding frame's FrameFlagHasMetadata bit. A nonzero table ID returns
// [ErrTableRefUnsupported].
func DecodeDataEnvelope(src []byte, hasMetadata bool) (DataEnvelope, error) {
	return decodeDataEnvelope(src, hasMetadata)
}

// decodeDataEnvelope is the inline-only decoder used by tests and the exported
// [DecodeDataEnvelope] helper.
func decodeDataEnvelope(src []byte, hasMetadata bool) (DataEnvelope, error) {
	return decodeDataEnvelopeWithTables(src, hasMetadata, nil, nil, nil)
}

// decodeDataEnvelopeWithTables resolves table refs through the supplied
// receiver tables. senderResolve, when non-nil, lazily fills SenderHandle on
// a sender-ref table hit.
func decodeDataEnvelopeWithTables(src []byte, hasMetadata bool, paths, types *receiverTable, senderResolve func(string) any) (DataEnvelope, error) {
	var env DataEnvelope
	pos := 0

	sender, handle, n, err := readSenderRef(src[pos:], paths, senderResolve)
	if err != nil {
		return DataEnvelope{}, fmt.Errorf("tcp: data envelope sender: %w", err)
	}
	env.Sender = sender
	env.SenderHandle = handle
	pos += n

	receiver, _, n, err := readRefResolved(src[pos:], paths)
	if err != nil {
		return DataEnvelope{}, fmt.Errorf("tcp: data envelope receiver: %w", err)
	}
	env.Receiver = receiver
	pos += n

	typeName, _, n, err := readRefResolved(src[pos:], types)
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

// EncodeReplyEnvelope serializes env into a REPLY frame payload using an
// inline type ref. Metadata is written only when non-empty; callers must set
// FrameFlagHasMetadata on the surrounding frame to match.
func EncodeReplyEnvelope(env ReplyEnvelope) ([]byte, error) {
	return encodeReplyEnvelope(env)
}

// EncodeReplyEnvelopeWithTables serializes env, encoding a table type ref when
// typeID is nonzero.
func EncodeReplyEnvelopeWithTables(env ReplyEnvelope, typeID uint64) ([]byte, error) {
	return encodeReplyEnvelopeWithTables(env, typeID)
}

// encodeReplyEnvelopeWithTables is the unexported implementation of the REPLY
// envelope encoders.
func encodeReplyEnvelopeWithTables(env ReplyEnvelope, typeID uint64) ([]byte, error) {
	if err := validateSerializerID(env.SerializerID, env.TypeName); err != nil {
		return nil, err
	}

	size := encodedRefSize(typeID, env.TypeName) + 1

	if len(env.Metadata) > 0 {
		size += 4 + len(env.Metadata)
	}

	size += len(env.Payload)

	buf := make([]byte, size)
	pos := 0
	pos += putEncodedRef(buf[pos:], typeID, env.TypeName)
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
// surrounding frame's FrameFlagHasMetadata bit. A nonzero table ID returns
// [ErrTableRefUnsupported].
func DecodeReplyEnvelope(src []byte, hasMetadata bool) (ReplyEnvelope, error) {
	return decodeReplyEnvelope(src, hasMetadata)
}

// decodeReplyEnvelope is the inline-only decoder used by tests and the exported
// [DecodeReplyEnvelope] helper.
func decodeReplyEnvelope(src []byte, hasMetadata bool) (ReplyEnvelope, error) {
	return decodeReplyEnvelopeWithTables(src, hasMetadata, nil)
}

// encodeReplyEnvelope is the inline-only encoder used by tests and the duplex
// reply path when table compression is unavailable.
func encodeReplyEnvelope(env ReplyEnvelope) ([]byte, error) {
	return encodeReplyEnvelopeWithTables(env, 0)
}

// decodeReplyEnvelopeWithTables resolves a table type ref through types when
// non-nil.
func decodeReplyEnvelopeWithTables(src []byte, hasMetadata bool, types *receiverTable) (ReplyEnvelope, error) {
	var env ReplyEnvelope
	pos := 0

	typeName, _, n, err := readRefResolved(src[pos:], types)
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
	return encodedRefSize(0, s)
}

// encodedRefSize returns the wire size of a table or inline ref.
func encodedRefSize(id uint64, literal string) int {
	if id != 0 {
		return uvarintSize(id)
	}

	return uvarintSize(0) + uvarintSize(uint64(len(literal))) + len(literal)
}

// putEncodedRef writes a table ref when id is nonzero, otherwise an inline
// literal ref for literal.
func putEncodedRef(dst []byte, id uint64, literal string) int {
	if id != 0 {
		return binary.PutUvarint(dst, id)
	}

	n := binary.PutUvarint(dst, 0)
	n += binary.PutUvarint(dst[n:], uint64(len(literal)))
	n += copy(dst[n:], literal)
	return n
}

// readRefResolved decodes one envelope ref. When id is nonzero, table must
// resolve it; a nil table rejects nonzero IDs with [ErrTableRefUnsupported].
func readRefResolved(src []byte, table *receiverTable) (literal string, id uint64, n int, err error) {
	id, n = binary.Uvarint(src)
	if n <= 0 {
		return "", 0, 0, fmt.Errorf("truncated table id")
	}

	if id != 0 {
		if table == nil {
			return "", 0, 0, fmt.Errorf("%w: id %d", ErrTableRefUnsupported, id)
		}

		entry := table.lookup(id)
		if entry == nil {
			return "", 0, 0, fmt.Errorf("%w: id %d", ErrUnknownTableRef, id)
		}

		return entry.literal, id, n, nil
	}

	literal, m, err := readInlineLiteral(src[n:])
	if err != nil {
		return "", 0, 0, err
	}

	return literal, 0, n + m, nil
}

// readSenderRef decodes the DATA sender ref and, on a table hit, fills the
// opaque sender handle in one table pass (resolve runs outside the mutex).
func readSenderRef(src []byte, table *receiverTable, resolve func(string) any) (literal string, handle any, n int, err error) {
	id, n := binary.Uvarint(src)
	if n <= 0 {
		return "", nil, 0, fmt.Errorf("truncated table id")
	}

	if id != 0 {
		if table == nil {
			return "", nil, 0, fmt.Errorf("%w: id %d", ErrTableRefUnsupported, id)
		}

		lit, h, ok := table.resolveRef(id, resolve)
		if !ok {
			return "", nil, 0, fmt.Errorf("%w: id %d", ErrUnknownTableRef, id)
		}

		return lit, h, n, nil
	}

	literal, m, err := readInlineLiteral(src[n:])
	if err != nil {
		return "", nil, 0, err
	}

	return literal, nil, n + m, nil
}

// readInlineLiteral decodes the length-prefixed bytes of an inline ref body.
func readInlineLiteral(src []byte) (literal string, n int, err error) {
	length, m := binary.Uvarint(src)
	if m <= 0 {
		return "", 0, fmt.Errorf("truncated inline length")
	}

	n = m
	if length > uint64(len(src)-n) {
		return "", 0, fmt.Errorf("inline length %d exceeds remaining %d", length, len(src)-n)
	}

	return string(src[n : n+int(length)]), n + int(length), nil
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
