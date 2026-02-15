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
	"testing"
	"time"

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

func TestProtoSerializer_MarshalUnmarshalBinaryWithMetadata_Success(t *testing.T) {
	orig := &testpb.Reply{Content: "hello with metadata"}
	serializer := NewProtoSerializer()

	t.Run("with metadata and deadline", func(t *testing.T) {
		md := NewMetadata()
		md.Set("trace-id", "abc123")
		md.Set("span-id", "xyz789")
		md.SetDeadline(time.Unix(1234567890, 0))

		data, err := serializer.MarshalBinaryWithMetadata(orig, md)
		require.NoError(t, err)

		actual, mdOut, typeName, err := serializer.UnmarshalBinaryWithMetadata(data)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.NotNil(t, mdOut)
		require.Equal(t, protoreflect.FullName("testpb.Reply"), typeName)

		reply, ok := actual.(*testpb.Reply)
		require.True(t, ok)
		require.Equal(t, orig.Content, reply.Content)

		traceID, ok := mdOut.Get("trace-id")
		require.True(t, ok)
		require.Equal(t, "abc123", traceID)

		spanID, ok := mdOut.Get("span-id")
		require.True(t, ok)
		require.Equal(t, "xyz789", spanID)

		deadline, ok := mdOut.GetDeadline()
		require.True(t, ok)
		require.Equal(t, time.Unix(1234567890, 0), deadline)
	})

	t.Run("with nil metadata", func(t *testing.T) {
		data, err := serializer.MarshalBinaryWithMetadata(orig, nil)
		require.NoError(t, err)

		actual, mdOut, typeName, err := serializer.UnmarshalBinaryWithMetadata(data)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.Nil(t, mdOut)
		require.Equal(t, protoreflect.FullName("testpb.Reply"), typeName)

		reply, ok := actual.(*testpb.Reply)
		require.True(t, ok)
		require.Equal(t, orig.Content, reply.Content)
	})

	t.Run("with empty metadata", func(t *testing.T) {
		md := NewMetadata()

		data, err := serializer.MarshalBinaryWithMetadata(orig, md)
		require.NoError(t, err)

		actual, mdOut, typeName, err := serializer.UnmarshalBinaryWithMetadata(data)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.NotNil(t, mdOut)
		require.Equal(t, protoreflect.FullName("testpb.Reply"), typeName)

		reply, ok := actual.(*testpb.Reply)
		require.True(t, ok)
		require.Equal(t, orig.Content, reply.Content)

		// Empty metadata should have no headers.
		_, ok = mdOut.Get("trace-id")
		require.False(t, ok)

		_, ok = mdOut.GetDeadline()
		require.False(t, ok)
	})

	t.Run("with many headers", func(t *testing.T) {
		md := NewMetadata()
		for i := 0; i < 20; i++ {
			md.Set(string(rune('a'+i)), string(rune('A'+i)))
		}

		data, err := serializer.MarshalBinaryWithMetadata(orig, md)
		require.NoError(t, err)

		actual, mdOut, _, err := serializer.UnmarshalBinaryWithMetadata(data)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.NotNil(t, mdOut)

		// Verify all headers round-trip.
		for i := 0; i < 20; i++ {
			val, ok := mdOut.Get(string(rune('a' + i)))
			require.True(t, ok)
			require.Equal(t, string(rune('A'+i)), val)
		}
	})
}

func TestProtoSerializer_MarshalBinaryWithMetadata_Errors(t *testing.T) {
	serializer := NewProtoSerializer()

	t.Run("nil message", func(t *testing.T) {
		md := NewMetadata()
		_, err := serializer.MarshalBinaryWithMetadata(nil, md)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrUnknownMessageType)
	})

	t.Run("nil message and nil metadata", func(t *testing.T) {
		_, err := serializer.MarshalBinaryWithMetadata(nil, nil)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrUnknownMessageType)
	})
}

