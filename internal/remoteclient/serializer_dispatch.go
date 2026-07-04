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

package remoteclient

import (
	"encoding/binary"
	"errors"
	"unsafe"

	"google.golang.org/protobuf/reflect/protoreflect"

	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/remote"
)

var (
	errNoSerializerEncode = errors.New("remote: no serializer could encode the message")
	errNoSerializerDecode = errors.New("remote: no serializer could decode the message")
)

// serializerDispatch is a composite [Serializer] returned by
// [remoting.Serializer] when the caller passes nil (receive path).
// Built once per [remoting] instance and reused for every inbound message.
//
// Decoding dispatches by the type name embedded in the shared frame layout:
// a frame whose name resolves in the protobuf registry goes straight to the
// registered proto serializer regardless of registration order, so no
// serializer wastes a full failed decode attempt on a frame it cannot
// handle. Frames that do not resolve fall back to trying each registered
// serializer in registration order.
type serializerDispatch struct {
	entries []ifaceEntry
	// proto is the first registered [*remote.ProtoSerializer], if any; it is
	// the type-name fast-path target.
	proto *remote.ProtoSerializer
}

var _ remote.Serializer = (*serializerDispatch)(nil)

// newSerializerDispatch builds the composite dispatcher over the frozen
// entries slice.
func newSerializerDispatch(entries []ifaceEntry) *serializerDispatch {
	dispatch := &serializerDispatch{entries: entries}

	for i := range entries {
		if ps, ok := entries[i].serializer.(*remote.ProtoSerializer); ok {
			dispatch.proto = ps
			break
		}
	}

	return dispatch
}

// frameTypeName extracts the fully-qualified type name embedded in the
// shared [totalLen|nameLen|typeName|payload] frame layout. The returned name
// is a zero-copy view into data, valid only while data is alive and
// unmodified.
func frameTypeName(data []byte) (protoreflect.FullName, bool) {
	if len(data) < 8 {
		return "", false
	}

	totalLen := int(binary.BigEndian.Uint32(data[:4]))
	nameLen := int(binary.BigEndian.Uint32(data[4:8]))

	if totalLen < 8 || len(data) < totalLen || nameLen <= 0 || 8+nameLen > totalLen {
		return "", false
	}

	return protoreflect.FullName(unsafe.String(unsafe.SliceData(data[8:8+nameLen]), nameLen)), true
}

// Serialize tries each registered serializer in order and returns the first
// successful encoding.
func (x *serializerDispatch) Serialize(message any) ([]byte, error) {
	var lastErr error

	for i := range x.entries {
		data, err := x.entries[i].serializer.Serialize(message)
		if err == nil {
			return data, nil
		}
		lastErr = err
	}

	if lastErr != nil {
		return nil, lastErr
	}

	return nil, errNoSerializerEncode
}

// Deserialize decodes data produced by any registered serializer. Frames
// whose embedded type name resolves in the protobuf registry decode via the
// proto serializer directly; everything else tries each registered
// serializer in registration order.
func (x *serializerDispatch) Deserialize(data []byte) (any, error) {
	if x.proto != nil {
		if name, ok := frameTypeName(data); ok {
			if _, err := inet.FindMessageType(name); err == nil {
				msg, err := x.proto.Deserialize(data)
				if err == nil {
					return msg, nil
				}
				// A registry hit with a failed decode is pathological (e.g. a
				// non-proto payload under a colliding name); fall through to
				// the ordered loop rather than failing outright.
			}
		}
	}

	var lastErr error

	for i := range x.entries {
		msg, err := x.entries[i].serializer.Deserialize(data)
		if err == nil {
			return msg, nil
		}
		lastErr = err
	}

	if lastErr != nil {
		return nil, lastErr
	}

	return nil, errNoSerializerDecode
}
