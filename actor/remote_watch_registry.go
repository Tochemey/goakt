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

	"github.com/tochemey/goakt/v4/internal/address"
)

// remoteWatchEntry is a single (local pid, remote actor) pair drawn from the
// registry. It is a neutral container: the role played by the remote actor
// is determined by which slice in remoteHostEntries the entry was returned in.
//
// Fields:
//   - LocalID:    canonical ID of the local actor involved in the
//     relationship. This is the value returned by PID.ID() and is used to
//     look the actor up in the pid tree.
//   - RemoteAddress: full address of the remote actor on the other side of
//     the relationship. When RemoteAddress appears in
//     remoteHostEntries.Watchers, it is the address of the remote watcher
//     that was watching the local pid. When it appears in
//     remoteHostEntries.Watchees, it is the address of the remote actor
//     that the local pid was watching.
type remoteWatchEntry struct {
	LocalID       string
	RemoteAddress *address.Address
}

// remoteHostEntries is the result of dropHost. It groups the entries that
// were removed because their remote peer lived on a departed cluster host,
// split by which side of the watch relationship each entry represents so the
// caller can apply the correct compensating action.
//
// Fields:
//   - Watchers: entries where the departed host hosted the remote watcher of
//     the local pid. These are dropped silently — the watcher is gone, no
//     further notification is possible or needed.
//   - Watchees: entries where the departed host hosted the remote actor that
//     a local pid was watching. The caller should synthesize a Terminated
//     message and deliver it to the local watcher so the application sees
//     the same lifecycle event it would on a clean shutdown.
type remoteHostEntries struct {
	Watchers []remoteWatchEntry
	Watchees []remoteWatchEntry
}

// remoteWatchRegistry tracks remote watch relationships outside the pid tree.
// It is sparse: maps are populated lazily and remain empty for actor systems
// that do not participate in remote watching, keeping the steady-state cost
// at zero for the common local-only case.
//
// The vocabulary mirrors the pid tree from the local actor's perspective:
//   - watchers: remote actors that are watching a local pid.
//   - watchees: remote actors that a local pid is watching.
//
// Two views are maintained for each direction:
//   - by-pid: O(1) fan-out from a local pid to its remote watchers or
//     watchees, consumed by freeWatchers and freeWatchees on shutdown.
//   - by-host: O(1) lookup of all entries touching a given remote host,
//     consumed by the NodeLeft cluster hook.
type remoteWatchRegistry struct {
	mu sync.RWMutex
	// watchers: localPidID -> watcherAddrString -> watcherAddr
	// Remote actors watching this local pid.
	watchers map[string]map[string]*address.Address
	// watchees: localPidID -> watcheeAddrString -> watcheeAddr
	// Remote actors this local pid is watching.
	watchees map[string]map[string]*address.Address
	// watchersByHost: host -> localPidID -> watcherAddrString -> struct{}
	// Reverse index for cheap NodeLeft cleanup of remote watchers.
	watchersByHost map[string]map[string]map[string]struct{}
	// watcheesByHost: host -> localPidID -> watcheeAddrString -> struct{}
	// Reverse index for cheap NodeLeft cleanup of remote watchees.
	watcheesByHost map[string]map[string]map[string]struct{}
}

// newRemoteWatchRegistry returns an empty registry. All maps are nil until
// the first registration to keep the steady-state footprint at zero.
func newRemoteWatchRegistry() *remoteWatchRegistry {
	return &remoteWatchRegistry{}
}

// addWatcher records that a remote actor at watcher is watching the local
// actor identified by id. Idempotent: re-adding the same pair overwrites the
// existing entry.
func (r *remoteWatchRegistry) addWatcher(id string, watcher *address.Address) {
	if id == "" || watcher == nil {
		return
	}

	addrKey := watcher.String()
	host := watcher.Host()

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.watchers == nil {
		r.watchers = make(map[string]map[string]*address.Address)
	}

	pidMap, ok := r.watchers[id]
	if !ok {
		pidMap = make(map[string]*address.Address)
		r.watchers[id] = pidMap
	}

	pidMap[addrKey] = watcher

	if r.watchersByHost == nil {
		r.watchersByHost = make(map[string]map[string]map[string]struct{})
	}

	hostMap, ok := r.watchersByHost[host]
	if !ok {
		hostMap = make(map[string]map[string]struct{})
		r.watchersByHost[host] = hostMap
	}

	addrs, ok := hostMap[id]
	if !ok {
		addrs = make(map[string]struct{})
		hostMap[id] = addrs
	}
	addrs[addrKey] = struct{}{}
}

// removeWatcher removes a previously recorded remote watcher of the local
// actor identified by id. No-op if the entry does not exist.
func (r *remoteWatchRegistry) removeWatcher(id string, watcher *address.Address) {
	if id == "" || watcher == nil {
		return
	}

	addrKey := watcher.String()
	host := watcher.Host()

	r.mu.Lock()
	defer r.mu.Unlock()

	if pidMap, ok := r.watchers[id]; ok {
		delete(pidMap, addrKey)
		if len(pidMap) == 0 {
			delete(r.watchers, id)
		}
	}

	if hostMap, ok := r.watchersByHost[host]; ok {
		if addrs, ok := hostMap[id]; ok {
			delete(addrs, addrKey)
			if len(addrs) == 0 {
				delete(hostMap, id)
			}
		}
		if len(hostMap) == 0 {
			delete(r.watchersByHost, host)
		}
	}
}

