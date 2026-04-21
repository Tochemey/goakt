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

package types

import (
	"reflect"

	"google.golang.org/protobuf/proto"
)

// GlobalRegistry is the global type registry used by CBOR serialization to
// resolve Go types from their wire names. Used by remote and internal/remoteclient.
var GlobalRegistry = NewRegistry()

func init() {
	// Pre-register Go primitive types so that CBORSerializer can encode and
	// decode them without explicit user registration. This is required for
	// CRDT types that store any-typed values (ORSet elements, LWWRegister
	// values, etc.). We register via reflect.Type to bypass the pointer-only
	// gate in RegisterSerializerType.
	primitives := [...]reflect.Type{
		reflect.TypeFor[string](),
		reflect.TypeFor[bool](),
		reflect.TypeFor[int](),
		reflect.TypeFor[int8](),
		reflect.TypeFor[int16](),
		reflect.TypeFor[int32](),
		reflect.TypeFor[int64](),
		reflect.TypeFor[uint](),
		reflect.TypeFor[uint8](),
		reflect.TypeFor[uint16](),
		reflect.TypeFor[uint32](),
		reflect.TypeFor[uint64](),
		reflect.TypeFor[float32](),
		reflect.TypeFor[float64](),
	}
	for _, t := range primitives {
		GlobalRegistry.Register(t)
	}
}

// UsesRegistry is implemented by serializers that use the global types registry.
// Used to avoid importing remote for type assertion.
type UsesRegistry interface {
	RegistryRequired()
}

// RegisterSerializerType registers msg in the global types registry when
// serializer implements UsesRegistry and msg is a concrete non-proto type.
// Called by remote.WithSerializers and remoteclient.WithClientSerializers.
func RegisterSerializerType(msg any, serializer any) {
	if _, ok := serializer.(UsesRegistry); !ok {
		return
	}
	typ := reflect.TypeOf(msg)
	if typ == nil || typ.Kind() != reflect.Pointer || typ.Elem().Kind() == reflect.Interface {
		return
	}
	protoMsgType := reflect.TypeFor[proto.Message]()
	if typ.Implements(protoMsgType) {
		return
	}
	GlobalRegistry.Register(msg)
}
