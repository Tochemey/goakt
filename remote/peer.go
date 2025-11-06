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

package remote

import (
	"net"
	"strconv"
)

// Peer describes a remote node discovered in the cluster and the network
// endpoints it exposes for discovery, inter-peer coordination, and remoting.
type Peer struct {
	// Host is the DNS name or IP address of the node that other peers can reach.
	Host string

	// DiscoveryPort is the network port on which the node participates in
	// service discovery (for advertising itself and discovering other nodes).
	// Valid range is 1–65535.
	DiscoveryPort int

	// PeersPort is the network port used for peer-to-peer coordination traffic
	// between nodes (such as membership, gossip, or health checks).
	// Valid range is 1–65535.
	PeersPort int

	// RemotingPort is the network port used for application remoting, such as
	// RPC or actor message delivery between nodes.
	// Valid range is 1–65535.
	RemotingPort int

	// Roles lists the logical roles or capabilities this node advertises
	// (for example: "frontend", "backend", "shard"). Roles can be used for
	// routing or placement decisions. An empty list means no special roles.
	Roles []string

	// CreatedAt is a Unix timestamp (in nanoseconds) indicating when this
	// peer was first discovered or registered.
	CreatedAt int64
}

// PeersAddress returns address the node's peers will use to connect To
func (peer Peer) PeersAddress() string {
	return net.JoinHostPort(peer.Host, strconv.Itoa(peer.PeersPort))
}

// RemotingAddress returns address the node's remoting clients will use to connect To
func (peer Peer) RemotingAddress() string {
	return net.JoinHostPort(peer.Host, strconv.Itoa(peer.RemotingPort))
}
