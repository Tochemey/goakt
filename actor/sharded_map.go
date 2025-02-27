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

package actors

import (
	"hash/fnv"
	"runtime"
	"sync"
)

const maxShards = 64

// Shard defines a Shard
type shard struct {
	sync.RWMutex
	m map[string]any
}

// ShardedMap defines a concurrent map with sharding for
// scalability
type shardedMap []*shard

// newShardedMap creates an instance of ShardedMap
func newShardedMap() shardedMap {
	numShards := calculateNumShards()
	shards := make([]*shard, numShards)
	for i := range numShards {
		shards[i] = &shard{
			m: make(map[string]any),
		}
	}
	return shards
}

// Load returns the value of a given key
func (s shardedMap) Load(key string) (any, bool) {
	shard := s.getShard(key)
	shard.RLock()
	val, ok := shard.m[key]
	shard.RUnlock()
	return val, ok
}

// Store adds a key/value pair to the sharded map
func (s shardedMap) Store(key string, value any) {
	shard := s.getShard(key)
	shard.Lock()
	shard.m[key] = value
	shard.Unlock()
}

// Delete removes a given key from the sharded map
func (s shardedMap) Delete(key string) {
	shard := s.getShard(key)
	shard.Lock()
	delete(shard.m, key)
	shard.Unlock()
}

// Range given a function iterate over the sharded map
func (s shardedMap) Range(f func(key, value any)) {
	for i := range s {
		shard := s[i]
		shard.RLock()
		for k, v := range shard.m {
			f(k, v)
		}
		shard.RUnlock()
	}
}

// Reset resets the sharded map
func (s shardedMap) Reset() {
	// Reset each Shard's map
	for i := range s {
		shard := s[i]
		shard.Lock()
		shard.m = make(map[string]any)
		shard.Unlock()
	}
}

// getShard returns the given Shard for a given key
func (s shardedMap) getShard(key string) *shard {
	hash := fnv64(key) % uint64(len(s))
	return s[int(hash)]
}

func fnv64(key string) uint64 {
	hash := fnv.New64()
	_, _ = hash.Write([]byte(key))
	return hash.Sum64()
}

// calculateNumShards returns the total number of shards to use
func calculateNumShards() uint64 {
	optimalShards := runtime.NumCPU() * 4
	if optimalShards > maxShards {
		return uint64(maxShards)
	}
	return uint64(optimalShards)
}
