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
	"sync"
	"time"

	"github.com/tochemey/goakt/v4/internal/types"
)

// sharedCBOR is a lazy-initialized singleton for WithSerializables.
// CBORSerializer is stateless and safe for concurrent use; reusing it
// avoids per-option allocations.
var (
	sharedCBOR     *CBORSerializer
	sharedCBOROnce sync.Once
)

// DefaultCBORSerializer returns a shared [CBORSerializer] instance.
// CBORSerializer is stateless and safe for concurrent use; reusing it
// avoids per-option allocations when using [WithSerializables] or
// [WithClientSerializables].
func DefaultCBORSerializer() *CBORSerializer {
	sharedCBOROnce.Do(func() { sharedCBOR = NewCBORSerializer() })
	return sharedCBOR
}

// Option is the interface that applies a configuration option.
type Option interface {
	// Apply sets the Option value of a config.
	Apply(*Config)
}

// enforce compilation error
var _ Option = OptionFunc(nil)

// OptionFunc implements the Option interface.
type OptionFunc func(config *Config)

func (f OptionFunc) Apply(c *Config) {
	f(c)
}

// WithWriteTimeout sets the write timeout
func WithWriteTimeout(timeout time.Duration) Option {
	return OptionFunc(func(config *Config) {
		config.writeTimeout = timeout
	})
}

// WithReadIdleTimeout sets the read timeout
// ReadIdleTimeout is the timeout after which a health check using a ping
// frame will be carried out if no frame is received on the connection.
// If zero, no health check is performed.
func WithReadIdleTimeout(timeout time.Duration) Option {
	return OptionFunc(func(config *Config) {
		config.readIdleTimeout = timeout
	})
}

// WithMaxFrameSize specifies the largest frame
// this server is willing to read. A valid value is between
// 16k and 16M, inclusive. If zero or otherwise invalid, an error will be thrown.
func WithMaxFrameSize(size uint32) Option {
	return OptionFunc(func(config *Config) {
		config.maxFrameSize = size
	})
}

// WithCompression sets the compression algorithm to use
// when sending or receiving data.
func WithCompression(c Compression) Option {
	return OptionFunc(func(config *Config) {
		config.compression = c
	})
}

// WithContextPropagator sets the ContextPropagator used to inject and extract
// cross-cutting metadata (e.g., custom headers, correlation IDs, auth tokens)
// for remote calls.
//
// Passing a non-nil propagator enables propagation across process boundaries,
// ensuring values from a context are serialized into headers on outgoing calls
// and restored into the context on incoming calls.
// If propagator is nil, this option is ignored and the default/no-op propagator
// remains in effect.
//
// Typical use:
//   - Integrate distributed tracing (e.g., OpenTelemetry) by providing a propagator
//     implementation that injects/extracts trace context.
//   - Forward request-scoped metadata like user/session IDs or feature flags.
//
// Note: Only non-nil propagators are applied.
// Multiple calls will overwrite the previous propagator with the last non-nil value.
func WithContextPropagator(propagator ContextPropagator) Option {
	return OptionFunc(func(config *Config) {
		if propagator != nil {
			config.contextPropagator = propagator
		}
	})
}

// WithSerializers registers a [Serializer] for a specific message type or for
// all messages that satisfy a given interface.
//
// # Concrete type registration
//
// Pass any value of the target type to bind a serializer to that exact type:
//
//	WithSerializers(new(MyMessage), mySerializer)
//
// When the serializer is [CBORSerializer] and the type is not a [proto.Message],
// the type is automatically registered in the global type registry used for
// CBOR serialization. No separate registration step is required.
//
// # Interface registration
//
// Pass a typed nil pointer to an interface to bind a serializer to every
// message that implements that interface:
//
//	WithSerializers((*proto.Message)(nil), remote.NewProtoSerializer())
//
// # Dispatch order
//
// When [Config.Serializer] resolves a serializer for a message it checks, in order:
//  1. Exact concrete type — the entry registered with the message's dynamic type.
//  2. Interface match — the first registered interface the message implements.
//
// Registration order within each category determines priority.
// If serializer is nil the option is silently ignored.
//
// The default configuration registers [ProtoSerializer] for all [proto.Message]
// implementations. Calling this option with a typed nil pointer to
// [proto.Message] overrides that default for proto messages.
func WithSerializers(msg any, serializer Serializer) Option {
	return OptionFunc(func(config *Config) {
		if serializer == nil {
			return
		}

		typ := reflect.TypeOf(msg)
		// A typed nil pointer whose element is an interface (e.g. (*proto.Message)(nil))
		// registers the serializer for all values that implement that interface.
		if typ != nil && typ.Kind() == reflect.Ptr && typ.Elem().Kind() == reflect.Interface {
			config.serializers[typ.Elem()] = serializer
			return
		}

		types.RegisterSerializerType(msg, serializer)
		config.serializers[reflect.TypeOf(msg)] = serializer
	})
}

// WithSerializables registers the CBOR serializer for each of the given concrete
// or interface types. It is a convenience for registering multiple types with
// [CBORSerializer] without repeating the serializer instance.
//
// # Concrete type registration
//
// Pass any value of the target type to bind the CBOR serializer to that exact type:
//
//	WithSerializables(new(MyMessage), new(OtherMessage))
//
// Each concrete type is automatically registered in the global type registry used
// for CBOR serialization. No separate registration step is required.
//
// # Interface registration
//
// Pass a typed nil pointer to an interface to bind the CBOR serializer to every
// message that implements that interface:
//
//	WithSerializables((*MyInterface)(nil))
//
// # Dispatch order
//
// When [Config.Serializer] resolves a serializer for a message it checks, in order:
//  1. Exact concrete type — the entry registered with the message's dynamic type.
//  2. Interface match — the first registered interface the message implements.
//
// Nil entries in the types slice are silently ignored.
func WithSerializables(msgs ...any) Option {
	cbor := DefaultCBORSerializer()
	return OptionFunc(func(config *Config) {
		for _, msg := range msgs {
			if msg == nil {
				continue
			}
			typ := reflect.TypeOf(msg)
			if typ == nil {
				continue
			}
			// A typed nil pointer whose element is an interface (e.g. (*MyInterface)(nil))
			// registers the serializer for all values that implement that interface.
			if typ.Kind() == reflect.Ptr && typ.Elem().Kind() == reflect.Interface {
				config.serializers[typ.Elem()] = cbor
				continue
			}
			// Concrete type — register in global registry and config
			types.RegisterSerializerType(msg, cbor)
			config.serializers[typ] = cbor
		}
	})
}
