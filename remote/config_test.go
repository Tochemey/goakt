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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/internal/size"
	"github.com/tochemey/goakt/v4/test/data/testpb"
	gtls "github.com/tochemey/goakt/v4/tls"
)

func TestConfig(t *testing.T) {
	t.Run("With default config", func(t *testing.T) {
		config := DefaultConfig()
		require.NoError(t, config.Validate())
		require.NoError(t, config.Sanitize())
		assert.EqualValues(t, 16*size.MB, config.MaxFrameSize())
		assert.Exactly(t, 10*time.Second, config.WriteTimeout())
		assert.Exactly(t, 10*time.Second, config.ReadIdleTimeout())
		assert.Exactly(t, 1200*time.Second, config.IdleTimeout())
		assert.Exactly(t, "127.0.0.1", config.BindAddr())
		assert.Exactly(t, 0, config.BindPort())
		assert.Exactly(t, DefaultMaxIdleConns, config.MaxIdleConns())
		assert.Exactly(t, 5*time.Second, config.DialTimeout())
		assert.Exactly(t, 15*time.Second, config.KeepAlive())
		assert.EqualValues(t, DefaultOrdinaryLanes, config.OrdinaryLanes())
		assert.EqualValues(t, DefaultMaxConcurrentLargeTransfers, config.MaxConcurrentLargeTransfers())
		assert.EqualValues(t, DefaultChunkSize, config.ChunkSize())
		assert.EqualValues(t, DefaultMaxMessageSize, config.MaxMessageSize())
		assert.EqualValues(t, DefaultCreditWindow, config.CreditWindow())
		assert.Nil(t, config.LargeMessageDestinations())
	})
	t.Run("With ordinary lanes and large destinations", func(t *testing.T) {
		config := NewConfig("127.0.0.1", 8080,
			WithOrdinaryLanes(4),
			WithLargeMessageDestinations("orders/*", "*/bulk-ingest"),
			WithMaxConcurrentLargeTransfers(8),
			WithChunkSize(64*size.KB),
			WithMaxMessageSize(32*size.MB),
			WithCreditWindow(2*size.MB),
		)
		require.NoError(t, config.Validate())
		assert.EqualValues(t, 4, config.OrdinaryLanes())
		assert.Equal(t, []string{"orders/*", "*/bulk-ingest"}, config.LargeMessageDestinations())
		assert.EqualValues(t, 8, config.MaxConcurrentLargeTransfers())
		assert.EqualValues(t, 64*size.KB, config.ChunkSize())
		assert.EqualValues(t, 32*size.MB, config.MaxMessageSize())
		assert.EqualValues(t, 2*size.MB, config.CreditWindow())
	})
	t.Run("With creditWindow below chunkSize", func(t *testing.T) {
		config := NewConfig("127.0.0.1", 8080, WithCreditWindow(8*size.KB))
		err := config.Validate()
		require.Error(t, err)
		assert.EqualError(t, err, "creditWindow must be at least chunkSize")
	})
	t.Run("With invalid chunk size", func(t *testing.T) {
		config := NewConfig("127.0.0.1", 8080, WithChunkSize(8*size.KB))
		err := config.Validate()
		require.Error(t, err)
		assert.EqualError(t, err, "chunkSize must be between 16KB and 4MB")
	})
	t.Run("With maxMessageSize below maxFrameSize", func(t *testing.T) {
		config := NewConfig("127.0.0.1", 8080, WithMaxMessageSize(1024))
		err := config.Validate()
		require.Error(t, err)
		assert.EqualError(t, err, "maxMessageSize must be greater than or equal to maxFrameSize")
	})
	t.Run("With invalid ordinary lanes", func(t *testing.T) {
		config := NewConfig("127.0.0.1", 8080, WithOrdinaryLanes(0))
		err := config.Validate()
		require.Error(t, err)
		assert.EqualError(t, err, "ordinaryLanes must be between 1 and 254")
	})
	t.Run("With readIdleTimeout not less than idleTimeout", func(t *testing.T) {
		config := NewConfig("127.0.0.1", 8080, WithReadIdleTimeout(1200*time.Second))
		err := config.Validate()
		require.Error(t, err)
		assert.EqualError(t, err, "readIdleTimeout must be less than idleTimeout when both are set")
	})
	t.Run("With invalid large destination pattern", func(t *testing.T) {
		config := NewConfig("127.0.0.1", 8080, WithLargeMessageDestinations("["))
		err := config.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid largeMessageDestinations pattern")
	})
	t.Run("With config", func(t *testing.T) {
		config := NewConfig("127.0.0.1", 8080, WithReadIdleTimeout(10*time.Second), WithWriteTimeout(10*time.Second))
		require.NoError(t, config.Validate())
		require.NoError(t, config.Sanitize())
		assert.EqualValues(t, 16*size.MB, config.MaxFrameSize())
		assert.Exactly(t, 10*time.Second, config.WriteTimeout())
		assert.Exactly(t, 10*time.Second, config.ReadIdleTimeout())
		assert.Exactly(t, 1200*time.Second, config.IdleTimeout())
		assert.Exactly(t, "127.0.0.1", config.BindAddr())
		assert.Exactly(t, 8080, config.BindPort())
	})
	t.Run("With invalid framesize", func(t *testing.T) {
		config := NewConfig("127.0.0.1", 8080, WithMaxFrameSize(20*size.MB))
		err := config.Validate()
		require.Error(t, err)
		assert.EqualError(t, err, "maxFrameSize must be between 16KB and 16MB")
	})
	t.Run("With invalid bindAddr", func(t *testing.T) {
		config := NewConfig("256.256.256.256", 8080, WithMaxFrameSize(20*size.MB))
		err := config.Sanitize()
		require.Error(t, err)
	})
	t.Run("With_default_serializer_resolves_proto_message", func(t *testing.T) {
		config := DefaultConfig()
		msg := testpb.Reply_builder{Content: "hello"}.Build()
		s := config.Serializer(msg)
		require.NotNil(t, s, "expected default ProtoSerializer for proto.Message")
		_, ok := s.(*ProtoSerializer)
		assert.True(t, ok)
	})
	t.Run("With_custom_concrete_type_serializer", func(t *testing.T) {
		custom := NewProtoSerializer()
		msg := &testpb.Reply{}
		config := NewConfig("127.0.0.1", 0, WithSerializers(msg, custom))
		s := config.Serializer(msg)
		require.NotNil(t, s)
		assert.Same(t, custom, s)
	})
	t.Run("With_interface_serializer_overrides_default", func(t *testing.T) {
		custom := NewProtoSerializer()
		config := NewConfig("127.0.0.1", 0, WithSerializers((*proto.Message)(nil), custom))
		msg := &testpb.Reply{}
		s := config.Serializer(msg)
		require.NotNil(t, s)
	})
	t.Run("With_nil_message_returns_nil_serializer", func(t *testing.T) {
		config := DefaultConfig()
		assert.Nil(t, config.Serializer(nil))
	})
	t.Run("With_nil_serializer_option_is_ignored", func(t *testing.T) {
		config := DefaultConfig()
		// Applying a nil serializer must not remove the default.
		WithSerializers((*proto.Message)(nil), nil).Apply(config)
		require.NotNil(t, config.Serializer(&testpb.Reply{}))
	})
	t.Run("With_unregistered_type_returns_nil_serializer", func(t *testing.T) {
		config := DefaultConfig()
		type unregistered struct{ X int }
		assert.Nil(t, config.Serializer(&unregistered{X: 1}))
	})
	t.Run("With_Serializers_returns_defensive_copy", func(t *testing.T) {
		config := DefaultConfig()
		serializers := config.Serializers()
		require.NotNil(t, serializers)
		require.NotEmpty(t, serializers, "default config has proto serializer")
		// Modifying the returned map must not affect the config
		for k := range serializers {
			delete(serializers, k)
			break
		}
		// Config should still resolve proto messages
		require.NotNil(t, config.Serializer(&testpb.Reply{}))
	})
}

