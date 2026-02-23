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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test types for CBOR round-trip. Defined in test file to avoid polluting
// production types. Names are remote.cborTestMsg, remote.cborTestNested, etc.

type cborTestMsg struct {
	ID    int    `cbor:"id"`
	Label string `cbor:"label"`
}

type cborTestNested struct {
	Inner cborTestMsg `cbor:"inner"`
	Flag  bool        `cbor:"flag"`
}

type cborTestWithSlice struct {
	Names []string `cbor:"names"`
}

type cborTestWithMap struct {
	Meta map[string]string `cbor:"meta"`
}

type cborTestWithTime struct {
	At time.Time `cbor:"at"`
}

// cborTestUnmarshalable has a channel field; CBOR cannot encode it, so Marshal fails.
type cborTestUnmarshalable struct {
	Ch chan int `cbor:"ch"`
}

func TestNewCBORSerializer(t *testing.T) {
	s := NewCBORSerializer()
	require.NotNil(t, s)
	var _ Serializer = s
}

func TestCBORSerializer_SerializeDeserialize_RoundTrip(t *testing.T) {
	RegisterSerializableTypes(new(cborTestMsg))
	t.Cleanup(func() { typesRegistry.Deregister(new(cborTestMsg)) })

	serializer := NewCBORSerializer()
	orig := &cborTestMsg{ID: 42, Label: "hello"}

	data, err := serializer.Serialize(orig)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	actual, err := serializer.Deserialize(data)
	require.NoError(t, err)
	require.NotNil(t, actual)

	decoded, ok := actual.(*cborTestMsg)
	require.True(t, ok)
	require.Equal(t, orig.ID, decoded.ID)
	require.Equal(t, orig.Label, decoded.Label)
}

func TestCBORSerializer_SerializeDeserialize_ValueType(t *testing.T) {
	RegisterSerializableTypes(new(cborTestMsg))
	t.Cleanup(func() { typesRegistry.Deregister(new(cborTestMsg)) })

	serializer := NewCBORSerializer()
	orig := cborTestMsg{ID: 1, Label: "value"}

	data, err := serializer.Serialize(orig)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	actual, err := serializer.Deserialize(data)
	require.NoError(t, err)
	require.NotNil(t, actual)

	decoded, ok := actual.(*cborTestMsg)
	require.True(t, ok)
	require.Equal(t, orig.ID, decoded.ID)
	require.Equal(t, orig.Label, decoded.Label)
}

func TestCBORSerializer_SerializeDeserialize_NestedStruct(t *testing.T) {
	RegisterSerializableTypes(new(cborTestNested))
	t.Cleanup(func() { typesRegistry.Deregister(new(cborTestNested)) })

	serializer := NewCBORSerializer()
	orig := &cborTestNested{
		Inner: cborTestMsg{ID: 10, Label: "nested"},
		Flag:  true,
	}

	data, err := serializer.Serialize(orig)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	actual, err := serializer.Deserialize(data)
	require.NoError(t, err)
	require.NotNil(t, actual)

	decoded, ok := actual.(*cborTestNested)
	require.True(t, ok)
	require.Equal(t, orig.Inner.ID, decoded.Inner.ID)
	require.Equal(t, orig.Inner.Label, decoded.Inner.Label)
	require.Equal(t, orig.Flag, decoded.Flag)
}

func TestCBORSerializer_SerializeDeserialize_Slice(t *testing.T) {
	RegisterSerializableTypes(new(cborTestWithSlice))
	t.Cleanup(func() { typesRegistry.Deregister(new(cborTestWithSlice)) })

	serializer := NewCBORSerializer()
	orig := &cborTestWithSlice{Names: []string{"a", "b", "c"}}

	data, err := serializer.Serialize(orig)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	actual, err := serializer.Deserialize(data)
	require.NoError(t, err)
	require.NotNil(t, actual)

	decoded, ok := actual.(*cborTestWithSlice)
	require.True(t, ok)
	require.Equal(t, orig.Names, decoded.Names)
}

func TestCBORSerializer_SerializeDeserialize_Map(t *testing.T) {
	RegisterSerializableTypes(new(cborTestWithMap))
	t.Cleanup(func() { typesRegistry.Deregister(new(cborTestWithMap)) })

	serializer := NewCBORSerializer()
	orig := &cborTestWithMap{Meta: map[string]string{"k1": "v1", "k2": "v2"}}

	data, err := serializer.Serialize(orig)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	actual, err := serializer.Deserialize(data)
	require.NoError(t, err)
	require.NotNil(t, actual)

	decoded, ok := actual.(*cborTestWithMap)
	require.True(t, ok)
	require.Equal(t, orig.Meta, decoded.Meta)
}

