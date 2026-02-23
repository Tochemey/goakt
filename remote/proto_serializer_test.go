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
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/test/data/testpb"
)

func TestProtoSerializer_SerializeDeserialize_Success(t *testing.T) {
	orig := &testpb.Reply{Content: "hello world"}
	serializer := NewProtoSerializer()

	data, err := serializer.Serialize(orig)
	require.NoError(t, err, "Serialize failed")
	require.NotEmpty(t, data)

	actual, err := serializer.Deserialize(data)
	require.NoError(t, err, "Deserialize failed")
	require.NotNil(t, actual)

	reply, ok := actual.(*testpb.Reply)
	require.True(t, ok, "expected *testpb.Reply from type assertion")
	require.Equal(t, orig.Content, reply.Content)
}

func TestProtoSerializer_Serialize_Errors(t *testing.T) {
	serializer := NewProtoSerializer()

	t.Run("non-proto value", func(t *testing.T) {
		_, err := serializer.Serialize("this is not a proto message")
		require.Error(t, err)
		require.ErrorIs(t, err, ErrNotProtoMessage)
	})

	t.Run("non-proto struct", func(t *testing.T) {
		type myStruct struct{ Value int }
		_, err := serializer.Serialize(myStruct{Value: 42})
		require.Error(t, err)
		require.ErrorIs(t, err, ErrNotProtoMessage)
	})
}

func TestProtoSerializer_Deserialize_InvalidFrame(t *testing.T) {
	serializer := NewProtoSerializer()

	t.Run("too short", func(t *testing.T) {
		actual, err := serializer.Deserialize([]byte{1, 2, 3})
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidFrame)
		require.Nil(t, actual)
	})

	t.Run("totalLen greater than data length", func(t *testing.T) {
		data := make([]byte, 8)
		binary.BigEndian.PutUint32(data[:4], 100)
		actual, err := serializer.Deserialize(data)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidFrame)
		require.Nil(t, actual)
	})

	t.Run("nameLen overflows totalLen", func(t *testing.T) {
		data := make([]byte, 12)
		binary.BigEndian.PutUint32(data[:4], 12)
		binary.BigEndian.PutUint32(data[4:8], 10) // 8 + 10 > 12
		actual, err := serializer.Deserialize(data)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidFrame)
		require.Nil(t, actual)
	})
}

func TestProtoSerializer_Deserialize_UnknownType(t *testing.T) {
	name := "DoesNotExist"
	totalLen := 4 + 4 + len(name)

	data := make([]byte, totalLen)
	binary.BigEndian.PutUint32(data[0:4], uint32(totalLen))
	binary.BigEndian.PutUint32(data[4:8], uint32(len(name)))
	copy(data[8:], name)

	serializer := NewProtoSerializer()
	actual, err := serializer.Deserialize(data)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrUnknownMessageType)
	require.Nil(t, actual)
}

func TestProtoSerializer_Deserialize_InvalidProtoPayload(t *testing.T) {
	msgName := string(proto.MessageName(&testpb.Reply{}))
	payload := []byte{0xff, 0xff, 0xff}
	totalLen := 4 + 4 + len(msgName) + len(payload)

	data := make([]byte, totalLen)
	binary.BigEndian.PutUint32(data[0:4], uint32(totalLen))
	binary.BigEndian.PutUint32(data[4:8], uint32(len(msgName)))
	copy(data[8:], msgName)
	copy(data[8+len(msgName):], payload)

	serializer := NewProtoSerializer()
	actual, err := serializer.Deserialize(data)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrDeserializeFailed)
	require.Nil(t, actual)
}
