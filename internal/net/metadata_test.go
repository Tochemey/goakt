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
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewMetadata(t *testing.T) {
	md := NewMetadata()
	require.NotNil(t, md)
	require.NotNil(t, md.headers)
	require.Equal(t, 0, len(md.headers))
	require.Equal(t, int64(0), md.deadlineNano)

	// Verify we can use it immediately.
	md.Set("test", "value")
	v, ok := md.Get("test")
	require.True(t, ok)
	require.Equal(t, "value", v)
}

func TestMetadata_SetGet(t *testing.T) {
	md := NewMetadata()

	t.Run("set and get single header", func(t *testing.T) {
		md.Set("key1", "value1")
		v, ok := md.Get("key1")
		require.True(t, ok)
		require.Equal(t, "value1", v)
	})

	t.Run("get non-existent header", func(t *testing.T) {
		_, ok := md.Get("nonexistent")
		require.False(t, ok)
	})

	t.Run("overwrite existing header", func(t *testing.T) {
		md.Set("key1", "new-value")
		v, ok := md.Get("key1")
		require.True(t, ok)
		require.Equal(t, "new-value", v)
	})

	t.Run("multiple headers", func(t *testing.T) {
		md.Set("key2", "value2")
		md.Set("key3", "value3")

		v2, ok := md.Get("key2")
		require.True(t, ok)
		require.Equal(t, "value2", v2)

		v3, ok := md.Get("key3")
		require.True(t, ok)
		require.Equal(t, "value3", v3)
	})

	t.Run("empty key and value", func(t *testing.T) {
		md.Set("", "")
		v, ok := md.Get("")
		require.True(t, ok)
		require.Equal(t, "", v)
	})

	t.Run("large values", func(t *testing.T) {
		largeValue := string(make([]byte, 10000))
		md.Set("large", largeValue)
		v, ok := md.Get("large")
		require.True(t, ok)
		require.Equal(t, largeValue, v)
	})
}

func TestMetadata_SetGetDeadline(t *testing.T) {
	md := NewMetadata()

	t.Run("no deadline set initially", func(t *testing.T) {
		_, ok := md.GetDeadline()
		require.False(t, ok)
	})

	t.Run("set and get deadline", func(t *testing.T) {
		deadline := time.Unix(1700000000, 123456789)
		md.SetDeadline(deadline)

		dl, ok := md.GetDeadline()
		require.True(t, ok)
		require.True(t, deadline.Equal(dl))
	})

	t.Run("overwrite deadline", func(t *testing.T) {
		newDeadline := time.Unix(1800000000, 987654321)
		md.SetDeadline(newDeadline)

		dl, ok := md.GetDeadline()
		require.True(t, ok)
		require.True(t, newDeadline.Equal(dl))
	})

	t.Run("set zero deadline", func(t *testing.T) {
		md.SetDeadline(time.Time{})
		_, ok := md.GetDeadline()
		require.False(t, ok)
		require.Equal(t, int64(0), md.deadlineNano)
	})

	t.Run("deadline round-trip precision", func(t *testing.T) {
		// Test that we preserve nanosecond precision.
		deadline := time.Unix(1234567890, 123456789)
		md.SetDeadline(deadline)

		dl, ok := md.GetDeadline()
		require.True(t, ok)
		require.Equal(t, deadline.UnixNano(), dl.UnixNano())
	})

	t.Run("very far future deadline", func(t *testing.T) {
		// Test a deadline far in the future.
		deadline := time.Unix(2000000000, 999999999)
		md.SetDeadline(deadline)

		dl, ok := md.GetDeadline()
		require.True(t, ok)
		require.True(t, deadline.Equal(dl))
	})
}

