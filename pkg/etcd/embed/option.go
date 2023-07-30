package embed

import (
	"time"

	"github.com/tochemey/goakt/log"
)

// Option is the interface that applies a configuration option.
type Option interface {
	// Apply sets the Option value of a config.
	Apply(config *Config)
}

var _ Option = OptionFunc(nil)

// OptionFunc implements the Option interface.
type OptionFunc func(config *Config)

func (f OptionFunc) Apply(c *Config) {
	f(c)
}

// WithLoggingEnable enables logging
func WithLoggingEnable() Option {
	return OptionFunc(func(config *Config) {
		config.enableLogging = true
	})
}

// WithInitialCluster sets the initial cluster
func WithInitialCluster(initialCluster string) Option {
	return OptionFunc(func(config *Config) {
		config.initialCluster = initialCluster
	})
}

// WithLogger sets the logger
func WithLogger(logger log.Logger) Option {
	return OptionFunc(func(config *Config) {
		config.logger = logger
	})
}

// WithStartTimeout sets the start timeout
func WithStartTimeout(timeout time.Duration) Option {
	return OptionFunc(func(config *Config) {
		config.startTimeout = timeout
	})
}

// WithDataDir sets the data dir
func WithDataDir(datadir string) Option {
	return OptionFunc(func(config *Config) {
		config.dataDir = datadir
	})
}

// WithJoin sets join field to true and tells the node to join an existing cluster
func WithJoin(join bool) Option {
	return OptionFunc(func(config *Config) {
		config.join = join
	})
}

// WithElectionTimeout sets the election timeout in milliseconds.
// reference: https://etcd.io/docs/v3.4/tuning/#time-parameters
// This setting in combination with the heartbeat interval setting enhance latency
// across multiple data centers or over networks with high latency,
// the heartbeat interval and election timeout settings may need tuning
func WithElectionTimeout(timeout time.Duration) Option {
	return OptionFunc(func(config *Config) {
		config.electionTimeoutMs = timeout
	})
}

// WithHeartbeatInterval sets the heartbeat interval.
// reference: https://etcd.io/docs/v3.4/tuning/#time-parameters
// This setting in combination with the election timeout interval setting enhance latency
// across multiple data centers or over networks with high latency,
// the heartbeat interval and election timeout settings may need tuning
func WithHeartbeatInterval(interval time.Duration) Option {
	return OptionFunc(func(config *Config) {
		config.heartbeatIntervalMs = interval
	})
}
