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

// TestRemoteHoldRegistryCompactUnlinksBehindParkedEntry pins the in-place
// unlink: released entries that sit behind a still-parked one are retired
// within the budget instead of accumulating until the parked share is
// released, as happens when a priority mailbox leaves one message waiting
// behind a steady flow of newer ones. The tail is left alone, and the parked
// entry stays until its own share is released.
func TestRemoteHoldRegistryCompactUnlinksBehindParkedEntry(t *testing.T) {
	t.Run("With released entries behind a parked front", func(t *testing.T) {
		registry := newRemoteHoldRegistry()

		parked := inet.DetachedCreditShare(64)
		registry.track(parked)

		for range 5 {
			registry.track(new(inet.CreditShare))
		}

		tail := inet.DetachedCreditShare(64)
		registry.track(tail)

		registry.compact()
		assert.Equal(t, 2, registryLen(registry), "the released entries behind the parked front must be unlinked")
		assert.False(t, parked.Released(), "compact must never release a parked share")
		assert.False(t, tail.Released())

		parked.Release()
		registry.compact()
		assert.Equal(t, 1, registryLen(registry), "the front retires once its share is released")

		tail.Release()
		registry.compact()
		assert.Zero(t, registryLen(registry))
	})

	t.Run("With a released tail behind a parked front", func(t *testing.T) {
		registry := newRemoteHoldRegistry()

		parked := inet.DetachedCreditShare(64)
		registry.track(parked)
		registry.track(new(inet.CreditShare))

		registry.compact()
		assert.Equal(t, 2, registryLen(registry), "the tail is never unlinked in place")

		// a later entry makes the released one a middle node
		registry.track(inet.DetachedCreditShare(64))
		registry.compact()
		assert.Equal(t, 2, registryLen(registry), "the released middle entry is unlinked once it is published")
	})

	t.Run("With the budget spent on parked entries", func(t *testing.T) {
		registry := newRemoteHoldRegistry()

		for range remoteHoldCompactBudget {
			registry.track(inet.DetachedCreditShare(1))
		}

		registry.track(new(inet.CreditShare))
		registry.track(inet.DetachedCreditShare(1))

		registry.compact()
		assert.Equal(t, remoteHoldCompactBudget+2, registryLen(registry), "one pass examines at most the compact budget")

		registry.compact()
		assert.Equal(t, remoteHoldCompactBudget+1, registryLen(registry), "the next pass reaches the released entry")
	})

	t.Run("With the cursor on a parked entry that is later retired", func(t *testing.T) {
		registry := newRemoteHoldRegistry()

		// a parked front, enough released entries behind it to spend the
		// budget, then a parked tail: the pass ends with the cursor on the
		// front entry
		front := inet.DetachedCreditShare(64)
		registry.track(front)

		for range remoteHoldCompactBudget - 1 {
			registry.track(new(inet.CreditShare))
		}

		second := inet.DetachedCreditShare(64)
		registry.track(second)

		registry.compact()
		require.Equal(t, 2, registryLen(registry))
		head := (*remoteHoldNode)(atomic.LoadPointer(&registry.head))
		require.Same(t, (*remoteHoldNode)(atomic.LoadPointer(&head.next)), registry.cursor, "the pass must stop on the front entry")

		// both parked entries release, so the next pass retires the front
		// entry the cursor names and recycles it; the pass must then walk the
		// live list from the head, not from the recycled node
		front.Release()
		second.Release()

		parked := inet.DetachedCreditShare(64)
		registry.track(parked)
		registry.track(new(inet.CreditShare))
		registry.track(inet.DetachedCreditShare(64))

		registry.compact()
		assert.Equal(t, 2, registryLen(registry), "the released entry behind the parked one must be unlinked in the same pass")
		assert.False(t, parked.Released())
	})

	t.Run("With entries released behind the cursor and a released tail", func(t *testing.T) {
		registry := newRemoteHoldRegistry()

		front := inet.DetachedCreditShare(64)
		registry.track(front)

		middle := inet.DetachedCreditShare(64)
		registry.track(middle)
		registry.track(new(inet.CreditShare))

		// the pass walks past both parked entries and stops at the released
		// tail, which it cannot unlink
		registry.compact()
		require.Equal(t, 3, registryLen(registry))

		// with no new arrival the tail stays the tail; the middle entry
		// releases behind where the pass stopped and must still be reached
		middle.Release()
		registry.compact()
		assert.Equal(t, 2, registryLen(registry), "a pass that stopped at a released tail must restart from the head")
		assert.False(t, front.Released())
	})

	t.Run("With releases concurrent to compaction", func(t *testing.T) {
		registry := newRemoteHoldRegistry()

		front := inet.DetachedCreditShare(64)
		registry.track(front)

		shares := make([]*inet.CreditShare, 0, 2000)
		for range cap(shares) {
			share := inet.DetachedCreditShare(1)
			shares = append(shares, share)
			registry.track(share)
		}

		var wg sync.WaitGroup
		wg.Add(1)

		go func() {
			defer wg.Done()

			for _, share := range shares {
				share.Release()
			}
		}()

		for range 4000 {
			registry.compact()
		}

		wg.Wait()

		for range len(shares) {
			registry.compact()
		}

		assert.Equal(t, 2, registryLen(registry), "every released middle entry is unlinked; the front and the tail stay")
		assert.False(t, front.Released())

		// releaseAll spins until the tail comes home, so a broken list would
		// hang it: bound the wait and report a failure instead
		drained := make(chan struct{})

		go func() {
			registry.releaseAll()
			close(drained)
		}()

		require.Eventually(t, func() bool {
			select {
			case <-drained:
				return true
			default:
				return false
			}
		}, 5*time.Second, 10*time.Millisecond, "releaseAll must drain a compacted registry")

		assert.True(t, front.Released())
		assert.Zero(t, registryLen(registry))
	})
}
