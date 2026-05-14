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
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type jsonTestMsg struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
}

type jsonTestNested struct {
	Inner jsonTestMsg `json:"inner"`
	Flag  bool        `json:"flag"`
}

type jsonTestWithSlice struct {
	Names []string `json:"names"`
}

type jsonTestWithMap struct {
	Meta map[string]string `json:"meta"`
}

type jsonTestWithTime struct {
	At time.Time `json:"at"`
}

// jsonTestUnmarshalable has a channel field; JSON cannot encode it.
type jsonTestUnmarshalable struct {
	Ch chan int `json:"ch"`
}

// applyJSONSerializerRegistration triggers auto-registration via WithSerializers.
func applyJSONSerializerRegistration(msgs ...any) {
	cfg := &Config{serializers: make(map[reflect.Type]Serializer)}
	for _, msg := range msgs {
		WithSerializers(msg, NewJSONSerializer()).Apply(cfg)
	}
}

func TestNewJSONSerializer(t *testing.T) {
	s := NewJSONSerializer()
	require.NotNil(t, s)
	var _ Serializer = s
}

func TestJSONSerializer_RegistryRequired(t *testing.T) {
	s := NewJSONSerializer()
	s.RegistryRequired() // exercises UsesRegistry interface; no-op but ensures coverage
}

func TestJSONSerializer_SerializeDeserialize_RoundTrip(t *testing.T) {
	applyJSONSerializerRegistration(new(jsonTestMsg))
	t.Cleanup(func() { typesRegistry.Deregister(new(jsonTestMsg)) })

	serializer := NewJSONSerializer()
	orig := &jsonTestMsg{ID: 42, Label: "hello"}

	data, err := serializer.Serialize(orig)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	actual, err := serializer.Deserialize(data)
	require.NoError(t, err)
	require.NotNil(t, actual)

	decoded, ok := actual.(*jsonTestMsg)
	require.True(t, ok)
	require.Equal(t, orig.ID, decoded.ID)
	require.Equal(t, orig.Label, decoded.Label)
}

func TestJSONSerializer_SerializeDeserialize_ValueType(t *testing.T) {
	applyJSONSerializerRegistration(new(jsonTestMsg))
	t.Cleanup(func() { typesRegistry.Deregister(new(jsonTestMsg)) })

	serializer := NewJSONSerializer()
	orig := jsonTestMsg{ID: 1, Label: "value"}

	data, err := serializer.Serialize(orig)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	actual, err := serializer.Deserialize(data)
	require.NoError(t, err)
	require.NotNil(t, actual)

	decoded, ok := actual.(*jsonTestMsg)
	require.True(t, ok)
	require.Equal(t, orig.ID, decoded.ID)
	require.Equal(t, orig.Label, decoded.Label)
}

func TestJSONSerializer_SerializeDeserialize_NestedStruct(t *testing.T) {
	applyJSONSerializerRegistration(new(jsonTestNested))
	t.Cleanup(func() { typesRegistry.Deregister(new(jsonTestNested)) })

	serializer := NewJSONSerializer()
	orig := &jsonTestNested{
		Inner: jsonTestMsg{ID: 10, Label: "nested"},
		Flag:  true,
	}

	data, err := serializer.Serialize(orig)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	actual, err := serializer.Deserialize(data)
	require.NoError(t, err)
	require.NotNil(t, actual)

	decoded, ok := actual.(*jsonTestNested)
	require.True(t, ok)
	require.Equal(t, orig.Inner.ID, decoded.Inner.ID)
	require.Equal(t, orig.Inner.Label, decoded.Inner.Label)
	require.Equal(t, orig.Flag, decoded.Flag)
}

func TestJSONSerializer_SerializeDeserialize_Slice(t *testing.T) {
	applyJSONSerializerRegistration(new(jsonTestWithSlice))
	t.Cleanup(func() { typesRegistry.Deregister(new(jsonTestWithSlice)) })

	serializer := NewJSONSerializer()
	orig := &jsonTestWithSlice{Names: []string{"a", "b", "c"}}

	data, err := serializer.Serialize(orig)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	actual, err := serializer.Deserialize(data)
	require.NoError(t, err)
	require.NotNil(t, actual)

	decoded, ok := actual.(*jsonTestWithSlice)
	require.True(t, ok)
	require.Equal(t, orig.Names, decoded.Names)
}