func TestConfigCompression(t *testing.T) {
	t.Run("default is none", func(t *testing.T) {
		config := DefaultConfig()
		assert.Equal(t, NoCompression, config.Compression())
	})
	t.Run("custom compression applied", func(t *testing.T) {
		config := NewConfig("127.0.0.1", 0, WithCompression(GzipCompression))
		assert.Equal(t, GzipCompression, config.Compression())
	})
	t.Run("no compression", func(t *testing.T) {
		config := NewConfig("127.0.0.1", 0, WithCompression(NoCompression))
		assert.Equal(t, NoCompression, config.Compression())
	})
}

func TestConfigContextPropagator(t *testing.T) {
	t.Run("nil by default", func(t *testing.T) {
		config := DefaultConfig()
		assert.Nil(t, config.ContextPropagator())
	})
	t.Run("non-nil after WithContextPropagator", func(t *testing.T) {
		prop := mockPropagator{}
		config := NewConfig("127.0.0.1", 0, WithContextPropagator(prop))
		assert.Equal(t, prop, config.ContextPropagator())
	})
}

func TestConfigTLS(t *testing.T) {
	t.Run("nil by default", func(t *testing.T) {
		config := DefaultConfig()
		assert.Nil(t, config.TLS())
	})
	t.Run("non-nil after WithTLS", func(t *testing.T) {
		info := &gtls.Info{}
		config := NewConfig("127.0.0.1", 0, WithTLS(info))
		assert.Same(t, info, config.TLS())
	})
}