// watchersFor returns a snapshot of all remote watcher addresses registered
// against id. Returns nil when there are no entries.
func (r *remoteWatchRegistry) watchersFor(id string) []*address.Address {
	if id == "" {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	pidMap := r.watchers[id]
	if len(pidMap) == 0 {
		return nil
	}

	list := make([]*address.Address, 0, len(pidMap))
	for _, addr := range pidMap {
		list = append(list, addr)
	}
	return list
}

// addWatchee records that id (local) is watching a remote actor at watchee.
// Idempotent: re-adding the same pair overwrites the existing entry.
func (r *remoteWatchRegistry) addWatchee(id string, watchee *address.Address) {
	if id == "" || watchee == nil {
		return
	}

	addrKey := watchee.String()
	host := watchee.Host()

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.watchees == nil {
		r.watchees = make(map[string]map[string]*address.Address)
	}

	pidMap, ok := r.watchees[id]
	if !ok {
		pidMap = make(map[string]*address.Address)
		r.watchees[id] = pidMap
	}
	pidMap[addrKey] = watchee

	if r.watcheesByHost == nil {
		r.watcheesByHost = make(map[string]map[string]map[string]struct{})
	}

	hostMap, ok := r.watcheesByHost[host]
	if !ok {
		hostMap = make(map[string]map[string]struct{})
		r.watcheesByHost[host] = hostMap
	}

	addrs, ok := hostMap[id]
	if !ok {
		addrs = make(map[string]struct{})
		hostMap[id] = addrs
	}
	addrs[addrKey] = struct{}{}
}

// removeWatchee removes a previously recorded remote watchee of id.
// No-op if the entry does not exist.
func (r *remoteWatchRegistry) removeWatchee(id string, watchee *address.Address) {
	if id == "" || watchee == nil {
		return
	}

	addrKey := watchee.String()
	host := watchee.Host()

	r.mu.Lock()
	defer r.mu.Unlock()

	if pidMap, ok := r.watchees[id]; ok {
		delete(pidMap, addrKey)
		if len(pidMap) == 0 {
			delete(r.watchees, id)
		}
	}

	if hostMap, ok := r.watcheesByHost[host]; ok {
		if addrs, ok := hostMap[id]; ok {
			delete(addrs, addrKey)
			if len(addrs) == 0 {
				delete(hostMap, id)
			}
		}
		if len(hostMap) == 0 {
			delete(r.watcheesByHost, host)
		}
	}
}

// watcheesFor returns a snapshot of all remote watchee addresses id is
// watching. Returns nil when there are no entries.
func (r *remoteWatchRegistry) watcheesFor(id string) []*address.Address {
	if id == "" {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	pidMap := r.watchees[id]
	if len(pidMap) == 0 {
		return nil
	}

	list := make([]*address.Address, 0, len(pidMap))
	for _, addr := range pidMap {
		list = append(list, addr)
	}
	return list
}

// dropPID removes every watcher and watchee entry that involves id.
// Called when the local actor terminates so freed entries no longer linger.
// Idempotent.
func (r *remoteWatchRegistry) dropPID(id string) {
	if id == "" {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if pidMap, ok := r.watchers[id]; ok {
		for addrKey, addr := range pidMap {
			host := addr.Host()
			if hostMap, ok := r.watchersByHost[host]; ok {
				if addrs, ok := hostMap[id]; ok {
					delete(addrs, addrKey)
					if len(addrs) == 0 {
						delete(hostMap, id)
					}
				}
				if len(hostMap) == 0 {
					delete(r.watchersByHost, host)
				}
			}
		}
		delete(r.watchers, id)
	}

	if pidMap, ok := r.watchees[id]; ok {
		for addrKey, addr := range pidMap {
			host := addr.Host()
			if hostMap, ok := r.watcheesByHost[host]; ok {
				if addrs, ok := hostMap[id]; ok {
					delete(addrs, addrKey)
					if len(addrs) == 0 {
						delete(hostMap, id)
					}
				}
				if len(hostMap) == 0 {
					delete(r.watcheesByHost, host)
				}
			}
		}
		delete(r.watchees, id)
	}
}

// dropHost removes every entry touching host and returns them in two groups
// so the caller can react accordingly: watcher entries are simply discarded
// (the remote watcher is unreachable), while watchee entries typically
// trigger synthesized Terminated delivery to the local watcher.
// Idempotent.
func (r *remoteWatchRegistry) dropHost(host string) remoteHostEntries {
	if host == "" {
		return remoteHostEntries{}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	var entries remoteHostEntries

	if hostMap, ok := r.watchersByHost[host]; ok {
		for pidID, addrs := range hostMap {
			pidMap := r.watchers[pidID]
			for addrKey := range addrs {
				if addr, ok := pidMap[addrKey]; ok {
					entries.Watchers = append(entries.Watchers, remoteWatchEntry{LocalID: pidID, RemoteAddress: addr})
					delete(pidMap, addrKey)
				}
			}
			if len(pidMap) == 0 {
				delete(r.watchers, pidID)
			}
		}
		delete(r.watchersByHost, host)
	}

	if hostMap, ok := r.watcheesByHost[host]; ok {
		for pidID, addrs := range hostMap {
			pidMap := r.watchees[pidID]
			for addrKey := range addrs {
				if addr, ok := pidMap[addrKey]; ok {
					entries.Watchees = append(entries.Watchees, remoteWatchEntry{LocalID: pidID, RemoteAddress: addr})
					delete(pidMap, addrKey)
				}
			}
			if len(pidMap) == 0 {
				delete(r.watchees, pidID)
			}
		}
		delete(r.watcheesByHost, host)
	}

	return entries
}
