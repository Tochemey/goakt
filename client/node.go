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

package client

import (
	"crypto/tls"
	"net"
	nethttp "net/http"
	"strconv"
	"sync"

	"github.com/tochemey/goakt/v3/internal/http"
	"github.com/tochemey/goakt/v3/internal/locker"
	"github.com/tochemey/goakt/v3/internal/size"
	"github.com/tochemey/goakt/v3/internal/validation"
	"github.com/tochemey/goakt/v3/remote"
)

type NodeOption func(*Node)

// WithWeight set the node weight
func WithWeight(weight float64) NodeOption {
	return func(n *Node) {
		n.weight = weight
	}
}

// WithTLS configures the node to use a secure connection
// for communication with the specified remote node. This requires a TLS
// client configuration to enable secure interactions with the remote actor system.
//
// Ensure that the actor cluster is configured with TLS enabled and
// capable of completing a successful handshake. It is recommended that both
// systems share the same root Certificate Authority (CA) for mutual trust and
// secure communication.
func WithTLS(config *tls.Config) NodeOption {
	return func(n *Node) {
		n.tlsConfig = config
	}
}

// Node represents the node in the cluster
type Node struct {
	_       locker.NoCopy
	address string
	weight  float64
	mutex   *sync.RWMutex

	client    *nethttp.Client
	remoting  remote.Remoting
	tlsConfig *tls.Config
}

// NewNode creates an instance of Node
// nolint
func NewNode(address string, opts ...NodeOption) *Node {
	remoting := remote.NewRemoting(remote.WithRemotingMaxReadFameSize(16 * size.MB))
	node := &Node{
		address:  address,
		mutex:    &sync.RWMutex{},
		client:   remoting.HTTPClient(),
		remoting: remoting,
		weight:   0,
	}

	for _, opt := range opts {
		opt(node)
	}

	if node.tlsConfig != nil {
		// overwrite the remoting
		node.remoting = remote.NewRemoting(
			remote.WithRemotingMaxReadFameSize(16*size.MB),
			remote.WithRemotingTLS(node.tlsConfig),
		)

		// reset the client
		node.client = remoting.HTTPClient()
	}

	return node
}

var _ validation.Validator = (*Node)(nil)

// SetWeight sets the node weight.
// This is thread safe
func (n *Node) SetWeight(weight float64) {
	n.mutex.Lock()
	n.weight = weight
	n.mutex.Unlock()
}

// Address returns the node address
func (n *Node) Address() string {
	n.mutex.RLock()
	address := n.address
	n.mutex.RUnlock()
	return address
}

// Weight returns the node weight
func (n *Node) Weight() float64 {
	n.mutex.RLock()
	load := n.weight
	n.mutex.RUnlock()
	return load
}

func (n *Node) Validate() error {
	address := n.Address()
	return validation.NewTCPAddressValidator(address).Validate()
}

// HTTPClient returns the underlying http client for the given node
func (n *Node) HTTPClient() *nethttp.Client {
	n.mutex.RLock()
	client := n.client
	n.mutex.RUnlock()
	return client
}

// Remoting returns the remoting instance
func (n *Node) Remoting() remote.Remoting {
	n.mutex.RLock()
	remoting := n.remoting
	n.mutex.RUnlock()
	return remoting
}

// HTTPEndPoint returns the node remote endpoint
func (n *Node) HTTPEndPoint() string {
	n.mutex.RLock()
	host, p, _ := net.SplitHostPort(n.address)
	port, _ := strconv.Atoi(p)
	n.mutex.RUnlock()
	if n.tlsConfig != nil {
		return http.URLs(host, port)
	}
	return http.URL(host, port)
}

// Free closes the underlying http client connection of the given node
func (n *Node) Free() {
	n.HTTPClient().CloseIdleConnections()
	n.Remoting().Close()
}

// HostAndPort returns the node host and port
func (n *Node) HostAndPort() (string, int) {
	n.mutex.RLock()
	host, p, _ := net.SplitHostPort(n.address)
	port, _ := strconv.Atoi(p)
	n.mutex.RUnlock()
	return host, port
}
