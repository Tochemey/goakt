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

package discovery

import (
	"fmt"
	"net"
	"strconv"
)

// Node represents a discovered Node
type Node struct {
	// Name specifies the discovered node's Name
	Name string
	// Host specifies the discovered node's Host
	Host string
	// DiscoveryPort
	DiscoveryPort int
	// PeersPort
	PeersPort int
	// RemotingPort
	RemotingPort int
}

// PeersAddress returns address the node's peers will use to connect to
func (n *Node) PeersAddress() string {
	return net.JoinHostPort(n.Host, strconv.Itoa(n.PeersPort))
}

// DiscoveryAddress returns the node discovery address
func (n *Node) DiscoveryAddress() string {
	return net.JoinHostPort(n.Host, strconv.Itoa(n.DiscoveryPort))
}

// String returns the printable representation of Node
func (n *Node) String() string {
	return fmt.Sprintf("[name=%s host=%s gossip=%d  peers=%d remoting=%d]", n.Name, n.Host, n.DiscoveryPort, n.PeersPort, n.RemotingPort)
}
