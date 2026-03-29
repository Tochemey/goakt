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

package ddata

import (
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/remote"
)

// CRDTValueSerializer is a composite serializer for CRDT element and key values.
// It routes proto.Message values to ProtoSerializer and all other types
// (Go primitives, user-registered CBOR structs) to CBORSerializer.
//
// Serialize uses a type assertion for O(1) dispatch.
// Deserialize tries Proto first (fails fast for non-proto frames),
// then falls back to CBOR.
type CRDTValueSerializer struct {
	proto *remote.ProtoSerializer
	cbor  *remote.CBORSerializer
}

var _ remote.Serializer = (*CRDTValueSerializer)(nil)

// NewCRDTValueSerializer creates a composite serializer that routes
// proto.Message values to ProtoSerializer and everything else to CBORSerializer.
func NewCRDTValueSerializer() *CRDTValueSerializer {
	return &CRDTValueSerializer{
		proto: remote.NewProtoSerializer(),
		cbor:  remote.NewCBORSerializer(),
	}
}

// Serialize encodes the message using ProtoSerializer for proto.Message types
// and CBORSerializer for all other types.
func (s *CRDTValueSerializer) Serialize(message any) ([]byte, error) {
	if _, ok := message.(proto.Message); ok {
		return s.proto.Serialize(message)
	}
	return s.cbor.Serialize(message)
}

// Deserialize decodes the data by trying ProtoSerializer first (fails fast
// for non-proto frames), then falling back to CBORSerializer.
func (s *CRDTValueSerializer) Deserialize(data []byte) (any, error) {
	msg, err := s.proto.Deserialize(data)
	if err == nil {
		return msg, nil
	}
	return s.cbor.Deserialize(data)
}
