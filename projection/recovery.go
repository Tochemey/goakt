package projection

import (
	"time"

	pb "github.com/tochemey/goakt/pb/goakt/v1"
)

// RecoverySetting specifies the various recovery settings of a projection
// The option helps defines what happens when the projection handler fails to process
// the consumed event for a given persistence ID
type RecoverySetting struct {
	// retries specifies the number of times to retry handler function.
	// This is only applicable to `RETRY_AND_FAIL` and `RETRY_AND_SKIP` recovery strategies
	// The default value is 5
	retries uint64
	// retryDelay specifies the delay between retry attempts
	// This is only applicable to `RETRY_AND_FAIL` and `RETRY_AND_SKIP` recovery strategies
	// The default value is 1 second
	retryDelay time.Duration
	// strategy specifies strategy to use to recover from unhandled exceptions without causing the projection to fail
	strategy pb.ProjectionRecoveryStrategy
}

// NewRecoverySetting creates an instance of RecoverySetting
func NewRecoverySetting(options ...Option) *RecoverySetting {
	cfg := &RecoverySetting{
		retries:    5,
		retryDelay: time.Second,
		strategy:   pb.ProjectionRecoveryStrategy_FAIL,
	}
	// apply the various options
	for _, opt := range options {
		opt.Apply(cfg)
	}

	return cfg
}

// Retries returns the number of times to retry handler function.
func (c RecoverySetting) Retries() uint64 {
	return c.retries
}

// RetryDelay returns the delay between retry attempts
func (c RecoverySetting) RetryDelay() time.Duration {
	return c.retryDelay
}

// RecoveryStrategy returns the recovery strategy
func (c RecoverySetting) RecoveryStrategy() pb.ProjectionRecoveryStrategy {
	return c.strategy
}
