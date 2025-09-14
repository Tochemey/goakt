/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package actor

import (
	"time"

	"github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/extension"
	"github.com/tochemey/goakt/v3/internal/validation"
	"github.com/tochemey/goakt/v3/passivation"
)

// SpawnPlacement defines the algorithm used by the actor system to determine
// where an actor should be spawned in a clustered environment.
//
// This strategy is only relevant when cluster mode is enabled.
// It affects how actors are distributed across the nodes in the cluster.
type SpawnPlacement int

const (
	// RoundRobin distributes actors evenly across nodes
	// by cycling through the available nodes in a round-robin manner.
	// This strategy provides balanced load distribution over time.
	RoundRobin SpawnPlacement = iota

	// Random selects a node at random from the available pool of nodes.
	// This strategy is stateless and can help quickly spread actors across the cluster,
	// but may result in uneven load distribution.
	Random

	// Local forces the actor to be spawned on the local node,
	// regardless of the cluster configuration.
	// Useful when locality is important (e.g., accessing local resources).
	Local

	// LeastLoad selects the node with the least current load to spawn the actor.
	// This strategy aims to optimize resource utilization by placing actors
	// on nodes that are less busy, potentially improving performance and responsiveness.
	// Note: This strategy may require additional overhead when placing actors,
	// as it needs to get nodes load metrics depending on the cluster size.
	LeastLoad
)

// spawnConfig defines the configuration options applied when creating an actor.
//
// It encapsulates all settings that influence actor spawning, such as mailbox type,
// supervisor strategy, passivation behavior, singleton status, relocation policy,
// dependency injection, stashing, system actor flag, placement strategy, and passivation strategy.
type spawnConfig struct {
	// mailbox defines the mailbox to use when spawning the actor.
	mailbox Mailbox
	// supervisor specifies the supervisor strategy for handling actor failures.
	supervisor *Supervisor
	// asSingleton indicates whether the actor should be a singleton within the system.
	asSingleton bool
	// relocatable determines if the actor can be relocated to another node in cluster mode.
	relocatable bool
	// dependencies lists the dependencies to inject into the actor upon initialization.
	dependencies []extension.Dependency
	// enableStash enables a stash buffer for temporarily storing unprocessable messages.
	enableStash bool
	// isSystem marks the actor as a system actor, used internally for system-level actors.
	isSystem bool
	// placement specifies the placement strategy for spawning the actor in a cluster.
	placement SpawnPlacement
	// passivationStrategy defines the strategy used for actor passivation.
	passivationStrategy passivation.Strategy
}

var _ validation.Validator = (*spawnConfig)(nil)

// Validate checks the validity of the spawnConfig, ensuring all dependencies have valid IDs.
//
// Returns an error if any dependency has an invalid ID, otherwise returns nil.
func (s *spawnConfig) Validate() error {
	if s.passivationStrategy != nil {
		switch s.passivationStrategy.(type) {
		case *passivation.TimeBasedStrategy, *passivation.LongLivedStrategy, *passivation.MessagesCountBasedStrategy:
			// pass
		default:
			return errors.NewErrInvalidPassivationStrategy(s.passivationStrategy)
		}
	}

	for _, dependency := range s.dependencies {
		if dependency != nil {
			if err := validation.NewIDValidator(dependency.ID()).Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

// newSpawnConfig creates and returns a new spawnConfig instance,
// applying any provided SpawnOption functions to customize the configuration.
//
// Parameters:
//   - opts: Variadic list of SpawnOption functions to apply.
//
// Returns:
//   - Pointer to a configured spawnConfig instance.
func newSpawnConfig(opts ...SpawnOption) *spawnConfig {
	config := &spawnConfig{
		relocatable:         true,
		asSingleton:         false,
		enableStash:         false,
		isSystem:            false,
		supervisor:          NewSupervisor(),
		dependencies:        make([]extension.Dependency, 0),
		placement:           RoundRobin,
		passivationStrategy: passivation.NewTimeBasedStrategy(DefaultPassivationTimeout),
	}

	for _, opt := range opts {
		opt.Apply(config)
	}
	return config
}

// SpawnOption defines the interface for configuring actor spawn behavior.
//
// Implementations of this interface can be passed to actor spawning functions
// to customize aspects such as mailbox, supervisor, passivation, dependencies, etc.
type SpawnOption interface {
	// Apply sets the option value on the provided spawnConfig.
	Apply(config *spawnConfig)
}

var _ SpawnOption = spawnOption(nil)

// spawnOption is a function type that implements the SpawnOption interface.
type spawnOption func(config *spawnConfig)

// Apply applies the spawnOption to the given spawnConfig.
func (f spawnOption) Apply(c *spawnConfig) {
	f(c)
}

// WithMailbox returns a SpawnOption that sets the mailbox to use when starting the given actor.
//
// Use this option to specify a custom mailbox implementation, such as a priority mailbox.
// Care should be taken to ensure the mailbox is compatible with the actor's message handling logic.
//
// Parameters:
//   - mailbox: The Mailbox implementation to use.
//
// Returns:
//   - SpawnOption that sets the mailbox in the spawn configuration.
func WithMailbox(mailbox Mailbox) SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.mailbox = mailbox
	})
}

// WithSupervisor returns a SpawnOption that sets the supervisor strategy to apply when the actor fails
// or panics during message processing. The specified supervisor determines how failures
// are handled, such as restarting, stopping, or resuming the actor.
//
// Parameters:
//   - supervisor: Pointer to a Supervisor defining the failure-handling policy.
//
// Returns:
//   - SpawnOption that sets the supervisor in the spawn configuration.
func WithSupervisor(supervisor *Supervisor) SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.supervisor = supervisor
	})
}