func TestProtoSerializer_UnmarshalBinaryWithMetadata_InvalidLength(t *testing.T) {
	serializer := NewProtoSerializer()

	t.Run("too short", func(t *testing.T) {
		actual, md, typeName, err := serializer.UnmarshalBinaryWithMetadata([]byte{1, 2, 3})
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidMessageLength)
		require.Nil(t, actual)
		require.Nil(t, md)
		require.Empty(t, typeName)
	})

	t.Run("messageLength > len(data)", func(t *testing.T) {
		data := make([]byte, 12)
		binary.BigEndian.PutUint32(data[:4], 100)
		actual, md, typeName, err := serializer.UnmarshalBinaryWithMetadata(data)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidMessageLength)
		require.Nil(t, actual)
		require.Nil(t, md)
		require.Empty(t, typeName)
	})

	t.Run("nameLen + metaLen overflow", func(t *testing.T) {
		data := make([]byte, 20)
		binary.BigEndian.PutUint32(data[:4], 20)
		binary.BigEndian.PutUint32(data[4:8], 10)  // nameLen
		binary.BigEndian.PutUint32(data[8:12], 10) // metaLen
		// 12 + 10 + 10 = 32 > 20
		actual, md, typeName, err := serializer.UnmarshalBinaryWithMetadata(data)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidMessageLength)
		require.Nil(t, actual)
		require.Nil(t, md)
		require.Empty(t, typeName)
	})
}

func TestProtoSerializer_UnmarshalBinaryWithMetadata_UnknownType(t *testing.T) {
	name := "DoesNotExist"
	totalLen := 4 + 4 + len(name) + 4 + 0 + 0

	data := make([]byte, totalLen)
	binary.BigEndian.PutUint32(data[0:4], uint32(totalLen))
	binary.BigEndian.PutUint32(data[4:8], uint32(len(name)))
	binary.BigEndian.PutUint32(data[8:12], 0) // metaLen
	copy(data[12:], name)

	serializer := NewProtoSerializer()
	actual, md, typeName, err := serializer.UnmarshalBinaryWithMetadata(data)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrUnknownMessageType)
	require.Nil(t, actual)
	require.Nil(t, md)
	require.Empty(t, typeName)
}

func TestProtoSerializer_UnmarshalBinaryWithMetadata_InvalidMetadata(t *testing.T) {
	msgName := string(proto.MessageName(&testpb.Reply{}))
	// Metadata bytes that are too short (claims 5 bytes but provides 3).
	invalidMeta := []byte{0, 1, 2}
	totalLen := 4 + 4 + len(msgName) + 4 + len(invalidMeta)

	data := make([]byte, totalLen)
	binary.BigEndian.PutUint32(data[0:4], uint32(totalLen))
	binary.BigEndian.PutUint32(data[4:8], uint32(len(msgName)))
	binary.BigEndian.PutUint32(data[8:12], uint32(len(invalidMeta)))
	pos := 12
	pos += copy(data[pos:], msgName)
	copy(data[pos:], invalidMeta)

	serializer := NewProtoSerializer()
	actual, md, typeName, err := serializer.UnmarshalBinaryWithMetadata(data)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidMetadata)
	require.Nil(t, actual)
	require.Nil(t, md)
	require.Empty(t, typeName)
}

func TestProtoSerializer_UnmarshalBinaryWithMetadata_InvalidProtoPayload(t *testing.T) {
	msgName := string(proto.MessageName(&testpb.Reply{}))
	md := NewMetadata()
	md.Set("key", "val")
	metaBytes := md.MarshalBinary()
	invalidProto := []byte{0xff, 0xff, 0xff}
	totalLen := 4 + 4 + len(msgName) + 4 + len(metaBytes) + len(invalidProto)

	data := make([]byte, totalLen)
	binary.BigEndian.PutUint32(data[0:4], uint32(totalLen))
	binary.BigEndian.PutUint32(data[4:8], uint32(len(msgName)))
	binary.BigEndian.PutUint32(data[8:12], uint32(len(metaBytes)))
	pos := 12
	pos += copy(data[pos:], msgName)
	pos += copy(data[pos:], metaBytes)
	copy(data[pos:], invalidProto)

	serializer := NewProtoSerializer()
	actual, mdOut, typeName, err := serializer.UnmarshalBinaryWithMetadata(data)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrUnmarshalBinaryFailed)
	require.Nil(t, actual)
	require.Nil(t, mdOut)
	require.Empty(t, typeName)
}

