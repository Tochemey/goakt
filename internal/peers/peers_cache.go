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

package peers

import (
	"sync"

	"github.com/tochemey/goakt/v2/internal/internalpb"
)

type peersCache struct {
	*sync.RWMutex
	storage map[string]*internalpb.PeerState
}

// newPeersCache creates an instance of peersCache
func newPeersCache() *peersCache {
	return &peersCache{
		RWMutex: &sync.RWMutex{},
		storage: make(map[string]*internalpb.PeerState),
	}
}

// set adds a peer to the peersCache
func (x *peersCache) set(peerAddress string, peerState *internalpb.PeerState) {
	x.Lock()
	x.storage[peerAddress] = peerState
	x.Unlock()
}

// get retrieve a peer from the peersCache
func (x *peersCache) get(peerAddress string) (*internalpb.PeerState, bool) {
	x.RLock()
	peer, ok := x.storage[peerAddress]
	x.RUnlock()
	return peer, ok
}

// remove deletes a peer from the peersCache
func (x *peersCache) remove(peerAddress string) {
	x.Lock()
	delete(x.storage, peerAddress)
	x.Unlock()
}

// peerStates returns the list of peer states
func (x *peersCache) peerStates() []*internalpb.PeerState {
	x.RLock()
	peers := make([]*internalpb.PeerState, 0, len(x.storage))
	for _, peer := range x.storage {
		peers = append(peers, peer)
	}
	x.RUnlock()
	return peers
}

// reset resets the peersCache
func (x *peersCache) reset() {
	x.Lock()
	x.storage = make(map[string]*internalpb.PeerState)
	x.Unlock()
}
