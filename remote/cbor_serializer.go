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

package remote

import (
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unsafe"

	"github.com/fxamacker/cbor/v2"

	"github.com/tochemey/goakt/v4/internal/types"
)

// typesRegistry is the global type registry used by all [CBORSerializer] instances
// to resolve Go types from their wire names on the receive path. Types are
// registered automatically when [WithSerializers] or [WithClientSerializers] is
// called with a [CBORSerializer] and a concrete message type; receive-only types
// may be registered explicitly via [RegisterSerializableTypes].
var typesRegistry = types.NewRegistry()

// CBORSerializer errors.
var (
	// ErrCBORNilMessage is returned by [CBORSerializer.Serialize] when the
	// supplied message is nil.
	ErrCBORNilMessage = errors.New("remote: CBOR message is nil")

	// ErrCBORSerializeFailed is returned when CBOR marshaling fails. It wraps
	// the underlying CBOR library error.
	ErrCBORSerializeFailed = errors.New("remote: failed to serialize CBOR message")

	// ErrCBORDeserializeFailed is returned when CBOR unmarshaling fails. It
	// wraps the underlying CBOR library error.
	ErrCBORDeserializeFailed = errors.New("remote: failed to deserialize CBOR message")

	// ErrCBORTypeNotRegistered is returned when the message type is not in the
	// global types registry. Register types via [RegisterSerializableTypes].
	ErrCBORTypeNotRegistered = errors.New("remote: CBOR type not registered")

	// ErrCBORInvalidFrame is returned by [CBORSerializer.Deserialize] when the
	// byte slice is too short or whose length header fields are inconsistent
	// with the actual payload size.
	ErrCBORInvalidFrame = errors.New("remote: malformed or truncated CBOR frame")

	cborEncOpts = cbor.EncOptions{
		Sort:        cbor.SortNone, // no key sorting — fastest
		IndefLength: cbor.IndefLengthForbidden,
		Time:        cbor.TimeUnixDynamic,
	}
	cborDecOpts = cbor.DecOptions{
		MaxNestedLevels: 64,
		IndefLength:     cbor.IndefLengthForbidden,
		UTF8:            cbor.UTF8DecodeInvalid,
	}
)

// CBORSerializer is a [Serializer] implementation that encodes and decodes
// arbitrary Go values using the Concise Binary Object Representation (CBOR)
// in a length-prefixed, self-describing frame format. The frame embeds the
// message's type name (from the global types registry) so the receiver can
// reconstruct the correct concrete Go type at runtime.
//
// # Frame layout
//
// All integers are big-endian uint32. The layout matches [ProtoSerializer] for
// consistency; the type name is the lowercased, trimmed [reflect.Type] string
// used by the global registry.
//
// ┌──────────┬──────────┬────────────┬──────────────┐
// │ totalLen │ nameLen  │ type name  │ CBOR bytes   │
// │ 4 bytes  │ 4 bytes  │ N bytes    │ M bytes      │
// │ uint32BE │ uint32BE │ lowTrim    │ raw CBOR     │
// └──────────┴──────────┴────────────┴──────────────┘
//
//	totalLen = 4 + 4 + N + M   (covers the entire frame including itself)
//
// # Usage
//
// CBORSerializer is stateless and safe for concurrent use. A single instance
// can be shared across goroutines without synchronization.
//
// Register concrete types when configuring the remoting layer; the type is
// automatically added to the global registry:
//
//	cfg := remote.NewConfig("0.0.0.0", 9000,
//	    remote.WithSerializers(new(MyMessage), remote.NewCBORSerializer()),
//	)
//
// For types that are only received (never sent from this node), register
// explicitly:
//
//	remote.RegisterSerializableTypes(new(MyMessage))
//
// # Constraints
//
// Both [Serialize] and [Deserialize] require that the message type is
// registered in the global types registry. Registration happens
// automatically when using a concrete type with [RegisterSerializableTypes]
type CBORSerializer struct {
	encMode cbor.EncMode // immutable after construction, thread-safe
	decMode cbor.DecMode // immutable after construction, thread-safe
}

// enforce the Serializer interface at compile time.
var _ Serializer = (*CBORSerializer)(nil)

// NewCBORSerializer returns a ready-to-use [CBORSerializer] that uses the
// package-level global type registry. Types must be registered before use via [RegisterSerializableTypes].
func NewCBORSerializer() *CBORSerializer {
	encMode, _ := cborEncOpts.EncMode()
	decMode, _ := cborDecOpts.DecMode()
	return &CBORSerializer{encMode: encMode, decMode: decMode}
}

