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
	"context"

	"github.com/tochemey/goakt/v3/extension"
)

// Context provides an environment for an actor when it is about to start.
//
// It embeds the standard context.Context interface and grants access to the ActorSystem
// that manages the actor's lifecycle.
//
// Use Context to:
//   - Access the actor's system-wide services and dependencies.
//   - Retrieve deadlines, cancellation signals, and request-scoped values
//     through the embedded context.Context.
//
// A Context is guaranteed to be available during the actor’s initialization phase
// and should be treated as immutable.
type Context struct {
	ctx          context.Context
	actorSystem  ActorSystem
	actorName    string
	dependencies []extension.Dependency
}

// newContext creates and returns a new Context instance.
//
// It wraps the provided context.Context and associates it with the specified ActorSystem.
func newContext(ctx context.Context, actorName string, actorSystem ActorSystem, dependencies ...extension.Dependency) *Context {
	return &Context{
		ctx:          ctx,
		actorSystem:  actorSystem,
		actorName:    actorName,
		dependencies: dependencies,
	}
}

// Context returns the underlying context associated with the actor's initialization.
//
// It carries deadlines, cancellation signals, and request-scoped values.
// This method allows direct access to standard context operations.
func (x *Context) Context() context.Context {
	return x.ctx
}

// ActorSystem returns the ActorSystem that manages the actor.
//
// It provides access to system-level services, configuration, and infrastructure components
// that the actor may interact with during its lifecycle.
func (x *Context) ActorSystem() ActorSystem {
	return x.actorSystem
}

// Extensions returns a slice of all extensions registered within the ActorSystem
// associated with the Context.
//
// This allows system-level introspection or iteration over all available extensions.
// It can be useful for message processing.
//
// Returns:
//   - []extension.Extension: All registered extensions in the ActorSystem.
func (x *Context) Extensions() []extension.Extension {
	return x.ActorSystem().Extensions()
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
func (x *Context) Extension(extensionID string) extension.Extension {
	return x.ActorSystem().Extension(extensionID)
}

// ActorName returns the name of the actor associated with this Context.
//
// The actor name is a unique string within the actor system, typically used for
// identification, logging, monitoring, and message routing purposes.
func (x *Context) ActorName() string {
	return x.actorName
}

// Dependencies returns a slice containing all dependencies currently registered
// within the PID's local context.
//
// These dependencies are typically injected at actor initialization (via SpawnOptions)
// and made accessible during the actor's lifecycle. They can include services, clients,
// or any resources that the actor requires to operate.
//
// This method is useful for diagnostic tools, dynamic inspection, or cases where
// an actor needs to introspect its environment.
//
// Returns: A slice of Dependency instances associated with this PID.
func (x *Context) Dependencies() []extension.Dependency {
	return x.dependencies
}
