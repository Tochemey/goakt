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

	"github.com/bytedance/sonic"
)

// JSONSerializer errors.
var (
	// ErrJSONNilMessage is returned by [JSONSerializer.Serialize] when the
	// supplied message is nil.
	ErrJSONNilMessage = errors.New("remote: JSON message is nil")

	// ErrJSONSerializeFailed is returned when JSON marshaling fails. It wraps
	// the underlying sonic library error.
	ErrJSONSerializeFailed = errors.New("remote: failed to serialize JSON message")

	// ErrJSONDeserializeFailed is returned when JSON unmarshaling fails. It
	// wraps the underlying sonic library error.
	ErrJSONDeserializeFailed = errors.New("remote: failed to deserialize JSON message")

	// ErrJSONTypeNotRegistered is returned when the message type is not in the
	// global types registry. Register types via [WithSerializers] or [WithClientSerializers].
	ErrJSONTypeNotRegistered = errors.New("remote: JSON type not registered")

	// ErrJSONInvalidFrame is returned by [JSONSerializer.Deserialize] when the
	// byte slice is too short or whose length header fields are inconsistent
	// with the actual payload size.
	ErrJSONInvalidFrame = errors.New("remote: malformed or truncated JSON frame")
)

// jsonAPI is the sonic configuration used by JSONSerializer. ConfigFastest
// drops HTML escaping and JSON-marshaler validation for maximum throughput;
// goAkt remoting consumes its own output, so neither guarantee is needed.
var jsonAPI = sonic.ConfigFastest

// JSONSerializer is a [Serializer] implementation that encodes and decodes
// arbitrary Go values using JSON (via the sonic library) in a length-prefixed,
// self-describing frame format. The frame embeds the message's type name (from
// the global types registry) so the receiver can reconstruct the correct
// concrete Go type at runtime.
//
// # Frame layout
//
// All integers are big-endian uint32. The layout matches [ProtoSerializer] and
// [CBORSerializer] for consistency; the type name is the lowercased, trimmed
// [reflect.Type] string used by the global registry.
//
// ┌──────────┬──────────┬────────────┬──────────────┐
// │ totalLen │ nameLen  │ type name  │ JSON bytes   │
// │ 4 bytes  │ 4 bytes  │ N bytes    │ M bytes      │
// │ uint32BE │ uint32BE │ lowTrim    │ raw JSON     │
// └──────────┴──────────┴────────────┴──────────────┘
//
//	totalLen = 4 + 4 + N + M   (covers the entire frame including itself)
//
// # Usage
//
// JSONSerializer is stateless and safe for concurrent use. A single instance
// can be shared across goroutines without synchronization.
//
// Register concrete types when configuring the remoting layer; the type is
// automatically added to the global registry:
//
//	cfg := remote.NewConfig("0.0.0.0", 9000,
//	    remote.WithSerializers(new(MyMessage), remote.NewJSONSerializer()),
//	)
//
// # Constraints
//
// Both [Serialize] and [Deserialize] require that the message type is
// registered in the global types registry. Registration happens automatically
// when using [WithSerializers] or [WithClientSerializers] with a concrete type.
//
// # Platform support
//
// sonic ships JIT-accelerated fast paths for amd64 and arm64; on other
// architectures (386, riscv64, s390x, …) it falls back to encoding/json. The
// API surface is identical — only throughput differs.
//
// # Frame size and nesting
//
// sonic does not expose a per-decode maximum nesting depth, so peer-controlled
// JSON payloads are bounded only by the remoting layer's MaxFrameSize
// (default 16MB, configurable via [WithMaxFrameSize]). If you accept frames
// from untrusted peers, tune MaxFrameSize accordingly.
type JSONSerializer struct{}

// enforce the Serializer interface at compile time.
var _ Serializer = (*JSONSerializer)(nil)

// RegistryRequired implements types.UsesRegistry so JSONSerializer
// is recognized by types.RegisterSerializerType.
func (*JSONSerializer) RegistryRequired() {}

// NewJSONSerializer returns a ready-to-use [JSONSerializer] that uses the
// package-level global type registry. Register types via [WithSerializers] or
// [WithClientSerializers]; they are added to the registry automatically.
func NewJSONSerializer() *JSONSerializer {
	return &JSONSerializer{}
}

// Serialize implements [Serializer]. It derives the message's type name from
// the global registry, encodes the value with JSON, and produces a
// self-describing binary frame so [Deserialize] can resolve the concrete type
// without out-of-band coordination.
//
// Returns [ErrJSONNilMessage] if message is nil.
// Returns an error for unregistered types (register via [WithSerializers] or [WithClientSerializers]).
// Returns [ErrJSONSerializeFailed] wrapping the sonic error on marshal failure.
func (s *JSONSerializer) Serialize(message any) ([]byte, error) {
	typ := reflect.TypeOf(message)
	if typ == nil {
		return nil, ErrJSONNilMessage
	}

	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	name := strings.ToLower(strings.TrimSpace(typ.String())) // == types.Name(new(T))

	if _, ok := typesRegistry.TypeOf(name); !ok {
		return nil, fmt.Errorf("remote: JSON type not registered: %s", name)
	}

	jsonBytes, err := jsonAPI.Marshal(message)
	if err != nil {
		return nil, errors.Join(ErrJSONSerializeFailed, err)
	}

	nameLen := len(name)
	totalLen := 4 + 4 + nameLen + len(jsonBytes)
	out := make([]byte, 0, totalLen)

	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[0:4], uint32(totalLen))
	binary.BigEndian.PutUint32(hdr[4:8], uint32(nameLen))
	out = append(out, hdr[:]...)
	out = append(out, name...)
	out = append(out, jsonBytes...)
	return out, nil
}

// Deserialize implements [Serializer]. It decodes a frame produced by
// [Serialize], extracts the type name from the frame header, resolves the
// concrete Go type from the global registry, and unmarshals the JSON payload
// into a fresh instance of that type. The returned value is a pointer to the
// decoded value wrapped as any; callers can recover the original concrete type
// with a type assertion.
//
// Built-in primitive types are returned as values (not pointers) so round-trip
// serialization preserves the original type — matching [CBORSerializer]'s
// behavior.
//
// Returns [ErrJSONInvalidFrame] for truncated or malformed frames.
// Returns an error wrapping the type name when the type is not registered.
// Returns [ErrJSONDeserializeFailed] wrapping the sonic error on unmarshal failure.
func (s *JSONSerializer) Deserialize(data []byte) (any, error) {
	if len(data) < 8 {
		return nil, ErrJSONInvalidFrame
	}

	totalLen := int(binary.BigEndian.Uint32(data[0:4]))
	if len(data) < totalLen || totalLen < 8 {
		return nil, ErrJSONInvalidFrame
	}

	nameLen := int(binary.BigEndian.Uint32(data[4:8]))
	if 8+nameLen > totalLen {
		return nil, ErrJSONInvalidFrame
	}

	// Zero-copy type name extraction (matches CBORSerializer / ProtoSerializer).
	name := unsafe.String(unsafe.SliceData(data[8:8+nameLen]), nameLen)

	elemType, ok := typesRegistry.TypeOf(name)
	if !ok {
		return nil, fmt.Errorf("remote: JSON type not registered: %s", name)
	}

	ptr := reflect.New(elemType)
	if err := jsonAPI.Unmarshal(data[8+nameLen:totalLen], ptr.Interface()); err != nil {
		return nil, errors.Join(ErrJSONDeserializeFailed, err)
	}

	if isBuiltinPrimitive(elemType) {
		return ptr.Elem().Interface(), nil
	}
	return ptr.Interface(), nil
}
