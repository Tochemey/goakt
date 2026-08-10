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

package actor

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	inet "github.com/tochemey/goakt/v4/internal/net"
)

// registryLen counts live entries; test-only, single-threaded.
func registryLen(x *remoteHoldRegistry) int {
	count := 0
	head := (*remoteHoldNode)(atomic.LoadPointer(&x.head))

	for current := (*remoteHoldNode)(atomic.LoadPointer(&head.next)); current != nil; {
		count++
		current = (*remoteHoldNode)(atomic.LoadPointer(&current.next))
	}

	return count
}

// TestRemoteHoldRegistryReleaseAll verifies the teardown walk: every tracked
// share is released whatever mailbox held its message, and the registry ends
// empty but reusable.
func TestRemoteHoldRegistryReleaseAll(t *testing.T) {
	registry := newRemoteHoldRegistry()

	shares := make([]*inet.CreditShare, 5)
	for i := range shares {
		shares[i] = new(inet.CreditShare)
		registry.track(shares[i])
	}

	require.Equal(t, 5, registryLen(registry))

	registry.releaseAll()
	assert.Zero(t, registryLen(registry), "releaseAll must retire every entry")

	for i, share := range shares {
		assert.True(t, share.Released(), "share %d must be released", i)
	}

	// The registry stays usable after a teardown walk (restart case).
	registry.track(new(inet.CreditShare))
	assert.Equal(t, 1, registryLen(registry))
	registry.releaseAll()
}

// TestRemoteHoldRegistryCompactRetiresSpentPrefix verifies the amortized
// cleanup: released entries at the front retire (bounded per pass), while an
// outstanding entry blocks retirement behind it without being released.
func TestRemoteHoldRegistryCompactRetiresSpentPrefix(t *testing.T) {
	registry := newRemoteHoldRegistry()

	// Zero-value shares report Released immediately; they model messages
	// already dispatched. Track a spent prefix, then a parked message.
	for range remoteHoldCompactBudget + 3 {
		registry.track(new(inet.CreditShare))
	}

	parked := inet.DetachedCreditShare(64)
	registry.track(parked)

	registry.compact()
	assert.Equal(t, 4, registryLen(registry), "one pass retires at most the compact budget")

	registry.compact()
	assert.Equal(t, 1, registryLen(registry), "the next pass retires the remaining spent prefix")

	registry.compact()
	assert.Equal(t, 1, registryLen(registry), "an outstanding entry must not retire")
	assert.False(t, parked.Released(), "compact must never release a parked share")

	parked.Release()
	registry.compact()
	assert.Zero(t, registryLen(registry))
}

// TestRemoteHoldRegistryConcurrentTrack verifies producers can track
// concurrently while the single consumer compacts, and that a final teardown
// walk accounts for every share exactly once.
func TestRemoteHoldRegistryConcurrentTrack(t *testing.T) {
	registry := newRemoteHoldRegistry()

	const producers = 8
	const perProducer = 500

	var wg sync.WaitGroup
	shares := make([][]*inet.CreditShare, producers)

	for p := range producers {
		wg.Add(1)
		shares[p] = make([]*inet.CreditShare, perProducer)

		go func(p int) {
			defer wg.Done()

			for i := range perProducer {
				share := inet.DetachedCreditShare(1)
				shares[p][i] = share
				registry.track(share)
			}
		}(p)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		wg.Wait()
	}()

	// Consumer-side compaction runs concurrently with the producers; it must
	// never retire an unreleased entry.
	for {
		select {
		case <-done:
			registry.releaseAll()

			for p := range producers {
				for i, share := range shares[p] {
					require.True(t, share.Released(), "producer %d share %d must be released", p, i)
				}
			}

			return
		default:
			registry.compact()
		}
	}
}

// TestRemoteHoldRegistryReleaseAllWaitsOutInFlightPublish pins the MPSC
// teardown race: a producer that has swung the tail but not yet published
// its link must not have its share stranded by a concurrent releaseAll.
func TestRemoteHoldRegistryReleaseAllWaitsOutInFlightPublish(t *testing.T) {
	registry := newRemoteHoldRegistry()

	// Simulate a producer paused between the tail swap and the link publish.
	share := inet.DetachedCreditShare(64)
	node := remoteHoldNodePool.Get().(*remoteHoldNode)
	node.share = share
	atomic.StorePointer(&node.next, nil)
	prev := (*remoteHoldNode)(atomic.SwapPointer(&registry.tail, unsafe.Pointer(node)))

	done := make(chan struct{})
	go func() {
		defer close(done)
		registry.releaseAll()
	}()

	// releaseAll must wait for the publish rather than declare the registry
	// drained while the tail is still away from home.
	select {
	case <-done:
		t.Fatal("releaseAll returned while a track publish was in flight")
	case <-time.After(50 * time.Millisecond):
	}

	atomic.StorePointer(&prev.next, unsafe.Pointer(node))

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("releaseAll did not finish after the publish landed")
	}

	assert.True(t, share.Released(), "the in-flight share must be repaid, not stranded")
	assert.Zero(t, registryLen(registry))
}

// TestRemoteHoldRegistryConcurrentCompactAndReleaseAll verifies the consumer
// side tolerates a dispatcher turn's compact racing a shutdown releaseAll:
// every share is repaid exactly once and the node pool is never corrupted.
func TestRemoteHoldRegistryConcurrentCompactAndReleaseAll(t *testing.T) {
	registry := newRemoteHoldRegistry()

	const producers = 4
	const perProducer = 400

	var wg sync.WaitGroup
	var tracked atomic.Int64

	for range producers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for range perProducer {
				share := inet.DetachedCreditShare(1)
				registry.track(share)
				tracked.Add(1)
				share.Release()
			}
		}()
	}

	stop := make(chan struct{})
	var consumers sync.WaitGroup

	for range 2 {
		consumers.Add(1)

		go func() {
			defer consumers.Done()

			for {
				select {
				case <-stop:
					return
				default:
					registry.compact()
					registry.releaseAll()
				}
			}
		}()
	}

	wg.Wait()
	close(stop)
	consumers.Wait()

	registry.releaseAll()
	assert.Zero(t, registryLen(registry))
	assert.EqualValues(t, producers*perProducer, tracked.Load())
}
