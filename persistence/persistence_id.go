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

package persistence

// PersistenceID uniquely identifies a stateful actor within the actor system.
//
// In the actor model, each actor is uniquely identified by a combination
// of its kind (type) and a string-based entity identifier (EntityID). The PersistenceID
// interface abstracts this concept, allowing implementations to define how actor identity
// is represented, compared, and serialized.
//
// Implementations of this interface must ensure that two PersistenceIDs with the same
// Kind and EntityID are considered equal.
type PersistenceID interface { //nolint:revive
	// EntityID returns the unique string identifier for the actor instance.
	//
	// This value distinguishes one actor from another within the same Kind.
	EntityID() string

	// Kind returns the actor's type or classification.
	//
	// This is useful for scoping or partitioning actor types in the system, e.g.,
	// "UserActor", "OrderActor", etc.
	Kind() string

	// String returns a stable, human-readable representation of the persistence ID.
	//
	// This method is useful for logging, diagnostics, and key serialization.
	String() string

	// Equal reports whether the current persistence ID and the provided persistence ID refer to the same actor.
	//
	// Equality must be based on both Kind and EntityID.
	Equal(other PersistenceID) bool
}
