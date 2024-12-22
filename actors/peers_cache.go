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
	"net"
	"strconv"
	"sync"

	"github.com/tochemey/goakt/v2/internal/internalpb"
)

type peersCache struct {
	*sync.RWMutex
	cache map[string]*internalpb.PeerState
}

// newPeerCache creates an instance of peersCache
func newPeerCache() *peersCache {
	return &peersCache{
		RWMutex: &sync.RWMutex{},
		cache:   make(map[string]*internalpb.PeerState),
	}
}

// set adds a peer to the cache
func (c *peersCache) set(peer *internalpb.PeerState) {
	peerAddress := net.JoinHostPort(peer.GetHost(), strconv.Itoa(int(peer.GetPeersPort())))
	c.Lock()
	c.cache[peerAddress] = peer
	c.Unlock()
}

// get retrieve a peer from the cache
func (c *peersCache) get(peerAddress string) (*internalpb.PeerState, bool) {
	c.RLock()
	peer, ok := c.cache[peerAddress]
	c.RUnlock()
	return peer, ok
}

// remove deletes a peer from the cache
func (c *peersCache) remove(peerAddress string) {
	c.Lock()
	delete(c.cache, peerAddress)
	c.Unlock()
}

// reset resets the cache
func (c *peersCache) reset() {
	c.Lock()
	c.cache = make(map[string]*internalpb.PeerState)
	c.Unlock()
}
