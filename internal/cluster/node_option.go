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

package cluster

import (
	"time"

	"github.com/tochemey/goakt/v2/log"
)

// NodeOption is the interface that applies to the Node
type NodeOption interface {
	// Apply sets the Option value of a config.
	Apply(*Node)
}

var _ NodeOption = NodeOptionFunc(nil)

// NodeOptionFunc implements the GroupOtion interface.
type NodeOptionFunc func(node *Node)

// Apply applies the Node's option
func (f NodeOptionFunc) Apply(node *Node) {
	f(node)
}

// WithNodeLogger sets the cacheLogger
func WithNodeLogger(logger log.Logger) NodeOption {
	return NodeOptionFunc(func(node *Node) {
		node.logger = logger
	})
}

// WithNodeShutdownTimeout sets the Node shutdown timeout.
func WithNodeShutdownTimeout(timeout time.Duration) NodeOption {
	return NodeOptionFunc(func(node *Node) {
		node.shutdownTimeout = timeout
	})
}

// WithNodesMinimumPeersQuorum sets the minimum number of nodes to form a quorum
func WithNodesMinimumPeersQuorum(minimumQuorum uint) NodeOption {
	return NodeOptionFunc(func(node *Node) {
		node.minimumPeersQuorum = minimumQuorum
	})
}

// WithNodeMaxJoinTimeout sets the max join timeout
func WithNodeMaxJoinTimeout(timeout time.Duration) NodeOption {
	return NodeOptionFunc(func(node *Node) {
		node.maxJoinTimeout = timeout
	})
}

// WithNodeMaxJoinAttempts sets the max join attempts
func WithNodeMaxJoinAttempts(attempts int) NodeOption {
	return NodeOptionFunc(func(node *Node) {
		node.maxJoinAttempts = attempts
	})
}

// WithNodeMaxJoinRetryInterval sets the max join retry interval
func WithNodeMaxJoinRetryInterval(retryInterval time.Duration) NodeOption {
	return NodeOptionFunc(func(node *Node) {
		node.maxJoinRetryInterval = retryInterval
	})
}

// WithNodeSyncInterval sets the sync interval
func WithNodeSyncInterval(interval time.Duration) NodeOption {
	return NodeOptionFunc(func(node *Node) {
		node.syncInterval = interval
	})
}

// WithNodeSecretKeys defines the secret keys
// A list of base64 encoded keys. Each key should be either 16, 24, or 32 bytes
// when decoded to select AES-128, AES-192, or AES-256 respectively.
// The first key in the list will be used for encrypting outbound messages. All keys are
// attempted when decrypting gossip, which allows for rotations.
// These keys should be the same for all nodes in the cluster
func WithNodeSecretKeys(secretKeys []string) NodeOption {
	return NodeOptionFunc(func(node *Node) {
		node.secretKeys = secretKeys
	})
}

// WithNodeReadTimeout sets the read timeout. This value defines
// how long it should take to timeout during a read operation in the cluster
func WithNodeReadTimeout(timeout time.Duration) NodeOption {
	return NodeOptionFunc(func(node *Node) {
		node.readTimeout = timeout
	})
}
