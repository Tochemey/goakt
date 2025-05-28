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

type schedulerContext struct {
	sender *senderConfig
}

type senderConfig struct {
	sender     *PID
	senderAddr *address.Address
}

func newSenderConfig() *senderConfig {
	return &senderConfig{
		sender:     NoSender,
		senderAddr: address.NoSender(),
	}
}

func (s *senderConfig) Sender() *PID {
	return s.sender
}

func (s *senderConfig) SenderAddr() *address.Address {
	return s.senderAddr
}

type SchedulerContextOption interface {
	// Apply sets the Option value of a config.
	Apply(*schedulerContext)
}

// enforce compilation error
var _ SchedulerContextOption = SchedulerContextOptionFunc(nil)

type SchedulerContextOptionFunc func(*schedulerContext)

func (f SchedulerContextOptionFunc) Apply(c *schedulerContext) {
	f(c)
}

func WithSchedulerSender(sender *senderConfig) SchedulerContextOption {
	return SchedulerContextOptionFunc(func(c *schedulerContext) {
		c.sender = sender
	})
}
