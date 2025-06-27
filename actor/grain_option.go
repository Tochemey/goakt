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

	"go.uber.org/atomic"
)

type GrainOption func(config *grainConfig)
type grainConfig struct {
	// initMaxRetries is the maximum number of retries when initializing a grain.
	initMaxRetries atomic.Int32
	// initTimeout is the timeout duration for grain initialization.
	initTimeout atomic.Duration
}

func newGrainConfig(opts ...GrainOption) *grainConfig {
	config := &grainConfig{
		initMaxRetries: atomic.Int32{},
		initTimeout:    atomic.Duration{},
	}

	// Set default values
	config.initMaxRetries.Store(DefaultInitMaxRetries)
	config.initTimeout.Store(DefaultInitTimeout)

	for _, opt := range opts {
		opt(config)
	}

	return config
}

// WithGrainInitMaxRetries sets the maximum number of retries
// when initializing a grain. This is useful in case the grain
// initialization fails due to transient errors, allowing the system
// to retry the initialization a specified number of times before giving up.
// The default value is 5 retries.
// Note: This option is only applicable when the grain is being initialized.
func WithGrainInitMaxRetries(value int) GrainOption {
	return func(config *grainConfig) {
		config.initMaxRetries.Store(int32(value))
	}
}

// WithGrainInitTimeout sets the timeout duration for grain initialization.
// If the grain does not initialize within this duration, it will be considered
// as failed and the system will stop retrying.
// The default value is 1 second.
func WithGrainInitTimeout(value time.Duration) GrainOption {
	return func(config *grainConfig) {
		config.initTimeout.Store(value)
	}
}
