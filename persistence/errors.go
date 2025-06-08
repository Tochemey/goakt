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

import (
	"errors"
)

// Predefined error instances for common state store error conditions.
// These can be used with errors.Is() for error type checking.
var (
	// ErrKeyNotFound indicates that a requested key does not exist in the state store.
	// Returned by Get, Delete, and CompareAndSwap operations.
	//
	// Example usage:
	//   record, err := store.Load(ctx, key)
	//   if errors.Is(err, ErrKeyNotFound) {
	//       // Handle missing key scenario
	//   }
	ErrKeyNotFound = errors.New("key not found")

	// ErrVersionMismatch indicates that a CompareAndSwap operation failed
	// because the current version doesn't match the expected version.
	// This typically means another concurrent operation modified the state.
	//
	// Example usage:
	//   _, err := store.CompareAndSwap(ctx, key, expectedVersion, newValue)
	//   if errors.Is(err, ErrVersionMismatch) {
	//       // Retry with fresh version or handle conflict
	//   }
	ErrVersionMismatch = errors.New("version mismatch")

	// ErrKeyExists indicates that a create operation failed because the key
	// already exists. Used by storage backends that distinguish between
	// create and update operations.
	//
	// Example usage:
	//   err := store.Save(ctx, record)
	//   if errors.Is(err, ErrKeyExists) {
	//       // Key already exists, maybe update instead
	//   }
	ErrKeyExists = errors.New("key already exists")
)