func TestCBORSerializer_SerializeDeserialize_Time(t *testing.T) {
	RegisterSerializableTypes(new(cborTestWithTime))
	t.Cleanup(func() { typesRegistry.Deregister(new(cborTestWithTime)) })

	serializer := NewCBORSerializer()
	ts := time.Date(2025, 2, 22, 12, 0, 0, 0, time.UTC)
	orig := &cborTestWithTime{At: ts}

	data, err := serializer.Serialize(orig)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	actual, err := serializer.Deserialize(data)
	require.NoError(t, err)
	require.NotNil(t, actual)

	decoded, ok := actual.(*cborTestWithTime)
	require.True(t, ok)
	require.True(t, ts.Equal(decoded.At))
}

func TestCBORSerializer_Serialize_Errors(t *testing.T) {
	serializer := NewCBORSerializer()

	t.Run("nil message", func(t *testing.T) {
		_, err := serializer.Serialize(nil)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrCBORNilMessage)
	})

	t.Run("unregistered type", func(t *testing.T) {
		type unregistered struct{ X int }
		_, err := serializer.Serialize(&unregistered{X: 1})
		require.Error(t, err)
		require.Contains(t, err.Error(), "not registered")
	})

	t.Run("registered type but CBOR marshal fails", func(t *testing.T) {
		RegisterSerializableTypes(new(cborTestUnmarshalable))
		t.Cleanup(func() { typesRegistry.Deregister(new(cborTestUnmarshalable)) })

		_, err := serializer.Serialize(&cborTestUnmarshalable{Ch: make(chan int)})
		require.Error(t, err)
		require.ErrorIs(t, err, ErrCBORSerializeFailed)
	})
}

func TestCBORSerializer_Deserialize_InvalidFrame(t *testing.T) {
	serializer := NewCBORSerializer()

	t.Run("too short", func(t *testing.T) {
		actual, err := serializer.Deserialize([]byte{1, 2, 3})
		require.Error(t, err)
		require.ErrorIs(t, err, ErrCBORInvalidFrame)
		require.Nil(t, actual)
	})

	t.Run("empty", func(t *testing.T) {
		actual, err := serializer.Deserialize(nil)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrCBORInvalidFrame)
		require.Nil(t, actual)
	})

	t.Run("totalLen greater than data length", func(t *testing.T) {
		data := make([]byte, 8)
		binary.BigEndian.PutUint32(data[:4], 100)
		actual, err := serializer.Deserialize(data)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrCBORInvalidFrame)
		require.Nil(t, actual)
	})

	t.Run("totalLen less than 8", func(t *testing.T) {
		data := make([]byte, 8)
		binary.BigEndian.PutUint32(data[:4], 4)
		actual, err := serializer.Deserialize(data)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrCBORInvalidFrame)
		require.Nil(t, actual)
	})

	t.Run("nameLen overflows totalLen", func(t *testing.T) {
		data := make([]byte, 12)
		binary.BigEndian.PutUint32(data[:4], 12)
		binary.BigEndian.PutUint32(data[4:8], 10) // 8 + 10 > 12
		actual, err := serializer.Deserialize(data)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrCBORInvalidFrame)
		require.Nil(t, actual)
	})
}

func TestCBORSerializer_Deserialize_UnknownType(t *testing.T) {
	name := "unknown.pkg.typ"
	totalLen := 4 + 4 + len(name)

	data := make([]byte, totalLen)
	binary.BigEndian.PutUint32(data[0:4], uint32(totalLen))
	binary.BigEndian.PutUint32(data[4:8], uint32(len(name)))
	copy(data[8:], name)

	serializer := NewCBORSerializer()
	actual, err := serializer.Deserialize(data)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not registered")
	require.Nil(t, actual)
}

