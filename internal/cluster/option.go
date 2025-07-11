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

package cluster

import (
	"crypto/tls"
	"time"

	"github.com/tochemey/goakt/v3/hash"
	"github.com/tochemey/goakt/v3/log"
)

// Option is the interface that applies a configuration option.
type Option interface {
	// Apply sets the Option value of a config.
	Apply(cl *Engine)
}

var _ Option = OptionFunc(nil)

// OptionFunc implements the Option interface.
type OptionFunc func(eng *Engine)

// Apply applies the Node's option
func (f OptionFunc) Apply(c *Engine) {
	f(c)
}

// WithPartitionsCount sets the total number of partitions
func WithPartitionsCount(count uint64) Option {
	return OptionFunc(func(eng *Engine) {
		eng.partitionsCount = count
	})
}

// WithLogger sets the logger
func WithLogger(logger log.Logger) Option {
	return OptionFunc(func(eng *Engine) {
		eng.logger = logger
	})
}

// WithWriteTimeout sets the Node write timeout.
// This timeout specifies the timeout of a data replication
func WithWriteTimeout(timeout time.Duration) Option {
	return OptionFunc(func(eng *Engine) {
		eng.writeTimeout = timeout
	})
}

// WithReadTimeout sets the Node read timeout.
// This timeout specifies the timeout of a data retrieval
func WithReadTimeout(timeout time.Duration) Option {
	return OptionFunc(func(eng *Engine) {
		eng.readTimeout = timeout
	})
}

// WithShutdownTimeout sets the Node shutdown timeout.
func WithShutdownTimeout(timeout time.Duration) Option {
	return OptionFunc(func(eng *Engine) {
		eng.shutdownTimeout = timeout
	})
}

// WithHasher sets the custom hasher
func WithHasher(hasher hash.Hasher) Option {
	return OptionFunc(func(eng *Engine) {
		eng.hasher = hasher
	})
}

// WithMinimumPeersQuorum sets the minimum number of nodes to form a quorum
func WithMinimumPeersQuorum(count uint32) Option {
	return OptionFunc(func(eng *Engine) {
		eng.minimumPeersQuorum = count
	})
}

// WithReplicaCount sets replica count
func WithReplicaCount(count uint32) Option {
	return OptionFunc(func(eng *Engine) {
		eng.replicaCount = count
	})
}

// WithWriteQuorum sets the write quorum
func WithWriteQuorum(count uint32) Option {
	return OptionFunc(func(eng *Engine) {
		eng.writeQuorum = count
	})
}

// WithReadQuorum sets the read quorum
func WithReadQuorum(count uint32) Option {
	return OptionFunc(func(eng *Engine) {
		eng.readQuorum = count
	})
}

// WithTLS sets the various TLS for both Server and Client
// configuration. Bear in mind that both Client and Server must have the same
// root CA for successful handshake and authentication
func WithTLS(serverConfig, clientConfig *tls.Config) Option {
	return OptionFunc(func(eng *Engine) {
		eng.clientTLS = clientConfig
		eng.serverTLS = serverConfig
	})
}

// WithTableSize sets the cluster table storage size
func WithTableSize(size uint64) Option {
	return OptionFunc(func(eng *Engine) {
		eng.tableSize = size
	})
}

// WithBootstrapTimeout sets the timeout for the bootstrap process
// This timeout specifies the maximum time to wait for the cluster to bootstrap
func WithBootstrapTimeout(timeout time.Duration) Option {
	return OptionFunc(func(eng *Engine) {
		eng.bootstrapTimeout = timeout
	})
}

// WithCacheSyncInterval sets the interval for syncing nodes' routing tables.
// This interval specifies how often the routing tables are synchronized
// across the cluster.
// It is important to note that this interval should be greater than the
// write timeout to ensure that the routing table is not updated while a write
// operation is in progress.
func WithCacheSyncInterval(interval time.Duration) Option {
	return OptionFunc(func(eng *Engine) {
		eng.cacheSyncInterval = interval
	})
}
