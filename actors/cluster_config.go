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

import (
	"github.com/tochemey/goakt/v2/discovery"
	"github.com/tochemey/goakt/v2/internal/validation"
)

// defaultKinds defines the default system kinds
var defaultKinds = []Actor{
	new(funcActor),
}

// ClusterConfig defines the cluster mode settings
type ClusterConfig struct {
	discovery          discovery.Provider
	partitionCount     uint64
	minimumPeersQuorum uint32
	replicaCount       uint32
	discoveryPort      int
	peersPort          int
	kinds              []Actor
}

// enforce compilation error
var _ validation.Validator = (*ClusterConfig)(nil)

// NewClusterConfig creates an instance of ClusterConfig
func NewClusterConfig() *ClusterConfig {
	return &ClusterConfig{
		kinds:              defaultKinds,
		minimumPeersQuorum: 1,
		replicaCount:       2,
	}
}

// WithPartitionCount sets the cluster config partition count
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
	x.kinds = append(x.kinds, kinds...)
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
func (x *ClusterConfig) WithReplicaCount(count uint32) *ClusterConfig {
	x.replicaCount = count
	return x
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
	return x.kinds
}

// Validate validates the cluster config
func (x *ClusterConfig) Validate() error {
	return validation.
		New(validation.AllErrors()).
		AddAssertion(x.discovery != nil, "discovery provider is not set").
		AddAssertion(x.partitionCount > 0, "partition count need to greater than zero").
		AddAssertion(x.minimumPeersQuorum >= 1, "minimum peers quorum must be at least one").
		AddAssertion(x.discoveryPort > 0, "gossip port is invalid").
		AddAssertion(x.peersPort > 0, "peers port is invalid").
		AddAssertion(len(x.kinds) > 1, "actor kinds are not defined").
		AddAssertion(x.replicaCount > 0, "actor replicaCount is invalid").
		AddAssertion(x.replicaCount <= x.minimumPeersQuorum, "replica count should be less or equal to minimum peers quorum").
		Validate()
}
