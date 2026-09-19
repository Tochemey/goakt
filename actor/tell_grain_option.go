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

// TellGrainOption configures a single TellGrain call.
//
// Unlike the other option types of this package it receives the configuration
// by value and returns the updated one: TellGrain is a hot path, and an option
// that took a pointer would force the configuration onto the heap on every
// call, since the compiler cannot see through the option's indirect call.
type TellGrainOption func(tellGrainConfig) tellGrainConfig

// tellGrainConfig holds the per-call settings of TellGrain.
type tellGrainConfig struct {
	// isOneWay makes the call return once the message is enqueued in the
	// grain mailbox. No acknowledgement is awaited from the grain and a
	// failure reported by the handler is not surfaced to the caller.
	isOneWay bool
}

// newTellGrainConfig applies opts to a zero config and returns it.
func newTellGrainConfig(opts ...TellGrainOption) tellGrainConfig {
	var config tellGrainConfig
	for _, opt := range opts {
		config = opt(config)
	}
	return config
}

// WithOneWay makes TellGrain fire-and-forget: the call returns as soon as the
// message is enqueued in the grain mailbox, locally or on the owning node,
// without waiting for the grain to process it.
//
// Errors that occur before the enqueue are still returned: an invalid
// identity, a stopped system, an activation failure, a full bounded mailbox
// or a transport failure toward a remote owner. Errors reported by the
// handler through Err or Unhandled are dropped, and a panic in OnReceive is
// only logged.
func WithOneWay() TellGrainOption {
	return func(config tellGrainConfig) tellGrainConfig {
		config.isOneWay = true
		return config
	}
}
