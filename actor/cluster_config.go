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
	"time"

	"github.com/tochemey/goakt/v3/discovery"
	"github.com/tochemey/goakt/v3/internal/collection"
	"github.com/tochemey/goakt/v3/internal/size"
	"github.com/tochemey/goakt/v3/internal/types"
	"github.com/tochemey/goakt/v3/internal/validation"
)

// defaultKinds defines the default system kinds
var defaultKinds = []Actor{
	new(FuncActor),
}

// ClusterConfig defines the cluster mode settings
type ClusterConfig struct {
	discovery          discovery.Provider
	partitionCount     uint64
	minimumPeersQuorum uint32
	replicaCount       uint32
	writeQuorum        uint32
	readQuorum         uint32
	discoveryPort      int
	peersPort          int
	kinds              *collection.Map[string, Actor]
	tableSize          uint64
	wal                *string
	grains             *collection.Map[string, Grain]
	writeTimeout       time.Duration
	readTimeout        time.Duration
	shutdownTimeout    time.Duration
}

// enforce compilation error
var _ validation.Validator = (*ClusterConfig)(nil)

// NewClusterConfig creates an instance of ClusterConfig
func NewClusterConfig() *ClusterConfig {
	c := &ClusterConfig{
		kinds:              collection.NewMap[string, Actor](),
		grains:             collection.NewMap[string, Grain](),
		minimumPeersQuorum: 1,
		writeQuorum:        1,
		readQuorum:         1,
		replicaCount:       1,
		partitionCount:     271,
		tableSize:          20 * size.MB,
		writeTimeout:       time.Second,
		readTimeout:        time.Second,
		shutdownTimeout:    3 * time.Minute,
	}
	fnActor := new(FuncActor)
	c.kinds.Set(types.Name(fnActor), fnActor)
	return c
}

// WithPartitionCount sets the cluster config partition count.
// Partition count should be a prime number.
// ref: https://medium.com/swlh/why-should-the-length-of-your-hash-table-be-a-prime-number-760ec65a75d1
func (x *ClusterConfig) WithPartitionCount(count uint64) *ClusterConfig {
	x.partitionCount = count
	return x
}

// WithMinimumPeersQuorum sets the cluster config minimum peers quorum
func (x *ClusterConfig) WithMinimumPeersQuorum(minimumQuorum uint32) *ClusterConfig {
	x.minimumPeersQuorum = minimumQuorum
	return x
}

// WithDiscovery sets the cluster discovery provider
func (x *ClusterConfig) WithDiscovery(discovery discovery.Provider) *ClusterConfig {
	x.discovery = discovery
	return x
}

// WithKinds sets the cluster actor kinds
func (x *ClusterConfig) WithKinds(kinds ...Actor) *ClusterConfig {
	for _, kind := range kinds {
		x.kinds.Set(types.Name(kind), kind)
	}
	return x
}

// WithGrains sets the cluster grains
func (x *ClusterConfig) WithGrains(grains ...Grain) *ClusterConfig {
	for _, grain := range grains {
		x.grains.Set(types.Name(grain), grain)
	}
	return x
}

// WithDiscoveryPort sets the discovery port
func (x *ClusterConfig) WithDiscoveryPort(port int) *ClusterConfig {
	x.discoveryPort = port
	return x
}

// WithPeersPort sets the peers port
func (x *ClusterConfig) WithPeersPort(peersPort int) *ClusterConfig {
	x.peersPort = peersPort
	return x
}

// WithReplicaCount sets the cluster replica count.
// Note: set this field means you have some advanced knowledge on quorum-based replica control
func (x *ClusterConfig) WithReplicaCount(count uint32) *ClusterConfig {
	x.replicaCount = count
	return x
}

// WriteQuorum returns the write quorum
func (x *ClusterConfig) WriteQuorum() uint32 {
	return x.writeQuorum
}

// WithWAL sets a custom WAL directory.
// GoAkt is required to have the permission to create this directory.
func (x *ClusterConfig) WithWAL(dir string) *ClusterConfig {
	x.wal = &dir
	return x
}

// WithWriteTimeout sets the write timeout.
// This is the timeout for write operations to the cluster.
func (x *ClusterConfig) WithWriteTimeout(timeout time.Duration) *ClusterConfig {
	x.writeTimeout = timeout
	return x
}

