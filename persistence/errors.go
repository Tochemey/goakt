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

import "errors"

// Predefined errors for standard state store failure conditions.
//
// These errors provide well-known failure modes for interacting with actor state
// and can be used with errors.Is for reliable type checking. They are returned
// by various operations in the StateStore interface depending on the storage semantics.
var (
	// ErrKeyNotFound indicates that the specified key does not exist in the state store.
	//
	// Returned by operations like Load, Delete, or CompareAndSwap when the requested
	// key is missing. This is not considered a fatal error and may indicate that
	// initialization or conditional creation logic is required.
	//
	// Example:
	//   state, err := store.Load(ctx, key)
	//   if errors.Is(err, ErrKeyNotFound) {
	//       // key is not present, create new state
	//   }
	ErrKeyNotFound = errors.New("key not found")

	// ErrVersionMismatch indicates that a CompareAndSwap operation failed
	// because the version of the record in the store did not match the
	// expected version supplied by the caller.
	//
	// This is used to implement optimistic concurrency control. It typically
	// means another actor or process updated the state concurrently, and the
	// operation should be retried after fetching the latest version.
	//
	// Example:
	//   _, err := store.CompareAndSwap(ctx, key, old.Version, updatedValue)
	//   if errors.Is(err, ErrVersionMismatch) {
	//       // retry with latest version or apply conflict resolution
	//   }
	ErrVersionMismatch = errors.New("version mismatch")

	// ErrKeyExists indicates that a create-only operation failed because the key
	// already exists in the state store.
	//
	// This is useful in backends that distinguish between insert and update paths,
	// or when enforcing strict state creation semantics for new actors.
	//
	// Example:
	//   err := store.Save(ctx, &State{Key: key, Value: data})
	//   if errors.Is(err, ErrKeyExists) {
	//       // handle duplicate creation, possibly switch to update
	//   }
	ErrKeyExists = errors.New("key already exists")
)