func TestMetadata_MarshalUnmarshal(t *testing.T) {
	t.Run("empty metadata", func(t *testing.T) {
		md := NewMetadata()
		data := md.MarshalBinary()
		require.Len(t, data, 10) // 2 (count) + 8 (deadline)

		md2 := NewMetadata()
		err := md2.UnmarshalBinary(data)
		require.NoError(t, err)
		require.Len(t, md2.headers, 0)

		_, ok := md2.GetDeadline()
		require.False(t, ok)
	})

	t.Run("with headers only", func(t *testing.T) {
		md := NewMetadata()
		md.Set("key1", "value1")
		md.Set("key2", "value2")

		data := md.MarshalBinary()

		md2 := NewMetadata()
		err := md2.UnmarshalBinary(data)
		require.NoError(t, err)

		v1, ok := md2.Get("key1")
		require.True(t, ok)
		require.Equal(t, "value1", v1)

		v2, ok := md2.Get("key2")
		require.True(t, ok)
		require.Equal(t, "value2", v2)
	})

	t.Run("with deadline only", func(t *testing.T) {
		md := NewMetadata()
		deadline := time.Unix(1700000000, 123456789)
		md.SetDeadline(deadline)

		data := md.MarshalBinary()

		md2 := NewMetadata()
		err := md2.UnmarshalBinary(data)
		require.NoError(t, err)

		dl, ok := md2.GetDeadline()
		require.True(t, ok)
		require.Equal(t, deadline, dl)
	})

	t.Run("with headers and deadline", func(t *testing.T) {
		md := NewMetadata()
		md.Set("trace-id", "abc123")
		// Use Unix time to avoid monotonic clock comparison issues.
		deadline := time.Unix(1700000000, 555000000)
		md.SetDeadline(deadline)

		data := md.MarshalBinary()

		md2 := NewMetadata()
		err := md2.UnmarshalBinary(data)
		require.NoError(t, err)

		traceID, ok := md2.Get("trace-id")
		require.True(t, ok)
		require.Equal(t, "abc123", traceID)

		dl, ok := md2.GetDeadline()
		require.True(t, ok)
		require.True(t, deadline.Equal(dl), "deadlines should be equal")
	})
}

func TestMetadata_Unmarshal_Errors(t *testing.T) {
	t.Run("too short", func(t *testing.T) {
		md := NewMetadata()
		err := md.UnmarshalBinary([]byte{1, 2, 3})
		require.ErrorIs(t, err, ErrInvalidMetadata)
	})

	t.Run("truncated key", func(t *testing.T) {
		// count=1, keyLen=10, but only 2 bytes of key data
		data := make([]byte, 6)
		binary.BigEndian.PutUint16(data[0:2], 1)  // count=1
		binary.BigEndian.PutUint16(data[2:4], 10) // keyLen=10
		data[4] = 'a'
		data[5] = 'b'

		md := NewMetadata()
		err := md.UnmarshalBinary(data)
		require.ErrorIs(t, err, ErrInvalidMetadata)
	})

	t.Run("truncated value", func(t *testing.T) {
		// count=1, keyLen=2, key="ab", valLen=10, but only 2 bytes of value
		data := make([]byte, 10)
		binary.BigEndian.PutUint16(data[0:2], 1) // count=1
		binary.BigEndian.PutUint16(data[2:4], 2) // keyLen=2
		data[4] = 'a'
		data[5] = 'b'
		binary.BigEndian.PutUint16(data[6:8], 10) // valLen=10
		data[8] = 'x'
		data[9] = 'y'

		md := NewMetadata()
		err := md.UnmarshalBinary(data)
		require.ErrorIs(t, err, ErrInvalidMetadata)
	})

	t.Run("missing deadline", func(t *testing.T) {
		// count=0, but no 8 bytes for deadline
		data := make([]byte, 2)
		binary.BigEndian.PutUint16(data[0:2], 0) // count=0

		md := NewMetadata()
		err := md.UnmarshalBinary(data)
		require.ErrorIs(t, err, ErrInvalidMetadata)
	})
}
