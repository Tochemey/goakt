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

package client

import (
	"net"
	nethttp "net/http"
	"strconv"
	"sync"

	"github.com/tochemey/goakt/v2/actors"
	"github.com/tochemey/goakt/v2/internal/http"
	"github.com/tochemey/goakt/v2/internal/validation"
)

type NodeOption func(*Node)

// WithWeight set the node weight
func WithWeight(weight float64) NodeOption {
	return func(n *Node) {
		n.weight = weight
	}
}

// Node represents the node in the cluster
type Node struct {
	address string
	weight  float64
	mutex   *sync.Mutex

	client   *nethttp.Client
	remoting *actors.Remoting
}

// NewNode creates an instance of Node
func NewNode(address string, opts ...NodeOption) *Node {
	node := &Node{
		address:  address,
		mutex:    &sync.Mutex{},
		client:   http.NewClient(),
		remoting: actors.NewRemoting(),
		weight:   0,
	}
	for _, opt := range opts {
		opt(node)
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
	n.mutex.Lock()
	address := n.address
	n.mutex.Unlock()
	return address
}

// Weight returns the node weight
func (n *Node) Weight() float64 {
	n.mutex.Lock()
	load := n.weight
	n.mutex.Unlock()
	return load
}

func (n *Node) Validate() error {
	address := n.Address()
	return validation.NewTCPAddressValidator(address).Validate()
}

// HTTPClient returns the underlying http client for the given node
func (n *Node) HTTPClient() *nethttp.Client {
	n.mutex.Lock()
	client := n.client
	n.mutex.Unlock()
	return client
}

// Remoting returns the remoting instance
func (n *Node) Remoting() *actors.Remoting {
	n.mutex.Lock()
	remoting := n.remoting
	n.mutex.Unlock()
	return remoting
}

// HTTPEndPoint returns the node remote endpoint
func (n *Node) HTTPEndPoint() string {
	n.mutex.Lock()
	host, p, _ := net.SplitHostPort(n.address)
	port, _ := strconv.Atoi(p)
	n.mutex.Unlock()
	return http.URL(host, port)
}

// Free closes the underlying http client connection of the given node
func (n *Node) Free() {
	n.HTTPClient().CloseIdleConnections()
	n.Remoting().Close()
}

// HostAndPort returns the node host and port
func (n *Node) HostAndPort() (string, int) {
	n.mutex.Lock()
	host, p, _ := net.SplitHostPort(n.address)
	port, _ := strconv.Atoi(p)
	n.mutex.Unlock()
	return host, port
}
