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
	"errors"
	"io/fs"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	bbolt "go.etcd.io/bbolt"

	"github.com/tochemey/goakt/v3/internal/internalpb"
)

func TestBoltDBStoreLifecycle(t *testing.T) {
	useTempHome(t)

	store, err := NewBoltStore()
	require.NoError(t, err)
	impl, ok := store.(*BoltStore)
	require.True(t, ok)

	ctx := context.Background()
	peer := &internalpb.PeerState{
		Host:         "127.0.0.1",
		RemotingPort: 6000,
		PeersPort:    7000,
	}

	require.NoError(t, store.PersistPeerState(ctx, peer))

	address := peerKey(peer)
	loaded, ok := store.GetPeerState(ctx, address)
	require.True(t, ok)
	require.Equal(t, peer.GetHost(), loaded.GetHost())
	require.Equal(t, peer.GetPeersPort(), loaded.GetPeersPort())

	require.NoError(t, store.DeletePeerState(ctx, address))
	_, ok = store.GetPeerState(ctx, address)
	require.False(t, ok)

	require.NoError(t, store.Close())
	_, statErr := os.Stat(impl.path)
	require.Error(t, statErr)
	require.True(t, errors.Is(statErr, fs.ErrNotExist))
}

func TestBoltDBStoreCloseIsIdempotent(t *testing.T) {
	useTempHome(t)

	store, err := NewBoltStore()
	require.NoError(t, err)
	require.NoError(t, store.Close())
	require.NoError(t, store.Close())
}

func TestBoltDBStoreNilPeerIsNoop(t *testing.T) {
	useTempHome(t)

	store, err := NewBoltStore()
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	require.NoError(t, store.PersistPeerState(context.Background(), nil))
}

func TestBoltDBStoreContextCancellation(t *testing.T) {
	useTempHome(t)

	store, err := NewBoltStore()
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	peer := &internalpb.PeerState{Host: "127.0.0.2", PeersPort: 7100}
	peerAddr := peerKey(peer)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.ErrorIs(t, store.PersistPeerState(ctx, peer), context.Canceled)
	state, ok := store.GetPeerState(ctx, peerAddr)
	require.False(t, ok)
	require.Nil(t, state)
	require.ErrorIs(t, store.DeletePeerState(ctx, peerAddr), context.Canceled)
}

func TestBoltDBStoreOperationsAfterClose(t *testing.T) {
	useTempHome(t)

	store, err := NewBoltStore()
	require.NoError(t, err)
	require.NoError(t, store.Close())

	peer := &internalpb.PeerState{Host: "127.0.0.3", PeersPort: 7200}
	peerAddr := peerKey(peer)

	require.ErrorIs(t, store.PersistPeerState(context.Background(), peer), errBoltStoreClosed)
	state, ok := store.GetPeerState(context.Background(), peerAddr)
	require.False(t, ok)
	require.Nil(t, state)
	require.ErrorIs(t, store.DeletePeerState(context.Background(), peerAddr), errBoltStoreClosed)
}

func TestBoltDBStoreMissingBucket(t *testing.T) {
	useTempHome(t)

	store, err := NewBoltStore()
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	impl, ok := store.(*BoltStore)
	require.True(t, ok)

	require.NoError(t, impl.db.Update(func(tx *bbolt.Tx) error {
		return tx.DeleteBucket(impl.bucket)
	}))

	peer := &internalpb.PeerState{Host: "127.0.0.4", PeersPort: 7300}
	peerAddr := peerKey(peer)

	require.Error(t, store.PersistPeerState(context.Background(), peer))
	state, ok := store.GetPeerState(context.Background(), peerAddr)
	require.False(t, ok)
	require.Nil(t, state)
	require.Error(t, store.DeletePeerState(context.Background(), peerAddr))
}

func useTempHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
}
