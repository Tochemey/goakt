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

	"github.com/tochemey/goakt/v3/extension"
	"github.com/tochemey/goakt/v3/hash"
	"github.com/tochemey/goakt/v3/internal/collection"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/remote"
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

// WithLogger sets the actor system custom log
func WithLogger(logger log.Logger) Option {
	return OptionFunc(
		func(a *actorSystem) {
			a.logger = logger
		},
	)
}

// WithActorInitMaxRetries sets the number of times to retry an actor init process
func WithActorInitMaxRetries(value int) Option {
	return OptionFunc(
		func(a *actorSystem) {
			a.actorInitMaxRetries = value
		},
	)
}

// WithRemote enables remoting on the actor system
func WithRemote(config *remote.Config) Option {
	return OptionFunc(func(a *actorSystem) {
		a.remotingEnabled.Store(true)
		a.remoteConfig = config
	})
}

// WithCluster enables the cluster mode
func WithCluster(config *ClusterConfig) Option {
	return OptionFunc(
		func(a *actorSystem) {
			a.clusterEnabled.Store(true)
			a.clusterConfig = config
		},
	)
}

// WithShutdownTimeout sets the shutdown timeout
// The timeout needs to be considerable reasonable based upon the total number of actors
// the system will probably needs. The graceful timeout is shared amongst all actors and children
// actors created in the system to graceful shutdown via a cancellation context.
func WithShutdownTimeout(timeout time.Duration) Option {
	return OptionFunc(
		func(a *actorSystem) {
			a.shutdownTimeout = timeout
		},
	)
}

// WithPartitionHasher sets the partition hasher.
func WithPartitionHasher(hasher hash.Hasher) Option {
	return OptionFunc(
		func(a *actorSystem) {
			a.partitionHasher = hasher
		},
	)
}

// WithActorInitTimeout sets how long in seconds an actor start timeout
func WithActorInitTimeout(timeout time.Duration) Option {
	return OptionFunc(
		func(a *actorSystem) {
			a.actorInitTimeout = timeout
		},
	)
}

// WithPeerStateLoopInterval sets the peer state loop interval
//
// Deprecated: This option is deprecated and will be removed in a future release.
// Use WithPeersStateSyncInterval instead to configure the interval for synchronizing cluster state when
// setting up a cluster using the ClusterConfig option.
func WithPeerStateLoopInterval(interval time.Duration) Option {
	return OptionFunc(
		func(system *actorSystem) {
			if system.clusterConfig != nil {
				system.clusterConfig = system.clusterConfig.WithPeersStateSyncInterval(interval)
			}
		},
	)
}

// WithCoordinatedShutdown registers internal and user-defined tasks to be executed during the shutdown process.
// The defined tasks will be executed in the same order of insertion.
// Any failure will halt the shutdown process.
func WithCoordinatedShutdown(hooks ...ShutdownHook) Option {
	return OptionFunc(func(system *actorSystem) {
		system.shutdownHooks = append(system.shutdownHooks, hooks...)
	})
}

// WithTLS configures TLS settings for both the Server and Client.
// Ensure that both the Server and Client are configured with the same
// root Certificate Authority (CA) to enable successful handshake and
// mutual authentication.
//
// In cluster mode, all nodesMap must share the same root CA to establish
// secure communication and complete handshakes successfully.
func WithTLS(tlsInfo *TLSInfo) Option {
	return OptionFunc(func(system *actorSystem) {
		system.serverTLS = tlsInfo.ServerTLS
		system.clientTLS = tlsInfo.ClientTLS
	})
}

// WithPubSub enables the pub-sub (publish-subscribe) mode for the actor system.
//
// In pub-sub mode, actors can subscribe to one or more named topics and receive
// messages that are published to those topics. This is useful for implementing
// decoupled communication patterns where the publisher does not need to know
// the identity or number of subscribers.
//
// When this option is applied during system initialization, internal mechanisms
// for managing topic subscriptions and broadcasting messages to subscribers
// will be activated.
//
// Example:
//
//	system := NewActorSystem(WithPubSub())
//
// Returns an Option that configures the actor system accordingly.
func WithPubSub() Option {
	return OptionFunc(func(system *actorSystem) {
		system.pubsubEnabled.Store(true)
	})
}

// WithoutRelocation returns an Option that disables actor relocation in the cluster.
//
// When this option is set, the actor system will not attempt to relocate actors
// from a node that leaves the cluster. This applies even if individual actors
// are configured to support relocation.
//
// Instead of migrating to a healthy node, actors hosted on the departing node
// will be terminated, and any associated in-memory state will be lost permanently.
//
// This is useful in scenarios where actor state is ephemeral, externally managed,
// or where graceful degradation is preferred over relocation overhead.
func WithoutRelocation() Option {
	return OptionFunc(func(system *actorSystem) {
		system.relocationEnabled.Store(false)
	})
}

// WithExtensions registers one or more Extensions with the ActorSystem during initialization.
//
// This function allows you to inject pluggable components that implement the Extension interface,
// enabling additional functionality such as event sourcing, metrics collection, or tracing.
//
// Registered extensions will be accessible from any actor's message context, allowing them
// to be used transparently across the system.
//
// Example:
//
//	system := NewActorSystem(
//	    WithExtensions(
//	        NewEventSourcingExtension(),
//	        NewMetricsExtension(),
//	    ),
//	)
//
// Each extension must have a unique ID to avoid collisions in the system registry.
//
// Parameters:
//   - extensions: One or more Extension instances to be registered.
//
// Returns:
//   - Option: A configuration option used when constructing the ActorSystem.
func WithExtensions(extensions ...extension.Extension) Option {
	return OptionFunc(func(system *actorSystem) {
		if system.extensions == nil {
			system.extensions = collection.NewMap[string, extension.Extension]()
		}
		for _, ext := range extensions {
			system.extensions.Set(ext.ID(), ext)
		}
	})
}
