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
	"time"

	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v3/extension"
	"github.com/tochemey/goakt/v3/internal/pointer"
	"github.com/tochemey/goakt/v3/internal/validation"
	"github.com/tochemey/goakt/v3/internal/xsync"
)

// ActivationStrategy defines the algorithm used by the actor system to determine
// where a grain should be activated in a clustered environment.
//
// This strategy is only relevant when cluster mode is enabled.
// It affects how grains are distributed across the nodes in the cluster.
type ActivationStrategy int

const (
	// RoundRobinActivation distributes grains evenly across nodes
	// by cycling through the available nodes in a round-robin manner.
	// This strategy provides balanced load distribution over time.
	// ⚠️ Note: This strategy is subject to the cluster topology at the time of activation. For a stable cluster topology,
	// it ensures an even distribution of grains across all nodes.
	// ⚠️ Note: This strategy will only be applied if the given Grain does not exist yet when cluster mode is enabled.
	// ⚠️ Note: If the Grain already exists on another node, it will be activated there instead.
	RoundRobinActivation ActivationStrategy = iota

	// Random selects a node at random from the available pool of nodes.
	// This strategy is stateless and can help quickly spread grains across the cluster,
	// but may result in uneven load distribution.
	// ⚠️ Note: This strategy will only be applied if the given Grain does not exist yet when cluster mode is enabled.
	// ⚠️ Note: If the Grain already exists on another node, it will be activated there instead.
	RandomActivation

	// LocalActivation forces the grain to be activated on the local node.
	// Useful when locality is important (e.g., accessing local resources).
	// ⚠️ Note: This strategy will only be applied if the given Grain does not exist yet when cluster mode is enabled.
	// ⚠️ Note: If the Grain already exists on another node, it will be activated there instead.
	LocalActivation

	// LeastLoadActivation selects the node with the least current load to activate the grain.
	// This strategy aims to optimize resource utilization by placing grains
	// on nodes that are less busy, potentially improving performance and responsiveness.
	// ⚠️ Note: This strategy may require additional overhead when placing grains,
	// as it needs to get nodes load metrics depending on the cluster size.
	// ⚠️ Note: This strategy will only be applied if the given Grain does not exist yet when cluster mode is enabled.
	// ⚠️ Note: If the Grain already exists on another node, it will be activated there instead.
	LeastLoadActivation
)

type GrainOption func(config *grainConfig)
type grainConfig struct {
	// initMaxRetries is the maximum number of retries when initializing a grain.
	initMaxRetries atomic.Int32
	// initTimeout is the timeout duration for grain initialization.
	initTimeout     atomic.Duration
	deactivateAfter time.Duration
	dependencies    *xsync.Map[string, extension.Dependency]
	// role defines the role required for the node to activate the grain.
	role *string
	// placement specifies the placement strategy for activating the grain in a cluster.
	activationStrategy ActivationStrategy
}

// newGrainConfig creates a new grainConfig instance and applies the provided GrainOption(s).
// It sets default values for initialization retries and timeout, and allows customization
// through functional options. This function is typically used internally to configure
// grain initialization and passivation behavior.
//
// Parameters:
//   - opts: zero or more GrainOption functions to customize the grainConfig.
//
// Returns:
//   - *grainConfig: a pointer to the configured grainConfig instance.
func newGrainConfig(opts ...GrainOption) *grainConfig {
	config := &grainConfig{
		initMaxRetries:     atomic.Int32{},
		initTimeout:        atomic.Duration{},
		deactivateAfter:    DefaultPassivationTimeout,
		dependencies:       xsync.NewMap[string, extension.Dependency](),
		activationStrategy: LocalActivation,
	}

	// Set default values
	config.initMaxRetries.Store(DefaultInitMaxRetries)
	config.initTimeout.Store(DefaultInitTimeout)

	for _, opt := range opts {
		opt(config)
	}

	return config
}

var _ validation.Validator = (*grainConfig)(nil)

