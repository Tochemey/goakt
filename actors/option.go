/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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
	"github.com/tochemey/goakt/v2/hash"
	"github.com/tochemey/goakt/v2/log"
)

// Option is the interface that applies a configuration option.
type Option interface {
	// Apply sets the Option value of a config.
	Apply(sys *actorSystem)
}

// enforce compilation error
var _ Option = OptionFunc(nil)

// OptionFunc implements the Option interface.
type OptionFunc func(*actorSystem)

func (f OptionFunc) Apply(c *actorSystem) {
	f(c)
}

// WithExpireActorAfter sets the actor expiry duration.
// After such duration an idle actor will be expired and removed from the actor system
func WithExpireActorAfter(duration time.Duration) Option {
	return OptionFunc(func(a *actorSystem) {
		a.expireActorAfter = duration
	})
}

// WithLogger sets the actor system custom log
func WithLogger(logger log.Logger) Option {
	return OptionFunc(func(a *actorSystem) {
		a.logger = logger
	})
}

// WithReplyTimeout sets how long in seconds an actor should reply a command
// in a receive-reply pattern
func WithReplyTimeout(timeout time.Duration) Option {
	return OptionFunc(func(a *actorSystem) {
		a.askTimeout = timeout
	})
}

// WithActorInitMaxRetries sets the number of times to retry an actor init process
func WithActorInitMaxRetries(max int) Option {
	return OptionFunc(func(a *actorSystem) {
		a.actorInitMaxRetries = max
	})
}

// WithPassivationDisabled disable the passivation mode
func WithPassivationDisabled() Option {
	return OptionFunc(func(a *actorSystem) {
		a.expireActorAfter = -1
	})
}

// WithSupervisorDirective sets the supervisor strategy directive
func WithSupervisorDirective(directive SupervisorDirective) Option {
	return OptionFunc(func(a *actorSystem) {
		a.supervisorDirective = directive
	})
}

// WithRemoting enables remoting on the actor system
func WithRemoting(host string, port int32) Option {
	return OptionFunc(func(a *actorSystem) {
		a.remotingEnabled.Store(true)
		a.port = port
		a.host = host
	})
}

// WithClustering enables the cluster mode.
// Deprecated: use rather WithCluster which offers a fluent api to set cluster configuration
func WithClustering(provider discovery.Provider, partitionCount uint64, minimumPeersQuorum uint16, discoveryPort, peersPort int, kinds ...Actor) Option {
	return OptionFunc(func(a *actorSystem) {
		a.clusterEnabled.Store(true)
		replicaCount := 2
		if minimumPeersQuorum < 2 {
			replicaCount = 1
		}

		a.clusterConfig = NewClusterConfig().
			WithDiscovery(provider).
			WithPartitionCount(partitionCount).
			WithDiscoveryPort(discoveryPort).
			WithPeersPort(peersPort).
			WithMinimumPeersQuorum(uint32(minimumPeersQuorum)).
			WithReplicaCount(uint32(replicaCount)).
			WithKinds(kinds...)
	})
}

// WithCluster enables the cluster mode
func WithCluster(config *ClusterConfig) Option {
	return OptionFunc(func(a *actorSystem) {
		a.clusterEnabled.Store(true)
		a.clusterConfig = config
	})
}

// WithShutdownTimeout sets the shutdown timeout
func WithShutdownTimeout(timeout time.Duration) Option {
	return OptionFunc(func(a *actorSystem) {
		a.shutdownTimeout = timeout
	})
}

// WithStash sets the stash buffer size
func WithStash() Option {
	return OptionFunc(func(a *actorSystem) {
		a.stashEnabled = true
	})
}

// WithPartitionHasher sets the partition hasher.
func WithPartitionHasher(hasher hash.Hasher) Option {
	return OptionFunc(func(a *actorSystem) {
		a.partitionHasher = hasher
	})
}

// WithActorInitTimeout sets how long in seconds an actor start timeout
func WithActorInitTimeout(timeout time.Duration) Option {
	return OptionFunc(func(a *actorSystem) {
		a.actorInitTimeout = timeout
	})
}

// WithPeerStateLoopInterval sets the peer state loop interval
func WithPeerStateLoopInterval(interval time.Duration) Option {
	return OptionFunc(func(system *actorSystem) {
		system.peersStateLoopInterval = interval
	})
}

// WithJanitorInterval sets the janitor interval
func WithJanitorInterval(interval time.Duration) Option {
	return OptionFunc(func(system *actorSystem) {
		system.janitorInterval = interval
	})
}
