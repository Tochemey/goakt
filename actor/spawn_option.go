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
}

// newSpawnConfig creates an instance of spawnConfig
func newSpawnConfig(opts ...SpawnOption) *spawnConfig {
	config := &spawnConfig{
		relocatable: true,
		asSingleton: false,
	}

	for _, opt := range opts {
		opt.Apply(config)
	}
	return config
}

// SpawnOption is the interface that applies to
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
// and maintain high availability by automatically redeploying them on healthy nodes.
//
// Use this option when you need strict control over an actor's lifecycle and want to ensure that the actor
// is not redeployed after a node failure, such as for actors with node-specific state or dependencies that
// cannot be easily replicated.
func WithRelocationDisabled() SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.relocatable = false
	})
}

// withSingleton ensures that the actor is a singleton.
// This is an internal method to set the singleton flag
func withSingleton() SpawnOption {
	return spawnOption(func(config *spawnConfig) {
		config.asSingleton = true
	})
}