func TestCBORSerializer_Deserialize_InvalidCBORPayload(t *testing.T) {
	RegisterSerializableTypes(new(cborTestMsg))
	t.Cleanup(func() { typesRegistry.Deregister(new(cborTestMsg)) })

	// Valid frame header, garbage CBOR payload.
	name := "remote.cbortestmsg"
	payload := []byte{0xff, 0xfe, 0xfd}
	totalLen := 4 + 4 + len(name) + len(payload)

	data := make([]byte, totalLen)
	binary.BigEndian.PutUint32(data[0:4], uint32(totalLen))
	binary.BigEndian.PutUint32(data[4:8], uint32(len(name)))
	copy(data[8:], name)
	copy(data[8+len(name):], payload)

	serializer := NewCBORSerializer()
	actual, err := serializer.Deserialize(data)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrCBORDeserializeFailed)
	require.Nil(t, actual)
}

func TestRegisterSerializableTypes(t *testing.T) {
	type localType struct{ V int }

	RegisterSerializableTypes(new(localType))
	t.Cleanup(func() { typesRegistry.Deregister(new(localType)) })

	require.True(t, typesRegistry.Exists(new(localType)))

	serializer := NewCBORSerializer()
	data, err := serializer.Serialize(&localType{V: 99})
	require.NoError(t, err)
	require.NotEmpty(t, data)

	actual, err := serializer.Deserialize(data)
	require.NoError(t, err)
	decoded, ok := actual.(*localType)
	require.True(t, ok)
	require.Equal(t, 99, decoded.V)
}

func TestRegisterSerializableTypes_Idempotent(t *testing.T) {
	RegisterSerializableTypes(new(cborTestMsg))
	RegisterSerializableTypes(new(cborTestMsg))
	t.Cleanup(func() { typesRegistry.Deregister(new(cborTestMsg)) })

	serializer := NewCBORSerializer()
	orig := &cborTestMsg{ID: 1, Label: "x"}
	data, err := serializer.Serialize(orig)
	require.NoError(t, err)
	actual, err := serializer.Deserialize(data)
	require.NoError(t, err)
	require.NotNil(t, actual)
}

func TestRegisterSerializableTypes_MultipleTypes(t *testing.T) {
	RegisterSerializableTypes(new(cborTestMsg), new(cborTestWithSlice))
	t.Cleanup(func() {
		typesRegistry.Deregister(new(cborTestMsg))
		typesRegistry.Deregister(new(cborTestWithSlice))
	})

	serializer := NewCBORSerializer()

	data1, err := serializer.Serialize(&cborTestMsg{ID: 1, Label: "a"})
	require.NoError(t, err)
	data2, err := serializer.Serialize(&cborTestWithSlice{Names: []string{"b"}})
	require.NoError(t, err)

	a1, err := serializer.Deserialize(data1)
	require.NoError(t, err)
	require.IsType(t, (*cborTestMsg)(nil), a1)

	a2, err := serializer.Deserialize(data2)
	require.NoError(t, err)
	require.IsType(t, (*cborTestWithSlice)(nil), a2)
}

func TestCBORSerializer_ConcurrentUse(t *testing.T) {
	RegisterSerializableTypes(new(cborTestMsg))
	t.Cleanup(func() { typesRegistry.Deregister(new(cborTestMsg)) })

	serializer := NewCBORSerializer()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			msg := &cborTestMsg{ID: n, Label: "concurrent"}
			data, err := serializer.Serialize(msg)
			assert.NoError(t, err)
			assert.NotEmpty(t, data)
			actual, err := serializer.Deserialize(data)
			assert.NoError(t, err)
			assert.NotNil(t, actual)
			decoded, ok := actual.(*cborTestMsg)
			assert.True(t, ok)
			assert.Equal(t, n, decoded.ID)
		}(i)
	}
	wg.Wait()
}

func TestCBORSerializer_FrameLayout(t *testing.T) {
	RegisterSerializableTypes(new(cborTestMsg))
	t.Cleanup(func() { typesRegistry.Deregister(new(cborTestMsg)) })

	serializer := NewCBORSerializer()
	msg := &cborTestMsg{ID: 7, Label: "frame"}
	data, err := serializer.Serialize(msg)
	require.NoError(t, err)

	require.GreaterOrEqual(t, len(data), 8, "frame must have at least 8-byte header")
	totalLen := int(binary.BigEndian.Uint32(data[0:4]))
	nameLen := int(binary.BigEndian.Uint32(data[4:8]))
	require.Equal(t, len(data), totalLen, "totalLen must match frame length")
	require.GreaterOrEqual(t, totalLen, 8+nameLen, "totalLen must cover header + type name")
	require.Greater(t, totalLen-8-nameLen, 0, "CBOR payload must be non-empty")
}
