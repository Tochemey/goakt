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
	"net"
	"strconv"
	"time"

	"github.com/tochemey/goakt/v4/datacenter"
	"github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/internal/validation"
	"github.com/tochemey/goakt/v4/passivation"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/supervisor"
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
	// ⚠️ Note: This strategy is subject to the cluster topology at the time of creation. For a stable cluster topology,
	// it ensures an even distribution of actors across all nodes.
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

type singletonSpec struct {
	SpawnTimeout time.Duration
	WaitInterval time.Duration
	MaxRetries   int32
}

// spawnConfig defines the configuration options applied when creating an actor.
//
// It encapsulates all settings that influence actor spawning, such as mailbox type,
// supervisor strategy, passivation behavior, singleton status, relocation policy,
// dependency injection, stashing, system actor flag, placement strategy, and passivation strategy.
type spawnConfig struct {
	// mailbox defines the mailbox to use when spawning the actor.
	mailbox Mailbox
	// supervisor specifies the supervisor strategy for handling actor failures.
	supervisor *supervisor.Supervisor
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
	// reentrancy defines the async request behavior for the actor.
	reentrancy *reentrancy.Reentrancy
	// role defines the role required for the node to spawn the actor.
	role *string
	// singletonSpec holds the singleton configuration if the actor is a singleton.
	singletonSpec *singletonSpec
	// dataCenter defines the datacenter to spawn the actor in.
	// this is only used when using the SpawnOn function with a datacenter-aware control plane.
	dataCenter *datacenter.DataCenter
	// host defines the host to spawn the actor on.
	// This will be used when remoting is enabled and the actor type must be registered
	// on the remote node.
	host *string
	// port defines the port to spawn the actor on.
	// This will be used when remoting is enabled and the actor type must be registered
	// on the remote node.
	port *int
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

	if s.reentrancy != nil && s.reentrancy.Validate() != nil {
		return errors.ErrInvalidReentrancyMode
	}

	for _, dependency := range s.dependencies {
		if dependency != nil {
			if err := validation.NewIDValidator(dependency.ID()).Validate(); err != nil {
				return err
			}
		}
	}

	// validate the host and port if any of them is set
	if s.host != nil || s.port != nil {
		address := net.JoinHostPort(*s.host, strconv.Itoa(*s.port))
		if err := validation.NewTCPAddressValidator(address).Validate(); err != nil {
			return err
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
		relocatable: true,
		asSingleton: false,
		enableStash: false,
		isSystem:    false,
		placement:   RoundRobin,
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
func WithSupervisor(supervisor *supervisor.Supervisor) SpawnOption {
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
// ⚠️ Note: This option only has an effect when used with SpawnOn in a cluster-enabled
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

// WithRole records a required node role for actor placement.
//
// In cluster mode, peers advertise roles via ClusterConfig.WithRoles (e.g. "projection",
// "payments", "api"). When used with SpawnOn in a cluster-enabled system, the actor will
// only be placed on nodes that advertise the same role. If multiple nodes match, the
// placement strategy (RoundRobin, Random, etc.) is applied among those nodes.
// If clustering is disabled, this option is ignored and the actor is spawned locally.
//
// If no node with the required role exists, spawning returns an error. This prevents
// accidental placement on unsuitable nodes and protects actors that depend on role-specific services or colocation.
//
// Tip: omit WithRole to allow placement on any node (or ensure all nodes advertise
// the role if you want it universal).
//
// Example:
//
//	pid, err := system.SpawnOn(ctx, "payment-saga", NewPaymentSaga(), WithRole("payments"))
//	if err != nil {
//	    return err
//	}
//
// Parameters:
//
//	role — label a node must advertise (e.g. "projection", "payments").
//
// Returns:
//   - SpawnOption that sets the role in the spawn configuration.
func WithRole(role string) SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.role = new(role)
	})
}

// WithReentrancy enables async requests for an actor and sets the default policy.
//
// Request/RequestName are disabled unless reentrancy is configured on the actor.
// Per-call overrides are available via WithReentrancyMode.
//
// Design decision: reentrancy is opt-in to preserve backward compatibility for
// existing actors. Enabling it makes Request/RequestName available; per-call
// overrides via WithReentrancyMode can tighten or relax behavior for a single
// request.
//
// Production note: prefer reentrancy.AllowAll for throughput and to avoid
// deadlocks in call cycles (A -> B -> A). Use reentrancy.StashNonReentrant only
// when strict message ordering is required, and always set Request timeouts to
// prevent indefinite stashing if a dependency slows or fails.
func WithReentrancy(reentrancy *reentrancy.Reentrancy) SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.reentrancy = reentrancy
	})
}

// WithDataCenter returns a SpawnOption that sets the datacenter to spawn the actor in.
//
// This option is only used when using the SpawnOn function with a datacenter-aware control plane.
//
// Parameters:
//   - dataCenter: The datacenter to spawn the actor in.
//
// Returns:
//   - SpawnOption that sets the datacenter in the spawn configuration.
func WithDataCenter(dataCenter *datacenter.DataCenter) SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		if dataCenter != nil {
			config.dataCenter = dataCenter
		}
	})
}

// WithHostAndPort returns a SpawnOption that sets the host and port to spawn the actor on.
//
// This option is only used when using the SpawnOn function with a datacenter-aware control plane.
// It will be used when remoting is enabled and the actor type must be registered
// on the remote node.
//
// Parameters:
//   - host: The host to spawn the actor on.
//   - port: The port to spawn the actor on.
//
// Returns:
//   - SpawnOption that sets the host and port in the spawn configuration.
func WithHostAndPort(host string, port int) SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.host = new(host)
		config.port = new(port)
	})
}

// withSingleton returns a SpawnOption that ensures the actor is a singleton within the system.
//
// This is an internal method to set the singleton flag and should not be used directly by end users.
//
// Returns:
//   - SpawnOption that marks the actor as a singleton.
func withSingleton(spec *singletonSpec) SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.asSingleton = true
		config.singletonSpec = spec
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
