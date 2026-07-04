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

package remoteclient

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v4/remote"
)

// stubSerializer is a test-only Serializer with configurable outcomes.
type stubSerializer struct {
	serializeData    []byte
	serializeErr     error
	deserializeMsg   any
	deserializeErr   error
	deserializeCalls int
}

func (s *stubSerializer) Serialize(any) ([]byte, error) {
	return s.serializeData, s.serializeErr
}

func (s *stubSerializer) Deserialize([]byte) (any, error) {
	s.deserializeCalls++
	return s.deserializeMsg, s.deserializeErr
}

func TestSerializerDispatch_Serialize(t *testing.T) {
	t.Run("first serializer succeeds", func(t *testing.T) {
		d := &serializerDispatch{
			entries: []ifaceEntry{
				{serializer: &stubSerializer{serializeData: []byte("ok")}},
				{serializer: &stubSerializer{serializeErr: errors.New("unreachable")}},
			},
		}
		data, err := d.Serialize(nil)
		require.NoError(t, err)
		assert.Equal(t, []byte("ok"), data)
	})

	t.Run("falls through to second on first failure", func(t *testing.T) {
		d := &serializerDispatch{
			entries: []ifaceEntry{
				{serializer: &stubSerializer{serializeErr: errors.New("first fail")}},
				{serializer: &stubSerializer{serializeData: []byte("second")}},
			},
		}
		data, err := d.Serialize(nil)
		require.NoError(t, err)
		assert.Equal(t, []byte("second"), data)
	})

	t.Run("all fail returns last error", func(t *testing.T) {
		lastErr := errors.New("last serialize error")
		d := &serializerDispatch{
			entries: []ifaceEntry{
				{serializer: &stubSerializer{serializeErr: errors.New("first")}},
				{serializer: &stubSerializer{serializeErr: lastErr}},
			},
		}
		_, err := d.Serialize(nil)
		require.Error(t, err)
		assert.Equal(t, lastErr, err)
	})

	t.Run("empty entries returns sentinel error", func(t *testing.T) {
		d := &serializerDispatch{entries: []ifaceEntry{}}
		_, err := d.Serialize(nil)
		require.ErrorIs(t, err, errNoSerializerEncode)
	})
}

func TestSerializerDispatch_Deserialize(t *testing.T) {
	t.Run("first serializer succeeds", func(t *testing.T) {
		want := durationpb.New(time.Second)
		d := &serializerDispatch{
			entries: []ifaceEntry{
				{serializer: &stubSerializer{deserializeMsg: want}},
				{serializer: &stubSerializer{deserializeErr: errors.New("unreachable")}},
			},
		}
		msg, err := d.Deserialize([]byte("data"))
		require.NoError(t, err)
		assert.Equal(t, want, msg)
	})

	t.Run("falls through to second on first failure", func(t *testing.T) {
		want := durationpb.New(2 * time.Second)
		d := &serializerDispatch{
			entries: []ifaceEntry{
				{serializer: &stubSerializer{deserializeErr: errors.New("first fail")}},
				{serializer: &stubSerializer{deserializeMsg: want}},
			},
		}
		msg, err := d.Deserialize([]byte("data"))
		require.NoError(t, err)
		assert.Equal(t, want, msg)
	})

	t.Run("all fail returns last error", func(t *testing.T) {
		lastErr := errors.New("last decode error")
		d := &serializerDispatch{
			entries: []ifaceEntry{
				{serializer: &stubSerializer{deserializeErr: errors.New("first")}},
				{serializer: &stubSerializer{deserializeErr: lastErr}},
			},
		}
		_, err := d.Deserialize([]byte("data"))
		require.Error(t, err)
		assert.Equal(t, lastErr, err)
	})

	t.Run("empty entries returns sentinel error", func(t *testing.T) {
		d := &serializerDispatch{entries: []ifaceEntry{}}
		_, err := d.Deserialize([]byte("data"))
		require.ErrorIs(t, err, errNoSerializerDecode)
	})

	t.Run("round-trip via default remoting dispatcher", func(t *testing.T) {
		// Verify the dispatcher wired into NewRemoting can decode a valid proto frame.
		r := NewClient().(*client)
		dispatcher := r.Serializer(nil)
		require.NotNil(t, dispatcher)

		// Produce a valid wire frame with the ProtoSerializer send path.
		ps := remote.NewProtoSerializer()
		msg := durationpb.New(5 * time.Second)
		raw, err := ps.Serialize(msg)
		require.NoError(t, err)
		require.NotEmpty(t, raw)

		// The dispatcher should decode it back to the same message.
		decoded, err := dispatcher.Deserialize(raw)
		require.NoError(t, err)
		require.NotNil(t, decoded)
	})
}

func TestSerializerDispatch_ProtoFastPath(t *testing.T) {
	t.Run("proto frame skips serializers registered ahead of proto", func(t *testing.T) {
		stub := &stubSerializer{deserializeErr: errors.New("must not be tried")}
		d := newSerializerDispatch([]ifaceEntry{
			{serializer: stub},
			{serializer: remote.NewProtoSerializer()},
		})
		require.NotNil(t, d.proto)

		raw, err := remote.NewProtoSerializer().Serialize(durationpb.New(3 * time.Second))
		require.NoError(t, err)

		decoded, err := d.Deserialize(raw)
		require.NoError(t, err)
		require.NotNil(t, decoded)

		assert.Equal(t, 0, stub.deserializeCalls, "non-proto serializer must not attempt a proto frame")
	})

	t.Run("unresolvable frame falls back to registration order", func(t *testing.T) {
		want := "decoded-by-stub"
		stub := &stubSerializer{deserializeMsg: want}
		d := newSerializerDispatch([]ifaceEntry{
			{serializer: stub},
			{serializer: remote.NewProtoSerializer()},
		})

		decoded, err := d.Deserialize([]byte("not-a-shared-layout-frame"))
		require.NoError(t, err)
		assert.Equal(t, want, decoded)
		assert.Equal(t, 1, stub.deserializeCalls)
	})

	t.Run("no proto serializer registered keeps ordered behavior", func(t *testing.T) {
		want := "stub-result"
		stub := &stubSerializer{deserializeMsg: want}
		d := newSerializerDispatch([]ifaceEntry{{serializer: stub}})
		require.Nil(t, d.proto)

		decoded, err := d.Deserialize([]byte("data"))
		require.NoError(t, err)
		assert.Equal(t, want, decoded)
	})
}

func TestFrameTypeName(t *testing.T) {
	t.Run("valid frame yields the embedded name", func(t *testing.T) {
		raw, err := remote.NewProtoSerializer().Serialize(durationpb.New(time.Second))
		require.NoError(t, err)

		name, ok := frameTypeName(raw)
		require.True(t, ok)
		assert.Equal(t, "google.protobuf.Duration", string(name))
	})

	t.Run("short frame is rejected", func(t *testing.T) {
		_, ok := frameTypeName([]byte{0, 1, 2})
		assert.False(t, ok)
	})

	t.Run("inconsistent lengths are rejected", func(t *testing.T) {
		// totalLen=16 but nameLen overruns it.
		frame := make([]byte, 16)
		frame[3] = 16
		frame[7] = 100
		_, ok := frameTypeName(frame)
		assert.False(t, ok)
	})
}
