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
	"path/filepath"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
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

func TestNewBoltStoreMkdirError(t *testing.T) {
	t.Setenv("HOME", "/dev/null")
	t.Setenv("USERPROFILE", "/dev/null")

	store, err := NewBoltStore()
	require.Error(t, err)
	require.Nil(t, store)
}

func TestNewBoltStoreDefaultBoltPathError(t *testing.T) {
	// Attempt to force os.UserHomeDir to fail by clearing home-related env vars
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")
	t.Setenv("GODEBUG", "osusergo=1")

	store, err := NewBoltStore()
	if err == nil {
		t.Skip("os.UserHomeDir resolved successfully; cannot force failure on this platform")
	}
	require.Error(t, err)
	require.Nil(t, store)
}

func TestNewBoltStoreOpenError(t *testing.T) {
	useTempHome(t)

	original := defaultBoltOptions
	defaultBoltOptions = &bbolt.Options{Timeout: boltTimeout, ReadOnly: true}
	defer func() { defaultBoltOptions = original }()

	store, err := NewBoltStore()
	require.Error(t, err)
	require.Nil(t, store)
}

func TestNewBoltStoreBucketInitializationError(t *testing.T) {
	useTempHome(t)

	// ensure predictable path
	boltPathCounter.Store(0)
	path, err := defaultBoltPath()
	require.NoError(t, err)

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))

	// create a bolt file so read-only open succeeds
	db, err := bbolt.Open(path, boltFileMode, defaultBoltOptions)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	// force read-only to trigger bucket creation failure
	original := defaultBoltOptions
	optsCopy := *defaultBoltOptions
	optsCopy.ReadOnly = true
	defaultBoltOptions = &optsCopy
	defer func() { defaultBoltOptions = original }()

	boltPathCounter.Store(0)
	store, err := NewBoltStore()
	require.Error(t, err)
	require.Nil(t, store)
}

func TestBoltDBStoreGetPeerStateUnmarshalError(t *testing.T) {
	useTempHome(t)

	store, err := NewBoltStore()
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	impl := store.(*BoltStore)
	require.NoError(t, impl.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(impl.bucket)
		require.NotNil(t, bucket)
		return bucket.Put([]byte("bad-peer"), []byte("not-proto"))
	}))

	state, ok := store.GetPeerState(context.Background(), "bad-peer")
	require.False(t, ok)
	require.Nil(t, state)
}

func TestBoltDBStoreCloseRemoveError(t *testing.T) {
	useTempHome(t)

	store, err := NewBoltStore()
	require.NoError(t, err)

	impl := store.(*BoltStore)
	origPath := impl.path
	impl.path = "/"

	err = impl.Close()
	require.Error(t, err)
	assert.ErrorContains(t, err, "/")

	_ = os.Remove(origPath)
}

func TestBoltDBStoreCloseJoinErrors(t *testing.T) {
	useTempHome(t)

	store, err := NewBoltStore()
	require.NoError(t, err)

	impl := store.(*BoltStore)
	origPath := impl.path
	impl.path = "/"

	fileField := reflect.ValueOf(impl.db).Elem().FieldByName("file")
	filePtr := (**os.File)(unsafe.Pointer(fileField.UnsafeAddr()))
	*filePtr = os.NewFile(^uintptr(0), impl.path)

	err = impl.Close()
	require.Error(t, err)
	assert.ErrorContains(t, err, "/")

	_ = os.Remove(origPath)
}

func TestBoltDBStoreCloseRemoveErrNotExist(t *testing.T) {
	useTempHome(t)

	store, err := NewBoltStore()
	require.NoError(t, err)
	impl := store.(*BoltStore)

	require.NoError(t, impl.db.Close())
	impl.closed.Store(false)

	impl.path = filepath.Join(t.TempDir(), "does-not-exist")

	err = impl.Close()
	require.NoError(t, err)
}

func TestHasNamespace(t *testing.T) {
	actorKey := composeKey(namespaceActors, "actor-1")
	assert.True(t, hasNamespace(actorKey, namespaceActors))
	assert.False(t, hasNamespace(actorKey, namespaceGrains))
}

func TestContextErr(t *testing.T) {
	var mockContext *MockContext
	mockContext = nil
	require.Nil(t, contextErr(mockContext))

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, contextErr(ctx))

	cancel()
	require.ErrorIs(t, contextErr(ctx), context.Canceled)
}
func useTempHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
}

type MockContext struct{}

var _ context.Context = (*MockContext)(nil)

func (m *MockContext) Deadline() (deadline time.Time, ok bool) {
	return time.Time{}, false
}

func (m *MockContext) Done() <-chan struct{} {
	return nil
}

func (m *MockContext) Err() error {
	return nil
}

func (m *MockContext) Value(key any) any { //nolint
	return nil
}
