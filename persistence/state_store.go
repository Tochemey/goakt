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

package virtual

import "context"

// StateStore defines the primary interface for persisting and retrieving actor state.
//
// StateStore provides the core persistence layer for the virtual actor system,
// supporting both individual and batch operations with strong consistency guarantees.
// Implementations should be thread-safe and support concurrent access from multiple
// actor instances.
//
// The interface is designed to support various storage backends including:
// - In-memory stores for testing and development
// - Redis for high-performance caching
// - PostgreSQL/MySQL for ACID compliance
// - MongoDB for document-based storage
// - DynamoDB for cloud-native scalability
type StateStore interface {
	// Load retrieves a single state record by its key.
	//
	// Returns ErrKeyNotFound if the key does not exist in the store.
	// The returned State includes all metadata (version, timestamps, TTL).
	//
	// Example:
	//   record, err := store.Load(ctx, NewKey("user-123", "UserActor", "profile"))
	//   if errors.Is(err, ErrKeyNotFound) {
	//       // Handle missing state
	//   }
	Load(ctx context.Context, key Key) (*State, error)

	// LoadMulti retrieves multiple state records by their keys in a single operation.
	//
	// This method is more efficient than multiple Get calls as it reduces
	// network round trips and can leverage batch operations in the underlying storage.
	// The returned slice corresponds to the input keys slice - missing keys
	// will have nil entries at their respective positions.
	//
	// Example:
	//   keys := []Key{profileKey, settingsKey, preferencesKey}
	//   records, err := store.LoadMulti(ctx, keys)
	//   // records[0] corresponds to profileKey, etc.
	LoadMulti(ctx context.Context, keys []Key) ([]*State, error)

	// Save stores a single state record, creating or updating as necessary.
	//
	// If the key already exists, the record is updated and the version is incremented.
	// If the key doesn't exist, a new record is created with version 1.
	// The UpdatedAt timestamp is always set to the current time.
	//
	// Example:
	//   record := &State{
	//       Key:   NewKey("user-123", "UserActor", "profile"),
	//       Value: []byte(`{"name": "John", "email": "john@example.com"}`),
	//   }
	//   err := store.Save(ctx, record)
	Save(ctx context.Context, record *State) error

	// SaveMulti stores multiple state records atomically.
	//
	// Either all records are stored successfully, or none are stored (atomic operation).
	// This is particularly useful for maintaining consistency across related state
	// that must be updated together.
	//
	// Example:
	//   records := []*Stat{profileRecord, settingsRecord}
	//   err := store.SaveMulti(ctx, records)
	SaveMulti(ctx context.Context, records []*State) error

	// Delete removes a state record by its key.
	//
	// Returns ErrKeyNotFound if the key does not exist.
	// This operation is idempotent - deleting a non-existent key is not an error.
	//
	// Example:
	//   err := store.Delete(ctx, NewKey("user-123", "UserActor", "temp_data"))
	Delete(ctx context.Context, key Key) error

	// DeleteMulti removes multiple state records by their keys atomically.
	//
	// Either all specified keys are deleted, or none are deleted.
	// Non-existent keys are ignored (idempotent operation).
	//
	// Example:
	//   keys := []Key{tempKey1, tempKey2, cacheKey}
	//   err := store.DeleteMulti(ctx, keys)
	DeleteMulti(ctx context.Context, keys []Key) error

	// Exists checks if a key exists in the store without retrieving the full record.
	//
	// This is more efficient than Get when you only need to check existence,
	// as it avoids transferring the potentially large state value.
	//
	// Example:
	//   exists, err := store.Exists(ctx, NewKey("user-123", "UserActor", "profile"))
	//   if exists {
	//       // Key exists, proceed with logic
	//   }
	Exists(ctx context.Context, key Key) (bool, error)

	// LoadKeys returns all state keys for a specific actor instance.
	//
	// This enables bulk operations on an actor's state, such as:
	// - Complete state migration
	// - Debugging and introspection
	// - Cleanup operations
	//
	// The returned keys are not guaranteed to be in any particular order.
	//
	// Example:
	//   keys, err := store.LoadKeys(ctx, "user-123", "UserActor")
	//   // Returns keys like: profile, settings, preferences, etc.
	LoadKeys(ctx context.Context, actorID, actorType string) ([]Key, error)

	// Clear removes all state for a specific actor instance.
	//
	// This is equivalent to calling DeleteMultiple with all keys for the actor,
	// but may be more efficiently implemented by the storage backend.
	// Used for actor deactivation, testing cleanup, and data purging.
	//
	// Example:
	//   err := store.Clear(ctx, "user-123", "UserActor")
	//   // All state for user-123 UserActor is now deleted
	Clear(ctx context.Context, actorID, actorType string) error

	// CompareAndSwap performs an atomic compare-and-swap operation.
	//
	// Updates the state value only if the current version matches expectedVersion.
	// This prevents lost updates in concurrent scenarios and implements optimistic
	// concurrency control. Returns the updated record on success.
	//
	// Returns ErrVersionMismatch if the current version doesn't match expectedVersion.
	// Returns ErrKeyNotFound if the key doesn't exist.
	//
	// Example:
	//   // Safe concurrent update
	//   current, _ := store.Get(ctx, key)
	//   newValue := updateLogic(current.Value)
	//   updated, err := store.CompareAndSwap(ctx, key, current.Version, newValue)
	//   if errors.Is(err, ErrVersionMismatch) {
	//       // Retry with fresh version
	//   }
	CompareAndSwap(ctx context.Context, key Key, expectedVersion int64, newValue []byte) (*State, error)
}
