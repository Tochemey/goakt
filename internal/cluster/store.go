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
	"context"

	"github.com/tochemey/goakt/v3/internal/internalpb"
)

// Store persists and retrieves peer state metadata for the local node.
// The primary purpose is to retain minimal information about known peers
// so the cluster membership can be reconstructed after process restarts
// or transient failures without requiring a full re-discovery round.
//
// Implementations may be in-memory (ephemeral) or durable (e.g. disk / KV).
// Implementations MUST be safe for concurrent use by multiple goroutines.
// Unless otherwise stated, operations should be idempotent where practical.
type Store interface {
	// PersistPeerState stores or updates the given peer state. If a state for
	// the peer already exists it is replaced. Implementations should treat
	// identical subsequent writes as idempotent. A non-nil error is returned
	// when the state cannot be persisted.
	PersistPeerState(ctx context.Context, peer *internalpb.PeerState) error

	// GetPeerState returns the last persisted state for the peer identified
	// by its address together with a boolean indicating presence. If the
	// peer is unknown the returned state is nil and the boolean is false.
	GetPeerState(ctx context.Context, peerAddress string) (*internalpb.PeerState, bool)

	// DeletePeerState removes any stored state for the given peer. Deleting
	// a non-existent peer should not be considered an error (idempotent).
	// A non-nil error is returned only when the deletion attempt fails.
	DeletePeerState(ctx context.Context, peerAddress string) error

	// Close releases any underlying resources (files, network handles, etc.).
	// After Close returns, the Store should not be used. Close must be safe
	// to call once; subsequent calls may return the same error or nil.
	Close() error
}
