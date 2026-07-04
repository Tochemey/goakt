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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/tochemey/goakt/v4/test/data/testpb"
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

func TestFindMessageType_NegativeCache(t *testing.T) {
	t.Run("registry miss is cached and keeps failing", func(t *testing.T) {
		name := protoreflect.FullName("goakt.test.DoesNotExist")
		t.Cleanup(func() { protoMessageTypes.Delete(name) })

		_, err := FindMessageType(name)
		require.ErrorIs(t, err, protoregistry.NotFound)

		cached, ok := protoMessageTypes.Load(name)
		require.True(t, ok, "the miss should be cached")

		_, miss := cached.(typeNotFound)
		require.True(t, miss, "the cached entry should be a miss marker")

		_, err = FindMessageType(name)
		require.ErrorIs(t, err, protoregistry.NotFound)
	})

	t.Run("cap flush drops miss markers but keeps resolved types", func(t *testing.T) {
		resolvedName := protoreflect.FullName("testpb.Reply")
		missName := protoreflect.FullName("goakt.test.FlushMe")
		newMissName := protoreflect.FullName("goakt.test.AnotherMiss")

		t.Cleanup(func() {
			protoMessageTypes.Delete(missName)
			protoMessageTypes.Delete(newMissName)
			negativeTypeCount.Store(0)
		})

		resolved, err := FindMessageType(resolvedName)
		require.NoError(t, err)
		require.NotNil(t, resolved)

		_, err = FindMessageType(missName)
		require.ErrorIs(t, err, protoregistry.NotFound)

		// Force the next miss to trigger the wholesale flush.
		negativeTypeCount.Store(negativeTypeCacheCap)

		_, err = FindMessageType(newMissName)
		require.ErrorIs(t, err, protoregistry.NotFound)

		_, ok := protoMessageTypes.Load(missName)
		require.False(t, ok, "old miss markers should be flushed")

		_, ok = protoMessageTypes.Load(resolvedName)
		require.True(t, ok, "resolved types must survive the flush")
	})

	t.Run("flush is single-flight", func(t *testing.T) {
		missName := protoreflect.FullName("goakt.test.SingleFlight")

		t.Cleanup(func() {
			protoMessageTypes.Delete(missName)
			typeCacheFlushing.Store(false)
			negativeTypeCount.Store(0)
		})

		_, err := FindMessageType(missName)
		require.ErrorIs(t, err, protoregistry.NotFound)

		// While another goroutine holds the flush, a losing caller must
		// return immediately without scanning: the miss marker survives.
		typeCacheFlushing.Store(true)
		cleanTypeCache()

		_, ok := protoMessageTypes.Load(missName)
		require.True(t, ok, "a losing flusher must not scan the cache")

		// Once the winner releases the gate, the next flush proceeds.
		typeCacheFlushing.Store(false)
		cleanTypeCache()

		_, ok = protoMessageTypes.Load(missName)
		require.False(t, ok, "the winning flusher must drop miss markers")
		require.Zero(t, negativeTypeCount.Load())
	})

	t.Run("concurrent misses past the cap stay bounded", func(t *testing.T) {
		t.Cleanup(func() {
			cleanTypeCache()
			negativeTypeCount.Store(0)
		})

		// Park the count at the cap so every miss below races into the
		// flush path; the race detector guards the single-flight gate.
		negativeTypeCount.Store(negativeTypeCacheCap)

		const workers = 8
		const missesPerWorker = 64

		var wg sync.WaitGroup
		wg.Add(workers)

		for w := range workers {
			go func(w int) {
				defer wg.Done()

				for i := range missesPerWorker {
					name := protoreflect.FullName(fmt.Sprintf("goakt.test.Concurrent%d_%d", w, i))
					_, err := FindMessageType(name)
					assert.ErrorIs(t, err, protoregistry.NotFound)
				}
			}(w)
		}

		wg.Wait()
		require.LessOrEqual(t, negativeTypeCount.Load(), int64(negativeTypeCacheCap))
	})
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
		expectedDeadline := time.Now().Add(time.Minute)
		md.SetDeadline(expectedDeadline)

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
		require.WithinDuration(t, expectedDeadline, deadline, time.Second)
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
		for i := range 20 {
			md.Set(string(rune('a'+i)), string(rune('A'+i)))
		}

		data, err := serializer.MarshalBinaryWithMetadata(orig, md)
		require.NoError(t, err)

		actual, mdOut, _, err := serializer.UnmarshalBinaryWithMetadata(data)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.NotNil(t, mdOut)

		// Verify all headers round-trip.
		for i := range 20 {
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
		deadline := time.Now().Add(time.Minute)
		md.SetDeadline(deadline)

		data := md.MarshalBinary()

		md2 := NewMetadata()
		err := md2.UnmarshalBinary(data)
		require.NoError(t, err)

		dl, ok := md2.GetDeadline()
		require.True(t, ok)
		require.WithinDuration(t, deadline, dl, time.Second)
	})

	t.Run("with headers and deadline", func(t *testing.T) {
		md := NewMetadata()
		md.Set("trace-id", "abc123")
		deadline := time.Now().Add(time.Minute)
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
		require.WithinDuration(t, deadline, dl, time.Second)
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
