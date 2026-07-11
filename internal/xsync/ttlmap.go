// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package xsync

import (
	"sync"
	"time"

	"github.com/tochemey/goakt/v4/internal/locker"
)

// ttlEntry is a compact, non-pointer record stored inline in a slice so the
// garbage collector has no per-entry node to allocate or scan.
type ttlEntry[K comparable, V any] struct {
	key      K
	value    V
	expireAt int64 // unix nanoseconds
}

// TTLMap is a generic, concurrency-safe map whose entries expire after a fixed
// time-to-live measured from their insertion.
//
// # Low-GC design
//
// Entries are stored inline in a single append-only slice rather than in
// per-entry list nodes, and deadlines are kept as int64 unix-nanoseconds rather
// than time.Time. This keeps allocations and the pointers the collector must
// scan to a minimum. The index map stores slice positions (int), not pointers.
//
// # Bounded footprint
//
// Expired entries are evicted lazily from the front of the slice on every Set
// (a head offset avoids shifting elements), and the slice is compacted once the
// dead prefix grows past half its length so it never grows without bound. Get
// drops the queried key when it has expired. Because the TTL is constant,
// insertion order is also expiration order, so the front-to-back sweep stops at
// the first live entry. No background goroutine is required and the footprint
// stays bounded by the arrival rate times the TTL.
//
// # Updating existing keys
//
// Setting a key that is already present refreshes its value and deadline in
// place without moving it, so a frequently re-set key can hold back the
// eviction of entries inserted after it. This is intentional: the map is
// optimized for write-once keys (such as deduplication identifiers). Liveness
// is always correct regardless of position because Get checks each entry's own
// deadline.
//
// # Thread safety
//
// All operations acquire the embedded mutex. The map is safe for concurrent
// use by multiple goroutines.
type TTLMap[K comparable, V any] struct {
	_     locker.NoCopy
	mu    sync.Mutex
	ttl   int64        // ttl in nanoseconds
	now   func() int64 // returns unix nanoseconds
	items map[K]int    // key -> index into order
	order []ttlEntry[K, V]
	head  int // index of the oldest live entry in order
}

// NewTTLMap creates a new TTLMap whose entries expire after the given ttl.
//
// A non-positive ttl yields a map whose entries expire immediately, so callers
// that want retention must pass a positive duration.
func NewTTLMap[K comparable, V any](ttl time.Duration) *TTLMap[K, V] {
	return &TTLMap[K, V]{
		ttl:   int64(ttl),
		now:   func() int64 { return time.Now().UnixNano() },
		items: make(map[K]int),
		order: make([]ttlEntry[K, V], 0, 128),
	}
}

// Set stores a key-value pair, stamping it with a fresh TTL. If the key already
// exists, its value and deadline are refreshed in place. Entries that have
// since expired are evicted before the method returns.
func (s *TTLMap[K, V]) Set(k K, v V) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	expireAt := now + s.ttl
	if idx, ok := s.items[k]; ok {
		entry := &s.order[idx]
		entry.value = v
		entry.expireAt = expireAt
	} else {
		s.items[k] = len(s.order)
		s.order = append(s.order, ttlEntry[K, V]{key: k, value: v, expireAt: expireAt})
	}

	s.evict(now)
	s.maybeCompact()
}

// Get retrieves the value associated with the given key. The second return
// value reports whether a live (non-expired) entry was found. A key whose
// entry has expired is removed and reported as absent.
func (s *TTLMap[K, V]) Get(k K) (V, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if idx, ok := s.items[k]; ok {
		entry := s.order[idx]
		if s.now() < entry.expireAt {
			return entry.value, true
		}
		delete(s.items, k)
	}

	var zero V
	return zero, false
}

// Len returns the number of entries currently retained, including any that have
// expired but not yet been evicted.
func (s *TTLMap[K, V]) Len() int {
	s.mu.Lock()
	l := len(s.items)
	s.mu.Unlock()
	return l
}

// Active reports whether the map holds at least one live (non-expired) entry.
// See ActiveLen for the semantics; this is a convenience for the common
// "is anything still active" gate.
func (s *TTLMap[K, V]) Active() bool {
	return s.ActiveLen() > 0
}

// ActiveLen returns the number of live (non-expired) entries.
//
// Unlike Len it ignores entries that have expired but have not yet been evicted
// (eviction otherwise only runs on Set), so a caller gets a truthful live count
// without first triggering a Set. Expired entries encountered during the scan
// are dropped from the index map, matching Get's lazy-delete behavior; the
// vacated order slots are reclaimed on the next Set-driven compaction.
func (s *TTLMap[K, V]) ActiveLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	n := 0
	for k, idx := range s.items {
		if now < s.order[idx].expireAt {
			n++
			continue
		}
		delete(s.items, k)
	}
	return n
}

// Delete removes the entry for k, if present, whether live or expired. The
// vacated order slot is reclaimed on the next Set-driven compaction, matching
// Get's lazy-delete behavior.
func (s *TTLMap[K, V]) Delete(k K) {
	s.mu.Lock()
	delete(s.items, k)
	s.mu.Unlock()
}

// Reset removes all entries from the map. Vacated slots are zeroed so any
// key or value references are released for garbage collection.
func (s *TTLMap[K, V]) Reset() {
	s.mu.Lock()
	clear(s.items)
	clear(s.order)
	s.order = s.order[:0]
	s.head = 0
	s.mu.Unlock()
}

// evict advances the head past every expired entry at the front of order,
// dropping each from the index map. Callers must hold the lock.
func (s *TTLMap[K, V]) evict(now int64) {
	for s.head < len(s.order) {
		entry := s.order[s.head]
		// stop at the first live entry; order is also expiration order
		if now < entry.expireAt {
			break
		}
		// only unmap when this slot is still the live mapping for the key
		if idx, ok := s.items[entry.key]; ok && idx == s.head {
			delete(s.items, entry.key)
		}
		s.head++
	}
}

// maybeCompact rebuilds order from its live suffix once the dead prefix grows
// past half the slice, so the backing array does not grow without bound.
// Callers must hold the lock.
//
// When the live region [head:] contains no lazily Get-deleted holes (every slot
// is still mapped, detected in O(1) via the item count) it takes a fast path: a
// single bulk move of the region to the front. Otherwise it filters the region
// so it never resurrects a key that Get removed lazily. Either way the vacated
// tail is zeroed to release key and value references for garbage collection.
func (s *TTLMap[K, V]) maybeCompact() {
	if s.head == 0 || s.head < len(s.order)/2 {
		return
	}

	var n int
	if len(s.items) == len(s.order)-s.head {
		// fast path: no holes, relocate the whole region with one memmove
		n = copy(s.order, s.order[s.head:])
	} else {
		// slow path: drop slots whose key is no longer mapped here
		for i := s.head; i < len(s.order); i++ {
			if idx, ok := s.items[s.order[i].key]; ok && idx == i {
				s.order[n] = s.order[i]
				n++
			}
		}
	}

	clear(s.order[n:]) // release references in the vacated tail
	s.order = s.order[:n]
	for i := range s.order {
		s.items[s.order[i].key] = i
	}
	s.head = 0
}
