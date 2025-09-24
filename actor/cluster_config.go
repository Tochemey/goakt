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
	"github.com/tochemey/goakt/v3/internal/registry"
	"github.com/tochemey/goakt/v3/internal/size"
	"github.com/tochemey/goakt/v3/internal/validation"
)

// ClusterConfig defines the cluster mode settings
type ClusterConfig struct {
	discovery                discovery.Provider
	partitionCount           uint64
	minimumPeersQuorum       uint32
	replicaCount             uint32
	writeQuorum              uint32
	readQuorum               uint32
	discoveryPort            int
	peersPort                int
	kinds                    *collection.Map[string, Actor]
	grains                   *collection.Map[string, Grain]
	tableSize                uint64
	wal                      *string
	writeTimeout             time.Duration
	readTimeout              time.Duration
	shutdownTimeout          time.Duration
	bootstrapTimeout         time.Duration
	clusterStateSyncInterval time.Duration
}

// enforce compilation error
var _ validation.Validator = (*ClusterConfig)(nil)

// NewClusterConfig creates an instance of ClusterConfig
func NewClusterConfig() *ClusterConfig {
	config := &ClusterConfig{
		kinds:                    collection.NewMap[string, Actor](),
		grains:                   collection.NewMap[string, Grain](),
		minimumPeersQuorum:       1,
		writeQuorum:              1,
		readQuorum:               1,
		replicaCount:             1,
		partitionCount:           271,
		tableSize:                20 * size.MB,
		writeTimeout:             time.Second,
		readTimeout:              time.Second,
		shutdownTimeout:          3 * time.Minute,
		bootstrapTimeout:         DefaultClusterBootstrapTimeout,
		clusterStateSyncInterval: DefaultClusterStateSyncInterval,
	}

	fnActor := new(FuncActor)
	config.kinds.Set(registry.Name(fnActor), fnActor)
	return config
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
		x.kinds.Set(registry.Name(kind), kind)
	}
	return x
}

