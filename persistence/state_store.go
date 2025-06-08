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

// StateStore defines the core interface for persisting and retrieving actor state.
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
	// Load retrieves a single state record by its key.
	//
	// Returns ErrKeyNotFound if the key is not present in the store.
	// The returned State includes all metadata such as version and TTL (if applicable).
	//
	// Example:
	//   record, err := store.Load(ctx, key)
	//   if errors.Is(err, ErrKeyNotFound) {
	//       // handle missing state
	//   }
	Load(ctx context.Context, key Key) (*State, error)

	// LoadMulti retrieves multiple state records in a single batch operation.
	//
	// The returned slice maintains the order of the input keys. Missing keys
	// will result in nil entries at their respective positions.
	// This is preferred over sequential Load calls for performance reasons.
	//
	// Example:
	//   records, err := store.LoadMulti(ctx, []Key{key1, key2, key3})
	//   // records[0] corresponds to key1, etc.
	LoadMulti(ctx context.Context, keys []Key) ([]*State, error)

	// Save writes a single state record to the store, inserting or updating as necessary.
	//
	// If the key exists, the state is updated and its version incremented.
	// If the key does not exist, a new record is inserted with version = 1.
	//
	// Example:
	//   err := store.Save(ctx, &State{
	//       Key:   key,
	//       Value: jsonBytes,
	//   })
	Save(ctx context.Context, record *State) error

	// SaveMulti atomically persists multiple state records in a single operation.
	//
	// Either all records are saved successfully, or none are. Implementations must
	// ensure atomicity to avoid partial writes.
	// Useful for multi-key transactional updates within a single actor.
	//
	// Example:
	//   err := store.SaveMulti(ctx, []*State{record1, record2})
	SaveMulti(ctx context.Context, records []*State) error

	// Delete removes a single state record by its key.
	//
	// Returns ErrKeyNotFound if the key does not exist.
	// This operation is idempotent: deleting a non-existent key is a no-op.
	//
	// Example:
	//   err := store.Delete(ctx, key)
	Delete(ctx context.Context, key Key) error

	// DeleteMulti atomically deletes multiple state records by their keys.
	//
	// All deletions must succeed or none should be applied.
	// Non-existent keys are ignored; the operation remains idempotent.
	//
	// Example:
	//   err := store.DeleteMulti(ctx, []Key{key1, key2})
	DeleteMulti(ctx context.Context, keys []Key) error

	// Exists checks if a state record exists without loading its contents.
	//
	// This is more efficient than Load if only presence is needed, as it avoids
	// deserializing or transferring large payloads.
	//
	// Example:
	//   exists, err := store.Exists(ctx, key)
	//   if exists {
	//       // proceed with update logic
	//   }
	Exists(ctx context.Context, key Key) (bool, error)

	// LoadKeys returns all state keys associated with a specific actor instance.
	//
	// This is useful for introspection, debugging, or cleaning up all state associated
	// with an actor. Returned keys are not guaranteed to be ordered.
	//
	// Example:
	//   keys, err := store.LoadKeys(ctx, "user-123", "UserActor")
	LoadKeys(ctx context.Context, actorID, kind string) ([]Key, error)

	// Clear deletes all state entries associated with a specific actor instance.
	//
	// This is a bulk operation equivalent to deleting all keys returned by LoadKeys,
	// but may be optimized by the underlying backend. Common use cases include:
	// actor deactivation, user data deletion, and test cleanup.
	//
	// Example:
	//   err := store.Clear(ctx, "user-123", "UserActor")
	Clear(ctx context.Context, actorID, kind string) error

	// CompareAndSwap performs an atomic update based on the current version.
	//
	// The value is only updated if the stored version matches the expectedVersion.
	// This provides optimistic concurrency control and prevents overwrites caused by
	// stale reads.
	//
	// Returns:
	//   - The updated State on success
	//   - ErrVersionMismatch if versions do not match
	//   - ErrKeyNotFound if the key does not exist
	//
	// Example:
	//   old, _ := store.Load(ctx, key)
	//   newValue := computeNewValue(old.Value)
	//   updated, err := store.CompareAndSwap(ctx, key, old.Version, newValue)
	CompareAndSwap(ctx context.Context, key Key, expectedVersion int64, newValue []byte) (*State, error)
}
