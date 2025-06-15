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

package actor

import (
	"time"
)

// GrainOption is the interface that applies to a Grain during activation
type GrainOption interface {
	// Apply sets the Option value of a config.
	Apply(config *grainOptConfig)
}

// This is a compile-time assertion to ensure that grainOption implements GrainOption.
var _ GrainOption = grainOption(nil)

// grainOption implements the GrainOption interface.
type grainOption func(config *grainOptConfig)

// Apply sets the Option value of a config.
func (f grainOption) Apply(c *grainOptConfig) {
	f(c)
}

type grainOptConfig struct {
	// specifies the request timeout
	requestTimeout time.Duration
	// specifies the request sender
	sender *Identity
}

// RequestTimeout returns the configured request timeout for the Grain.
//
// This value determines the maximum duration the Grain will wait for a request to complete.
// If not explicitly set, it defaults to 5 minutes.
func (g *grainOptConfig) RequestTimeout() time.Duration {
	return g.requestTimeout
}

// RequestSender returns the sender of the request.
func (g *grainOptConfig) RequestSender() *Identity {
	return g.sender
}

// WithRequestTimeout returns a GrainOption that sets the request timeout for the Grain.
//
// This option allows you to specify the maximum duration the Grain will wait for a request to complete.
// If the provided timeout is less than or equal to zero, a default timeout of 5 minutes will be used.
//
// Parameters:
//   - timeout: the duration to use as the request timeout.
//
// Returns:
//   - A GrainOption that sets the grain's request timeout.
func WithRequestTimeout(timeout time.Duration) GrainOption {
	return grainOption(func(config *grainOptConfig) {
		if timeout <= 0 {
			timeout = 5 * time.Minute // default timeout
		}
		config.requestTimeout = timeout
	})
}

// WithRequestSender returns a GrainOption that sets the sender for the Grain.
// This option allows you to specify the Identity of the sender that will be used
// when sending messages to the Grain. This is useful for tracking the origin of messages
// and for implementing features like request tracing or logging.
//
// Parameters:
//   - sender: a pointer to an Identity instance representing the sender of the Grain request.
//
// Returns:
//   - A GrainOption that sets the grain's sender.
func WithRequestSender(sender *Identity) GrainOption {
	return grainOption(func(config *grainOptConfig) {
		config.sender = sender
	})
}

// newGrainOptConfig creates an instance of grainConfig
func newGrainOptConfig(opts ...GrainOption) *grainOptConfig {
	config := &grainOptConfig{
		requestTimeout: 5 * time.Minute,
	}
	for _, opt := range opts {
		opt.Apply(config)
	}
	return config
}