// WithPassivateAfter returns a SpawnOption that sets a custom duration after which an idle actor
// will be passivated. Passivation allows the actor system to free up resources by stopping actors
// that have been inactive for the specified duration. If the actor receives a message before this timeout,
// the passivation timer is reset.
//
// Deprecated: Use WithPassivationStrategy with TimeBasedStrategy instead.
//
// Parameters:
//   - after: Duration of inactivity before passivation.
//
// Returns:
//   - SpawnOption that sets the passivation timeout.
func WithPassivateAfter(after time.Duration) SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.passivationStrategy = passivation.NewTimeBasedStrategy(after)
	})
}

// WithLongLived returns a SpawnOption that ensures the given actor, once created, will persist
// for the entire lifespan of the running actor system. Unlike short-lived actors that may be restarted
// or garbage-collected, a long-lived actor remains active until the actor system itself shuts down.
//
// Returns:
//   - SpawnOption that disables passivation for the actor.
func WithLongLived() SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.passivationStrategy = passivation.NewLongLivedStrategy()
	})
}

// WithRelocationDisabled returns a SpawnOption that prevents the actor from being relocated to another node
// when cluster mode is active and its host node shuts down unexpectedly. By default, actors are relocatable
// to support system resilience and maintain high availability by automatically redeploying them on healthy nodes.
//
// Use this option when you need strict control over an actor's lifecycle and want to ensure that the actor
// is not redeployed after a node failure, such as for actors with node-specific state or dependencies that
// cannot be easily replicated.
//
// Returns:
//   - SpawnOption that disables relocation for the actor.
func WithRelocationDisabled() SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.relocatable = false
	})
}

// WithDependencies returns a SpawnOption that injects the given dependencies into
// the actor during its initialization.
//
// This function allows you to configure an actor with one or more dependencies,
// such as services, clients, or configuration objects it needs to function.
// These dependencies will be made available to the actor when it is spawned,
// enabling better modularity and testability.
//
// Parameters:
//   - dependencies: Variadic list of objects implementing the Dependency interface.
//
// Returns:
//   - SpawnOption that sets the actor's dependencies in the spawn configuration.
func WithDependencies(dependencies ...extension.Dependency) SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.dependencies = dependencies
	})
}

// WithStashing returns a SpawnOption that enables stashing and sets the stash buffer for the actor,
// allowing it to temporarily store incoming messages that cannot be immediately processed.
//
// This is particularly useful in scenarios where the actor must delay handling certain messages—for example,
// during initialization, while awaiting external resources, or transitioning between states.
//
// By stashing messages, the actor can defer processing until it enters a stable or ready state,
// at which point the buffered messages can be retrieved and handled in a controlled sequence.
// This helps maintain a clean and predictable message flow without dropping or prematurely
// processing input.
//
// ⚠️ Note: The stash buffer is *not* a substitute for robust message handling or proper
// supervision strategies. Misuse may lead to unbounded memory growth if messages are
// stashed but never unstashed. Always ensure the actor eventually processes or discards
// stashed messages to avoid leaks or state inconsistencies.
//
// Returns:
//   - SpawnOption that enables stashing for the actor.
func WithStashing() SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.enableStash = true
	})
}

// WithPlacement returns a SpawnOption that sets the placement strategy to be used when spawning an actor
// in cluster mode via the SpawnOn function.
//
// This option determines how the actor system selects a target node for spawning
// the actor across the cluster. Valid strategies include RoundRobin, Random, and Local.
//
// Note: This option only has an effect when used with SpawnOn in a cluster-enabled
// actor system. If cluster mode is disabled, the placement strategy is ignored
// and the actor will be spawned locally.
//
// Example:
//
//	err := system.SpawnOn(ctx, "analytics-worker", NewWorkerActor(),
//	    WithPlacement(RoundRobin))
//
// Parameters:
//   - strategy: A SpawnPlacement value specifying how to distribute the actor.
//
// Returns:
//   - SpawnOption that sets the placement strategy in the spawn configuration.
func WithPlacement(strategy SpawnPlacement) SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.placement = strategy
	})
}

// WithPassivationStrategy returns a SpawnOption that sets the passivation strategy to be used when spawning an actor.
//
// This option allows you to define how and when the actor should be passivated,
// which can help manage resource usage and actor lifecycle in a more controlled manner.
// The following strategies are available:
//   - TimeBasedStrategy: Passivates the actor after a specified duration of inactivity.
//   - LongLivedStrategy: Ensures the actor remains active for the entire lifespan of the actor system,
//     effectively preventing passivation.
//   - MessagesCountStrategy: Passivates the actor after processing a specified number of messages.
//
// Parameters:
//   - strategy: passivation.Strategy to apply.
//
// Returns:
//   - SpawnOption that sets the passivation strategy in the spawn configuration.
func WithPassivationStrategy(strategy passivation.Strategy) SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.passivationStrategy = strategy
	})
}

// withSingleton returns a SpawnOption that ensures the actor is a singleton within the system.
//
// This is an internal method to set the singleton flag and should not be used directly by end users.
//
// Returns:
//   - SpawnOption that marks the actor as a singleton.
func withSingleton() SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.asSingleton = true
	})
}

// asSystem returns a SpawnOption that marks the given actor as a system actor.
//
// This is used internally to spawn system-based actors and should not be used directly by end users.
//
// Returns:
//   - SpawnOption that marks the actor as a system actor.
func asSystem() SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.isSystem = true
	})
}
