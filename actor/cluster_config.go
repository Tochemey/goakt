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

import (
	"sort"
	"time"

	goset "github.com/deckarep/golang-set/v2"

	"github.com/tochemey/goakt/v3/discovery"
	"github.com/tochemey/goakt/v3/internal/ds"
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
	kinds                    *ds.Map[string, Actor]
	grains                   *ds.Map[string, Grain]
	tableSize                uint64
	writeTimeout             time.Duration
	readTimeout              time.Duration
	shutdownTimeout          time.Duration
	bootstrapTimeout         time.Duration
	clusterStateSyncInterval time.Duration
	grainActivationBarrier   *grainActivationBarrierConfig
	roles                    goset.Set[string]
	clusterBalancerInterval  time.Duration
}

type grainActivationBarrierConfig struct {
	enabled bool
	timeout time.Duration
}

// enforce compilation error
var _ validation.Validator = (*ClusterConfig)(nil)

// NewClusterConfig creates an instance of ClusterConfig
func NewClusterConfig() *ClusterConfig {
	config := &ClusterConfig{
		kinds:                    ds.NewMap[string, Actor](),
		grains:                   ds.NewMap[string, Grain](),
		minimumPeersQuorum:       1,
		writeQuorum:              1,
		readQuorum:               1,
		replicaCount:             1,
		partitionCount:           271,
		tableSize:                4 * size.MB,
		writeTimeout:             time.Second,
		readTimeout:              time.Second,
		shutdownTimeout:          3 * time.Minute,
		bootstrapTimeout:         DefaultClusterBootstrapTimeout,
		clusterStateSyncInterval: DefaultClusterStateSyncInterval,
		roles:                    goset.NewSet[string](),
		clusterBalancerInterval:  DefaultClusterBalancerInterval,
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

// WithGrainActivationBarrier enables the grain activation barrier.
//
// When enabled, grain activation will be delayed until the cluster has reached
// the configured minimum peers quorum (see WithMinimumPeersQuorum), or until
// the provided timeout elapses—whichever happens first.
//
// This is useful during startup and rolling deployments to avoid activating
// grains while the cluster is still forming, which can reduce early churn and
// unnecessary rebalancing.
//
// Timeout semantics:
//   - timeout == 0: wait indefinitely for quorum
//   - timeout  > 0: wait up to the given duration, then proceed even if quorum
//     has not been reached
//
// Example:
//
//	cfg := NewClusterConfig().
//		WithMinimumPeersQuorum(3).
//		WithGrainActivationBarrier(10 * time.Second)
func (x *ClusterConfig) WithGrainActivationBarrier(timeout time.Duration) *ClusterConfig {
	x.grainActivationBarrier = &grainActivationBarrierConfig{enabled: true, timeout: timeout}
	return x
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

// WithRoles sets the roles advertised by this node.
//
// A role is a label/metadata used by the cluster to define a node’s
// responsibilities (e.g., "web", "entity", "projection"). Not all nodes
// need to run the same workloads—roles let you dedicate nodes to specific
// purposes such as the web front-end, data access layer, or background
// processing.
//
// In practice, nodes with the "entity" role run actors/services such as
// persistent entities, while nodes with the "projection" role run read-side
// projections. This lets you scale parts of your application independently
// and optimize resource usage.
//
// Once roles are set, you can use SpawnOn("<role>") to spawn an actor on a
// node that advertises that role.
//
// This call replaces any previously configured roles. Duplicates are
// de-duplicated; order is not meaningful
func (x *ClusterConfig) WithRoles(roles ...string) *ClusterConfig {
	x.roles.Append(roles...)
	return x
}

// WithClusterBalancerInterval sets the cluster balancer interval.
//
// This interval controls how frequently the cluster balancer runs to evaluate
// and adjust actor/grain placement. It also drives when a rebalance epoch can
// be acknowledged, which in turn gates stable cluster events.
//
// Relationship to WithClusterStateSyncInterval:
//   - Keep the balancer interval shorter than the state sync interval so each
//     routing epoch can complete before the next one starts. If the balancer
//     interval is too large relative to the sync interval, epochs may overlap
//     and stable cluster events can be delayed.
//
// Recommended starting points:
//   - Small/medium clusters: 1s to 5s balancer interval with 30s to 1m state sync.
//   - Large/busy clusters: increase both intervals together (for example,
//     5s balancer with 1m to 2m state sync) to reduce overhead while keeping
//     epochs from stacking.
//
// Example usage:
//
//	cfg := NewClusterConfig().
//		WithClusterStateSyncInterval(1 * time.Minute).
//		WithClusterBalancerInterval(2 * time.Second)
//
// Returns the updated ClusterConfig instance for chaining.
func (x *ClusterConfig) WithClusterBalancerInterval(interval time.Duration) *ClusterConfig {
	if interval > 0 {
		x.clusterBalancerInterval = interval
	}
	return x
}

// getRoles returns the roles advertised by this node.
//
// A role is a label/metadata used by the cluster to define a node’s
// responsibilities (see WithRoles for details and examples). The returned
// slice is derived from an internal set: there are no duplicates and the
// order is unspecified.
func (x *ClusterConfig) getRoles() []string {
	roles := x.roles.ToSlice()
	sort.Strings(roles)
	return roles
}

// grainActivationBarrierEnabled reports whether the grain activation barrier is enabled.
func (x *ClusterConfig) grainActivationBarrierEnabled() bool {
	return x.grainActivationBarrier != nil && x.grainActivationBarrier.enabled
}

// grainActivationBarrierTimeout returns the grain activation barrier timeout.
// A zero value means wait indefinitely.
func (x *ClusterConfig) grainActivationBarrierTimeout() time.Duration {
	if x.grainActivationBarrier == nil {
		return 0
	}
	return x.grainActivationBarrier.timeout
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
		AddAssertion(x.grainActivationBarrier == nil || x.grainActivationBarrier.timeout >= 0, "grain activation barrier timeout is invalid").
		Validate()
}
