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

import "github.com/tochemey/goakt/v3/extension"

// GrainProps encapsulates configuration and metadata for a Grain (virtual actor) in the goakt actor system.
//
// It holds the unique identity of the Grain and a reference to the ActorSystem that manages it.
// GrainProps is used internally to track and manage Grain instances, ensuring each Grain is uniquely addressable
// and associated with the correct actor system.
//
// Fields:
//   - identity:    The unique identity of the Grain, used for addressing and routing.
//   - actorSystem: The ActorSystem instance that owns and manages the Grain.
type GrainProps struct {
	identity     *GrainIdentity
	actorSystem  ActorSystem
	dependencies []extension.Dependency
}

// newGrainProps creates and returns a new GrainProps instance for the specified identity and actor system.
//
// This function is used internally by the framework to construct the properties required to manage a Grain.
//
// Parameters:
//   - identity:    The unique identity of the Grain.
//   - actorSystem: The ActorSystem that will manage the Grain.
//
// Returns:
//   - *GrainProps: A new instance containing the provided identity and actor system.
func newGrainProps(identity *GrainIdentity, actorSystem ActorSystem, dependencies []extension.Dependency) *GrainProps {
	return &GrainProps{
		identity:     identity,
		actorSystem:  actorSystem,
		dependencies: dependencies,
	}
}

// Identity returns the unique identity of the Grain associated with these properties.
//
// The GrainIdentity is used to uniquely identify and address the Grain within the actor system.
// This is typically a composite of the Grain's type and instance key.
//
// Returns:
//   - *GrainIdentity: The identity object representing this Grain.
func (props *GrainProps) Identity() *GrainIdentity {
	return props.identity
}

// ActorSystem returns the ActorSystem instance associated with these Grain properties.
//
// This provides access to the actor system that manages the lifecycle, messaging, and clustering
// for the Grain represented by these properties.
//
// Returns:
//   - ActorSystem: The actor system managing this Grain.
func (props *GrainProps) ActorSystem() ActorSystem {
	return props.actorSystem
}

// Dependencies returns the dependencies registered with this GrainProps.
//
// Dependencies are external services, resources, or components that the Grain requires to operate.
// These are typically injected into the Grain at creation time, enabling loose coupling and easier testing.
//
// Returns:
//   - []extension.Dependency: A slice containing all dependencies associated with this GrainProps instance.
//
// Example usage:
//
//	deps := grainProps.Dependencies()
//	for _, dep := range deps {
//	    // Use dep as needed
//	}
func (props *GrainProps) Dependencies() []extension.Dependency {
	return props.dependencies
}