func TestJSONSerializer_SerializeDeserialize_Map(t *testing.T) {
	applyJSONSerializerRegistration(new(jsonTestWithMap))
	t.Cleanup(func() { typesRegistry.Deregister(new(jsonTestWithMap)) })

	serializer := NewJSONSerializer()
	orig := &jsonTestWithMap{Meta: map[string]string{"k1": "v1", "k2": "v2"}}

	data, err := serializer.Serialize(orig)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	actual, err := serializer.Deserialize(data)
	require.NoError(t, err)
	require.NotNil(t, actual)

	decoded, ok := actual.(*jsonTestWithMap)
	require.True(t, ok)
	require.Equal(t, orig.Meta, decoded.Meta)
}

func TestJSONSerializer_SerializeDeserialize_Time(t *testing.T) {
	applyJSONSerializerRegistration(new(jsonTestWithTime))
	t.Cleanup(func() { typesRegistry.Deregister(new(jsonTestWithTime)) })

	serializer := NewJSONSerializer()
	ts := time.Date(2025, 2, 22, 12, 0, 0, 0, time.UTC)
	orig := &jsonTestWithTime{At: ts}

	data, err := serializer.Serialize(orig)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	actual, err := serializer.Deserialize(data)
	require.NoError(t, err)
	require.NotNil(t, actual)

	decoded, ok := actual.(*jsonTestWithTime)
	require.True(t, ok)
	require.True(t, ts.Equal(decoded.At))
}

func TestJSONSerializer_PrimitiveRoundTrip(t *testing.T) {
	// Primitive types are pre-registered in types.GlobalRegistry init().
	// JSONSerializer must round-trip them as values (not pointers).
	serializer := NewJSONSerializer()

	tests := []struct {
		name  string
		value any
	}{
		{"string", "hello"},
		{"empty string", ""},
		{"bool true", true},
		{"bool false", false},
		{"int", int(42)},
		{"int8", int8(-7)},
		{"int16", int16(300)},
		{"int32", int32(-100000)},
		{"int64", int64(9223372036854775807)},
		{"uint", uint(99)},
		{"uint8", uint8(255)},
		{"uint16", uint16(65535)},
		{"uint32", uint32(4294967295)},
		{"uint64", uint64(18446744073709551615)},
		{"float32", float32(3.14)},
		{"float64", float64(2.718281828)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := serializer.Serialize(tt.value)
			require.NoError(t, err)
			require.NotEmpty(t, data)

			actual, err := serializer.Deserialize(data)
			require.NoError(t, err)

			require.Equal(t, reflect.TypeOf(tt.value), reflect.TypeOf(actual),
				"round-trip should preserve value type, got %T", actual)
			require.Equal(t, tt.value, actual)
		})
	}
}

func TestJSONSerializer_Serialize_Errors(t *testing.T) {
	serializer := NewJSONSerializer()

	t.Run("nil message", func(t *testing.T) {
		_, err := serializer.Serialize(nil)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrJSONNilMessage)
	})

	t.Run("unregistered type", func(t *testing.T) {
		type unregistered struct{ X int }
		_, err := serializer.Serialize(&unregistered{X: 1})
		require.Error(t, err)
		require.Contains(t, err.Error(), "not registered")
	})

	t.Run("registered type but JSON marshal fails", func(t *testing.T) {
		applyJSONSerializerRegistration(new(jsonTestUnmarshalable))
		t.Cleanup(func() { typesRegistry.Deregister(new(jsonTestUnmarshalable)) })

		_, err := serializer.Serialize(&jsonTestUnmarshalable{Ch: make(chan int)})
		require.Error(t, err)
		require.ErrorIs(t, err, ErrJSONSerializeFailed)
	})
}

