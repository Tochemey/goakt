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

package testkit

import "google.golang.org/protobuf/proto"

type spawnConfig struct {
	// specifies the actor initial state
	initialState proto.Message
}

// SpawnOption is the interface that applies to
type SpawnOption interface {
	// Apply sets the Option value of a config.
	Apply(config *spawnConfig)
}

var _ SpawnOption = spawnOption(nil)

// spawnOption implements the SpawnOption interface.
type spawnOption func(config *spawnConfig)

// Apply sets the Option value of a config.
func (f spawnOption) Apply(c *spawnConfig) {
	f(c)
}

// WithInitialState sets the initial state for the actor during the spawning process.
//
// This option is typically used when spawning a persistent actor (entity) that requires
// a predefined state at startup, such as when restoring from a snapshot or initializing
// with domain defaults. The provided state must be a protocol buffer message representing
// the actor's domain model.
//
// If the actor system supports persistence, this state will be treated as the baseline
// until a snapshot is persisted.
//
// Should be passed to SpawnEntity or other compatible spawn functions as a SpawnOption.
func WithInitialState(state proto.Message) SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.initialState = state
	})
}
