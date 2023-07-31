package cluster

import "github.com/tochemey/goakt/log"

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
