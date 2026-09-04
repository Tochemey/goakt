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

package cluster

import (
	"fmt"
	"os"
	"time"

	oconfig "github.com/tochemey/olric/config"

	"github.com/tochemey/goakt/v4/hash"
	"github.com/tochemey/goakt/v4/internal/size"
	"github.com/tochemey/goakt/v4/log"
	gtls "github.com/tochemey/goakt/v4/tls"
)

// NetworkProfile describes the network the cluster nodes share. It sets how
// often the cluster probes its peers and how many missed probes confirm that a
// node is gone, which decides how quickly NodeLeft and relocation follow an
// abrupt failure and how likely the cluster is to mistake a slow but healthy
// node for a dead one.
type NetworkProfile int

const (
	// NetworkProfileLAN is the default: nodes in one data center or
	// availability zone with sub-millisecond round trips. An abrupt failure is
	// confirmed in about six seconds on a cluster of up to ten nodes; the
	// window grows slowly with the cluster size.
	NetworkProfileLAN NetworkProfile = iota
	// NetworkProfileLocal suits nodes on one host or a low-latency test
	// cluster. It confirms failures fastest, about five seconds on a cluster
	// of up to ten nodes, and is the most sensitive to pauses such as garbage
	// collection or CPU throttling, which it can mistake for a crash.
	NetworkProfileLocal
	// NetworkProfileWAN suits nodes spread across regions or reached over the
	// public internet. It tolerates high latency and packet loss at the cost
	// of slow failure confirmation, about forty seconds on a cluster of up to
	// ten nodes.
	NetworkProfileWAN
)

// Valid reports whether the profile is one of the defined network profiles.
func (x NetworkProfile) Valid() bool {
	return x >= NetworkProfileLAN && x <= NetworkProfileWAN
}

// memberlistEnv returns the memberlist environment whose failure detection
// preset implements the given network profile, and an error for a profile that
// is not defined.
func memberlistEnv(profile NetworkProfile) (string, error) {
	switch profile {
	case NetworkProfileLocal:
		return oconfig.MemberlistEnvLocal, nil
	case NetworkProfileLAN:
		return oconfig.MemberlistEnvLAN, nil
	case NetworkProfileWAN:
		return oconfig.MemberlistEnvWAN, nil
	default:
		return "", fmt.Errorf("unknown network profile: %d", profile)
	}
}

type config struct {
	shardCount              uint64
	minimumMembersQuorum    uint32
	replicasCount           uint32
	membersWriteQuorum      uint32
	membersReadQuorum       uint32
	tableSize               uint64
	writeTimeout            time.Duration
	readTimeout             time.Duration
	shutdownTimeout         time.Duration
	bootstrapTimeout        time.Duration
	routingTableInterval    time.Duration
	triggerBalancerInterval time.Duration
	logger                  log.Logger
	shardHasher             hash.Hasher
	tlsInfo                 *gtls.Info
	// convergenceTimeout bounds how long a confirmed join or departure waits
	// for the routing table to converge on it before the cluster announces it
	// anyway.
	convergenceTimeout time.Duration
	// networkProfile is the network the cluster nodes share. It selects the
	// failure detection preset applied to the memberlist configuration.
	networkProfile NetworkProfile
}

func defaultConfig() *config {
	return &config{
		shardCount:              271,
		minimumMembersQuorum:    1,
		replicasCount:           1,
		membersWriteQuorum:      1,
		membersReadQuorum:       1,
		tableSize:               4 * size.MB,
		writeTimeout:            time.Second,
		readTimeout:             time.Second,
		shutdownTimeout:         3 * time.Minute,
		bootstrapTimeout:        10 * time.Second,
		routingTableInterval:    time.Minute,
		triggerBalancerInterval: time.Second,
		logger:                  log.NewZap(log.ErrorLevel, os.Stderr),
		shardHasher:             hash.DefaultHasher(),
		tlsInfo:                 nil,
		convergenceTimeout:      pendingEventEmitTimeout,
		networkProfile:          NetworkProfileLAN,
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

// WithBalancerInterval configures how frequently the Olric balancer runs.
func WithBalancerInterval(interval time.Duration) ConfigOption {
	return func(cfg *config) {
		if interval > 0 {
			cfg.triggerBalancerInterval = interval
		}
	}
}

// WithConvergenceTimeout bounds how long a confirmed join or departure waits
// for the routing table to converge on it before the cluster announces the
// membership event anyway.
func WithConvergenceTimeout(timeout time.Duration) ConfigOption {
	return func(cfg *config) {
		if timeout > 0 {
			cfg.convergenceTimeout = timeout
		}
	}
}

// WithNetworkProfile sets the network the cluster nodes share, which selects
// the failure detection preset applied to the memberlist configuration.
func WithNetworkProfile(profile NetworkProfile) ConfigOption {
	return func(cfg *config) {
		cfg.networkProfile = profile
	}
}

// WithTLS enables TLS communication using the provided configuration.
func WithTLS(info *gtls.Info) ConfigOption {
	return func(cfg *config) {
		cfg.tlsInfo = info
	}
}
