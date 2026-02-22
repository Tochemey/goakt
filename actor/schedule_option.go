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

type scheduleConfig struct {
	sender    *PID
	reference string
}

// newScheduleConfig creates and returns a new scheduleConfig instance using the provided ScheduleOption arguments.
// Options are applied sequentially to configure the instance.
func newScheduleConfig(opts ...ScheduleOption) *scheduleConfig {
	config := &scheduleConfig{
		reference: uuid.NewString(),
	}

	for _, opt := range opts {
		opt.Apply(config)
	}
	return config
}

// Sender retrieves and returns the associated sender PID from the scheduleConfig instance.
func (s *scheduleConfig) Sender() *PID {
	return s.sender
}

// Reference returns the scheduled message reference.
func (s *scheduleConfig) Reference() string {
	return s.reference
}

// ScheduleOption defines an interface for applying configuration options to a scheduleConfig instance
type ScheduleOption interface {
	// Apply sets the Option value of a config.
	Apply(*scheduleConfig)
}

// enforce compilation error
var _ ScheduleOption = ScheduleOptionFunc(nil)

// ScheduleOptionFunc is a function type used to configure a scheduleConfig instance.
// It implements the ScheduleOption interface by applying modifications to scheduleConfig.
type ScheduleOptionFunc func(*scheduleConfig)

// Apply applies the ScheduleOptionFunc to the given scheduleConfig instance, modifying its fields as defined within the function.
func (f ScheduleOptionFunc) Apply(c *scheduleConfig) {
	f(c)
}

// WithSender returns a ScheduleOption that explicitly sets the sender PID for a scheduled message.
//
// This is useful when you want to associate the scheduled message with a specific sender (PID).
//
// Parameters:
//   - sender: The PID of the actor initiating the schedule.
//
// Returns:
//   - ScheduleOption: An option that can be passed to the scheduler.
func WithSender(sender *PID) ScheduleOption {
	return ScheduleOptionFunc(func(c *scheduleConfig) {
		c.sender = sender
	})
}

// WithReference sets a custom reference ID for the scheduled message.
//
// This reference ID uniquely identifies the scheduled message and can be used later to manage it,
// such as canceling, pausing, or resuming the message.
//
// If no reference ID is explicitly set using this option, the scheduler will generate an automatic reference internally.
// However, omitting a reference may make it impossible to manage the message later, as you'll lack a consistent identifier.
//
// Parameters:
//   - referenceID: A user-defined unique identifier for the scheduled message.
//
// Returns:
//   - ScheduleOption: An option that can be passed to the scheduler to associate the reference ID with the message.
//
// Note:
//   - It's strongly recommended to set a reference ID if you plan to cancel, pause, or resume the message later.
func WithReference(referenceID string) ScheduleOption {
	return ScheduleOptionFunc(func(sc *scheduleConfig) {
		sc.reference = referenceID
	})
}
