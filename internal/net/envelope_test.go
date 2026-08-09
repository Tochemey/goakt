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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDataEnvelopeRoundTrip(t *testing.T) {
	md := NewMetadata()
	md.Set("trace", "abc")
	md.SetDeadline(time.Now().Add(time.Second))
	meta := md.MarshalBinary()

	original := DataEnvelope{
		Sender:       "goakt://sys@127.0.0.1:8080/from",
		Receiver:     "goakt://sys@127.0.0.1:8080/to",
		TypeName:     "testpb.Reply",
		SerializerID: SerializerIDInternalProto,
		Metadata:     meta,
		Payload:      []byte{1, 2, 3, 4},
	}

	encoded, err := encodeDataEnvelope(original)
	require.NoError(t, err)

	decoded, err := decodeDataEnvelope(encoded, true)
	require.NoError(t, err)
	assert.Equal(t, original.Sender, decoded.Sender)
	assert.Equal(t, original.Receiver, decoded.Receiver)
	assert.Equal(t, original.TypeName, decoded.TypeName)
	assert.Equal(t, original.SerializerID, decoded.SerializerID)
	assert.Equal(t, original.Payload, decoded.Payload)
	assert.Equal(t, original.Metadata, decoded.Metadata)
}

func TestDataEnvelopeWithoutMetadata(t *testing.T) {
	original := DataEnvelope{
		Sender:       "a",
		Receiver:     "b",
		TypeName:     "t",
		SerializerID: SerializerIDJSON,
		Payload:      []byte(`{"ok":true}`),
	}

	encoded, err := encodeDataEnvelope(original)
	require.NoError(t, err)

	decoded, err := decodeDataEnvelope(encoded, false)
	require.NoError(t, err)
	assert.Nil(t, decoded.Metadata)
	assert.Equal(t, original.Payload, decoded.Payload)
}

func TestDataEnvelopeEmptyControlRefs(t *testing.T) {
	original := DataEnvelope{
		TypeName:     "internalpb.RemoteLookupRequest",
		SerializerID: SerializerIDInternalProto,
		Payload:      []byte{9, 9},
	}

	encoded, err := encodeDataEnvelope(original)
	require.NoError(t, err)

	decoded, err := decodeDataEnvelope(encoded, false)
	require.NoError(t, err)
	assert.Empty(t, decoded.Sender)
	assert.Empty(t, decoded.Receiver)
	assert.Equal(t, original.TypeName, decoded.TypeName)
}

func TestDataEnvelopeCustomSerializer(t *testing.T) {
	original := DataEnvelope{
		Sender:       "a",
		Receiver:     "b",
		SerializerID: SerializerIDCustom,
		Payload:      []byte("self-describing"),
	}

	encoded, err := encodeDataEnvelope(original)
	require.NoError(t, err)

	decoded, err := decodeDataEnvelope(encoded, false)
	require.NoError(t, err)
	assert.Empty(t, decoded.TypeName)
	assert.Equal(t, SerializerIDCustom, decoded.SerializerID)
}

func TestDataEnvelopeRejectsTableRef(t *testing.T) {
	var buf []byte
	buf = binary.AppendUvarint(buf, 7) // nonzero table id
	buf = append(buf, 0)               // incomplete remainder

	_, err := decodeDataEnvelope(buf, false)
	require.ErrorIs(t, err, ErrTableRefUnsupported)
}

func TestDataEnvelopeTruncated(t *testing.T) {
	_, err := decodeDataEnvelope([]byte{0}, false)
	require.Error(t, err)
}

func TestDataEnvelopeUnknownSerializer(t *testing.T) {
	_, err := encodeDataEnvelope(DataEnvelope{
		TypeName:     "t",
		SerializerID: 42,
	})
	require.ErrorIs(t, err, ErrUnknownSerializerID)
}

func TestDataEnvelopeFlagMetadataMismatch(t *testing.T) {
	original := DataEnvelope{
		TypeName:     "t",
		SerializerID: SerializerIDInternalProto,
		Payload:      []byte{1},
	}
	encoded, err := encodeDataEnvelope(original)
	require.NoError(t, err)

	// Claiming metadata when none was encoded consumes the payload as metaLen.
	_, err = decodeDataEnvelope(encoded, true)
	require.Error(t, err)
}

func TestDataEnvelopeOversizeMetadata(t *testing.T) {
	var buf []byte
	buf = append(buf, putInlineRefBytes("")...)
	buf = append(buf, putInlineRefBytes("")...)
	buf = append(buf, putInlineRefBytes("t")...)
	buf = append(buf, SerializerIDInternalProto)
	metaLen := make([]byte, 4)
	binary.BigEndian.PutUint32(metaLen, 1<<30)
	buf = append(buf, metaLen...)

	_, err := decodeDataEnvelope(buf, true)
	require.Error(t, err)
}

func TestReplyEnvelopeRoundTrip(t *testing.T) {
	original := ReplyEnvelope{
		TypeName:     "testpb.Reply",
		SerializerID: SerializerIDInternalProto,
		Metadata:     []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
		Payload:      []byte("ok"),
	}

	encoded, err := encodeReplyEnvelope(original)
	require.NoError(t, err)

	decoded, err := decodeReplyEnvelope(encoded, true)
	require.NoError(t, err)
	assert.Equal(t, original, decoded)
}

func TestReplyEnvelopeWithoutMetadata(t *testing.T) {
	original := ReplyEnvelope{
		TypeName:     "testpb.Reply",
		SerializerID: SerializerIDCBOR,
		Payload:      []byte{0xA0},
	}

	encoded, err := encodeReplyEnvelope(original)
	require.NoError(t, err)

	decoded, err := decodeReplyEnvelope(encoded, false)
	require.NoError(t, err)
	assert.Equal(t, original.TypeName, decoded.TypeName)
	assert.Equal(t, original.Payload, decoded.Payload)
	assert.Nil(t, decoded.Metadata)
}

func TestReplyEnvelopeRejectsTableRef(t *testing.T) {
	var buf []byte
	buf = binary.AppendUvarint(buf, 3)
	_, err := decodeReplyEnvelope(buf, false)
	require.ErrorIs(t, err, ErrTableRefUnsupported)
}

func TestReplyEnvelopeCustomRequiresEmptyType(t *testing.T) {
	_, err := encodeReplyEnvelope(ReplyEnvelope{
		TypeName:     "must-be-empty",
		SerializerID: SerializerIDCustom,
	})
	require.Error(t, err)
}

func TestValidateSerializerIDRequiresTypeName(t *testing.T) {
	err := validateSerializerID(SerializerIDInternalProto, "")
	require.Error(t, err)
}

// putInlineRefBytes is a test helper that returns an encoded inline ref.
func putInlineRefBytes(s string) []byte {
	buf := make([]byte, refSize(s))
	putInlineRef(buf, s)
	return buf
}
