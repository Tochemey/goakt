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

import "github.com/tochemey/goakt/v3/address"

type senderConfig struct {
	sender     *PID
	senderAddr *address.Address
}

// newSenderConfig creates and returns a new senderConfig instance using the provided SenderOption arguments.
// Options are applied sequentially to configure the instance.
func newSenderConfig(opts ...SenderOption) *senderConfig {
	config := &senderConfig{
		sender:     NoSender,
		senderAddr: address.NoSender(),
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

// WithSender returns a SenderOption that sets the sender.
func WithSender(sender *PID) SenderOption {
	return SenderOptionFunc(func(c *senderConfig) {
		c.sender = sender
	})
}

// WithSenderAddress sets the sender address and returns a SenderOption.
func WithSenderAddress(addr *address.Address) SenderOption {
	return SenderOptionFunc(func(c *senderConfig) {
		c.senderAddr = addr
	})
}