func TestMetadata_Marshal(t *testing.T) {
	t.Run("empty metadata", func(t *testing.T) {
		md := NewMetadata()
		data := md.MarshalBinary()

		// Should be exactly 10 bytes: 2 (count) + 8 (deadline).
		require.Len(t, data, 10)

		// Verify structure.
		count := binary.BigEndian.Uint16(data[0:2])
		require.Equal(t, uint16(0), count)

		deadlineNano := int64(binary.BigEndian.Uint64(data[2:10]))
		require.Equal(t, int64(0), deadlineNano)
	})

	t.Run("single header", func(t *testing.T) {
		md := NewMetadata()
		md.Set("key", "value")
		data := md.MarshalBinary()

		// 2 (count) + 2 (keyLen) + 3 (key) + 2 (valLen) + 5 (value) + 8 (deadline) = 22.
		expectedLen := 2 + 2 + 3 + 2 + 5 + 8
		require.Len(t, data, expectedLen)

		count := binary.BigEndian.Uint16(data[0:2])
		require.Equal(t, uint16(1), count)
	})

	t.Run("multiple headers", func(t *testing.T) {
		md := NewMetadata()
		md.Set("k1", "v1")
		md.Set("k2", "v2")
		md.Set("k3", "v3")
		data := md.MarshalBinary()

		count := binary.BigEndian.Uint16(data[0:2])
		require.Equal(t, uint16(3), count)
	})

	t.Run("with deadline", func(t *testing.T) {
		md := NewMetadata()
		deadline := time.Unix(1700000000, 123456789)
		md.SetDeadline(deadline)
		data := md.MarshalBinary()

		require.GreaterOrEqual(t, len(data), 10)

		// Extract deadline from end of data.
		deadlineNano := int64(binary.BigEndian.Uint64(data[len(data)-8:]))
		require.Equal(t, deadline.UnixNano(), deadlineNano)
	})

	t.Run("large number of headers", func(t *testing.T) {
		md := NewMetadata()
		for i := 0; i < 100; i++ {
			md.Set("key"+string(rune('0'+i%10)), "value")
		}
		data := md.MarshalBinary()

		// Should successfully marshal without error.
		require.NotNil(t, data)
		require.Greater(t, len(data), 10)
	})

	t.Run("unicode headers", func(t *testing.T) {
		md := NewMetadata()
		md.Set("キー", "値")
		md.Set("🔑", "🎁")
		data := md.MarshalBinary()

		require.NotNil(t, data)
		require.Greater(t, len(data), 10)
	})
}

func TestMetadata_Unmarshal(t *testing.T) {
	t.Run("empty metadata round-trip", func(t *testing.T) {
		md1 := NewMetadata()
		data := md1.MarshalBinary()

		md2 := NewMetadata()
		err := md2.UnmarshalBinary(data)
		require.NoError(t, err)
		require.Equal(t, 0, len(md2.headers))
		require.Equal(t, int64(0), md2.deadlineNano)
	})

	t.Run("headers round-trip", func(t *testing.T) {
		md1 := NewMetadata()
		md1.Set("key1", "value1")
		md1.Set("key2", "value2")
		md1.Set("key3", "value3")
		data := md1.MarshalBinary()

		md2 := NewMetadata()
		err := md2.UnmarshalBinary(data)
		require.NoError(t, err)

		v1, ok := md2.Get("key1")
		require.True(t, ok)
		require.Equal(t, "value1", v1)

		v2, ok := md2.Get("key2")
		require.True(t, ok)
		require.Equal(t, "value2", v2)

		v3, ok := md2.Get("key3")
		require.True(t, ok)
		require.Equal(t, "value3", v3)
	})

	t.Run("deadline round-trip", func(t *testing.T) {
		md1 := NewMetadata()
		deadline := time.Unix(1700000000, 555000000)
		md1.SetDeadline(deadline)
		data := md1.MarshalBinary()

		md2 := NewMetadata()
		err := md2.UnmarshalBinary(data)
		require.NoError(t, err)

		dl, ok := md2.GetDeadline()
		require.True(t, ok)
		require.Equal(t, deadline.UnixNano(), dl.UnixNano())
	})

	t.Run("full round-trip", func(t *testing.T) {
		md1 := NewMetadata()
		md1.Set("trace-id", "abc123")
		md1.Set("span-id", "xyz789")
		deadline := time.Unix(1800000000, 999999999)
		md1.SetDeadline(deadline)
		data := md1.MarshalBinary()

		md2 := NewMetadata()
		err := md2.UnmarshalBinary(data)
		require.NoError(t, err)

		traceID, ok := md2.Get("trace-id")
		require.True(t, ok)
		require.Equal(t, "abc123", traceID)

		spanID, ok := md2.Get("span-id")
		require.True(t, ok)
		require.Equal(t, "xyz789", spanID)

		dl, ok := md2.GetDeadline()
		require.True(t, ok)
		require.Equal(t, deadline.UnixNano(), dl.UnixNano())
	})

	t.Run("zero-copy string references", func(t *testing.T) {
		md1 := NewMetadata()
		md1.Set("key", "value")
		data := md1.MarshalBinary()

		md2 := NewMetadata()
		err := md2.UnmarshalBinary(data)
		require.NoError(t, err)

		// Verify the string is usable (zero-copy implementation detail).
		v, ok := md2.Get("key")
		require.True(t, ok)
		require.Equal(t, "value", v)
	})

	t.Run("map pre-sizing", func(t *testing.T) {
		// Create metadata with many headers to verify map pre-sizing works.
		md1 := NewMetadata()
		for i := 0; i < 50; i++ {
			md1.Set("key"+string(rune('a'+i%26)), "value")
		}
		data := md1.MarshalBinary()

		md2 := NewMetadata()
		err := md2.UnmarshalBinary(data)
		require.NoError(t, err)
		require.Greater(t, len(md2.headers), 0)
	})
}

