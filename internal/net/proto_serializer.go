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
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// protoMessageTypes caches protobuf registry lookups keyed by fully-qualified
// type name. [protoregistry.GlobalTypes.FindMessageByName] takes a global
// lock shared by every connection, and steady-state traffic resolves a small
// bounded set of message types, so a lock-free read-mostly cache removes the
// contention from the per-message path. Registry misses are cached too (as
// typeNotFound markers) so traffic that can never resolve, such as sniffed
// names from non-proto payloads, does not re-enter the registry lock per
// message.
var protoMessageTypes sync.Map // protoreflect.FullName -> protoreflect.MessageType | typeNotFound

// typeNotFound marks a registry miss in protoMessageTypes so repeated lookups
// for the same unregistered name skip the registry lock.
type typeNotFound struct{}

// negativeTypeCacheCap bounds the number of cached registry misses. Sniffed
// type names from non-proto payloads can be arbitrary bytes, so the negative
// side of the cache is flushed wholesale once it reaches this cap instead of
// growing without bound.
const negativeTypeCacheCap = 8192

// negativeTypeCount approximately tracks how many miss markers
// protoMessageTypes holds; it only gates the wholesale flush, so races that
// skew it by a few entries are harmless.
var negativeTypeCount atomic.Int64

// typeCacheFlushing makes the wholesale flush single-flight: when several
// goroutines cross the cap together, one runs the O(n) Range and the rest
// skip it instead of piling concurrent full-map scans onto the lookup path.
var typeCacheFlushing atomic.Bool

// FindMessageType resolves a protobuf message type by fully-qualified name
// through a lock-free cache in front of [protoregistry.GlobalTypes]. name
// may be a transient view into a pooled buffer: it is used only for the
// lookup, and the key stored on a cache miss is cloned first.
//
// Misses are cached negatively, so a message type registered with the global
// registry after its name has already been looked up keeps resolving as not
// found until the negative cache is flushed. Register proto types at init
// time, before any traffic flows.
func FindMessageType(name protoreflect.FullName) (protoreflect.MessageType, error) {
	if cached, ok := protoMessageTypes.Load(name); ok {
		if msgType, ok := cached.(protoreflect.MessageType); ok {
			return msgType, nil
		}

		return nil, protoregistry.NotFound
	}

	msgType, err := protoregistry.GlobalTypes.FindMessageByName(name)
	if err != nil {
		if !errors.Is(err, protoregistry.NotFound) {
			return nil, err
		}

		if negativeTypeCount.Load() >= negativeTypeCacheCap {
			cleanTypeCache()
		}

		key := protoreflect.FullName(strings.Clone(string(name)))
		if _, loaded := protoMessageTypes.LoadOrStore(key, typeNotFound{}); !loaded {
			negativeTypeCount.Add(1)
		}

		return nil, err
	}

	protoMessageTypes.Store(protoreflect.FullName(strings.Clone(string(name))), msgType)

	return msgType, nil
}

// cleanTypeCache drops every miss marker from protoMessageTypes while
// keeping resolved types. The wholesale flush keeps the lookup path free of
// per-entry bookkeeping (no LRU, no timestamps). Only one flush runs at a
// time; callers that lose the race return immediately and let the winner
// finish (their own miss is still stored, just possibly a flush cycle late).
func cleanTypeCache() {
	if !typeCacheFlushing.CompareAndSwap(false, true) {
		return
	}
	defer typeCacheFlushing.Store(false)

	protoMessageTypes.Range(func(key, value any) bool {
		if _, miss := value.(typeNotFound); miss {
			protoMessageTypes.Delete(key)
		}

		return true
	})

	negativeTypeCount.Store(0)
}

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
	return x.MarshalBinaryTo(nil, message)
}

// MarshalBinaryTo is identical to [MarshalBinary] but draws the output frame
// buffer from pool when non-nil, falling back to a fresh allocation otherwise.
// Callers using the pooled variant own the returned slice until they write it
// to the wire, at which point they must return it via pool.Put. The slice is
// unsafe to reuse once released.
func (x *ProtoSerializer) MarshalBinaryTo(pool *FramePool, message proto.Message) ([]byte, error) {
	if message == nil {
		return nil, ErrUnknownMessageType
	}

	messageName := proto.MessageName(message)
	nameLen := len(messageName)
	if nameLen == 0 {
		return nil, ErrUnknownMessageType
	}

	// UseCachedSize lets MarshalAppend reuse the size computed here instead
	// of traversing the message a second time.
	opts := proto.MarshalOptions{UseCachedSize: true}

	protoSize := opts.Size(message)
	totalLen := 4 + 4 + nameLen + protoSize

	var out []byte
	if pool != nil {
		out = pool.Get(totalLen)[:0]
	} else {
		out = make([]byte, 0, totalLen)
	}

	// Write the two uint32 header fields into a stack-allocated array,
	// then append. No reflection, no interface boxing.
	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[0:4], uint32(totalLen))
	binary.BigEndian.PutUint32(hdr[4:8], uint32(nameLen))
	out = append(out, hdr[:]...)

	// Append the fully-qualified type name.
	out = append(out, messageName...)

	// Marshal the proto payload directly into the tail of out.
	out, err := opts.MarshalAppend(out, message)
	if err != nil {
		if pool != nil {
			pool.Put(out)
		}
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

	// Zero-copy string from the frame bytes. The returned FullName is a view
	// into data: it is valid only while the caller keeps data alive and
	// unmodified, and must not be retained after returning the buffer to a
	// pool (a reused buffer overwrites the name in place).
	typeName := protoreflect.FullName(unsafe.String(unsafe.SliceData(data[8:8+nameLen]), nameLen))

	msgType, err := FindMessageType(typeName)
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
	return x.MarshalBinaryWithMetadataTo(nil, message, md)
}

// MarshalBinaryWithMetadataTo is identical to [MarshalBinaryWithMetadata] but
// draws the output frame buffer from pool when non-nil. Ownership semantics
// match [MarshalBinaryTo].
func (x *ProtoSerializer) MarshalBinaryWithMetadataTo(pool *FramePool, message proto.Message, md *Metadata) ([]byte, error) {
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

	// UseCachedSize lets MarshalAppend reuse the size computed here instead
	// of traversing the message a second time.
	opts := proto.MarshalOptions{UseCachedSize: true}

	protoSize := opts.Size(message)
	totalLen := 4 + 4 + nameLen + 4 + metaLen + protoSize

	var out []byte
	if pool != nil {
		out = pool.Get(totalLen)[:0]
	} else {
		out = make([]byte, 0, totalLen)
	}

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
	out, err := opts.MarshalAppend(out, message)
	if err != nil {
		if pool != nil {
			pool.Put(out)
		}
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

	// Zero-copy string from the frame bytes (same technique and same
	// lifetime caveat as UnmarshalBinary).
	typeName := protoreflect.FullName(unsafe.String(unsafe.SliceData(data[12:12+nameLen]), nameLen))

	msgType, err := FindMessageType(typeName)
	if err != nil {
		return nil, nil, "", errors.Join(ErrUnknownMessageType, err)
	}

	// Parse metadata if present. The zero value is used directly instead of
	// NewMetadata: UnmarshalBinary allocates the headers map itself, so the
	// constructor's map would be immediate garbage.
	var md *Metadata
	if metaLen > 0 {
		md = &Metadata{}
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
