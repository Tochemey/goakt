package cluster

import (
	"time"

	"github.com/tochemey/goakt/hash"

	"github.com/tochemey/goakt/log"
)

// Option is the interface that applies a configuration option.
type Option interface {
	// Apply sets the Option value of a config.
	Apply(cluster *Cluster)
}

var _ Option = OptionFunc(nil)

// OptionFunc implements the Option interface.
type OptionFunc func(cluster *Cluster)

func (f OptionFunc) Apply(c *Cluster) {
	f(c)
}

// WithPartitionsCount sets the total number of partitions
func WithPartitionsCount(count uint64) Option {
	return OptionFunc(func(cluster *Cluster) {
		cluster.partitionsCount = count
	})
}

// WithLogger sets the logger
func WithLogger(logger log.Logger) Option {
	return OptionFunc(func(cluster *Cluster) {
		cluster.logger = logger
	})
}

// WithWriteTimeout sets the cluster write timeout.
// This timeout specifies the timeout of a data replication
func WithWriteTimeout(timeout time.Duration) Option {
	return OptionFunc(func(cluster *Cluster) {
		cluster.writeTimeout = timeout
	})
}

// WithReadTimeout sets the cluster read timeout.
// This timeout specifies the timeout of a data retrieval
func WithReadTimeout(timeout time.Duration) Option {
	return OptionFunc(func(cluster *Cluster) {
		cluster.readTimeout = timeout
	})
}

// WithShutdownTimeout sets the cluster shutdown timeout.
func WithShutdownTimeout(timeout time.Duration) Option {
	return OptionFunc(func(cluster *Cluster) {
		cluster.shutdownTimeout = timeout
	})
}

// WithHasher sets the custom hasher
func WithHasher(hasher hash.Hasher) Option {
	return OptionFunc(func(cluster *Cluster) {
		cluster.hasher = hasher
	})
}
