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

package actor

import (
	"github.com/google/uuid"
)

// grainTimerConfig carries the settings a grain timer is registered with.
type grainTimerConfig struct {
	reference string
	keepAlive bool
}

// newGrainTimerConfig creates a grainTimerConfig with an auto-generated reference
// and applies the provided options in order.
func newGrainTimerConfig(opts ...GrainTimerOption) *grainTimerConfig {
	config := &grainTimerConfig{
		reference: uuid.NewString(),
	}

	for _, opt := range opts {
		opt.Apply(config)
	}
	return config
}

// GrainTimerOption configures a grain timer at registration time.
type GrainTimerOption interface {
	// Apply sets the option value on the given config.
	Apply(*grainTimerConfig)
}

// enforce compilation error
var _ GrainTimerOption = GrainTimerOptionFunc(nil)

// GrainTimerOptionFunc is a function type that implements the GrainTimerOption interface.
type GrainTimerOptionFunc func(*grainTimerConfig)

// Apply applies the GrainTimerOptionFunc to the given grainTimerConfig instance.
func (f GrainTimerOptionFunc) Apply(c *grainTimerConfig) {
	f(c)
}

// WithTimerReference sets a custom reference for a grain timer.
//
// The reference identifies the timer within its grain and is the handle used to
// cancel it. References are scoped per grain, so two grains can use the same
// reference without interfering with each other; within one grain, registering a
// timer under a reference that is already in use cancels and replaces the
// existing timer.
//
// Without this option an automatic reference is generated and returned by the
// schedule call.
func WithTimerReference(reference string) GrainTimerOption {
	return GrainTimerOptionFunc(func(c *grainTimerConfig) {
		c.reference = reference
	})
}

// WithTimerKeepAlive marks the timer's ticks as passivation activity.
//
// By default a timer tick does not reset the grain's passivation clock, so a
// grain that only ever receives timer ticks still passivates on schedule. With
// this option each tick counts as activity and keeps the grain alive, which
// effectively prevents passivation while the timer's period is shorter than the
// grain's passivation timeout.
func WithTimerKeepAlive() GrainTimerOption {
	return GrainTimerOptionFunc(func(c *grainTimerConfig) {
		c.keepAlive = true
	})
}
