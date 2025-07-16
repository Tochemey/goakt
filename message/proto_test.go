/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package message

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/test/data/testpb"
)

func TestProto_MarshalUnmarshalBinary_Success(t *testing.T) {
	orig := &testpb.Reply{Content: "hello world"}
	p := &Proto{message: orig}

	data, err := p.MarshalBinary()
	require.NoError(t, err, "MarshalBinary failed")

	var p2 Proto
	err = p2.UnmarshalBinary(data)
	require.NoError(t, err, "UnmarshalBinary failed")

	reply, ok := p2.Message().(*testpb.Reply)
	require.True(t, ok, "Expected *testpb.Reply, got %T", p2.Message)
	require.Equal(t, orig.Content, reply.Content, "Expected message content to match")
}

func TestProto_MarshalBinary_Errors(t *testing.T) {
	p := &Proto{}
	_, err := p.MarshalBinary()
	require.Error(t, err, "Expected error when marshaling empty Proto")
	require.ErrorIs(t, err, ErrUnknownMessageType, "Expected ErrUnknownMessageType")
}

func TestProto_UnmarshalBinary_InvalidLength(t *testing.T) {
	p := &Proto{}
	// Too short
	err := p.UnmarshalBinary([]byte{1, 2, 3})
	require.Error(t, err, "Expected error for too short data")
	require.ErrorIs(t, err, ErrInvalidMessageLength, "Expected ErrInvalidMessageLength")

	// messageLength > len(data)
	data := make([]byte, 8)
	binary.BigEndian.PutUint32(data[:4], 100)
	err = p.UnmarshalBinary(data)
	require.Error(t, err, "Expected error for messageLength > len(data)")
	require.ErrorIs(t, err, ErrInvalidMessageLength, "Expected ErrInvalidMessageLength")

	// messageNameLength+8 > messageLength
	data = make([]byte, 12)
	binary.BigEndian.PutUint32(data[:4], 12)
	binary.BigEndian.PutUint32(data[4:8], 10)
	err = p.UnmarshalBinary(data)
	require.Error(t, err, "Expected error for messageNameLength+8 > messageLength")
	require.ErrorIs(t, err, ErrInvalidMessageLength, "Expected ErrInvalidMessageLength")
}

func TestProto_UnmarshalBinary_UnknownType(t *testing.T) {
	name := "DoesNotExist"
	nameLen := uint32(len(name))
	payload := []byte{}
	totalLen := 4 + 4 + len(name) + len(payload)
	buf := bytes.NewBuffer(nil)
	_ = binary.Write(buf, binary.BigEndian, uint32(totalLen))
	_ = binary.Write(buf, binary.BigEndian, nameLen)
	_, _ = buf.Write([]byte(name))
	_, _ = buf.Write(payload)
	p := &Proto{}
	err := p.UnmarshalBinary(buf.Bytes())
	require.Error(t, err, "Expected error for unknown message type")
	require.ErrorIs(t, err, ErrUnknownMessageType, "Expected ErrUnknownMessageType")
}

func TestProto_UnmarshalBinary_InvalidProtoPayload(t *testing.T) {
	msgName := string(proto.MessageName(&testpb.Reply{}))
	nameLen := uint32(len(msgName))
	payload := []byte{0xff, 0xff, 0xff}
	totalLen := 4 + 4 + len(msgName) + len(payload)
	buf := bytes.NewBuffer(nil)
	_ = binary.Write(buf, binary.BigEndian, uint32(totalLen))
	_ = binary.Write(buf, binary.BigEndian, nameLen)
	_, _ = buf.Write([]byte(msgName))
	_, _ = buf.Write(payload)
	p := &Proto{}
	err := p.UnmarshalBinary(buf.Bytes())
	require.Error(t, err, "Expected error for invalid proto payload")
	require.ErrorIs(t, err, ErrUnmarshalBinaryFailed, "Expected ErrUnmarshalBinaryFailed")
}

func TestProto_NewProto(t *testing.T) {
	msg := &testpb.Reply{Content: "foo"}
	p := NewProto(msg)
	require.NotNil(t, p, "Expected NewProto to return a non-nil Proto")
	require.True(t, proto.Equal(p.Message(), msg))
}

func TestProto_ImplementsMessage(t *testing.T) {
	msg := &testpb.Reply{Content: "foo"}
	p := NewProto(msg)
	var i any = p
	_, ok := i.(Message)
	require.True(t, ok, "Expected Proto to implement Message interface")
}

func TestProto_MarshalUnmarshalBinary_RoundTrip(t *testing.T) {
	orig := &testpb.Reply{Content: "roundtrip"}
	p := NewProto(orig)
	data, err := p.MarshalBinary()
	require.NoError(t, err, "MarshalBinary failed")
	p2 := &Proto{}
	err = p2.UnmarshalBinary(data)
	require.NoError(t, err, "UnmarshalBinary failed")
	got, ok := p2.Message().(*testpb.Reply)
	require.True(t, ok, "Expected *testpb.Reply, got %T", p2.Message)
	require.True(t, proto.Equal(got, orig), "Expected messages to be equal after round trip")
}
