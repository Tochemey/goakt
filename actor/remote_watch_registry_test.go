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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/internal/address"
)

const (
	testRegistrySystemName = "TestSys"
	testRegistryPidA       = "goakt://TestSys@local-host:0/a"
	testRegistryPidB       = "goakt://TestSys@local-host:0/b"
)

func newTestAddress(t *testing.T, name, host string, port int) *address.Address {
	t.Helper()
	return address.New(name, testRegistrySystemName, host, port)
}

func TestRemoteWatchRegistry_New(t *testing.T) {
	r := newRemoteWatchRegistry()
	require.NotNil(t, r)
	require.Nil(t, r.watchers)
	require.Nil(t, r.watchees)
	require.Nil(t, r.watchersByHost)
	require.Nil(t, r.watcheesByHost)
}

func TestRemoteWatchRegistry_AddWatcher(t *testing.T) {
	t.Run("empty id is a no-op", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		r.addWatcher("", newTestAddress(t, "w", "h1", 1))
		require.Nil(t, r.watchers)
		require.Nil(t, r.watchersByHost)
	})

	t.Run("nil watcher is a no-op", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		r.addWatcher(testRegistryPidA, nil)
		require.Nil(t, r.watchers)
		require.Nil(t, r.watchersByHost)
	})

	t.Run("first add populates both indexes lazily", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		w := newTestAddress(t, "w", "h1", 1)
		r.addWatcher(testRegistryPidA, w)

		require.Len(t, r.watchers, 1)
		require.Len(t, r.watchers[testRegistryPidA], 1)
		require.Equal(t, w, r.watchers[testRegistryPidA][w.String()])

		require.Len(t, r.watchersByHost, 1)
		require.Contains(t, r.watchersByHost, "h1")
	})

	t.Run("re-add is idempotent", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		w := newTestAddress(t, "w", "h1", 1)
		r.addWatcher(testRegistryPidA, w)
		r.addWatcher(testRegistryPidA, w)

		require.Len(t, r.watchers[testRegistryPidA], 1)
		require.Len(t, r.watchersByHost["h1"][testRegistryPidA], 1)
	})

	t.Run("multiple watchers for same pid", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		w1 := newTestAddress(t, "w1", "h1", 1)
		w2 := newTestAddress(t, "w2", "h2", 2)
		r.addWatcher(testRegistryPidA, w1)
		r.addWatcher(testRegistryPidA, w2)

		require.Len(t, r.watchers[testRegistryPidA], 2)
		require.Len(t, r.watchersByHost, 2)
	})
}

func TestRemoteWatchRegistry_RemoveWatcher(t *testing.T) {
	t.Run("empty id is a no-op", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		r.removeWatcher("", newTestAddress(t, "w", "h1", 1))
	})

	t.Run("nil watcher is a no-op", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		r.removeWatcher(testRegistryPidA, nil)
	})

	t.Run("remove unknown pair is a no-op", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		r.removeWatcher(testRegistryPidA, newTestAddress(t, "w", "h1", 1))
	})

	t.Run("remove last entry drops the pid map and host map", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		w := newTestAddress(t, "w", "h1", 1)
		r.addWatcher(testRegistryPidA, w)
		r.removeWatcher(testRegistryPidA, w)

		require.NotContains(t, r.watchers, testRegistryPidA)
		require.NotContains(t, r.watchersByHost, "h1")
	})

	t.Run("removes one of many leaves others intact", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		w1 := newTestAddress(t, "w1", "h1", 1)
		w2 := newTestAddress(t, "w2", "h2", 2)
		r.addWatcher(testRegistryPidA, w1)
		r.addWatcher(testRegistryPidA, w2)

		r.removeWatcher(testRegistryPidA, w1)

		require.Len(t, r.watchers[testRegistryPidA], 1)
		require.NotContains(t, r.watchersByHost, "h1")
		require.Contains(t, r.watchersByHost, "h2")
	})
}

