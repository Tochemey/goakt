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
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/test/data/testpb"
)

func TestNewCRDTValueSerializer(t *testing.T) {
	s := NewCRDTValueSerializer()
	require.NotNil(t, s)
	require.NotNil(t, s.proto)
	require.NotNil(t, s.cbor)
}

func TestCRDTValueSerializer_PrimitiveRoundTrip(t *testing.T) {
	s := NewCRDTValueSerializer()

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
			data, err := s.Serialize(tt.value)
			require.NoError(t, err)
			require.NotEmpty(t, data)

			actual, err := s.Deserialize(data)
			require.NoError(t, err)
			assert.Equal(t, reflect.TypeOf(tt.value), reflect.TypeOf(actual))
			assert.Equal(t, tt.value, actual)
		})
	}
}

func TestCRDTValueSerializer_ProtoMessageRoundTrip(t *testing.T) {
	s := NewCRDTValueSerializer()

	orig := new(testpb.TestSend)

	data, err := s.Serialize(orig)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	actual, err := s.Deserialize(data)
	require.NoError(t, err)
	require.NotNil(t, actual)

	_, ok := actual.(*testpb.TestSend)
	require.True(t, ok)
}

func TestCRDTValueSerializer_SerializeUnregisteredType(t *testing.T) {
	s := NewCRDTValueSerializer()

	type unregistered struct{ X int }
	_, err := s.Serialize(&unregistered{X: 1})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not registered")
}

func TestCRDTValueSerializer_DeserializeInvalidData(t *testing.T) {
	s := NewCRDTValueSerializer()

	_, err := s.Deserialize([]byte{0xff, 0xfe})
	require.Error(t, err)
}

func TestCRDTValueSerializer_DeserializeEmptyData(t *testing.T) {
	s := NewCRDTValueSerializer()

	_, err := s.Deserialize(nil)
	require.Error(t, err)
}
