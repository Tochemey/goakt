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

package actor

import (
	"time"

	"github.com/tochemey/goakt/v2/hash"
	"github.com/tochemey/goakt/v2/log"
)

// Option is the interface that applies a configuration option.
type Option interface {
	// Apply sets the Option value of a config.
	Apply(sys *system)
}

// enforce compilation error
var _ Option = OptionFunc(nil)

// OptionFunc implements the Option interface.
type OptionFunc func(*system)

func (f OptionFunc) Apply(c *system) {
	f(c)
}

// WithExpireActorAfter sets the actor expiry duration.
// After such duration an idle actor will be expired and removed from the actor system
func WithExpireActorAfter(duration time.Duration) Option {
	return OptionFunc(func(a *system) {
		a.expireActorAfter = duration
	})
}

// WithLogger sets the actor system custom log
func WithLogger(logger log.Logger) Option {
	return OptionFunc(func(a *system) {
		a.logger = logger
	})
}

// WithAskTimeout sets how long in seconds an actor should reply a command
// in a receive-reply pattern
func WithAskTimeout(timeout time.Duration) Option {
	return OptionFunc(func(a *system) {
		a.askTimeout = timeout
	})
}

// WithActorInitMaxRetries sets the number of times to retry an actor init process
func WithActorInitMaxRetries(max int) Option {
	return OptionFunc(func(a *system) {
		a.actorInitMaxRetries = max
	})
}

// WithPassivationDisabled disable the passivation mode
func WithPassivationDisabled() Option {
	return OptionFunc(func(a *system) {
		a.expireActorAfter = -1
	})
}

// WithSupervisorDirective sets the supervisor strategy directive
func WithSupervisorDirective(directive SupervisorDirective) Option {
	return OptionFunc(func(a *system) {
		a.supervisorDirective = directive
	})
}

// WithCluster enables the cluster mode
func WithCluster(config *ClusterConfig) Option {
	return OptionFunc(func(a *system) {
		a.clusterEnabled.Store(true)
		a.clusterConfig = config
	})
}

// WithShutdownTimeout sets the shutdown timeout
func WithShutdownTimeout(timeout time.Duration) Option {
	return OptionFunc(func(a *system) {
		a.shutdownTimeout = timeout
	})
}

// WithStash sets the stash buffer size
func WithStash() Option {
	return OptionFunc(func(a *system) {
		a.stashEnabled = true
	})
}

// WithPartitionHasher sets the partition hasher.
func WithPartitionHasher(hasher hash.Hasher) Option {
	return OptionFunc(func(a *system) {
		a.partitionHasher = hasher
	})
}

// WithActorInitTimeout sets how long in seconds an actor start timeout
func WithActorInitTimeout(timeout time.Duration) Option {
	return OptionFunc(func(a *system) {
		a.actorInitTimeout = timeout
	})
}

// WithMetric enables metrics
func WithMetric() Option {
	return OptionFunc(func(system *system) {
		system.metricEnabled.Store(true)
	})
}

// WithPeerStateLoopInterval sets the peer state loop interval
func WithPeerStateLoopInterval(interval time.Duration) Option {
	return OptionFunc(func(system *system) {
		system.peersStateLoopInterval = interval
	})
}

// WithGCInterval sets the GC interval
func WithGCInterval(interval time.Duration) Option {
	return OptionFunc(func(system *system) {
		system.gcInterval = interval
	})
}

// WithHost sets the actor system host
func WithHost(host string) Option {
	return OptionFunc(func(system *system) {
		system.host = host
	})
}