// RegisterSerializableTypes registers one or more Go types in the global types
// registry. Pass a value of each type (typically a pointer to the zero value):
//
//	remote.RegisterSerializableTypes(new(MyMessage), new(OrderEvent))
//
// Use this for types that are only received on this node and never passed to
// [WithSerializers] or [WithClientSerializers]. Types that are registered via
// those options are added to the registry automatically.
func RegisterSerializableTypes(values ...any) {
	for _, v := range values {
		typesRegistry.Register(v)
	}
}

// Deserialize implements [Serializer]. It decodes a frame produced by
// [Serialize], extracts the type name from the frame header, resolves the
// concrete Go type from the global registry, and unmarshals the CBOR payload
// into a fresh instance of that type. The returned value is a pointer to the
// decoded value wrapped as any; callers can recover the original concrete type
// with a type assertion.
//
// The type-name extraction uses an unsafe []byte→string conversion to avoid a
// heap allocation; the resulting string is used only for the registry lookup.
//
// Returns [ErrCBORInvalidFrame] for truncated or malformed frames.
// Returns an error wrapping the type name when the type is not registered.
// Returns [ErrCBORDeserializeFailed] wrapping the CBOR error on unmarshal failure.
func (s *CBORSerializer) Deserialize(data []byte) (any, error) {
	// 1. Validate frame (same guards as ProtoSerializer).
	if len(data) < 8 {
		return nil, ErrCBORInvalidFrame
	}

	totalLen := int(binary.BigEndian.Uint32(data[0:4]))
	if len(data) < totalLen || totalLen < 8 {
		return nil, ErrCBORInvalidFrame
	}

	nameLen := int(binary.BigEndian.Uint32(data[4:8]))
	if 8+nameLen > totalLen {
		return nil, ErrCBORInvalidFrame
	}

	// 2. Zero-copy type name extraction (same unsafe trick as ProtoSerializer).
	name := unsafe.String(unsafe.SliceData(data[8:8+nameLen]), nameLen)

	// 3. Look up the concrete type — lock-free xsync.Map read.
	elemType, ok := typesRegistry.TypeOf(name)
	if !ok {
		return nil, fmt.Errorf("remote: CBOR type not registered: %s", name)
	}

	// 4. Allocate a pointer to a zero value and unmarshal into it.
	ptr := reflect.New(elemType)
	if err := s.decMode.Unmarshal(data[8+nameLen:totalLen], ptr.Interface()); err != nil {
		return nil, errors.Join(ErrCBORDeserializeFailed, err)
	}

	return ptr.Interface(), nil
}

// Serialize implements [Serializer]. It derives the message's type name from
// the global registry, encodes the value with CBOR, and produces a
// self-describing binary frame so [Deserialize] can resolve the concrete type
// without out-of-band coordination.
//
// The encoding uses a single allocation for the output frame, writes both
// uint32 header fields from a stack-allocated array, and appends the type name
// and CBOR bytes in place — no intermediate buffers.
//
// Returns [ErrCBORNilMessage] if message is nil.
// Returns an error for unregistered types (register via [RegisterSerializableTypes]).
// Returns [ErrCBORSerializeFailed] wrapping the CBOR error on marshal failure.
func (s *CBORSerializer) Serialize(message any) ([]byte, error) {
	// 1. Derive the wire name — same formula as types.Name but handles
	//    both pointer and value receivers without panicking.
	typ := reflect.TypeOf(message)
	if typ == nil {
		return nil, ErrCBORNilMessage
	}

	if typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
	}

	name := strings.ToLower(strings.TrimSpace(typ.String())) // == types.Name(new(T))

	// 2. Confirm the type is registered (fast xsync.Map read).
	if _, ok := typesRegistry.TypeOf(name); !ok {
		return nil, fmt.Errorf("remote: CBOR type not registered: %s", name)
	}

	// 3. CBOR-encode the message value.
	cborBytes, err := s.encMode.Marshal(message)
	if err != nil {
		return nil, errors.Join(ErrCBORSerializeFailed, err)
	}

	// 4. Build the frame — single allocation, stack-allocated header.
	nameLen := len(name)
	totalLen := 4 + 4 + nameLen + len(cborBytes)
	out := make([]byte, 0, totalLen)

	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[0:4], uint32(totalLen))
	binary.BigEndian.PutUint32(hdr[4:8], uint32(nameLen))
	out = append(out, hdr[:]...)
	out = append(out, name...)
	out = append(out, cborBytes...)
	return out, nil
}