// WithReadTimeout sets the read timeout.
// This is the timeout for read operations to the cluster.
func (x *ClusterConfig) WithReadTimeout(timeout time.Duration) *ClusterConfig {
	x.readTimeout = timeout
	return x
}

// WithShutdownTimeout sets the shutdown timeout.
// This is the timeout for graceful shutdown of the cluster.
// The timeout should be less or proportional to the actor's shutdown timeout to allow a clean graceful shutdown.
func (x *ClusterConfig) WithShutdownTimeout(timeout time.Duration) *ClusterConfig {
	x.shutdownTimeout = timeout
	return x
}

// WriteTimeout returns the write timeout.
// This is the timeout for write operations to the cluster.
func (x *ClusterConfig) WriteTimeout() time.Duration {
	return x.writeTimeout
}

// ReadTimeout returns the read timeout.
// This is the timeout for read operations to the cluster.
func (x *ClusterConfig) ReadTimeout() time.Duration {
	return x.readTimeout
}

// ShutdownTimeout returns the shutdown timeout.
// This is the timeout for graceful shutdown of the cluster.
func (x *ClusterConfig) ShutdownTimeout() time.Duration {
	return x.shutdownTimeout
}

// ReplicaCount returns the replica count.
func (x *ClusterConfig) ReplicaCount() uint32 {
	return x.replicaCount
}

// Discovery returns the discovery provider
func (x *ClusterConfig) Discovery() discovery.Provider {
	return x.discovery
}

// PartitionCount returns the partition count
func (x *ClusterConfig) PartitionCount() uint64 {
	return x.partitionCount
}

// MinimumPeersQuorum returns the minimum peers quorum
func (x *ClusterConfig) MinimumPeersQuorum() uint32 {
	return x.minimumPeersQuorum
}

// DiscoveryPort returns the discovery port
func (x *ClusterConfig) DiscoveryPort() int {
	return x.discoveryPort
}

// PeersPort returns the peers port
func (x *ClusterConfig) PeersPort() int {
	return x.peersPort
}

// Kinds returns the actor kinds
func (x *ClusterConfig) Kinds() []Actor {
	return x.kinds.Values()
}

func (x *ClusterConfig) Grains() []Grain {
	return x.grains.Values()
}

// ReadQuorum returns the read quorum
func (x *ClusterConfig) ReadQuorum() uint32 {
	return x.readQuorum
}

// WithWriteQuorum sets the write quorum
// Note: set this field means you have some advanced knowledge on quorum-based replica control
// The default value should be sufficient for most use cases
func (x *ClusterConfig) WithWriteQuorum(count uint32) *ClusterConfig {
	x.writeQuorum = count
	return x
}

// WithReadQuorum sets the read quorum
// Note: set this field means you have some advanced knowledge on quorum-based replica control
// The default value should be sufficient for most use cases
func (x *ClusterConfig) WithReadQuorum(count uint32) *ClusterConfig {
	x.readQuorum = count
	return x
}

// WithTableSize sets the key/value in-memory storage size
// The default values is 20MB
func (x *ClusterConfig) WithTableSize(size uint64) *ClusterConfig {
	x.tableSize = size
	return x
}

// TableSize returns the cluster storage size
func (x *ClusterConfig) TableSize() uint64 {
	return x.tableSize
}

// WAL returns the WAL directory
func (x *ClusterConfig) WAL() *string {
	return x.wal
}

// Validate validates the cluster config
func (x *ClusterConfig) Validate() error {
	return validation.
		New(validation.AllErrors()).
		AddAssertion(x.discovery != nil, "discovery provider is not set").
		AddAssertion(x.partitionCount > 0, "partition count need to greater than zero").
		AddAssertion(x.minimumPeersQuorum >= 1, "minimum peers quorum must be at least one").
		AddAssertion(x.discoveryPort > 0, "discovery port is invalid").
		AddAssertion(x.peersPort > 0, "peers port is invalid").
		AddAssertion(len(x.kinds.Values()) > 1, "actor kinds are not defined").
		AddAssertion(x.replicaCount >= 1, "cluster replicaCount is invalid").
		AddAssertion(x.writeQuorum >= 1, "cluster writeQuorum is invalid").
		AddAssertion(x.readQuorum >= 1, "cluster readQuorum is invalid").
		Validate()
}
