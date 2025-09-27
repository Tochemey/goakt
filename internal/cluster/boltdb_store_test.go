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

func useTempHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
}
