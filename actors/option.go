/*
 * MIT License
 *
 * Copyright (c) 2022-2023 Tochemey
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
	"errors"
	"time"

	"github.com/tochemey/goakt/discovery"
	"github.com/tochemey/goakt/hash"
	"github.com/tochemey/goakt/log"
	"github.com/tochemey/goakt/telemetry"
)

var (
	ErrNameRequired = errors.New("actor system is required")
)

// Option is the interface that applies a configuration option.
type Option interface {
	// Apply sets the Option value of a config.
	Apply(sys *actorSystem)
}

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
		a.replyTimeout = timeout
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

// WithSupervisorStrategy sets the supervisor strategy
func WithSupervisorStrategy(strategy StrategyDirective) Option {
	return OptionFunc(func(a *actorSystem) {
		a.supervisorStrategy = strategy
	})
}

// WithTelemetry sets the custom telemetry
func WithTelemetry(telemetry *telemetry.Telemetry) Option {
	return OptionFunc(func(a *actorSystem) {
		a.telemetry = telemetry
	})
}

// WithRemoting enables remoting on the actor system
func WithRemoting(host string, port int32) Option {
	return OptionFunc(func(a *actorSystem) {
		a.remotingEnabled.Store(true)
		a.remotingPort = port
		a.remotingHost = host
	})
}

// WithClustering enables clustering on the actor system. This enables remoting on the actor system as well
// and set the remotingHost to the cluster node host when the cluster is fully enabled.
func WithClustering(serviceDiscovery *discovery.ServiceDiscovery, partitionCount uint64) Option {
	return OptionFunc(func(a *actorSystem) {
		a.clusterEnabled.Store(true)
		a.remotingEnabled.Store(true)
		a.partitionsCount = partitionCount
		a.serviceDiscovery = serviceDiscovery
	})
}

// WithShutdownTimeout sets the shutdown timeout
func WithShutdownTimeout(timeout time.Duration) Option {
	return OptionFunc(func(a *actorSystem) {
		a.shutdownTimeout = timeout
	})
}

// WithMailboxSize sets the actors mailbox size
func WithMailboxSize(size uint64) Option {
	return OptionFunc(func(a *actorSystem) {
		a.mailboxSize = size
	})
}

// WithMailbox sets the custom mailbox used by the actors in the actor system
func WithMailbox(mailbox Mailbox) Option {
	return OptionFunc(func(a *actorSystem) {
		a.mailbox = mailbox
	})
}

// WithStash sets the stash buffer size
func WithStash(capacity uint64) Option {
	return OptionFunc(func(a *actorSystem) {
		a.stashBuffer = capacity
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

// WithTracing enables tracing
func WithTracing() Option {
	return OptionFunc(func(system *actorSystem) {
		system.traceEnabled.Store(true)
	})
}

// WithMetric enables metrics
func WithMetric() Option {
	return OptionFunc(func(system *actorSystem) {
		system.metricEnabled.Store(true)
	})
}