func TestRemoteWatchRegistry_WatchersFor(t *testing.T) {
	t.Run("empty id returns nil", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		require.Nil(t, r.watchersFor(""))
	})

	t.Run("unknown pid returns nil", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		require.Nil(t, r.watchersFor(testRegistryPidA))
	})

	t.Run("returns snapshot of all watchers", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		w1 := newTestAddress(t, "w1", "h1", 1)
		w2 := newTestAddress(t, "w2", "h2", 2)
		r.addWatcher(testRegistryPidA, w1)
		r.addWatcher(testRegistryPidA, w2)

		got := r.watchersFor(testRegistryPidA)
		require.Len(t, got, 2)
		require.ElementsMatch(t, []*address.Address{w1, w2}, got)
	})

	t.Run("snapshot is decoupled from registry", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		w := newTestAddress(t, "w", "h1", 1)
		r.addWatcher(testRegistryPidA, w)

		snap := r.watchersFor(testRegistryPidA)
		r.removeWatcher(testRegistryPidA, w)

		require.Len(t, snap, 1)
		require.Nil(t, r.watchersFor(testRegistryPidA))
	})
}

func TestRemoteWatchRegistry_AddWatchee(t *testing.T) {
	t.Run("empty id is a no-op", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		r.addWatchee("", newTestAddress(t, "x", "h1", 1))
		require.Nil(t, r.watchees)
		require.Nil(t, r.watcheesByHost)
	})

	t.Run("nil watchee is a no-op", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		r.addWatchee(testRegistryPidA, nil)
		require.Nil(t, r.watchees)
		require.Nil(t, r.watcheesByHost)
	})

	t.Run("first add populates both indexes lazily", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		w := newTestAddress(t, "x", "h1", 1)
		r.addWatchee(testRegistryPidA, w)

		require.Len(t, r.watchees, 1)
		require.Equal(t, w, r.watchees[testRegistryPidA][w.String()])
		require.Contains(t, r.watcheesByHost, "h1")
	})

	t.Run("re-add is idempotent", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		w := newTestAddress(t, "x", "h1", 1)
		r.addWatchee(testRegistryPidA, w)
		r.addWatchee(testRegistryPidA, w)

		require.Len(t, r.watchees[testRegistryPidA], 1)
	})
}

func TestRemoteWatchRegistry_RemoveWatchee(t *testing.T) {
	t.Run("empty id is a no-op", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		r.removeWatchee("", newTestAddress(t, "x", "h1", 1))
	})

	t.Run("nil watchee is a no-op", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		r.removeWatchee(testRegistryPidA, nil)
	})

	t.Run("remove last entry drops the pid map and host map", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		w := newTestAddress(t, "x", "h1", 1)
		r.addWatchee(testRegistryPidA, w)
		r.removeWatchee(testRegistryPidA, w)

		require.NotContains(t, r.watchees, testRegistryPidA)
		require.NotContains(t, r.watcheesByHost, "h1")
	})

	t.Run("removes one of many leaves others intact", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		w1 := newTestAddress(t, "x1", "h1", 1)
		w2 := newTestAddress(t, "x2", "h2", 2)
		r.addWatchee(testRegistryPidA, w1)
		r.addWatchee(testRegistryPidA, w2)

		r.removeWatchee(testRegistryPidA, w1)

		require.Len(t, r.watchees[testRegistryPidA], 1)
		require.NotContains(t, r.watcheesByHost, "h1")
		require.Contains(t, r.watcheesByHost, "h2")
	})
}

func TestRemoteWatchRegistry_WatcheesFor(t *testing.T) {
	t.Run("empty id returns nil", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		require.Nil(t, r.watcheesFor(""))
	})

	t.Run("unknown pid returns nil", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		require.Nil(t, r.watcheesFor(testRegistryPidA))
	})

	t.Run("returns snapshot of all watchees", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		x1 := newTestAddress(t, "x1", "h1", 1)
		x2 := newTestAddress(t, "x2", "h2", 2)
		r.addWatchee(testRegistryPidA, x1)
		r.addWatchee(testRegistryPidA, x2)

		got := r.watcheesFor(testRegistryPidA)
		require.Len(t, got, 2)
		require.ElementsMatch(t, []*address.Address{x1, x2}, got)
	})
}