func TestMetadata_UnmarshalBinary_Errors(t *testing.T) {
	t.Run("nil data", func(t *testing.T) {
		md := NewMetadata()
		err := md.UnmarshalBinary(nil)
		require.ErrorIs(t, err, ErrInvalidMetadata)
	})

	t.Run("too short - less than 10 bytes", func(t *testing.T) {
		md := NewMetadata()
		err := md.UnmarshalBinary([]byte{1, 2, 3, 4, 5})
		require.ErrorIs(t, err, ErrInvalidMetadata)
	})

	t.Run("exactly 9 bytes", func(t *testing.T) {
		md := NewMetadata()
		err := md.UnmarshalBinary(make([]byte, 9))
		require.ErrorIs(t, err, ErrInvalidMetadata)
	})

	t.Run("truncated after count", func(t *testing.T) {
		// count=1 but no key length field.
		data := make([]byte, 2)
		binary.BigEndian.PutUint16(data[0:2], 1)

		md := NewMetadata()
		err := md.UnmarshalBinary(data)
		require.ErrorIs(t, err, ErrInvalidMetadata)
	})

	t.Run("truncated key length", func(t *testing.T) {
		// count=1, only 1 byte of keyLen field.
		data := make([]byte, 3)
		binary.BigEndian.PutUint16(data[0:2], 1)

		md := NewMetadata()
		err := md.UnmarshalBinary(data)
		require.ErrorIs(t, err, ErrInvalidMetadata)
	})

	t.Run("truncated key data", func(t *testing.T) {
		// count=1, keyLen=10, but only 2 bytes of key.
		data := make([]byte, 6)
		binary.BigEndian.PutUint16(data[0:2], 1)  // count
		binary.BigEndian.PutUint16(data[2:4], 10) // keyLen
		data[4] = 'a'
		data[5] = 'b'

		md := NewMetadata()
		err := md.UnmarshalBinary(data)
		require.ErrorIs(t, err, ErrInvalidMetadata)
	})

	t.Run("truncated value length", func(t *testing.T) {
		// count=1, keyLen=2, key="ab", but no valLen field.
		data := make([]byte, 6)
		binary.BigEndian.PutUint16(data[0:2], 1) // count
		binary.BigEndian.PutUint16(data[2:4], 2) // keyLen
		data[4] = 'a'
		data[5] = 'b'

		md := NewMetadata()
		err := md.UnmarshalBinary(data)
		require.ErrorIs(t, err, ErrInvalidMetadata)
	})

	t.Run("truncated value data", func(t *testing.T) {
		// count=1, keyLen=2, key="ab", valLen=10, but only 2 bytes of value.
		data := make([]byte, 10)
		binary.BigEndian.PutUint16(data[0:2], 1) // count
		binary.BigEndian.PutUint16(data[2:4], 2) // keyLen
		data[4] = 'a'
		data[5] = 'b'
		binary.BigEndian.PutUint16(data[6:8], 10) // valLen
		data[8] = 'x'
		data[9] = 'y'

		md := NewMetadata()
		err := md.UnmarshalBinary(data)
		require.ErrorIs(t, err, ErrInvalidMetadata)
	})

	t.Run("missing deadline field", func(t *testing.T) {
		// count=1, keyLen=2, key="ab", valLen=2, val="cd", but no deadline.
		data := make([]byte, 10)
		binary.BigEndian.PutUint16(data[0:2], 1) // count
		binary.BigEndian.PutUint16(data[2:4], 2) // keyLen
		data[4] = 'a'
		data[5] = 'b'
		binary.BigEndian.PutUint16(data[6:8], 2) // valLen
		data[8] = 'c'
		data[9] = 'd'
		// Missing 8 bytes for deadline.

		md := NewMetadata()
		err := md.UnmarshalBinary(data)
		require.ErrorIs(t, err, ErrInvalidMetadata)
	})

	t.Run("partial deadline", func(t *testing.T) {
		// Valid headers but only 4 bytes of deadline instead of 8.
		data := make([]byte, 14)
		binary.BigEndian.PutUint16(data[0:2], 1) // count
		binary.BigEndian.PutUint16(data[2:4], 1) // keyLen
		data[4] = 'k'
		binary.BigEndian.PutUint16(data[5:7], 1) // valLen
		data[7] = 'v'
		// Only 6 more bytes, but need 8 for deadline.

		md := NewMetadata()
		err := md.UnmarshalBinary(data)
		require.ErrorIs(t, err, ErrInvalidMetadata)
	})
}