// Validate checks the validity of the spawnConfig, ensuring all dependencies have valid IDs.
//
// Returns an error if any dependency has an invalid ID, otherwise returns nil.
func (s *grainConfig) Validate() error {
	for _, dependency := range s.dependencies.Values() {
		if dependency != nil {
			if err := validation.NewIDValidator(dependency.ID()).Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

// WithGrainInitMaxRetries returns a GrainOption that sets the maximum number of retries
// for grain initialization. This is useful for handling transient initialization errors
// by retrying the initialization process up to the specified number of times before giving up.
//
// Parameters:
//   - value: the maximum number of retries (default is 5).
//
// Returns:
//   - GrainOption: a function that sets the initMaxRetries field in grainConfig.
//
// Usage example:
//
//	cfg := newGrainConfig(WithGrainInitMaxRetries(10))
func WithGrainInitMaxRetries(value int) GrainOption {
	return func(config *grainConfig) {
		config.initMaxRetries.Store(int32(value))
	}
}

// WithGrainInitTimeout returns a GrainOption that sets the timeout duration for grain initialization.
// If the grain does not initialize within this duration, initialization is considered failed and
// no further retries will be attempted.
//
// Parameters:
//   - value: the timeout duration (default is 1 second).
//
// Returns:
//   - GrainOption: a function that sets the initTimeout.
//
// Usage example:
//
//	WithGrainInitTimeout(2 * time.Second)
func WithGrainInitTimeout(value time.Duration) GrainOption {
	return func(config *grainConfig) {
		config.initTimeout.Store(value)
	}
}

// WithGrainDeactivateAfter returns a GrainOption that sets the duration after which a grain
// will be deactivated if it remains idle. This helps manage resources by deactivating grains
// that are not in use, reducing memory usage and improving system performance.
//
// Parameters:
//   - value: the duration of inactivity after which the grain is deactivated (default is 2 minutes).
//
// Returns:
//   - GrainOption: a function that sets the deactivateAfter.
func WithGrainDeactivateAfter(value time.Duration) GrainOption {
	return func(config *grainConfig) {
		config.deactivateAfter = value
	}
}

// WithLongLivedGrain returns a GrainOption that configures the grain to never be deactivated
// due to inactivity. When this option is set, the grain will remain active in memory
// regardless of idle time, and the passivation timer is effectively disabled.
//
// This is useful for grains that must always be available, such as those managing
// critical state, coordinating long-running workflows, or acting as singletons.
//
// Note: Use this option judiciously, as long-lived grains consume system resources
// for their entire lifetime and are not subject to automatic passivation.
func WithLongLivedGrain() GrainOption {
	return func(config *grainConfig) {
		config.deactivateAfter = -1
	}
}

// WithGrainDependencies returns a GrainOption that registers one or more dependencies
// for the grain. Dependencies are external services, resources, or components that
// the grain requires to operate. This option enables dependency injection, promoting
// loose coupling and easier testing.
//
// Each dependency must implement the extension.Dependency interface and must have a unique ID.
// If a dependency with the same ID already exists, it will be overwritten.
//
// Parameters:
//   - deps: One or more extension.Dependency instances to associate with the grain.
//
// Returns:
//   - GrainOption: A function that registers the provided dependencies in the grain's configuration.
//
// Example usage:
//
//	db := NewDatabaseDependency()
//	cache := NewCacheDependency()
//	cfg := newGrainConfig(WithGrainDependencies(db, cache))
func WithGrainDependencies(deps ...extension.Dependency) GrainOption {
	return func(config *grainConfig) {
		if config.dependencies == nil {
			config.dependencies = xsync.NewMap[string, extension.Dependency]()
		}
		for _, dep := range deps {
			if dep != nil {
				config.dependencies.Set(dep.ID(), dep)
			}
		}
	}
}

// WithActivationStrategy returns a GrainOption that sets the placement strategy to be used when activating a Grain
// in cluster mode.
//
// This option determines how the actor system selects a target node for activating
// the Grain across the cluster. Valid strategies include RoundRobin, Random, and Local.
//
// Note: This option only has an effect in a cluster-enabled actor system. If cluster mode is disabled, the placement strategy is ignored
// and the Grain will be activated locally.
//
// Parameters:
//   - strategy: A ActivationStrategy value specifying how to distribute the Grain.
//
// Returns:
//   - GrainOption that sets the activation strategy.
func WithActivationStrategy(strategy ActivationStrategy) GrainOption {
	return func(config *grainConfig) {
		config.activationStrategy = strategy
	}
}

// WithActivationRole records a required node role for Grain placement.
//
// In cluster mode, peers advertise roles via ClusterConfig.WithRoles (e.g. "projection",
// "payments", "api"). When role is used the Grain will
// only be activated on nodes that advertise the same role. If multiple nodes match, the
// placement strategy (RoundRobin, Random, etc.) is applied among those nodes.
// If clustering is disabled, this option is ignored and the Grain is activated locally.
//
// If no node with the required role exists, activation returns an error. This prevents
// accidental placement on unsuitable nodes and protects Grains that depend on role-specific services or colocation.
//
// Tip: omit WithActivationRole to allow placement on any node (or ensure all nodes advertise
// the role if you want it universal).
//
// Parameters:
//
//	role — label a node must advertise (e.g. "projection", "payments").
//
// Returns:
//   - GrainOption that sets the role in the grainConfig.
func WithActivationRole(role string) GrainOption {
	return func(config *grainConfig) {
		config.role = pointer.To(role)
	}
}
