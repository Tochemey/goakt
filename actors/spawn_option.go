/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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

package actors

// spawnConfig defines the configuration to apply when creating an actor
type spawnConfig struct {
	// mailbox defines the mailbox to use when spawning the actor
	mailbox Mailbox
}

// newSpawnConfig creates an instance of spawnConfig
func newSpawnConfig(opts ...SpawnOption) *spawnConfig {
	config := new(spawnConfig)
	for _, opt := range opts {
		opt.Apply(config)
	}
	return config
}

// SpawnOption is the interface that applies to
type SpawnOption interface {
	// Apply sets the Option value of a config.
	Apply(config *spawnConfig)
}

var _ SpawnOption = spawnOption(nil)

// funcOption implements the FuncOption interface.
type spawnOption func(config *spawnConfig)

// Apply implementation
func (f spawnOption) Apply(c *spawnConfig) {
	f(c)
}

// WithMailbox sets the mailbox to use when starting the given actor
func WithMailbox(mailbox Mailbox) SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.mailbox = mailbox
	})
}