func TestMetadata_ToContext(t *testing.T) {
	t.Run("empty metadata to context", func(t *testing.T) {
		md := NewMetadata()
		ctx := md.ToContext(context.Background())
		require.NotNil(t, ctx)

		// Verify metadata is attached.
		extracted, ok := FromContext(ctx)
		require.True(t, ok)
		require.NotNil(t, extracted)
		require.Equal(t, 0, len(extracted.headers))
	})

	t.Run("metadata with headers to context", func(t *testing.T) {
		md := NewMetadata()
		md.Set("key1", "value1")
		md.Set("key2", "value2")

		ctx := md.ToContext(context.Background())

		extracted, ok := FromContext(ctx)
		require.True(t, ok)

		v1, ok := extracted.Get("key1")
		require.True(t, ok)
		require.Equal(t, "value1", v1)

		v2, ok := extracted.Get("key2")
		require.True(t, ok)
		require.Equal(t, "value2", v2)
	})

	t.Run("metadata with deadline to context", func(t *testing.T) {
		md := NewMetadata()
		deadline := time.Now().Add(5 * time.Second)
		md.SetDeadline(deadline)

		ctx := md.ToContext(context.Background())

		// Verify context has deadline.
		ctxDeadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.True(t, deadline.Equal(ctxDeadline))

		// Verify metadata is attached.
		extracted, ok := FromContext(ctx)
		require.True(t, ok)
		require.NotNil(t, extracted)
	})

	t.Run("metadata without deadline to context", func(t *testing.T) {
		md := NewMetadata()
		md.Set("key", "value")

		ctx := md.ToContext(context.Background())

		// Verify context has no deadline.
		_, ok := ctx.Deadline()
		require.False(t, ok)

		// Verify metadata is still attached.
		extracted, ok := FromContext(ctx)
		require.True(t, ok)
		require.NotNil(t, extracted)
	})

	t.Run("parent context with existing deadline", func(t *testing.T) {
		parentDeadline := time.Now().Add(10 * time.Second)
		parentCtx, cancel := context.WithDeadline(context.Background(), parentDeadline)
		defer cancel()

		md := NewMetadata()
		mdDeadline := time.Now().Add(2 * time.Second)
		md.SetDeadline(mdDeadline)

		ctx := md.ToContext(parentCtx)

		// The metadata deadline should take precedence (it's sooner).
		ctxDeadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.True(t, mdDeadline.Equal(ctxDeadline))
	})

	t.Run("parent context with values", func(t *testing.T) {
		type keyType string
		parentCtx := context.WithValue(context.Background(), keyType("parent"), "value")

		md := NewMetadata()
		md.Set("child", "metadata")

		ctx := md.ToContext(parentCtx)

		// Verify parent value is preserved.
		parentValue := ctx.Value(keyType("parent"))
		require.Equal(t, "value", parentValue)

		// Verify metadata is attached.
		extracted, ok := FromContext(ctx)
		require.True(t, ok)
		v, ok := extracted.Get("child")
		require.True(t, ok)
		require.Equal(t, "metadata", v)
	})
}

func TestFromContext(t *testing.T) {
	t.Run("no metadata in context", func(t *testing.T) {
		ctx := context.Background()
		_, ok := FromContext(ctx)
		require.False(t, ok)
	})

	t.Run("extract attached metadata", func(t *testing.T) {
		md := NewMetadata()
		md.Set("key", "value")

		ctx := md.ToContext(context.Background())

		extracted, ok := FromContext(ctx)
		require.True(t, ok)
		require.NotNil(t, extracted)

		v, ok := extracted.Get("key")
		require.True(t, ok)
		require.Equal(t, "value", v)
	})

	t.Run("nil metadata in context", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), metadataKey{}, (*Metadata)(nil))
		extracted, ok := FromContext(ctx)
		// Type assertion succeeds even for nil pointer, but value is nil.
		require.True(t, ok)
		require.Nil(t, extracted)
	})

	t.Run("wrong type in context", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), metadataKey{}, "not metadata")
		_, ok := FromContext(ctx)
		require.False(t, ok)
	})
}

