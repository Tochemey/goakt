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

package discovery

// Provider helps discover other running actor system in a cloud environment
type Provider interface {
	// ID returns the service discovery provider name
	ID() string
	// Initialize initializes the service discovery provider.
	Initialize() error
	// Register registers the service discovery provider.
	//
	// The cluster calls it when the node boots, before DiscoverPeers. Once it
	// has returned, DiscoverPeers on the other nodes must list this node, so
	// that two nodes booting at the same moment cannot both miss each other.
	Register() error
	// Deregister de-registers the service discovery provider.
	Deregister() error
	// DiscoverPeers returns a list discovered nodes' addresses.
	//
	// The cluster calls it when the node boots and joins the nodes it returns.
	// With the default minimum peers quorum of 1 it is not called again once
	// it has succeeded. A list that holds no other node means that this node
	// is alone and forms a cluster of one, and two clusters formed separately
	// never merge. An error makes the cluster retry the call, once a second
	// for ten seconds, before the node forms a cluster alone. A provider whose
	// view may be incomplete must therefore return an error rather than a list
	// without any other node.
	DiscoverPeers() ([]string, error)
	// Close closes the provider
	Close() error
}
