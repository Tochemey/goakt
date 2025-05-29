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
	"github.com/google/uuid"

	"github.com/tochemey/goakt/v3/address"
)

type senderConfig struct {
	sender     *PID
	senderAddr *address.Address
	reference  string
}

// newSenderConfig creates and returns a new senderConfig instance using the provided SenderOption arguments.
// Options are applied sequentially to configure the instance.
func newSenderConfig(opts ...SenderOption) *senderConfig {
	config := &senderConfig{
		sender:     NoSender,
		senderAddr: address.NoSender(),
		reference:  uuid.NewString(),
	}

	for _, opt := range opts {
		opt.Apply(config)
	}
	return config
}

// Sender retrieves and returns the associated sender PID from the senderConfig instance.
func (s *senderConfig) Sender() *PID {
	return s.sender
}

// SenderAddr retrieves and returns the sender's address from the senderConfig instance.
func (s *senderConfig) SenderAddr() *address.Address {
	return s.senderAddr
}

// Reference returns the scheduled message reference.
func (s *senderConfig) Reference() string {
	return s.reference
}

// SenderOption defines an interface for applying configuration options to a senderConfig instance
type SenderOption interface {
	// Apply sets the Option value of a config.
	Apply(*senderConfig)
}

// enforce compilation error
var _ SenderOption = SenderOptionFunc(nil)

// SenderOptionFunc is a function type used to configure a senderConfig instance.
// It implements the SenderOption interface by applying modifications to senderConfig.
type SenderOptionFunc func(*senderConfig)

// Apply applies the SenderOptionFunc to the given senderConfig instance, modifying its fields as defined within the function.
func (f SenderOptionFunc) Apply(c *senderConfig) {
	f(c)
}

// WithSender returns a SenderOption that explicitly sets the sender PID for a scheduled message.
//
// This is useful when you want to associate the scheduled message with a specific sender (PID).
//
// Parameters:
//   - sender: The PID of the actor initiating the schedule.
//
// Returns:
//   - SenderOption: An option that can be passed to the scheduler.
func WithSender(sender *PID) SenderOption {
	return SenderOptionFunc(func(c *senderConfig) {
		c.sender = sender
	})
}

// WithSenderAddress returns a SenderOption that explicitly sets the sender address for a scheduled message.
//
// This is typically used for remote scheduling scenarios where the sender is identified by an address
// rather than a local PID. Setting the sender address ensures accurate tracking of the scheduled message,
// especially when multiple distributed nodes are involved.
//
// Parameters:
//   - addr: The address.Address of the remote sender.
//
// Returns:
//   - SenderOption: An option that can be passed to the scheduler.
func WithSenderAddress(addr *address.Address) SenderOption {
	return SenderOptionFunc(func(c *senderConfig) {
		c.senderAddr = addr
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
//   - SenderOption: An option that can be passed to the scheduler to associate the reference ID with the message.
//
// Note:
//   - It's strongly recommended to set a reference ID if you plan to cancel, pause, or resume the message later.
func WithReference(referenceID string) SenderOption {
	return SenderOptionFunc(func(sc *senderConfig) {
		sc.reference = referenceID
	})
}
