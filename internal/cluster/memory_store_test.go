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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/internal/internalpb"
)

func TestMemoryStore(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	peerState := &internalpb.PeerState{
		Host:         "127.0.0.1",
		RemotingPort: 2280,
		PeersPort:    2281,
		Actors:       nil,
	}

	require.NoError(t, store.PersistPeerState(ctx, peerState))

	key := "127.0.0.1:2281"
	actual, ok := store.GetPeerState(ctx, key)
	require.True(t, ok)
	assert.True(t, proto.Equal(peerState, actual))

	require.NoError(t, store.DeletePeerState(ctx, key))
	_, ok = store.GetPeerState(ctx, key)
	require.False(t, ok)

	require.NoError(t, store.Close())
}

func TestMemoryPersistPeerStateWhenNil(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	err := store.PersistPeerState(ctx, nil)
	require.NoError(t, err)
}

func TestMemoryStoreClose(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	peerState := &internalpb.PeerState{
		Host:         "127.0.0.1",
		RemotingPort: 2280,
		PeersPort:    2281,
		Actors:       nil,
	}

	require.NoError(t, store.PersistPeerState(ctx, peerState))

	key := "127.0.0.1:2281"
	actual, ok := store.GetPeerState(ctx, key)
	require.True(t, ok)
	assert.True(t, proto.Equal(peerState, actual))

	require.NoError(t, store.Close())
}
