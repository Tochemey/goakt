/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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

package cluster

import (
	"net"
	"slices"
	"strconv"

	"github.com/tochemey/goakt/v3/remote"
)

// Peer defines the peer info
type Peer struct {
	// Host represents the peer address.
	Host string
	// DiscoveryPort
	DiscoveryPort int
	// PeersPort represents the peer port
	PeersPort int
	// Coordinator states that the given peer is the leader not.
	// A peer is a coordinator when it is the oldest node in the cluster
	Coordinator bool
	// RemotingPort
	RemotingPort int
	// Roles represents the peer roles
	Roles []string
	// CreatedAt represents the time in nanoseconds the peer was created
	CreatedAt int64
}

// PeerAddress returns address the node's peers will use to connect to
func (peer Peer) PeerAddress() string {
	return net.JoinHostPort(peer.Host, strconv.Itoa(peer.PeersPort))
}

// HasRole checks if the peer has the given role
func (peer Peer) HasRole(role string) bool {
	return slices.Contains(peer.Roles, role)
}

func ToRemotePeers(peers []*Peer) []*remote.Peer {
	remotePeers := make([]*remote.Peer, len(peers))
	for i, peer := range peers {
		remotePeers[i] = &remote.Peer{
			Host:          peer.Host,
			RemotingPort:  peer.RemotingPort,
			PeersPort:     peer.PeersPort,
			Roles:         peer.Roles,
			DiscoveryPort: peer.DiscoveryPort,
		}
	}
	return remotePeers
}