func TestJSONSerializer_Deserialize_InvalidFrame(t *testing.T) {
	serializer := NewJSONSerializer()

	t.Run("too short", func(t *testing.T) {
		actual, err := serializer.Deserialize([]byte{1, 2, 3})
		require.Error(t, err)
		require.ErrorIs(t, err, ErrJSONInvalidFrame)
		require.Nil(t, actual)
	})

	t.Run("empty", func(t *testing.T) {
		actual, err := serializer.Deserialize(nil)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrJSONInvalidFrame)
		require.Nil(t, actual)
	})

	t.Run("totalLen greater than data length", func(t *testing.T) {
		data := make([]byte, 8)
		binary.BigEndian.PutUint32(data[:4], 100)
		actual, err := serializer.Deserialize(data)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrJSONInvalidFrame)
		require.Nil(t, actual)
	})

	t.Run("totalLen less than 8", func(t *testing.T) {
		data := make([]byte, 8)
		binary.BigEndian.PutUint32(data[:4], 4)
		actual, err := serializer.Deserialize(data)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrJSONInvalidFrame)
		require.Nil(t, actual)
	})

	t.Run("nameLen overflows totalLen", func(t *testing.T) {
		data := make([]byte, 12)
		binary.BigEndian.PutUint32(data[:4], 12)
		binary.BigEndian.PutUint32(data[4:8], 10) // 8 + 10 > 12
		actual, err := serializer.Deserialize(data)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrJSONInvalidFrame)
		require.Nil(t, actual)
	})
}

func TestJSONSerializer_Deserialize_UnknownType(t *testing.T) {
	name := "unknown.pkg.typ"
	totalLen := 4 + 4 + len(name)

	data := make([]byte, totalLen)
	binary.BigEndian.PutUint32(data[0:4], uint32(totalLen))
	binary.BigEndian.PutUint32(data[4:8], uint32(len(name)))
	copy(data[8:], name)

	serializer := NewJSONSerializer()
	actual, err := serializer.Deserialize(data)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not registered")
	require.Nil(t, actual)
}

func TestJSONSerializer_Deserialize_InvalidJSONPayload(t *testing.T) {
	applyJSONSerializerRegistration(new(jsonTestMsg))
	t.Cleanup(func() { typesRegistry.Deregister(new(jsonTestMsg)) })

	// Valid frame header, garbage JSON payload.
	name := "remote.jsontestmsg"
	payload := []byte{0xff, 0xfe, 0xfd}
	totalLen := 4 + 4 + len(name) + len(payload)

	data := make([]byte, totalLen)
	binary.BigEndian.PutUint32(data[0:4], uint32(totalLen))
	binary.BigEndian.PutUint32(data[4:8], uint32(len(name)))
	copy(data[8:], name)
	copy(data[8+len(name):], payload)

	serializer := NewJSONSerializer()
	actual, err := serializer.Deserialize(data)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrJSONDeserializeFailed)
	require.Nil(t, actual)
}

func TestJSONSerializer_ConcurrentUse(t *testing.T) {
	applyJSONSerializerRegistration(new(jsonTestMsg))
	t.Cleanup(func() { typesRegistry.Deregister(new(jsonTestMsg)) })

	serializer := NewJSONSerializer()
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			msg := &jsonTestMsg{ID: n, Label: "concurrent"}
			data, err := serializer.Serialize(msg)
			assert.NoError(t, err)
			assert.NotEmpty(t, data)
			actual, err := serializer.Deserialize(data)
			assert.NoError(t, err)
			assert.NotNil(t, actual)
			decoded, ok := actual.(*jsonTestMsg)
			assert.True(t, ok)
			assert.Equal(t, n, decoded.ID)
		}(i)
	}
	wg.Wait()
}

func TestJSONSerializer_FrameLayout(t *testing.T) {
	applyJSONSerializerRegistration(new(jsonTestMsg))
	t.Cleanup(func() { typesRegistry.Deregister(new(jsonTestMsg)) })

	serializer := NewJSONSerializer()
	msg := &jsonTestMsg{ID: 7, Label: "frame"}
	data, err := serializer.Serialize(msg)
	require.NoError(t, err)

	require.GreaterOrEqual(t, len(data), 8, "frame must have at least 8-byte header")
	totalLen := int(binary.BigEndian.Uint32(data[0:4]))
	nameLen := int(binary.BigEndian.Uint32(data[4:8]))
	require.Equal(t, len(data), totalLen, "totalLen must match frame length")
	require.GreaterOrEqual(t, totalLen, 8+nameLen, "totalLen must cover header + type name")
	require.Greater(t, totalLen-8-nameLen, 0, "JSON payload must be non-empty")
}
