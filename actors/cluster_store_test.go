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

package actors

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/log"
)

func TestClusterStore(t *testing.T) {
	t.Run("Open when directory exists", func(t *testing.T) {
		dir := t.TempDir()
		logger := log.DiscardLogger
		store, err := newClusterStore(dir, logger)
		require.NoError(t, err)
		require.NotNil(t, store)
	})

	t.Run("Open when directory does not exist", func(t *testing.T) {
		dir := "fake"
		logger := log.DiscardLogger
		store, err := newClusterStore(dir, logger)
		require.Error(t, err)
		require.Nil(t, store)
		store.close()
	})

	t.Run("SetAndGet", func(t *testing.T) {
		dir := t.TempDir()
		logger := log.DiscardLogger
		store, err := newClusterStore(dir, logger)
		require.NoError(t, err)
		require.NotNil(t, store)

		peerState := &internalpb.PeerState{
			Host:         "127.0.0.1",
			RemotingPort: 2280,
			PeersPort:    2281,
			Actors:       nil,
		}

		// Set successful
		err = store.set(peerState)
		require.NoError(t, err)

		// Get found
		key := "127.0.0.1:2281"
		actual, ok := store.get(key)
		require.True(t, ok)
		assert.True(t, proto.Equal(peerState, actual))

		// Get not found
		_, ok = store.get("127.0.0.1:2282")
		require.False(t, ok)

		// Get unmarshalling failed
		// Assume that wrong bytes array in kept in the LogSM
		err = store.store.Set(key, []byte("hello"))
		require.NoError(t, err)
		_, ok = store.get(key)
		require.False(t, ok)

		store.close()
	})

	t.Run("Remove", func(t *testing.T) {
		dir := t.TempDir()
		logger := log.DiscardLogger
		store, err := newClusterStore(dir, logger)
		require.NoError(t, err)
		require.NotNil(t, store)

		peerState := &internalpb.PeerState{
			Host:         "127.0.0.1",
			RemotingPort: 2280,
			PeersPort:    2281,
			Actors:       nil,
		}

		// Set successful
		err = store.set(peerState)
		require.NoError(t, err)

		// Get found
		key := "127.0.0.1:2281"
		actual, ok := store.get(key)
		require.True(t, ok)
		assert.True(t, proto.Equal(peerState, actual))

		// Remove
		err = store.remove(key)
		require.NoError(t, err)

		_, ok = store.get(key)
		require.False(t, ok)

		store.close()
	})
}
