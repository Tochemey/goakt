/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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
	"sync"
)

const numShards = 64

// shardedMap is a concurrent map with sharding for scalability
type shardedMap struct {
	shards []*sync.Map
}

// newShardedMap creates an instance of shardedMap
func newShardedMap() *shardedMap {
	shards := make([]*sync.Map, numShards)
	for i := range shards {
		shards[i] = &sync.Map{}
	}
	return &shardedMap{
		shards: shards,
	}
}

func (s *shardedMap) getShard(key string) *sync.Map {
	hash := fnv64(key) % numShards
	return s.shards[hash]
}

func (s *shardedMap) Load(key string) (any, bool) {
	shard := s.getShard(key)
	return shard.Load(key)
}

func (s *shardedMap) Store(key string, value any) {
	shard := s.getShard(key)
	shard.Store(key, value)
}

func (s *shardedMap) Delete(key string) {
	shard := s.getShard(key)
	shard.Delete(key)
}

func (s *shardedMap) Range(f func(key, value any) bool) {
	for i := 0; i < numShards; i++ {
		shard := s.shards[i]
		shard.Range(f)
	}
}

func (s *shardedMap) Reset() {
	// Reset each shard's map
	for i := 0; i < numShards; i++ {
		shard := s.shards[i]
		shard.Range(func(key, _ any) bool {
			shard.Delete(key) // Clear the entry
			return true
		})
	}
}

func fnv64(key string) uint64 {
	hash := fnv.New64()
	_, _ = hash.Write([]byte(key))
	return hash.Sum64()
}
