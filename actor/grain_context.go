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
	"context"

	"github.com/tochemey/goakt/v3/extension"
)

type GrainContext struct {
	ctx          context.Context
	self         *GrainKey
	actorSystem  ActorSystem
	dependencies []extension.Dependency
}

// Context returns the underlying context associated with the GrainContext.
//
// It carries deadlines, cancellation signals, and request-scoped values.
// This method allows direct access to standard context operations.
func (g *GrainContext) Context() context.Context {
	return g.ctx
}

// Self returns the unique identifier of the Grain instance.
func (g *GrainContext) Self() *GrainKey {
	return g.self
}

// ActorSystem returns the ActorSystem that manages the Grain.
//
// It provides access to system-level services, configuration, and infrastructure components
// that the actor may interact with during its lifecycle.
func (g *GrainContext) ActorSystem() ActorSystem {
	return g.actorSystem
}

// Dependencies returns a slice containing all dependencies currently registered
// within the Grain.
//
// These dependencies are typically injected during the Grain activation phase
// and made accessible during the Grain's lifecycle. They can include services, clients,
// or any resources that the actor requires to operate.
//
// This method is useful for diagnostic tools, dynamic inspection, or cases where
// an actor needs to introspect its environment.
//
// Returns: A slice of Dependency instances associated with the Grain
func (g *GrainContext) Dependencies() []extension.Dependency {
	return g.dependencies
}

// Extensions returns a slice of all extensions registered within the ActorSystem
// associated with the GrainContext.
//
// This allows system-level introspection or iteration over all available extensions.
// It can be useful for message processing.
//
// Returns:
//   - []extension.Extension: All registered extensions in the ActorSystem.
func (g *GrainContext) Extensions() []extension.Extension {
	return g.ActorSystem().Extensions()
}

// Extension retrieves a specific extension registered in the ActorSystem by its unique ID.
//
// This allows actors to access shared functionality injected into the system, such as
// event sourcing, metrics, tracing, or custom application services, directly from the Context.
//
// Example:
//
//	logger := x.Extension("extensionID").(MyExtension)
//
// Parameters:
//   - extensionID: A unique string identifier used when the extension was registered.
//
// Returns:
//   - extension.Extension: The corresponding extension if found, or nil otherwise.
func (g *GrainContext) Extension(extensionID string) extension.Extension {
	return g.ActorSystem().Extension(extensionID)
}

// newGrainContext creates and returns a new GrainContext instance.
func newGrainContext(ctx context.Context, self *GrainKey, actorSystem ActorSystem, dependencies ...extension.Dependency) *GrainContext {
	return &GrainContext{
		ctx:          ctx,
		self:         self,
		actorSystem:  actorSystem,
		dependencies: dependencies,
	}
}