func TestContextWithMetadata(t *testing.T) {
	t.Run("attach metadata to context", func(t *testing.T) {
		md := NewMetadata()
		md.Set("key", "value")

		ctx := ContextWithMetadata(context.Background(), md)

		extracted, ok := FromContext(ctx)
		require.True(t, ok)
		require.Equal(t, md, extracted)
	})

	t.Run("attach nil metadata", func(t *testing.T) {
		ctx := ContextWithMetadata(context.Background(), nil)

		extracted, ok := FromContext(ctx)
		// Type assertion succeeds even for nil pointer.
		require.True(t, ok)
		require.Nil(t, extracted)
	})

	t.Run("overwrite existing metadata", func(t *testing.T) {
		md1 := NewMetadata()
		md1.Set("key1", "value1")

		ctx1 := ContextWithMetadata(context.Background(), md1)

		md2 := NewMetadata()
		md2.Set("key2", "value2")

		ctx2 := ContextWithMetadata(ctx1, md2)

		// Should get the new metadata.
		extracted, ok := FromContext(ctx2)
		require.True(t, ok)

		_, ok = extracted.Get("key1")
		require.False(t, ok)

		v2, ok := extracted.Get("key2")
		require.True(t, ok)
		require.Equal(t, "value2", v2)
	})

	t.Run("preserve parent context values", func(t *testing.T) {
		type keyType string
		parentCtx := context.WithValue(context.Background(), keyType("parent"), "value")

		md := NewMetadata()
		md.Set("child", "metadata")

		ctx := ContextWithMetadata(parentCtx, md)

		// Verify parent value is preserved.
		parentValue := ctx.Value(keyType("parent"))
		require.Equal(t, "value", parentValue)

		// Verify metadata is attached.
		extracted, ok := FromContext(ctx)
		require.True(t, ok)
		v, ok := extracted.Get("child")
		require.True(t, ok)
		require.Equal(t, "metadata", v)
	})
}

func TestMetadata_EdgeCases(t *testing.T) {
	t.Run("very long header name", func(t *testing.T) {
		md := NewMetadata()
		longKey := string(make([]byte, 1000))
		md.Set(longKey, "value")

		v, ok := md.Get(longKey)
		require.True(t, ok)
		require.Equal(t, "value", v)

		// Verify it round-trips.
		data := md.MarshalBinary()
		md2 := NewMetadata()
		err := md2.UnmarshalBinary(data)
		require.NoError(t, err)

		v2, ok := md2.Get(longKey)
		require.True(t, ok)
		require.Equal(t, "value", v2)
	})

	t.Run("special characters in headers", func(t *testing.T) {
		md := NewMetadata()
		md.Set("key\x00\x01\x02", "value\n\r\t")

		data := md.MarshalBinary()
		md2 := NewMetadata()
		err := md2.UnmarshalBinary(data)
		require.NoError(t, err)

		v, ok := md2.Get("key\x00\x01\x02")
		require.True(t, ok)
		require.Equal(t, "value\n\r\t", v)
	})

	t.Run("deadline with zero nanoseconds", func(t *testing.T) {
		md := NewMetadata()
		deadline := time.Unix(1700000000, 0)
		md.SetDeadline(deadline)

		data := md.MarshalBinary()
		md2 := NewMetadata()
		err := md2.UnmarshalBinary(data)
		require.NoError(t, err)

		dl, ok := md2.GetDeadline()
		require.True(t, ok)
		require.Equal(t, deadline.UnixNano(), dl.UnixNano())
	})

	t.Run("reuse metadata instance", func(t *testing.T) {
		md := NewMetadata()
		md.Set("key1", "value1")
		md.SetDeadline(time.Unix(1700000000, 0))

		data1 := md.MarshalBinary()

		// Reuse the same instance.
		md.Set("key2", "value2")
		md.SetDeadline(time.Unix(1800000000, 0))

		data2 := md.MarshalBinary()

		// Verify they're different.
		require.NotEqual(t, data1, data2)

		// Verify both can be unmarshaled correctly.
		md1 := NewMetadata()
		err := md1.UnmarshalBinary(data1)
		require.NoError(t, err)
		_, ok := md1.Get("key1")
		require.True(t, ok)

		md2 := NewMetadata()
		err = md2.UnmarshalBinary(data2)
		require.NoError(t, err)
		_, ok = md2.Get("key2")
		require.True(t, ok)
	})
}
