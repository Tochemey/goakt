package projection

import (
	"time"

	pb "github.com/tochemey/goakt/pb/goakt/v1"
)

// Option is the interface that applies a configuration option.
type Option interface {
	// Apply sets the Option value of a config.
	Apply(config *RecoverySetting)
}

var _ Option = OptionFunc(nil)

// OptionFunc implements the Option interface.
type OptionFunc func(config *RecoverySetting)

func (f OptionFunc) Apply(c *RecoverySetting) {
	f(c)
}

// WithRetries sets the number of retries
func WithRetries(retries uint64) Option {
	return OptionFunc(func(config *RecoverySetting) {
		config.retries = retries
	})
}

// WithRetryDelay sets the retry delay
func WithRetryDelay(delay time.Duration) Option {
	return OptionFunc(func(config *RecoverySetting) {
		config.retryDelay = delay
	})
}

// WithRecoveryStrategy sets the recovery strategy
func WithRecoveryStrategy(strategy pb.ProjectionRecoveryStrategy) Option {
	return OptionFunc(func(config *RecoverySetting) {
		config.strategy = strategy
	})
}