func TestRemoteWatchRegistry_DropPID(t *testing.T) {
	t.Run("empty id is a no-op", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		r.dropPID("")
	})

	t.Run("unknown pid is a no-op", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		r.dropPID(testRegistryPidA)
	})

	t.Run("clears both watchers and watchees for the pid", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		w := newTestAddress(t, "w", "h1", 1)
		x := newTestAddress(t, "x", "h2", 2)
		r.addWatcher(testRegistryPidA, w)
		r.addWatchee(testRegistryPidA, x)

		r.dropPID(testRegistryPidA)

		require.Nil(t, r.watchersFor(testRegistryPidA))
		require.Nil(t, r.watcheesFor(testRegistryPidA))
		require.NotContains(t, r.watchersByHost, "h1")
		require.NotContains(t, r.watcheesByHost, "h2")
	})

	t.Run("leaves other pids intact", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		w := newTestAddress(t, "w", "h1", 1)
		r.addWatcher(testRegistryPidA, w)
		r.addWatcher(testRegistryPidB, w)

		r.dropPID(testRegistryPidA)

		require.Nil(t, r.watchersFor(testRegistryPidA))
		require.Len(t, r.watchersFor(testRegistryPidB), 1)
		require.Contains(t, r.watchersByHost, "h1")
	})
}

func TestRemoteWatchRegistry_DropHost(t *testing.T) {
	t.Run("empty host returns empty entries and is a no-op", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		entries := r.dropHost("")
		require.Empty(t, entries.Watchers)
		require.Empty(t, entries.Watchees)
	})

	t.Run("unknown host returns empty entries", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		entries := r.dropHost("h1")
		require.Empty(t, entries.Watchers)
		require.Empty(t, entries.Watchees)
	})

	t.Run("collects matching watcher and watchee entries and removes them", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		w := newTestAddress(t, "w", "h1", 1)
		x := newTestAddress(t, "x", "h1", 2)
		other := newTestAddress(t, "y", "h2", 3)

		r.addWatcher(testRegistryPidA, w)
		r.addWatchee(testRegistryPidA, x)
		r.addWatcher(testRegistryPidB, other)
		r.addWatchee(testRegistryPidB, other)

		entries := r.dropHost("h1")

		require.Len(t, entries.Watchers, 1)
		require.Equal(t, testRegistryPidA, entries.Watchers[0].LocalID)
		require.Equal(t, w, entries.Watchers[0].RemoteAddress)

		require.Len(t, entries.Watchees, 1)
		require.Equal(t, testRegistryPidA, entries.Watchees[0].LocalID)
		require.Equal(t, x, entries.Watchees[0].RemoteAddress)

		require.NotContains(t, r.watchersByHost, "h1")
		require.NotContains(t, r.watcheesByHost, "h1")
		require.Contains(t, r.watchersByHost, "h2")
		require.Contains(t, r.watcheesByHost, "h2")
	})

	t.Run("preserves entries on the same pid for other hosts", func(t *testing.T) {
		r := newRemoteWatchRegistry()
		w1 := newTestAddress(t, "w1", "h1", 1)
		w2 := newTestAddress(t, "w2", "h2", 2)
		r.addWatcher(testRegistryPidA, w1)
		r.addWatcher(testRegistryPidA, w2)

		entries := r.dropHost("h1")
		require.Len(t, entries.Watchers, 1)

		remaining := r.watchersFor(testRegistryPidA)
		require.Len(t, remaining, 1)
		require.Equal(t, w2, remaining[0])
	})
}

func TestRemoteWatchRegistry_ConcurrentAccess(t *testing.T) {
	// Exercises the RWMutex under interleaved readers/writers; relies on -race
	// at the package level to surface any data race.
	r := newRemoteWatchRegistry()
	w := newTestAddress(t, "w", "h1", 1)

	const goroutines = 16
	const iterations = 200

	var wg sync.WaitGroup
	wg.Add(goroutines * 4)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				r.addWatcher(testRegistryPidA, w)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				r.removeWatcher(testRegistryPidA, w)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = r.watchersFor(testRegistryPidA)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = r.dropHost("h1")
			}
		}()
	}

	wg.Wait()
}
