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

package crdt

import "time"

const (
	defaultAntiEntropyInterval = 30 * time.Second
	defaultMaxDeltaSize        = 64 * 1024 // 64KB
	defaultPruneInterval       = 5 * time.Minute
	defaultTombstoneTTL        = 24 * time.Hour
)

// Config holds the configuration for the CRDT Replicator.
type Config struct {
	antiEntropyInterval time.Duration
	maxDeltaSize        int
	pruneInterval       time.Duration
	tombstoneTTL        time.Duration
	role                string
}

// NewConfig creates a Config with default values.
func NewConfig(opts ...Option) *Config {
	c := &Config{
		antiEntropyInterval: defaultAntiEntropyInterval,
		maxDeltaSize:        defaultMaxDeltaSize,
		pruneInterval:       defaultPruneInterval,
		tombstoneTTL:        defaultTombstoneTTL,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// AntiEntropyInterval returns the configured anti-entropy interval.
func (c *Config) AntiEntropyInterval() time.Duration {
	return c.antiEntropyInterval
}

// MaxDeltaSize returns the maximum serialized delta size in bytes.
func (c *Config) MaxDeltaSize() int {
	return c.maxDeltaSize
}

// PruneInterval returns the interval for pruning departed node slots.
func (c *Config) PruneInterval() time.Duration {
	return c.pruneInterval
}

// TombstoneTTL returns the time-to-live for deletion tombstones.
func (c *Config) TombstoneTTL() time.Duration {
	return c.tombstoneTTL
}

// Role returns the optional role filter for CRDT replication.
// An empty string means all cluster nodes participate.
func (c *Config) Role() string {
	return c.role
}

// Option defines a configuration option for the CRDT Replicator.
type Option func(*Config)

// WithAntiEntropyInterval sets the interval between anti-entropy synchronization rounds.
// Anti-entropy is a periodic safety net that ensures all nodes converge even if
// some deltas were lost during TopicActor pub/sub dissemination.
func WithAntiEntropyInterval(duration time.Duration) Option {
	return func(c *Config) {
		c.antiEntropyInterval = duration
	}
}

// WithMaxDeltaSize sets the maximum serialized size in bytes for a single delta
// published via TopicActor. If a delta exceeds this size, the Replicator falls
// back to a full-state transfer for that key.
func WithMaxDeltaSize(size int) Option {
	return func(c *Config) {
		c.maxDeltaSize = size
	}
}

// WithPruneInterval sets the interval for pruning state associated with
// permanently departed cluster nodes.
func WithPruneInterval(duration time.Duration) Option {
	return func(c *Config) {
		c.pruneInterval = duration
	}
}

// WithTombstoneTTL sets the time-to-live for deletion tombstones.
// Tombstones are retained for this duration to ensure that late-arriving
// deltas for deleted keys are correctly discarded.
func WithTombstoneTTL(duration time.Duration) Option {
	return func(c *Config) {
		c.tombstoneTTL = duration
	}
}

// WithRole restricts CRDT replication to cluster nodes that advertise
// the specified role. Only nodes with this role will spawn a Replicator
// and subscribe to CRDT key topics. An empty string means all nodes participate.
func WithRole(role string) Option {
	return func(c *Config) {
		c.role = role
	}
}
