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

package tcp

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/tochemey/goakt/v3/test/data/testpb"
)

func TestProtoSerializer_MarshalUnmarshalBinary_Success(t *testing.T) {
	orig := &testpb.Reply{Content: "hello world"}
	serializer := NewProtoSerializer()

	data, err := serializer.MarshalBinary(orig)
	require.NoError(t, err, "MarshalBinary failed")

	actual, typeName, err := serializer.UnmarshalBinary(data)
	require.NoError(t, err, "UnmarshalBinary failed")
	require.NotNil(t, actual)
	require.Equal(t, protoreflect.FullName("testpb.Reply"), typeName)

	reply, ok := actual.(*testpb.Reply)
	require.True(t, ok)
	require.Equal(t, orig.Content, reply.Content, "Expected message content to match")
}

func TestProtoSerializer_MarshalBinary_Errors(t *testing.T) {
	serializer := NewProtoSerializer()
	_, err := serializer.MarshalBinary(nil)
	require.Error(t, err, "Expected error when marshaling empty Proto")
	require.ErrorIs(t, err, ErrUnknownMessageType, "Expected ErrUnknownMessageType")
}

func TestProtoSerializer_UnmarshalBinary_InvalidLength(t *testing.T) {
	serializer := NewProtoSerializer()

	// Too short.
	actual, _, err := serializer.UnmarshalBinary([]byte{1, 2, 3})
	require.Error(t, err, "Expected error for too short data")
	require.ErrorIs(t, err, ErrInvalidMessageLength)
	require.Nil(t, actual)

	// messageLength > len(data).
	data := make([]byte, 8)
	binary.BigEndian.PutUint32(data[:4], 100)
	actual, _, err = serializer.UnmarshalBinary(data)
	require.Error(t, err, "Expected error for messageLength > len(data)")
	require.ErrorIs(t, err, ErrInvalidMessageLength)
	require.Nil(t, actual)

	// messageNameLength+8 > messageLength.
	data = make([]byte, 12)
	binary.BigEndian.PutUint32(data[:4], 12)
	binary.BigEndian.PutUint32(data[4:8], 10)
	actual, _, err = serializer.UnmarshalBinary(data)
	require.Error(t, err, "Expected error for messageNameLength+8 > messageLength")
	require.ErrorIs(t, err, ErrInvalidMessageLength)
	require.Nil(t, actual)
}

func TestProtoSerializer_UnmarshalBinary_UnknownType(t *testing.T) {
	name := "DoesNotExist"
	totalLen := 4 + 4 + len(name)

	data := make([]byte, totalLen)
	binary.BigEndian.PutUint32(data[0:4], uint32(totalLen))
	binary.BigEndian.PutUint32(data[4:8], uint32(len(name)))
	copy(data[8:], name)

	serializer := NewProtoSerializer()
	actual, _, err := serializer.UnmarshalBinary(data)
	require.Error(t, err, "Expected error for unknown message type")
	require.ErrorIs(t, err, ErrUnknownMessageType)
	require.Nil(t, actual)
}

func TestProtoSerializer_UnmarshalBinary_InvalidProtoPayload(t *testing.T) {
	msgName := string(proto.MessageName(&testpb.Reply{}))
	payload := []byte{0xff, 0xff, 0xff}
	totalLen := 4 + 4 + len(msgName) + len(payload)

	data := make([]byte, totalLen)
	binary.BigEndian.PutUint32(data[0:4], uint32(totalLen))
	binary.BigEndian.PutUint32(data[4:8], uint32(len(msgName)))
	copy(data[8:], msgName)
	copy(data[8+len(msgName):], payload)

	serializer := NewProtoSerializer()
	actual, _, err := serializer.UnmarshalBinary(data)
	require.Error(t, err, "Expected error for invalid proto payload")
	require.ErrorIs(t, err, ErrUnmarshalBinaryFailed)
	require.Nil(t, actual)
}
