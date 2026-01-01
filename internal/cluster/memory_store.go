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

package cluster

import (
	"context"
	"net"
	"strconv"
	"sync"

	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/internal/internalpb"
)

// MemoryStore is an in-memory implementation of Store that keeps cluster
// peer state information in a mutex-protected map.
//
// Concurrency:
//   - A RWMutex guards the underlying map allowing multiple concurrent readers
//     while writes are exclusive.
//   - Returned PeerState values are deep-cloned using proto.Clone to prevent
//     external callers from mutating the internal copies.
//
// Use cases:
//   - Suitable for tests, single-process deployments, or ephemeral runtime
//     state where durability is not required.
//   - Not suitable when persistence across restarts or multi-process sharing
//     is needed.
//
// Lifecycle:
//   - Call Close to explicitly clear all retained peer state (mainly useful
//     in tests to release memory deterministically).
type MemoryStore struct {
	mu    sync.RWMutex
	peers map[string]*internalpb.PeerState
}

var _ Store = (*MemoryStore)(nil) // enforce compilation error

// NewMemoryStore returns a new in-memory Store implementation.
// The provided logger is stored for potential future diagnostic use.
func NewMemoryStore() Store {
	return &MemoryStore{
		peers: make(map[string]*internalpb.PeerState),
	}
}

// PersistPeerState stores (create or update) the given peer state in memory.
// A nil peer is ignored and returns nil. The peer is cloned before storage
// to ensure encapsulation.
func (m *MemoryStore) PersistPeerState(_ context.Context, peer *internalpb.PeerState) error {
	if peer == nil {
		return nil
	}

	key := net.JoinHostPort(peer.GetHost(), strconv.Itoa(int(peer.GetPeersPort())))
	clone := proto.Clone(peer).(*internalpb.PeerState)

	m.mu.Lock()
	m.peers[key] = clone
	m.mu.Unlock()

	return nil
}

// GetPeerState retrieves the peer state associated with the given peerAddress.
// The returned state (if found) is a clone, so modifications will not affect
// the internal store. The boolean indicates presence.
func (m *MemoryStore) GetPeerState(_ context.Context, peerAddress string) (*internalpb.PeerState, bool) {
	m.mu.RLock()
	peer, ok := m.peers[peerAddress]
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}

	clone := proto.Clone(peer).(*internalpb.PeerState)
	return clone, true
}

// DeletePeerState removes the peer state for the given peerAddress.
// It is a no-op if the key does not exist.
func (m *MemoryStore) DeletePeerState(_ context.Context, peerAddress string) error {
	m.mu.Lock()
	delete(m.peers, peerAddress)
	m.mu.Unlock()
	return nil
}

// Close clears all retained peer states. After Close the instance can still
// be reused (subsequent operations repopulate the map). It always returns nil.
func (m *MemoryStore) Close() error {
	m.mu.Lock()
	for key := range m.peers {
		delete(m.peers, key)
	}
	m.mu.Unlock()
	return nil
}
