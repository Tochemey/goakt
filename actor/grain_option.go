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
	initTimeout     atomic.Duration
	deactivateAfter time.Duration
}

// newGrainConfig creates a new grainConfig instance and applies the provided GrainOption(s).
// It sets default values for initialization retries and timeout, and allows customization
// through functional options. This function is typically used internally to configure
// grain initialization and passivation behavior.
//
// Parameters:
//   - opts: zero or more GrainOption functions to customize the grainConfig.
//
// Returns:
//   - *grainConfig: a pointer to the configured grainConfig instance.
func newGrainConfig(opts ...GrainOption) *grainConfig {
	config := &grainConfig{
		initMaxRetries:  atomic.Int32{},
		initTimeout:     atomic.Duration{},
		deactivateAfter: DefaultPassivationTimeout,
	}

	// Set default values
	config.initMaxRetries.Store(DefaultInitMaxRetries)
	config.initTimeout.Store(DefaultInitTimeout)

	for _, opt := range opts {
		opt(config)
	}

	return config
}

// WithGrainInitMaxRetries returns a GrainOption that sets the maximum number of retries
// for grain initialization. This is useful for handling transient initialization errors
// by retrying the initialization process up to the specified number of times before giving up.
//
// Parameters:
//   - value: the maximum number of retries (default is 5).
//
// Returns:
//   - GrainOption: a function that sets the initMaxRetries field in grainConfig.
//
// Usage example:
//
//	cfg := newGrainConfig(WithGrainInitMaxRetries(10))
func WithGrainInitMaxRetries(value int) GrainOption {
	return func(config *grainConfig) {
		config.initMaxRetries.Store(int32(value))
	}
}

// WithGrainInitTimeout returns a GrainOption that sets the timeout duration for grain initialization.
// If the grain does not initialize within this duration, initialization is considered failed and
// no further retries will be attempted.
//
// Parameters:
//   - value: the timeout duration (default is 1 second).
//
// Returns:
//   - GrainOption: a function that sets the initTimeout.
//
// Usage example:
//
//	WithGrainInitTimeout(2 * time.Second)
func WithGrainInitTimeout(value time.Duration) GrainOption {
	return func(config *grainConfig) {
		config.initTimeout.Store(value)
	}
}

// WithGrainDeactivateAfter returns a GrainOption that sets the duration after which a grain
// will be deactivated if it remains idle. This helps manage resources by deactivating grains
// that are not in use, reducing memory usage and improving system performance.
//
// Parameters:
//   - value: the duration of inactivity after which the grain is deactivated (default is 2 minutes).
//
// Returns:
//   - GrainOption: a function that sets the deactivateAfter.
func WithGrainDeactivateAfter(value time.Duration) GrainOption {
	return func(config *grainConfig) {
		config.deactivateAfter = value
	}
}

// WithLongLivedGrain returns a GrainOption that configures the grain to never be deactivated
// due to inactivity. When this option is set, the grain will remain active in memory
// regardless of idle time, and the passivation timer is effectively disabled.
//
// This is useful for grains that must always be available, such as those managing
// critical state, coordinating long-running workflows, or acting as singletons.
//
// Note: Use this option judiciously, as long-lived grains consume system resources
// for their entire lifetime and are not subject to automatic passivation.
func WithLongLivedGrain() GrainOption {
	return func(config *grainConfig) {
		config.deactivateAfter = -1
	}
}
