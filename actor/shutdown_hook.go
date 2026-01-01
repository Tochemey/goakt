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

package actor

import (
	"context"
	"time"
)

// ShutdownHook defines the interface for a coordinated shutdown hook.
//
// A ShutdownHook is executed during the shutdown process of an ActorSystem to perform cleanup,
// resource release, or other termination logic. Implementations should provide the Execute method
// to define the shutdown behavior, and Recovery to specify error handling and retry strategies.
//
// Example:
//
//	type MyShutdownHook struct{}
//
//	func (h *MyShutdownHook) Execute(ctx context.Context, actorSystem ActorSystem) error {
//	    // custom shutdown logic
//	    return nil
//	}
//
//	func (h *MyShutdownHook) Recovery() *ShutdownHookRecovery {
//	    return NewShutdownHookRecovery(
//	        WithShutdownHookRetry(2, time.Second),
//	        WithShutdownHookRecoveryStrategy(ShouldRetryAndSkip),
//	    )
//	}
type ShutdownHook interface {
	// Execute runs the shutdown logic for this hook.
	//
	// Parameters:
	//   - ctx:         The context for cancellation and deadlines.
	//   - actorSystem: The ActorSystem being shut down.
	//
	// Returns:
	//   - error: An error if the shutdown logic fails, or nil on success.
	Execute(ctx context.Context, actorSystem ActorSystem) error

	// Recovery returns the ShutdownHookRecovery configuration for this hook.
	//
	// This determines how failures are handled, including retry and recovery strategies.
	//
	// Returns:
	//   - *ShutdownHookRecovery: The recovery configuration for this hook.
	Recovery() *ShutdownHookRecovery
}

// RecoveryStrategy defines the strategy to apply when a ShutdownHook fails during the shutdown process.
//
// This policy determines how the shutdown sequence should proceed if a hook returns an error.
// It allows fine-grained control over error handling, including whether to halt, retry, skip, or combine these actions.
//
// The available policies are:
//   - ShouldFail:         Stop execution and report the error.
//   - ShouldRetryAndFail: Retry the failed hook, then stop if still unsuccessful.
//   - ShouldSkip:         Skip the failed hook and continue with the next.
//   - ShouldRetryAndSkip: Retry the failed hook, then skip and continue if still unsuccessful.
type RecoveryStrategy int

const (
	// ShouldFail indicates that if a ShutdownHook fails, the shutdown process should immediately stop executing any remaining hooks.
	//
	// The error from the failed hook is reported, and no further shutdown hooks are run.
	// Use this policy when subsequent hooks depend on the success of previous ones, or when a failure should halt the shutdown sequence.
	ShouldFail RecoveryStrategy = iota

	// ShouldRetryAndFail indicates that if a ShutdownHook fails, the system should retry executing the hook.
	//
	// The shutdown process will pause and repeatedly attempt the failed hook until it succeeds or a maximum retry limit is reached.
	// If the hook still fails after all retries, the error is reported and no further hooks are executed.
	// Use this policy when the hook is critical and transient errors may be recoverable.
	ShouldRetryAndFail

	// ShouldSkip indicates that if a ShutdownHook fails, the error should be reported, but the shutdown process should skip the failed hook and continue executing the remaining hooks.
	//
	// Use this policy when hooks are independent and a failure in one should not prevent the execution of others.
	ShouldSkip

	// ShouldRetryAndSkip indicates that if a ShutdownHook fails, the system should retry executing the hook.
	//
	// The shutdown process will pause and repeatedly attempt the failed hook until it succeeds or a maximum retry limit is reached.
	// If the hook still fails after all retries, the error is reported, but the shutdown process continues with the remaining hooks.
	// Use this policy when you want to maximize the chance of successful execution, but do not want a persistent failure to block the shutdown sequence.
	ShouldRetryAndSkip
)

const (
	// DefaultShutdownRecoveryMaxRetries defines the default number of retries for coordinated shutdown hooks
	DefaultShutdownRecoveryMaxRetries = 3

	// DefaultShutdownHookRecoveryRetryInterval defines the default delay between retries for coordinated shutdown hooks when a retry policy is applied
	DefaultShutdownHookRecoveryRetryInterval = time.Second
)

