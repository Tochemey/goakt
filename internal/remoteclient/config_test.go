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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/remote"
)

func TestClientSerializerOptions(t *testing.T) {
	t.Run("returns nil when no user serializers registered", func(t *testing.T) {
		config := remote.DefaultConfig()
		assert.Nil(t, ClientSerializerOptions(config))
	})
	t.Run("returns nil for NewConfig with only proto default", func(t *testing.T) {
		config := remote.NewConfig("127.0.0.1", 0)
		assert.Nil(t, ClientSerializerOptions(config))
	})
	t.Run("returns one option per user-registered serializer", func(t *testing.T) {
		custom := &stubSerializer{}
		config := remote.NewConfig("127.0.0.1", 0, remote.WithSerializers(new(nonProtoMsg), custom))
		opts := ClientSerializerOptions(config)
		require.Len(t, opts, 1)
	})
	t.Run("user serializer resolves on client built from options", func(t *testing.T) {
		custom := &stubSerializer{}
		config := remote.NewConfig("127.0.0.1", 0, remote.WithSerializers(new(nonProtoMsg), custom))

		cl := NewClient(ClientSerializerOptions(config)...)
		s := cl.Serializer(&nonProtoMsg{"hello"})
		require.NotNil(t, s)
		assert.Same(t, custom, s)
	})
	t.Run("proto default is not forwarded to avoid duplicate registration", func(t *testing.T) {
		custom := &stubSerializer{}
		config := remote.NewConfig("127.0.0.1", 0, remote.WithSerializers(new(nonProtoMsg), custom))

		opts := ClientSerializerOptions(config)
		cl := NewClient(opts...).(*client)

		// The client seeds itself with one proto entry; forwarding must not add
		// a second one, so the total count is proto-default + user entry = 2.
		assert.Len(t, cl.serializers, 2)
	})
	t.Run("multiple user serializers all forwarded", func(t *testing.T) {
		s1 := &stubSerializer{}
		s2 := &stubSerializer{}
		config := remote.NewConfig("127.0.0.1", 0,
			remote.WithSerializers(new(nonProtoMsg), s1),
			remote.WithSerializers((*testInterface)(nil), s2),
		)

		opts := ClientSerializerOptions(config)
		require.Len(t, opts, 2)

		cl := NewClient(opts...)
		assert.Same(t, s1, cl.Serializer(&nonProtoMsg{"x"}))
		assert.Same(t, s2, cl.Serializer(&nonProtoImpl{}))
	})
}
