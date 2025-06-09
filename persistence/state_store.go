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

import "context"

// StateStore defines the core interface for persisting and retrieving actor state snapshot.
//
// It serves as the primary persistence layer for the virtual actor system,
// supporting both single-record and batch operations. Implementations must
// be thread-safe and capable of handling concurrent access across multiple
// actor instances.
//
// This interface is designed to be backend-agnostic and can be implemented using
// various storage engines, including:
//
//   - In-memory stores for testing or ephemeral state
//   - Redis for low-latency, high-throughput caching
//   - PostgreSQL or MySQL for transactional, ACID-compliant persistence
//   - MongoDB for document-oriented state models
//   - DynamoDB or similar cloud-native key-value stores for elastic scalability
type StateStore interface {
	// Load retrieves a single state snapshot by its persistence ID.
	//
	// Returns ErrKeyNotFound if the persistence ID is not present in the store.
	// The returned State includes all metadata such as version and TTL (if applicable).
	//
	// Example:
	//   snapshot, err := store.Load(ctx, persistenceID)
	//   if errors.Is(err, ErrKeyNotFound) {
	//       // handle missing state
	//   }
	Load(ctx context.Context, persistenceID PersistenceID) (*State, error)

	// Save writes a single state snapshot to the store, inserting or updating as necessary.
	//
	// If the persistence ID exists, the snapshot is updated and its version incremented.
	// If the persistence ID does not exist, a new snapshot record is inserted with version = 1.
	//
	// Example:
	//   err := store.Save(ctx, &State{
	//       Key:   persistenceID,
	//       Value: jsonBytes,
	//   })
	Save(ctx context.Context, snapshot *State) error

	// Delete removes a single state snapshot by its persistence ID.
	//
	// Returns ErrKeyNotFound if the persistence ID does not exist.
	// This operation is idempotent: deleting a non-existent key is a no-op.
	//
	// Example:
	//   err := store.Delete(ctx, persistenceID)
	Delete(ctx context.Context, persistenceID PersistenceID) error

	// Exists checks if a state snapshot exists without loading its contents.
	//
	// This is more efficient than Load if only presence is needed, as it avoids
	// deserializing or transferring large payloads.
	//
	// Example:
	//   exists, err := store.Exists(ctx, persistenceID)
	//   if exists {
	//       // proceed with update logic
	//   }
	Exists(ctx context.Context, persistenceID PersistenceID) (bool, error)
}
