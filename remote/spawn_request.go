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

package remote

import (
	"strings"

	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/internal/validation"
	"github.com/tochemey/goakt/v4/passivation"
)

// SpawnRequest defines configuration options for spawning an actor on a remote node.
// These options control the actor’s identity, behavior, and lifecycle, especially in scenarios involving node failures or load balancing.
type SpawnRequest struct {
	// Name represents the unique name of the actor.
	// This name is used to identify and reference the actor across different nodes.
	Name string

	// Kind represents the type of the actor.
	// It typically corresponds to the actor’s implementation within the system
	Kind string

	// Singleton specifies whether the actor is a singleton, meaning only one instance of the actor
	// can exist across the entire cluster at any given time.
	// This option is useful for actors responsible for global coordination or shared state.
	// When Singleton is set to true it means that the given actor is automatically relocatable
	Singleton bool

	// Relocatable indicates whether the actor can be automatically relocated to another node
	// if its current host node unexpectedly shuts down.
	// By default, actors are relocatable to ensure system resilience and high availability.
	// Setting this to false ensures that the actor will not be redeployed after a node failure,
	// which may be necessary for actors with node-specific dependencies or state.
	Relocatable bool

	// PassivationStrategy sets the passivation strategy after which an actor
	// will be passivated. Passivation allows the actor system to free up
	// resources by stopping actors that have been inactive for the specified
	// duration. If the actor receives a message before this timeout,
	// the passivation timer is reset.
	PassivationStrategy passivation.Strategy

	// Dependencies define the list of dependencies that injects the given dependencies into
	// the actor during its initialization.
	//
	// This allows you to configure an actor with one or more dependencies,
	// such as services, clients, or configuration objects it needs to function.
	// These dependencies will be made available to the actor when it is spawned,
	// enabling better modularity and testability.
	Dependencies []extension.Dependency

	// EnableStashing enables stashing and sets the stash buffer for the actor, allowing it to temporarily store
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
	EnableStashing bool
}

// _ ensures that SpawnRequest implements the validation.Validator interface at compile time.
var _ validation.Validator = (*SpawnRequest)(nil)

// Validate validates the SpawnRequest
func (s *SpawnRequest) Validate() error {
	if err := validation.
		New(validation.FailFast()).
		AddValidator(validation.NewEmptyStringValidator("Name", s.Name)).
		AddValidator(validation.NewEmptyStringValidator("Kind", s.Kind)).
		Validate(); err != nil {
		return err
	}

	if len(s.Dependencies) > 0 {
		for _, dependency := range s.Dependencies {
			if err := validation.NewIDValidator(dependency.ID()).Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

// Sanitize sanitizes the request
func (s *SpawnRequest) Sanitize() {
	s.Name = strings.TrimSpace(s.Name)
	s.Kind = strings.TrimSpace(s.Kind)
	if s.Singleton {
		s.Relocatable = true
	}
}
