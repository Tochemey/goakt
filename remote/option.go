/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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

package remote

import "time"

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
