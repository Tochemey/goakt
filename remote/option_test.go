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
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/test/data/testpb"
)

func TestOption(t *testing.T) {
	testCases := []struct {
		name     string
		option   Option
		expected Config
	}{
		{
			name:     "WithWriteTimeout",
			option:   WithWriteTimeout(10 * time.Second),
			expected: Config{writeTimeout: 10 * time.Second},
		},
		{
			name:     "WithReadIdleTimeout",
			option:   WithReadIdleTimeout(10 * time.Second),
			expected: Config{readIdleTimeout: 10 * time.Second},
		},
		{
			name:     "WithMaxFrameSize",
			option:   WithMaxFrameSize(1024),
			expected: Config{maxFrameSize: 1024},
		},
		{
			name:   "WithCompression",
			option: WithCompression(GzipCompression),
			expected: Config{
				compression: GzipCompression,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var config Config
			tc.option.Apply(&config)
			assert.Equal(t, tc.expected, config)
		})
	}
}

func TestWithContextPropagator(t *testing.T) {
	t.Run("nil propagator ignored", func(t *testing.T) {
		config := DefaultConfig()
		WithContextPropagator(nil).Apply(config)
		assert.Nil(t, config.contextPropagator)
	})

	t.Run("non-nil propagator applied", func(t *testing.T) {
		config := DefaultConfig()
		prop := mockPropagator{}
		WithContextPropagator(prop).Apply(config)
		assert.Equal(t, prop, config.contextPropagator)
	})
}

func TestWithSerializers(t *testing.T) {
	t.Run("interface registration", func(t *testing.T) {
		custom := NewProtoSerializer()
		config := NewConfig("127.0.0.1", 0, WithSerializers((*proto.Message)(nil), custom))
		s := config.Serializer(&testpb.Reply{})
		require.NotNil(t, s)
		assert.Same(t, custom, s)
	})

	t.Run("concrete type registration", func(t *testing.T) {
		config := &Config{serializers: make(map[reflect.Type]Serializer)}
		msg := &testpb.Reply{}
		ser := NewProtoSerializer()
		WithSerializers(msg, ser).Apply(config)
		s := config.Serializer(msg)
		require.NotNil(t, s)
		assert.Same(t, ser, s)
	})

	t.Run("nil serializer is ignored", func(t *testing.T) {
		config := DefaultConfig()
		WithSerializers(&testpb.Reply{}, nil).Apply(config)
		// Default proto serializer should still be present
		require.NotNil(t, config.Serializer(&testpb.Reply{}))
	})
}
