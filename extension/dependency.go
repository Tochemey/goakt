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

package extension

import (
	"encoding"
)

// Dependency defines the contract for an actor's external dependency within the actor system.
//
// A Dependency represents a service, resource, or capability that an actor interacts with
// during its lifecycle. Dependencies are critical to the actor’s runtime behavior and are
// managed centrally through the system’s dependency registry.
//
// Implementations of Dependency must be serializable to enable dynamic management, persistence,
// and reconstruction of dependencies during actor restarts, migrations, or failovers.
//
// Typical examples of dependencies include:
//   - Database or cache clients (e.g., PostgreSQL, Redis)
//   - External API clients (e.g., Payment gateways, Authentication services)
//   - Configuration providers
//   - Internal utility services (e.g., Metrics collectors)
//
// All Dependency implementations must be registered with the actor system to support their
// recreation and lifecycle management.
//
// Dependency extends Serializable to guarantee that each instance can be transported or persisted
// safely across system boundaries.
type Dependency interface {
	Serializable
	// ID returns the unique identifier for the extension.
	//
	// The identifier must:
	//   - Be no more than 255 characters long.
	//   - Start with an alphanumeric character [a-zA-Z0-9].
	//   - Contain only alphanumeric characters, hyphens (-), or underscores (_) thereafter.
	//
	// Identifiers that do not meet these constraints are considered invalid.
	ID() string
}

// Serializable defines the contract for types that can be serialized to and deserialized from binary form.
//
// Serializable types must implement both encoding.BinaryMarshaler and encoding.BinaryUnmarshaler,
// ensuring compatibility with standard binary encoding mechanisms.
//
// This guarantees that serialized representations of Dependencies and their arguments are portable,
// efficient, and suitable for storage or transmission across the network.
type Serializable interface {
	encoding.BinaryMarshaler
	encoding.BinaryUnmarshaler
}
