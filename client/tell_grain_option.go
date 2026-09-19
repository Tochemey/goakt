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

package client

// TellGrainOption configures a single TellGrain call. It receives the
// configuration by value and returns the updated one.
type TellGrainOption func(tellGrainConfig) tellGrainConfig

// tellGrainConfig holds the per-call settings of TellGrain.
type tellGrainConfig struct {
	// isOneWay makes the node hosting the grain answer once the message is
	// enqueued in the grain mailbox instead of after the grain processed it.
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

// WithOneWay makes TellGrain fire-and-forget: the call returns once the node
// hosting the grain has enqueued the message, without waiting for the grain to
// process it. Transport failures and an enqueue failure reported by that node,
// such as a full bounded mailbox, are still returned. A failure the grain
// handler reports never reaches the caller: the hosting node records it as a
// deadletter with the grain as receiver.
func WithOneWay() TellGrainOption {
	return func(config tellGrainConfig) tellGrainConfig {
		config.isOneWay = true
		return config
	}
}
