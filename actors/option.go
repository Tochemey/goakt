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
	"context"
	"time"

	"github.com/tochemey/goakt/v2/hash"
	"github.com/tochemey/goakt/v2/log"
	"github.com/tochemey/goakt/v2/remote"
)

// ShutdownHook defines the shutdown hook to be executed alongside the
// termination of the actor system
type ShutdownHook func(ctx context.Context) error

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

// WithExpireActorAfter sets a custom duration after which an idle actor
// will be passivated. Passivation allows the actor system to free up
// resources by stopping actors that have been inactive for the specified
// duration. If the actor receives a message before this timeout,
// the passivation timer is reset.
func WithExpireActorAfter(duration time.Duration) Option {
	return OptionFunc(
		func(a *actorSystem) {
			a.expireActorAfter = duration
		},
	)
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

// WithPassivationDisabled disable the passivation mode
func WithPassivationDisabled() Option {
	return OptionFunc(
		func(a *actorSystem) {
			a.expireActorAfter = -1
		},
	)
}

// WithRemoting enables remoting on the actor system
// Deprecated: use WithRemote which provides better remoting configuration and optimization
func WithRemoting(host string, port int32) Option {
	return OptionFunc(
		func(a *actorSystem) {
			a.remotingEnabled.Store(true)
			a.remoteConfig = remote.NewConfig(host, int(port))
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

// WithStash sets the stash buffer size
func WithStash() Option {
	return OptionFunc(
		func(a *actorSystem) {
			a.stashEnabled = true
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
func WithPeerStateLoopInterval(interval time.Duration) Option {
	return OptionFunc(
		func(system *actorSystem) {
			system.peersStateLoopInterval = interval
		},
	)
}

// WithJanitorInterval sets the janitor interval
func WithJanitorInterval(interval time.Duration) Option {
	return OptionFunc(
		func(system *actorSystem) {
			system.janitorInterval = interval
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
// In cluster mode, all nodes must share the same root CA to establish
// secure communication and complete handshakes successfully.
func WithTLS(tlsInfo *TLSInfo) Option {
	return OptionFunc(func(system *actorSystem) {
		system.serverTLS = tlsInfo.ServerConfig
		system.tlsClientConfig = tlsInfo.ClientConfig
	})
}
