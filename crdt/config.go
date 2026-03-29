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
	defaultAntiEntropyInterval           = 30 * time.Second
	defaultMaxDeltaSize                  = 64 * 1024 // 64KB
	defaultPruneInterval                 = 5 * time.Minute
	defaultTombstoneTTL                  = 24 * time.Hour
	defaultCoordinationTimeout           = 5 * time.Second
	defaultSnapshotInterval              = 0 // disabled by default
	defaultDataCenterReplicationInterval = 5 * time.Second
	defaultDataCenterAntiEntropyInterval = 2 * time.Minute
	defaultDataCenterSendTimeout         = 10 * time.Second
)

// Config holds the configuration for the CRDT Replicator.
type Config struct {
	antiEntropyInterval time.Duration
	maxDeltaSize        int
	pruneInterval       time.Duration
	tombstoneTTL        time.Duration
	role                string
	coordinationTimeout time.Duration
	snapshotInterval    time.Duration
	snapshotDir         string

	// data center replication settings
	dataCenterEnabled             bool
	dataCenterReplicationInterval time.Duration
	dataCenterAntiEntropy         bool
	dataCenterAntiEntropyInterval time.Duration
	dataCenterSendTimeout         time.Duration
}

// NewConfig creates a Config with default values.
func NewConfig(opts ...Option) *Config {
	c := &Config{
		antiEntropyInterval:           defaultAntiEntropyInterval,
		maxDeltaSize:                  defaultMaxDeltaSize,
		pruneInterval:                 defaultPruneInterval,
		tombstoneTTL:                  defaultTombstoneTTL,
		coordinationTimeout:           defaultCoordinationTimeout,
		snapshotInterval:              defaultSnapshotInterval,
		dataCenterReplicationInterval: defaultDataCenterReplicationInterval,
		dataCenterAntiEntropyInterval: defaultDataCenterAntiEntropyInterval,
		dataCenterSendTimeout:         defaultDataCenterSendTimeout,
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
// and subscribe to the shared CRDT delta topic. An empty string means all nodes participate.
func WithRole(role string) Option {
	return func(c *Config) {
		c.role = role
	}
}

// CoordinationTimeout returns the timeout for coordinated read/write operations.
func (c *Config) CoordinationTimeout() time.Duration {
	return c.coordinationTimeout
}

// SnapshotInterval returns the interval for periodic CRDT state snapshots.
// A zero value means snapshots are disabled.
func (c *Config) SnapshotInterval() time.Duration {
	return c.snapshotInterval
}

// SnapshotDir returns the directory for CRDT state snapshot files.
// An empty string means snapshots are disabled.
func (c *Config) SnapshotDir() string {
	return c.snapshotDir
}

// WithCoordinationTimeout sets the timeout for coordinated WriteTo/ReadFrom operations.
// Peers that do not respond within this timeout are excluded from the coordination result.
func WithCoordinationTimeout(duration time.Duration) Option {
	return func(c *Config) {
		c.coordinationTimeout = duration
	}
}

// WithSnapshotInterval sets the interval for periodic CRDT state snapshots to BoltDB.
// A zero value disables snapshots. Requires WithSnapshotDir to be set.
func WithSnapshotInterval(duration time.Duration) Option {
	return func(c *Config) {
		c.snapshotInterval = duration
	}
}

// WithSnapshotDir sets the directory for CRDT state snapshot files.
// Snapshots are only enabled when both SnapshotInterval and SnapshotDir are set.
func WithSnapshotDir(dir string) Option {
	return func(c *Config) {
		c.snapshotDir = dir
	}
}

// DataCenterEnabled returns whether cross-datacenter CRDT replication is enabled.
func (c *Config) DataCenterEnabled() bool {
	return c.dataCenterEnabled
}

// DataCenterReplicationInterval returns the interval at which batched deltas
// are flushed to remote datacenters.
func (c *Config) DataCenterReplicationInterval() time.Duration {
	return c.dataCenterReplicationInterval
}

// DataCenterAntiEntropy returns whether cross-datacenter anti-entropy is enabled.
func (c *Config) DataCenterAntiEntropy() bool {
	return c.dataCenterAntiEntropy
}

// DataCenterAntiEntropyInterval returns the interval for cross-datacenter
// anti-entropy digest exchange.
func (c *Config) DataCenterAntiEntropyInterval() time.Duration {
	return c.dataCenterAntiEntropyInterval
}

// WithDataCenterReplication enables cross-datacenter CRDT replication.
// When enabled, the replicator on the cluster leader batches deltas and
// forwards them to replicators on remote datacenters via remoting.
func WithDataCenterReplication() Option {
	return func(c *Config) {
		c.dataCenterEnabled = true
	}
}

// WithDataCenterReplicationInterval sets the interval at which the replicator
// flushes batched deltas to remote datacenters.
func WithDataCenterReplicationInterval(d time.Duration) Option {
	return func(c *Config) {
		c.dataCenterReplicationInterval = d
	}
}

// WithDataCenterAntiEntropy enables periodic cross-datacenter anti-entropy
// digest exchange between replicators.
func WithDataCenterAntiEntropy() Option {
	return func(c *Config) {
		c.dataCenterAntiEntropy = true
	}
}

// WithDataCenterAntiEntropyInterval sets the interval for cross-datacenter
// anti-entropy rounds.
func WithDataCenterAntiEntropyInterval(d time.Duration) Option {
	return func(c *Config) {
		c.dataCenterAntiEntropyInterval = d
	}
}

// DataCenterSendTimeout returns the per-DC timeout for cross-datacenter
// RemoteLookup + RemoteTell operations during delta batch flush.
func (c *Config) DataCenterSendTimeout() time.Duration {
	return c.dataCenterSendTimeout
}

// WithDataCenterSendTimeout sets the per-DC timeout applied to each
// cross-datacenter send attempt (RemoteLookup + RemoteTell).
func WithDataCenterSendTimeout(d time.Duration) Option {
	return func(c *Config) {
		c.dataCenterSendTimeout = d
	}
}
