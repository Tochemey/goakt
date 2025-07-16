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
	"testing"

	"github.com/dgraph-io/badger/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/log"
)

func TestClusterStore(t *testing.T) {
	t.Run("SetAndGet", func(t *testing.T) {
		logger := log.DiscardLogger
		wal := t.TempDir()
		store, err := NewStore(logger, &wal)
		require.NoError(t, err)
		require.NotNil(t, store)

		peerState := &internalpb.PeerState{
			Host:         "127.0.0.1",
			RemotingPort: 2280,
			PeersPort:    2281,
			Actors:       nil,
		}

		// Set successful
		err = store.PersistPeerState(peerState)
		require.NoError(t, err)

		// Get found
		key := "127.0.0.1:2281"
		actual, ok := store.GetPeerState(key)
		require.True(t, ok)
		assert.True(t, proto.Equal(peerState, actual))

		// Get not found
		_, ok = store.GetPeerState("127.0.0.1:2282")
		require.False(t, ok)

		// Get unmarshalling failed
		// Assume that wrong bytes array in kept in the store
		err = store.db.Update(func(txn *badger.Txn) error {
			err := txn.Set([]byte(key), []byte("hello"))
			return err
		})

		require.NoError(t, err)
		_, ok = store.GetPeerState(key)
		require.False(t, ok)

		err = store.Close()
		require.NoError(t, err)
	})

	t.Run("Remove", func(t *testing.T) {
		logger := log.DiscardLogger
		store, err := NewStore(logger, nil)
		require.NoError(t, err)
		require.NotNil(t, store)

		peerState := &internalpb.PeerState{
			Host:         "127.0.0.1",
			RemotingPort: 2280,
			PeersPort:    2281,
			Actors:       nil,
		}

		// Set successful
		err = store.PersistPeerState(peerState)
		require.NoError(t, err)

		// Get found
		key := "127.0.0.1:2281"
		actual, ok := store.GetPeerState(key)
		require.True(t, ok)
		assert.True(t, proto.Equal(peerState, actual))

		// Remove
		err = store.DeletePeerState(key)
		require.NoError(t, err)

		_, ok = store.GetPeerState(key)
		require.False(t, ok)

		err = store.Close()
		require.NoError(t, err)
	})
}
