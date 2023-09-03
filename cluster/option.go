package cluster

import (
	"time"

	"github.com/tochemey/goakt/hash"
	"github.com/tochemey/goakt/log"
)

// Option is the interface that applies a configuration option.
type Option interface {
	// Apply sets the Option value of a config.
	Apply(cl *Cluster)
}

var _ Option = OptionFunc(nil)

// OptionFunc implements the Option interface.
type OptionFunc func(cl *Cluster)

// Apply applies the Cluster's option
func (f OptionFunc) Apply(c *Cluster) {
	f(c)
}

// WithPartitionsCount sets the total number of partitions
func WithPartitionsCount(count uint64) Option {
	return OptionFunc(func(cl *Cluster) {
		cl.partitionsCount = count
	})
}

// WithLogger sets the logger
func WithLogger(logger log.Logger) Option {
	return OptionFunc(func(cl *Cluster) {
		cl.logger = logger
	})
}

// WithWriteTimeout sets the Cluster write timeout.
// This timeout specifies the timeout of a data replication
func WithWriteTimeout(timeout time.Duration) Option {
	return OptionFunc(func(cl *Cluster) {
		cl.writeTimeout = timeout
	})
}

// WithReadTimeout sets the Cluster read timeout.
// This timeout specifies the timeout of a data retrieval
func WithReadTimeout(timeout time.Duration) Option {
	return OptionFunc(func(cl *Cluster) {
		cl.readTimeout = timeout
	})
}

// WithShutdownTimeout sets the Cluster shutdown timeout.
func WithShutdownTimeout(timeout time.Duration) Option {
	return OptionFunc(func(cl *Cluster) {
		cl.shutdownTimeout = timeout
	})
}

// WithHasher sets the custom hasher
func WithHasher(hasher hash.Hasher) Option {
	return OptionFunc(func(cl *Cluster) {
		cl.hasher = hasher
	})
}
