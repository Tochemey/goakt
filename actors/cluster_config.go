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

package actors

import (
	"time"

	"github.com/tochemey/goakt/v2/discovery"
	"github.com/tochemey/goakt/v2/internal/validation"
)

// defaultKinds defines the default system kinds
var defaultKinds = []Actor{
	new(funcActor),
}

// ClusterConfig defines the cluster mode settings
type ClusterConfig struct {
	discovery     discovery.Provider
	discoveryPort int
	peersPort     int
	kinds         []Actor

	joinRetryInterval      time.Duration
	joinTimeout            time.Duration
	broadcastRetryInterval time.Duration
	broadcastTimeout       time.Duration
}

// enforce compilation error
var _ validation.Validator = (*ClusterConfig)(nil)

// NewClusterConfig creates an instance of ClusterConfig
func NewClusterConfig() *ClusterConfig {
	return &ClusterConfig{
		kinds:                  defaultKinds,
		joinRetryInterval:      100 * time.Millisecond,
		joinTimeout:            time.Second,
		broadcastRetryInterval: 200 * time.Millisecond,
		broadcastTimeout:       time.Second,
	}
}

// WithJoinRetryInterval defines the time gap between attempts to join an existing cluster.
func (x *ClusterConfig) WithJoinRetryInterval(interval time.Duration) *ClusterConfig {
	x.joinRetryInterval = interval
	return x
}

// WithJoinTimeout defines how long cluster join attempts should last before quitting.
func (x *ClusterConfig) WithJoinTimeout(timeout time.Duration) *ClusterConfig {
	x.joinTimeout = timeout
	return x
}

// WithBroadcastRetryInterval defines the time gap between attempts to broadcast a message to the cluster.
func (x *ClusterConfig) WithBroadcastRetryInterval(interval time.Duration) *ClusterConfig {
	x.broadcastRetryInterval = interval
	return x
}

// WithBroadcastTimeout defines how long a broadcast attempt should last before quitting.
func (x *ClusterConfig) WithBroadcastTimeout(timeout time.Duration) *ClusterConfig {
	x.broadcastTimeout = timeout
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

// JoinRetryInterval is the time gap between attempts to join an existing
// cluster.
func (x *ClusterConfig) JoinRetryInterval() time.Duration {
	return x.joinRetryInterval
}

// JoinTimeout is the join timeout
func (x *ClusterConfig) JoinTimeout() time.Duration {
	return x.joinTimeout
}

// BroadcastRetryInterval is time gap between attempts to broadcast a message to the cluster.
// BroadcastRetryInterval is used to by each node when communicating with their peer in the cluster
func (x *ClusterConfig) BroadcastRetryInterval() time.Duration {
	return x.broadcastRetryInterval
}

// BroadcastTimeout is the broadcast timeout
func (x *ClusterConfig) BroadcastTimeout() time.Duration {
	return x.broadcastTimeout
}

// Discovery returns the discovery provider
func (x *ClusterConfig) Discovery() discovery.Provider {
	return x.discovery
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
		AddAssertion(x.discoveryPort > 0, "gossip port is invalid").
		AddAssertion(x.peersPort > 0, "peers port is invalid").
		AddAssertion(x.joinTimeout > 0, "invalid join timeout").
		AddAssertion(x.joinRetryInterval > 0, "invalid join retry interval").
		AddAssertion(x.joinTimeout > x.joinRetryInterval, "join timeout should be greater than join retry interval").
		AddAssertion(x.broadcastTimeout > 0, "invalid broadcast timeout").
		AddAssertion(x.broadcastRetryInterval > 0, "invalid broadcast retry interval").
		AddAssertion(x.broadcastTimeout > x.broadcastRetryInterval, "broadcast timeout should be greater than broadcast retry interval").
		AddAssertion(len(x.kinds) > 1, "actor kinds are not defined").
		Validate()
}
