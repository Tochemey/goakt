/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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
	"os"
	"time"

	"github.com/tochemey/goakt/v3/hash"
	"github.com/tochemey/goakt/v3/internal/size"
	"github.com/tochemey/goakt/v3/log"
	gtls "github.com/tochemey/goakt/v3/tls"
)

type config struct {
	shardCount           uint64
	minimumMembersQuorum uint32
	replicasCount        uint32
	membersWriteQuorum   uint32
	membersReadQuorum    uint32
	tableSize            uint64
	writeTimeout         time.Duration
	readTimeout          time.Duration
	shutdownTimeout      time.Duration
	bootstrapTimeout     time.Duration
	routingTableInterval time.Duration
	logger               log.Logger
	shardHasher          hash.Hasher
	tlsInfo              *gtls.Info
}

func defaultConfig() *config {
	return &config{
		shardCount:           271,
		minimumMembersQuorum: 1,
		replicasCount:        1,
		membersWriteQuorum:   1,
		membersReadQuorum:    1,
		tableSize:            4 * size.MB,
		writeTimeout:         time.Second,
		readTimeout:          time.Second,
		shutdownTimeout:      3 * time.Minute,
		bootstrapTimeout:     10 * time.Second,
		routingTableInterval: time.Minute,
		logger:               log.New(log.ErrorLevel, os.Stderr),
		shardHasher:          hash.DefaultHasher(),
		tlsInfo:              nil,
	}
}

// ConfigOption configures cluster creation parameters before the engine is
// started.
type ConfigOption func(*config)

// WithLogger overrides the default cluster logger.
func WithLogger(logger log.Logger) ConfigOption {
	return func(cfg *config) {
		if logger != nil {
			cfg.logger = logger
		}
	}
}

// WithPartitioner sets the hash function used to derive shard ids.
func WithPartitioner(h hash.Hasher) ConfigOption {
	return func(cfg *config) {
		if h != nil {
			cfg.shardHasher = h
		}
	}
}

// WithShardCount configures the number of shards maintained by the cluster engine.
func WithShardCount(count uint64) ConfigOption {
	return func(cfg *config) {
		if count > 0 {
			cfg.shardCount = count
		}
	}
}

// WithReplicasCount sets the replication factor of cluster data.
func WithReplicasCount(count uint32) ConfigOption {
	return func(cfg *config) {
		if count > 0 {
			cfg.replicasCount = count
		}
	}
}

// WithMinimumMembersQuorum sets the minimum number of peers required for
// quorum operations.
func WithMinimumMembersQuorum(quorum uint32) ConfigOption {
	return func(cfg *config) {
		if quorum > 0 {
			cfg.minimumMembersQuorum = quorum
		}
	}
}

// WithMembersWriteQuorum configures how many peers must ack write operations.
func WithMembersWriteQuorum(quorum uint32) ConfigOption {
	return func(cfg *config) {
		if quorum > 0 {
			cfg.membersWriteQuorum = quorum
		}
	}
}

// WithMembersReadQuorum configures how many peers must ack read operations.
func WithMembersReadQuorum(quorum uint32) ConfigOption {
	return func(cfg *config) {
		if quorum > 0 {
			cfg.membersReadQuorum = quorum
		}
	}
}

// WithDataTableSize overrides the unified map table size.
func WithDataTableSize(size uint64) ConfigOption {
	return func(cfg *config) {
		if size > 0 {
			cfg.tableSize = size
		}
	}
}

// WithWriteTimeout sets the default timeout applied to write operations.
func WithWriteTimeout(timeout time.Duration) ConfigOption {
	return func(cfg *config) {
		if timeout > 0 {
			cfg.writeTimeout = timeout
		}
	}
}

// WithReadTimeout sets the default timeout applied to read operations.
func WithReadTimeout(timeout time.Duration) ConfigOption {
	return func(cfg *config) {
		if timeout > 0 {
			cfg.readTimeout = timeout
		}
	}
}

// WithShutdownTimeout sets the timeout used to gracefully stop the
// cluster engine.
func WithShutdownTimeout(timeout time.Duration) ConfigOption {
	return func(cfg *config) {
		if timeout > 0 {
			cfg.shutdownTimeout = timeout
		}
	}
}

// WithBootstrapTimeout sets how long to wait for the engine bootstrap.
func WithBootstrapTimeout(timeout time.Duration) ConfigOption {
	return func(cfg *config) {
		if timeout > 0 {
			cfg.bootstrapTimeout = timeout
		}
	}
}

// WithRoutingTableInterval sets the refresh interval of the routing table.
func WithRoutingTableInterval(interval time.Duration) ConfigOption {
	return func(cfg *config) {
		if interval > 0 {
			cfg.routingTableInterval = interval
		}
	}
}

// WithTLS enables TLS communication using the provided configuration.
func WithTLS(info *gtls.Info) ConfigOption {
	return func(cfg *config) {
		cfg.tlsInfo = info
	}
}
