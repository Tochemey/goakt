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

import "encoding"

// State defines the contract for an actor's persisted state within the state store.
//
// This interface abstracts the data and metadata for a single actor instance, supporting
// reliable persistence, retrieval, and optimistic concurrency control in the actor system.
//
// Features:
//
//   - Uniquely identified by a Key, which combines the actor's Kind (type) and EntityID.
//   - Supports versioning for optimistic concurrency: each update increments the version,
//     and conditional writes (Compare-And-Swap) prevent lost updates in concurrent scenarios.
//   - Designed for serialization and deserialization for storage and network transmission.
//
// Typical Usage:
//
//  1. Retrieve the State from the store using its PersistenceID.
//  2. Read or modify the state data as needed.
//  3. Persist changes by writing the updated State back to the store, using the Version for concurrency checks.
//  4. If the write fails due to a version mismatch, reload and retry as appropriate.
//
// Implementations must ensure that each actor instance maps to exactly one State entry and
// that the Version is incremented on each successful write.
type State interface {
	encoding.BinaryMarshaler
	encoding.BinaryUnmarshaler
	// PersistenceID returns the unique identifier for this state record.
	//
	// The PersistenceID combines the actor's Kind (type) and a unique EntityID,
	// ensuring a one-to-one mapping between actor instances and state snapshot records.
	PersistenceID() PersistenceID

	// Version returns the current version of this state entry for optimistic concurrency control.
	//
	// Each successful write to the store should increment the version.
	// When performing conditional updates, the store should compare the existing
	// version with the provided one, rejecting the write if there's a mismatch.
	// This protects against race conditions and lost updates in concurrent systems.
	Version() int64
}

// EmptyState is a sentinel implementation of the State interface representing the absence of persisted state.
//
// EmptyState is used for actors that do not require state persistence or when an actor's state
// has not yet been initialized in the state store. It provides a valid, non-nil State object
// with a unique Key and a Version of zero.
//
// # Usage
//
//   - Returned by state stores when no state exists for a given actor Key.
//   - Used as a placeholder for stateless actors or for initialization scenarios.
//   - Allows code to uniformly handle State without special nil checks.
//
// The Version method always returns zero, and EmptyState should never be persisted to the store.
type EmptyState struct {
	// key is the unique identifier for this state record.
	key PersistenceID
}

// ensure EmptyState implements the State interface
var _ State = (*EmptyState)(nil)

// NewEmptyState creates a new EmptyState instance for the given key.
func NewEmptyState(key PersistenceID) *EmptyState {
	return &EmptyState{
		key: key,
	}
}

// PersistenceID returns the unique identifier for this state record.
//
// The key combines the actor's Kind (type) and a unique ActorID,
// ensuring a one-to-one mapping between actor instances and state records.
func (n *EmptyState) PersistenceID() PersistenceID {
	return n.key
}

// Version returns the current version of this state entry for optimistic concurrency control.
//
// Each successful write to the store should increment the version.
// When performing conditional updates, the store should compare the existing
// version with the provided one, rejecting the write if there's a mismatch.
// This protects against race conditions and lost updates in concurrent systems.
//
// For EmptyState, Version always returns zero.
func (n *EmptyState) Version() int64 {
	return 0 // EmptyState does not have a version, as it represents an absence of state.
}

func (n *EmptyState) MarshalBinary() (data []byte, err error) {
	return []byte{}, nil
}

func (n *EmptyState) UnmarshalBinary(data []byte) error { //nolint:revive
	return nil
}
