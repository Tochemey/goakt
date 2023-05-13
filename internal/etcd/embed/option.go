package embed

import (
	"time"

	"github.com/coreos/etcd/pkg/types"
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

// WithSize sets the etcd server size
func WithSize(size int) Option {
	return OptionFunc(func(config *Config) {
		config.size = size
	})
}

// WithLoggingEnable enables logging
func WithLoggingEnable() Option {
	return OptionFunc(func(config *Config) {
		config.enableLogging = true
	})
}

// WithPeerURLs sets the peer URLs
func WithPeerURLs(peerURLs types.URLs) Option {
	return OptionFunc(func(config *Config) {
		config.peerURLs = peerURLs
	})
}

// WithClientURLs sets the client URLs
func WithClientURLs(clientURLs types.URLs) Option {
	return OptionFunc(func(config *Config) {
		config.clientURLs = clientURLs
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
