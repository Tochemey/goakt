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

package syncmap

import "sync"

// SyncMap is a generic, concurrency-safe map that allows storing key-value pairs
// while ensuring thread safety using a read-write mutex.
//
// K represents the key type, which must be comparable.
// V represents the value type, which can be any type.
type SyncMap[K comparable, V any] struct {
	mu   sync.RWMutex
	data map[K]V
}

// New creates and returns a new instance of SyncMap.
// It initializes the internal map for storing key-value pairs.
//
// Example usage:
//
//	sm := New[string, int]()
//	sm.Set("foo", 42)
//	value, ok := sm.Get("foo")
func New[K comparable, V any]() *SyncMap[K, V] {
	return &SyncMap[K, V]{
		data: make(map[K]V),
	}
}

// Set stores a key-value pair in the SyncMap.
// If the key already exists, its value is updated.
//
// This method acquires a write lock to ensure safe concurrent access.
func (s *SyncMap[K, V]) Set(k K, v V) {
	s.mu.Lock()
	s.data[k] = v
	s.mu.Unlock()
}

// Get retrieves the value associated with the given key from the SyncMap.
// The second return value indicates whether the key was found.
//
// This method acquires a read lock to ensure safe concurrent access.
func (s *SyncMap[K, V]) Get(k K) (V, bool) {
	s.mu.RLock()
	val, ok := s.data[k]
	s.mu.RUnlock()
	return val, ok
}

// Delete removes the key-value pair associated with the given key from the SyncMap.
// If the key does not exist, this operation has no effect.
//
// This method acquires a write lock to ensure safe concurrent access.
func (s *SyncMap[K, V]) Delete(k K) {
	s.mu.Lock()
	delete(s.data, k)
	s.mu.Unlock()
}

// Len returns the number of key-value pairs currently stored in the SyncMap.
//
// This method acquires a read lock to ensure safe concurrent access.
func (s *SyncMap[K, V]) Len() int {
	s.mu.RLock()
	l := len(s.data)
	s.mu.RUnlock()
	return l
}

// Range iterates over all key-value pairs in the SyncMap and executes the given function `f`
// for each pair. The iteration order is not guaranteed.
//
// This method acquires a read lock to ensure safe concurrent access.
func (s *SyncMap[K, V]) Range(f func(K, V)) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for k, v := range s.data {
		f(k, v)
	}
}