// WithGrains sets the cluster grains
func (x *ClusterConfig) WithGrains(grains ...Grain) *ClusterConfig {
	for _, grain := range grains {
		x.grains.Set(registry.Name(grain), grain)
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

// WithWriteTimeout sets the write timeout for cluster write operations.
//
// This timeout specifies the maximum duration allowed for a write operation to complete
// before it is considered failed. If a write operation exceeds this duration, it will be
// aborted and an error will be returned. Adjust this value based on your cluster's expected
// workload and network conditions to balance responsiveness and reliability.
//
// Example:
//
//	cfg := NewClusterConfig().WithWriteTimeout(2 * time.Second)
//
// Returns the updated ClusterConfig instance for chaining.
func (x *ClusterConfig) WithWriteTimeout(timeout time.Duration) *ClusterConfig {
	x.writeTimeout = timeout
	return x
}

// WithReadTimeout sets the read timeout for cluster read operations.
//
// This timeout specifies the maximum duration allowed for a read operation to complete
// before it is considered failed. If a read operation exceeds this duration, it will be
// aborted and an error will be returned. Adjust this value based on your cluster's expected
// workload and network conditions to balance responsiveness and reliability.
//
// Example:
//
//	cfg := NewClusterConfig().WithReadTimeout(2 * time.Second)
//
// Returns the updated ClusterConfig instance for chaining.
func (x *ClusterConfig) WithReadTimeout(timeout time.Duration) *ClusterConfig {
	x.readTimeout = timeout
	return x
}

// WithShutdownTimeout sets the timeout for graceful cluster shutdown.
//
// This timeout determines the maximum duration allowed for the cluster to shut down gracefully.
// It should be less than or proportional to the actor's shutdown timeout to ensure a clean shutdown
// process. If the shutdown process exceeds this duration, it may be forcibly terminated.
//
// Example:
//
//	cfg := NewClusterConfig().WithShutdownTimeout(1 * time.Minute)
//
// Returns the updated ClusterConfig instance for chaining.
func (x *ClusterConfig) WithShutdownTimeout(timeout time.Duration) *ClusterConfig {
	x.shutdownTimeout = timeout
	return x
}

// WithBootstrapTimeout sets the timeout for the cluster bootstrap process.
//
// This timeout determines the maximum duration the cluster will wait for all
// required nodes to join and complete the bootstrap sequence before considering
// the operation as failed. If the cluster does not bootstrap within this period,
// an error will be returned and the cluster will not start.
//
// Use this option to control startup responsiveness in environments where
// cluster formation speed is critical, such as automated deployments or
// orchestrated environments. The default value is 10 seconds.
//
// Example usage:
//
//	cfg := NewClusterConfig().WithBootstrapTimeout(15 * time.Second)
//
// Returns the updated ClusterConfig instance for chaining.
func (x *ClusterConfig) WithBootstrapTimeout(timeout time.Duration) *ClusterConfig {
	x.bootstrapTimeout = timeout
	return x
}

// WithClusterStateSyncInterval sets the interval for syncing nodes' routing tables.
//
// This interval determines how frequently the cluster synchronizes its routing tables
// across all nodes. Regular synchronization ensures that each node has an up-to-date
// view of the cluster topology, which is essential for accurate message routing and
// partition management.
//
// It is important to set this interval to a value greater than the write timeout to
// avoid updating the routing table while a write operation is in progress. Setting
// the interval too low may increase network and processing overhead, while setting it
// too high may delay the propagation of cluster topology changes.
//
// The default value is 1 minute, which provides a balance between consistency and
// resource usage. Adjust this value based on your cluster's size, network
// characteristics, and desired responsiveness.
//
// Example usage:
//
//	cfg := NewClusterConfig().WithClusterStateSyncInterval(2 * time.Minute)
//
// Returns the updated ClusterConfig instance for chaining.
func (x *ClusterConfig) WithClusterStateSyncInterval(interval time.Duration) *ClusterConfig {
	x.clusterStateSyncInterval = interval
	return x
}

// ClusterStateSyncInterval returns the interval at which the cluster synchronizes its routing tables across all nodes.
//
// This interval determines how frequently the cluster updates its internal routing information to reflect changes
// in topology, such as node joins or departures. Keeping routing tables up-to-date is essential for accurate message
// delivery and partition management.
//
// It is recommended to set this interval to a value greater than the write timeout to avoid updating routing tables
// during ongoing write operations. A shorter interval increases consistency and responsiveness to cluster changes,
// but may introduce additional network and processing overhead. Conversely, a longer interval reduces overhead but
// may delay the propagation of topology changes.
//
// The default value is 1 minute. Adjust this value based on your cluster's size, network characteristics, and
// required responsiveness.
//
// Example:
//
//	cfg := NewClusterConfig().WithClusterStateSyncInterval(2 * time.Minute)
//
// Returns the configured sync interval.
func (x *ClusterConfig) ClusterStateSyncInterval() time.Duration {
	return x.clusterStateSyncInterval
}

// BootstrapTimeout returns the maximum duration the cluster will wait for all required nodes to join and complete
// the bootstrap sequence before considering the operation as failed.
//
// If the cluster does not bootstrap within this timeout, an error is returned and the cluster will not start.
// This setting is useful for controlling startup responsiveness, especially in automated or orchestrated environments
// where rapid cluster formation is critical.
//
// The default value is 10 seconds. Increase this value if your environment has slow node startups or network delays.
//
// Example:
//
//	cfg := NewClusterConfig().WithBootstrapTimeout(15 * time.Second)
//
// Returns the configured bootstrap timeout.
func (x *ClusterConfig) BootstrapTimeout() time.Duration {
	return x.bootstrapTimeout
}

// WriteTimeout returns the configured write timeout for cluster write operations.
//
// This value represents the maximum duration a write operation is allowed to take before
// being considered failed. Use this to tune the responsiveness and reliability of write
// operations in your cluster.
//
// Returns the write timeout as a time.Duration.
func (x *ClusterConfig) WriteTimeout() time.Duration {
	return x.writeTimeout
}

// ReadTimeout returns the configured read timeout for cluster read operations.
//
// This value represents the maximum duration a read operation is allowed to take before
// being considered failed. Use this to tune the responsiveness and reliability of read
// operations in your cluster.
//
// Returns the read timeout as a time.Duration.
func (x *ClusterConfig) ReadTimeout() time.Duration {
	return x.readTimeout
}

// ShutdownTimeout returns the configured timeout for graceful cluster shutdown.
//
// This value determines how long the cluster will wait for all shutdown operations to
// complete before forcing termination. Adjust this value to ensure a clean and orderly
// shutdown process.
//
// Returns the shutdown timeout as a time.Duration.
func (x *ClusterConfig) ShutdownTimeout() time.Duration {
	return x.shutdownTimeout
}

// ReplicaCount returns the configured number of replicas for the cluster.
//
// This value determines how many copies of each partition are maintained across the cluster
// for redundancy and fault tolerance. Increasing the replica count improves data availability
// but may increase resource usage.
//
// Returns the replica count as a uint32.
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
		AddAssertion(len(x.kinds.Values()) > 1 || len(x.grains.Values()) >= 1, "actor kinds are not defined").
		AddAssertion(x.replicaCount >= 1, "cluster replicaCount is invalid").
		AddAssertion(x.writeQuorum >= 1, "cluster writeQuorum is invalid").
		AddAssertion(x.readQuorum >= 1, "cluster readQuorum is invalid").
		Validate()
}
