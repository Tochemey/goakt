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

// Key uniquely identifies the state of a virtual actor in the actor system.
//
// In an actor model, each actor is uniquely identified by a combination
// of its kind (type) and a string-based identifier (actor ID). The Key interface
// abstracts this concept, allowing the underlying implementation to define
// how actor identity is represented, compared, and serialized.
//
// Implementations of this interface should ensure that two Keys with the same
// Kind and ActorID are considered equal.
type Key interface {
	// ActorID returns the unique string identifier for the actor instance.
	//
	// This value typically distinguishes one actor from another within the same Kind.
	ActorID() string

	// Kind returns the actor's type or classification.
	//
	// This is useful for scoping or partitioning actor types in the system, e.g.,
	// "UserActor", "OrderActor", etc.
	Kind() string

	// String returns a stable, human-readable representation of the key.
	//
	// This method is useful for logging, diagnostics, and key serialization.
	String() string

	// Equal reports whether the current key and the provided key refer to the same actor.
	//
	// Equality should be based on both Kind and ActorID.
	Equal(other Key) bool
}