// RecoveryOption defines a functional option for configuring a ShutdownHookRecovery.
//
// These options are used with NewShutdownHookRecovery to customize the number of retries,
// the delay between retries, and the recovery policy for shutdown hooks. Each option is a function
// that modifies the ShutdownHookRecovery instance during its construction.
//
// Example usage:
//
//	recovery := NewShutdownHookRecovery(
//	    WithShutdownHookRetries(3),
//	    WithShutdownHookRetryDelay(2 * time.Second),
//	    WithShutdownHookRecoveryPolicy(ShouldRetryAndSkip),
//	)
type RecoveryOption func(*ShutdownHookRecovery)

// WithShutdownHookRetry configures the number of retries and the interval between retries
// for a ShutdownHookRecovery. Use this option with NewShutdownHookRecovery to specify how
// many times a shutdown hook should be retried and how long to wait between attempts.
//
// Parameters:
//   - retries:  The number of retry attempts.
//   - interval: The duration to wait between retries.
//
// Example:
//
//	recovery := NewShutdownHookRecovery(
//	    WithShutdownHookRetry(2, time.Second),
//	)
func WithShutdownHookRetry(retries int, interval time.Duration) RecoveryOption {
	return func(r *ShutdownHookRecovery) {
		r.retries = retries
		r.interval = interval
	}
}

// WithShutdownHookRecoveryStrategy sets the RecoveryStrategy for a ShutdownHookRecovery.
// Use this option with NewShutdownHookRecovery to specify the error handling policy
// to apply if a shutdown hook fails after all retries.
//
// Parameters:
//   - strategy: The RecoveryStrategy to use (e.g., ShouldFail, ShouldRetryAndSkip).
//
// Example:
//
//	recovery := NewShutdownHookRecovery(
//	    WithShutdownHookRecoveryStrategy(ShouldRetryAndSkip),
//	)
func WithShutdownHookRecoveryStrategy(strategy RecoveryStrategy) RecoveryOption {
	return func(r *ShutdownHookRecovery) {
		r.strategy = strategy
	}
}

// ShutdownHookRecovery defines the configuration for handling failures during the execution
// of a ShutdownHook. It specifies the number of retries, the interval between retries, and
// the recovery strategy to use if a shutdown hook fails.
//
// Use NewShutdownHookRecovery and RecoveryOption functions to construct and configure an instance.
type ShutdownHookRecovery struct {
	retries  int
	interval time.Duration
	strategy RecoveryStrategy
}

// NewShutdownHookRecovery creates a new ShutdownHookRecovery with the provided options.
//
// By default, it uses DefaultShutdownRecoveryMaxRetries, DefaultShutdownHookRecoveryRetryInterval,
// and the ShouldFail strategy. You can override these defaults using RecoveryOption functions.
//
// Example:
//
//	recovery := NewShutdownHookRecovery(
//	    WithShutdownHookRetry(2, time.Second),
//	    WithShutdownHookRecoveryStrategy(ShouldRetryAndSkip),
//	)
func NewShutdownHookRecovery(opts ...RecoveryOption) *ShutdownHookRecovery {
	recovery := &ShutdownHookRecovery{
		retries:  DefaultShutdownRecoveryMaxRetries,
		interval: DefaultShutdownHookRecoveryRetryInterval,
		strategy: ShouldFail,
	}
	// Apply options to configure the recovery behavior
	for _, opt := range opts {
		opt(recovery)
	}
	return recovery
}

// Retry returns the configured number of retries and the interval between retries
// for the ShutdownHookRecovery.
//
// Returns:
//   - int:          The number of retry attempts.
//   - time.Duration: The duration to wait between retries.
func (r *ShutdownHookRecovery) Retry() (int, time.Duration) {
	return r.retries, r.interval
}

// Strategy returns the RecoveryStrategy configured for the ShutdownHookRecovery.
//
// Returns:
//   - RecoveryStrategy: The strategy to use when a shutdown hook fails.
func (r *ShutdownHookRecovery) Strategy() RecoveryStrategy {
	return r.strategy
}
