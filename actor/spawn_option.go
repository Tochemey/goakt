/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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

	"github.com/tochemey/goakt/v3/extension"
	"github.com/tochemey/goakt/v3/internal/validation"
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
)

// spawnConfig defines the configuration to apply when creating an actor
type spawnConfig struct {
	// mailbox defines the mailbox to use when spawning the actor
	mailbox Mailbox
	// defines the supervisor
	supervisor *Supervisor
	// specifies at what point in time to passivate the actor.
	// when the actor is passivated it is stopped which means it does not consume
	// any further resources like memory and cpu. The default value is 120 seconds
	passivateAfter *time.Duration
	// specifies if the actor is a singleton
	asSingleton bool
	// specifies if the actor should be relocated
	relocatable bool
	// specifies the list of dependencies
	dependencies []extension.Dependency
	// specifies whether that actor should be having stash buffer
	enableStash bool
	// specifies whether the given actor is a system actor
	isSystem bool
	// specifies the placement
	placement SpawnPlacement
}

var _ validation.Validator = (*spawnConfig)(nil)

func (s *spawnConfig) Validate() error {
	for _, dependency := range s.dependencies {
		if dependency != nil {
			if err := validation.NewIDValidator(dependency.ID()).Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

// newSpawnConfig creates an instance of spawnConfig
func newSpawnConfig(opts ...SpawnOption) *spawnConfig {
	config := &spawnConfig{
		relocatable: true,
		asSingleton: false,
		enableStash: false,
		isSystem:    false,
		supervisor: NewSupervisor(
			WithStrategy(OneForOneStrategy),
			WithAnyErrorDirective(ResumeDirective),
		),
		dependencies: make([]extension.Dependency, 0),
		placement:    RoundRobin,
	}

	for _, opt := range opts {
		opt.Apply(config)
	}
	return config
}

// SpawnOption is the interface that applies to an actor during spawning.
type SpawnOption interface {
	// Apply sets the Option value of a config.
	Apply(config *spawnConfig)
}

var _ SpawnOption = spawnOption(nil)

// spawnOption implements the SpawnOption interface.
type spawnOption func(config *spawnConfig)

// Apply sets the Option value of a config.
func (f spawnOption) Apply(c *spawnConfig) {
	f(c)
}

// WithMailbox sets the mailbox to use when starting the given actor
// Care should be taken when using a specific mailbox for a given actor on how to handle
// messages particularly when it comes to priority mailbox
func WithMailbox(mailbox Mailbox) SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.mailbox = mailbox
	})
}

// WithSupervisor sets the supervisor to apply when the actor fails
// or panics during message processing. The specified supervisor behavior determines how failures
// are handled, such as restarting, stopping, or resuming the actor.
//
// Parameters:
//   - supervisor: A pointer to a `Supervisor` that defines
//     the failure-handling policy for the actor.
//
// Returns:
//   - A `SpawnOption` that applies the given supervisor strategy when spawning an actor.
func WithSupervisor(supervisor *Supervisor) SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.supervisor = supervisor
	})
}

// WithPassivateAfter sets a custom duration after which an idle actor
// will be passivated. Passivation allows the actor system to free up
// resources by stopping actors that have been inactive for the specified
// duration. If the actor receives a message before this timeout,
// the passivation timer is reset.
func WithPassivateAfter(after time.Duration) SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.passivateAfter = &after
	})
}

// WithLongLived ensures that the given actor, once created, will persist
// for the entire lifespan of the running actor system. Unlike short-lived
// actors that may be restarted or garbage-collected, a long-lived actor
// remains active until the actor system itself shuts down.
func WithLongLived() SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.passivateAfter = &longLived
	})
}

// WithRelocationDisabled prevents the actor from being relocated to another node when cluster mode is active
// and its host node shuts down unexpectedly. By default, actors are relocatable to support system resilience
// and maintain high availability by automatically redeploying them on healthy nodesMap.
//
// Use this option when you need strict control over an actor's lifecycle and want to ensure that the actor
// is not redeployed after a node failure, such as for actors with node-specific state or dependencies that
// cannot be easily replicated.
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
// Parameters: dependencies - a variadic list of objects implementing the Dependency interface.
//
// Returns: A SpawnOption that sets the actor's dependencies in the internal spawn configuration.
func WithDependencies(dependencies ...extension.Dependency) SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.dependencies = dependencies
	})
}

// WithStashing enables stashing and sets the stash buffer for the actor, allowing it to temporarily store
// incoming messages that cannot be immediately processed. This is particularly useful
// in scenarios where the actor must delay handling certain messages—for example,
// during initialization, while awaiting external resources, or transitioning between states.
//
// By stashing messages, the actor can defer processing until it enters a stable or ready state,
// at which point the buffered messages can be retrieved and handled in a controlled sequence.
// This helps maintain a clean and predictable message flow without dropping or prematurely
// processing input.
//
// Use WithStashing when spawning the actor to activate this capability. By default, the stash
// buffer is disabled.
//
// ⚠️ Note: The stash buffer is *not* a substitute for robust message handling or proper
// supervision strategies. Misuse may lead to unbounded memory growth if messages are
// stashed but never unstashed. Always ensure the actor eventually processes or discards
// stashed messages to avoid leaks or state inconsistencies.
//
// When used correctly, the stash buffer is a powerful tool for managing transient states
// and preserving actor responsiveness while maintaining orderly message handling.
func WithStashing() SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.enableStash = true
	})
}

// WithPlacement sets the placement strategy to be used when spawning an actor
// in cluster mode via the SpawnOn function.
//
// This option determines how the actor system selects a target node for spawning
// the actor across the cluster. Valid strategies include RoundRobin,
// Random, and Local.
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
//   - SpawnOption: An option that can be passed to SpawnOn.
func WithPlacement(strategy SpawnPlacement) SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.placement = strategy
	})
}

// withSingleton ensures that the actor is a singleton.
// This is an internal method to set the singleton flag
func withSingleton() SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.asSingleton = true
	})
}

// asSystem states that the given actor is a system actor;
// this is used internally to spawn system-based actors
func asSystem() SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.isSystem = true
	})
}
