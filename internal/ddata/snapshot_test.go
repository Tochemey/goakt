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

package ddata

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/crdt"
	"github.com/tochemey/goakt/v4/internal/codec"
	"github.com/tochemey/goakt/v4/internal/internalpb"
)

func TestStore(t *testing.T) {
	t.Run("save and load round trip with GCounter", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)
		defer store.Close()

		entry := internalpb.CRDTSnapshotEntry_builder{
			Key:     codec.EncodeCRDTKey("counter-1", crdt.GCounterType),
			Data:    internalpb.CRDTData_builder{GCounter: internalpb.GCounterData_builder{State: map[string]uint64{"node-1": 10}}.Build()}.Build(),
			Version: 5,
		}.Build()
		entries := map[string]*internalpb.CRDTSnapshotEntry{"counter-1": entry}

		err = store.Save(entries, nil, time.Time{}, time.Time{})
		require.NoError(t, err)

		loaded, err := store.Load()
		require.NoError(t, err)
		require.Len(t, loaded, 1)
		require.NotNil(t, loaded["counter-1"])
		assert.Equal(t, uint64(5), loaded["counter-1"].GetVersion())
		assert.Equal(t, uint64(10), loaded["counter-1"].GetData().GetGCounter().GetState()["node-1"])
	})

	t.Run("save and load round trip with PNCounter", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)
		defer store.Close()

		entry := internalpb.CRDTSnapshotEntry_builder{
			Key: codec.EncodeCRDTKey("pn-1", crdt.PNCounterType),
			Data: internalpb.CRDTData_builder{PnCounter: internalpb.PNCounterData_builder{
				Increments: internalpb.GCounterData_builder{State: map[string]uint64{"node-1": 20}}.Build(),
				Decrements: internalpb.GCounterData_builder{State: map[string]uint64{"node-1": 5}}.Build(),
			}.Build()}.Build(),
			Version: 3,
		}.Build()
		entries := map[string]*internalpb.CRDTSnapshotEntry{"pn-1": entry}

		err = store.Save(entries, nil, time.Time{}, time.Time{})
		require.NoError(t, err)

		loaded, err := store.Load()
		require.NoError(t, err)
		require.Len(t, loaded, 1)
		assert.Equal(t, uint64(3), loaded["pn-1"].GetVersion())
		assert.Equal(t, uint64(20), loaded["pn-1"].GetData().GetPnCounter().GetIncrements().GetState()["node-1"])
		assert.Equal(t, uint64(5), loaded["pn-1"].GetData().GetPnCounter().GetDecrements().GetState()["node-1"])
	})

	t.Run("save and load round trip with Flag", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)
		defer store.Close()

		entry := internalpb.CRDTSnapshotEntry_builder{
			Key:     codec.EncodeCRDTKey("flag-1", crdt.FlagType),
			Data:    internalpb.CRDTData_builder{Flag: internalpb.FlagData_builder{Enabled: true}.Build()}.Build(),
			Version: 1,
		}.Build()
		entries := map[string]*internalpb.CRDTSnapshotEntry{"flag-1": entry}

		err = store.Save(entries, nil, time.Time{}, time.Time{})
		require.NoError(t, err)

		loaded, err := store.Load()
		require.NoError(t, err)
		require.Len(t, loaded, 1)
		assert.True(t, loaded["flag-1"].GetData().GetFlag().GetEnabled())
	})

	t.Run("load from empty DB returns empty map", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)
		defer store.Close()

		loaded, err := store.Load()
		require.NoError(t, err)
		assert.Empty(t, loaded)
	})

	t.Run("save overwrites previous data", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)
		defer store.Close()

		// first save
		entries1 := map[string]*internalpb.CRDTSnapshotEntry{
			"a": internalpb.CRDTSnapshotEntry_builder{
				Key:     codec.EncodeCRDTKey("a", crdt.GCounterType),
				Data:    internalpb.CRDTData_builder{GCounter: internalpb.GCounterData_builder{State: map[string]uint64{"n1": 1}}.Build()}.Build(),
				Version: 1,
			}.Build(),
			"b": internalpb.CRDTSnapshotEntry_builder{
				Key:     codec.EncodeCRDTKey("b", crdt.GCounterType),
				Data:    internalpb.CRDTData_builder{GCounter: internalpb.GCounterData_builder{State: map[string]uint64{"n1": 2}}.Build()}.Build(),
				Version: 1,
			}.Build(),
		}
		err = store.Save(entries1, nil, time.Time{}, time.Time{})
		require.NoError(t, err)

		// second save with different keys
		entries2 := map[string]*internalpb.CRDTSnapshotEntry{
			"c": internalpb.CRDTSnapshotEntry_builder{
				Key:     codec.EncodeCRDTKey("c", crdt.GCounterType),
				Data:    internalpb.CRDTData_builder{GCounter: internalpb.GCounterData_builder{State: map[string]uint64{"n1": 3}}.Build()}.Build(),
				Version: 2,
			}.Build(),
		}
		err = store.Save(entries2, nil, time.Time{}, time.Time{})
		require.NoError(t, err)

		// load should only have "c"
		loaded, err := store.Load()
		require.NoError(t, err)
		assert.Len(t, loaded, 1)
		_, hasA := loaded["a"]
		assert.False(t, hasA)
		_, hasC := loaded["c"]
		assert.True(t, hasC)
	})

	t.Run("close is idempotent", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)

		err = store.Close()
		require.NoError(t, err)
		err = store.Close()
		require.NoError(t, err)
	})

	t.Run("save after close returns error", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)

		err = store.Close()
		require.NoError(t, err)

		err = store.Save(nil, nil, time.Time{}, time.Time{})
		assert.ErrorIs(t, err, ErrStoreClosed)
	})

	t.Run("load after close returns error", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)

		err = store.Close()
		require.NoError(t, err)

		_, err = store.Load()
		assert.ErrorIs(t, err, ErrStoreClosed)
	})

	t.Run("multiple CRDT types in single snapshot", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)
		defer store.Close()

		entries := map[string]*internalpb.CRDTSnapshotEntry{
			"gc": internalpb.CRDTSnapshotEntry_builder{
				Key:     codec.EncodeCRDTKey("gc", crdt.GCounterType),
				Data:    internalpb.CRDTData_builder{GCounter: internalpb.GCounterData_builder{State: map[string]uint64{"n1": 5}}.Build()}.Build(),
				Version: 1,
			}.Build(),
			"pn": internalpb.CRDTSnapshotEntry_builder{
				Key: codec.EncodeCRDTKey("pn", crdt.PNCounterType),
				Data: internalpb.CRDTData_builder{PnCounter: internalpb.PNCounterData_builder{
					Increments: internalpb.GCounterData_builder{State: map[string]uint64{"n1": 10}}.Build(),
					Decrements: internalpb.GCounterData_builder{State: map[string]uint64{}}.Build(),
				}.Build()}.Build(),
				Version: 2,
			}.Build(),
			"flag": internalpb.CRDTSnapshotEntry_builder{
				Key:     codec.EncodeCRDTKey("flag", crdt.FlagType),
				Data:    internalpb.CRDTData_builder{Flag: internalpb.FlagData_builder{Enabled: true}.Build()}.Build(),
				Version: 3,
			}.Build(),
		}

		err = store.Save(entries, nil, time.Time{}, time.Time{})
		require.NoError(t, err)

		loaded, err := store.Load()
		require.NoError(t, err)
		assert.Len(t, loaded, 3)
		assert.Equal(t, uint64(5), loaded["gc"].GetData().GetGCounter().GetState()["n1"])
		assert.Equal(t, uint64(10), loaded["pn"].GetData().GetPnCounter().GetIncrements().GetState()["n1"])
		assert.True(t, loaded["flag"].GetData().GetFlag().GetEnabled())
		assert.Equal(t, uint64(1), loaded["gc"].GetVersion())
		assert.Equal(t, uint64(2), loaded["pn"].GetVersion())
		assert.Equal(t, uint64(3), loaded["flag"].GetVersion())
	})

	t.Run("EnsureOpen returns error when closed", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)

		err = store.Close()
		require.NoError(t, err)

		err = store.EnsureOpen()
		assert.ErrorIs(t, err, ErrStoreClosed)
	})

	t.Run("EnsureOpen returns nil when open", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)
		defer store.Close()

		err = store.EnsureOpen()
		assert.NoError(t, err)
	})

	t.Run("close then remove deletes file", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)

		err = store.Close()
		require.NoError(t, err)

		err = store.Remove()
		require.NoError(t, err)
	})

	t.Run("remove before close returns error", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)
		defer store.Close()

		err = store.Remove()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "store is still open")
	})

	t.Run("NewStore with invalid directory", func(t *testing.T) {
		dir := t.TempDir()
		filePath := filepath.Join(dir, "blockerfile")
		err := os.WriteFile(filePath, []byte("x"), 0o600)
		require.NoError(t, err)

		_, err = NewStore(filepath.Join(filePath, "subdir"))
		require.Error(t, err)
	})

	t.Run("load with corrupted entry returns error", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)
		defer store.Close()

		err = store.db.Update(func(tx *bbolt.Tx) error {
			b := tx.Bucket([]byte(bucketName))
			return b.Put([]byte("corrupt-key"), []byte{0xff, 0xff})
		})
		require.NoError(t, err)

		_, err = store.Load()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshal snapshot entry")
	})

	t.Run("contact times round trip", func(t *testing.T) {
		store, err := NewStore(t.TempDir())
		require.NoError(t, err)
		defer store.Close()

		lastContact := time.Unix(0, time.Now().UnixNano())
		since := lastContact.Add(-time.Hour)
		require.NoError(t, store.Save(nil, nil, lastContact, since))

		loadedContact, loadedSince, err := store.Contact()
		require.NoError(t, err)
		assert.True(t, lastContact.Equal(loadedContact))
		assert.True(t, since.Equal(loadedSince))
	})

	t.Run("contact times survive reopening the store", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)

		lastContact := time.Unix(0, time.Now().UnixNano())
		require.NoError(t, store.Save(nil, nil, lastContact, lastContact))
		require.NoError(t, store.Close())

		reopened, err := NewStore(dir)
		require.NoError(t, err)
		defer reopened.Close()

		loadedContact, loadedSince, err := reopened.Contact()
		require.NoError(t, err)
		assert.True(t, lastContact.Equal(loadedContact))
		assert.True(t, lastContact.Equal(loadedSince))
	})

	t.Run("zero or missing contact times read back as the zero time", func(t *testing.T) {
		store, err := NewStore(t.TempDir())
		require.NoError(t, err)
		defer store.Close()

		// never saved, as in a snapshot of a version that predates them
		lastContact, since, err := store.Contact()
		require.NoError(t, err)
		assert.True(t, lastContact.IsZero())
		assert.True(t, since.IsZero())

		// saved by a node that has never heard from a peer and has stale keys
		require.NoError(t, store.Save(nil, nil, time.Time{}, time.Time{}))
		lastContact, since, err = store.Contact()
		require.NoError(t, err)
		assert.True(t, lastContact.IsZero())
		assert.True(t, since.IsZero())
	})

	t.Run("tombstones round trip and survive reopening the store", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)

		deleted := internalpb.CRDTTombstone_builder{
			Key:            codec.EncodeCRDTKey("gone", crdt.GCounterType),
			DeletedAtNanos: 42,
			DeletedByNode:  "node-1",
		}.Build()
		require.NoError(t, store.Save(nil, []*internalpb.CRDTTombstone{deleted}, time.Time{}, time.Time{}))

		tombstones, err := store.Tombstones()
		require.NoError(t, err)
		require.Len(t, tombstones, 1)
		assert.Equal(t, "gone", tombstones[0].GetKey().GetId())
		assert.Equal(t, int64(42), tombstones[0].GetDeletedAtNanos())
		assert.Equal(t, "node-1", tombstones[0].GetDeletedByNode())
		require.NoError(t, store.Close())

		store, err = NewStore(dir)
		require.NoError(t, err)
		defer store.Close()

		tombstones, err = store.Tombstones()
		require.NoError(t, err)
		require.Len(t, tombstones, 1)
		assert.Equal(t, "gone", tombstones[0].GetKey().GetId())
	})

	t.Run("save replaces the tombstones of the previous snapshot", func(t *testing.T) {
		store, err := NewStore(t.TempDir())
		require.NoError(t, err)
		defer store.Close()

		first := internalpb.CRDTTombstone_builder{Key: codec.EncodeCRDTKey("first", crdt.GCounterType)}.Build()
		second := internalpb.CRDTTombstone_builder{Key: codec.EncodeCRDTKey("second", crdt.GCounterType)}.Build()
		require.NoError(t, store.Save(nil, []*internalpb.CRDTTombstone{first}, time.Time{}, time.Time{}))
		require.NoError(t, store.Save(nil, []*internalpb.CRDTTombstone{second}, time.Time{}, time.Time{}))

		tombstones, err := store.Tombstones()
		require.NoError(t, err)
		require.Len(t, tombstones, 1)
		assert.Equal(t, "second", tombstones[0].GetKey().GetId())
	})

	t.Run("an entry or a tombstone with an empty key ID is skipped", func(t *testing.T) {
		store, err := NewStore(t.TempDir())
		require.NoError(t, err)
		defer store.Close()

		entries := map[string]*internalpb.CRDTSnapshotEntry{
			"":       internalpb.CRDTSnapshotEntry_builder{Key: codec.EncodeCRDTKey("", crdt.GCounterType)}.Build(),
			"kept-1": internalpb.CRDTSnapshotEntry_builder{Key: codec.EncodeCRDTKey("kept-1", crdt.GCounterType)}.Build(),
		}
		tombstones := []*internalpb.CRDTTombstone{
			internalpb.CRDTTombstone_builder{Key: codec.EncodeCRDTKey("", crdt.GCounterType)}.Build(),
			internalpb.CRDTTombstone_builder{Key: codec.EncodeCRDTKey("kept-2", crdt.GCounterType)}.Build(),
		}
		require.NoError(t, store.Save(entries, tombstones, time.Time{}, time.Time{}))

		loaded, err := store.Load()
		require.NoError(t, err)
		require.Len(t, loaded, 1)
		assert.Contains(t, loaded, "kept-1")

		saved, err := store.Tombstones()
		require.NoError(t, err)
		require.Len(t, saved, 1)
		assert.Equal(t, "kept-2", saved[0].GetKey().GetId())
	})

	t.Run("a snapshot file of a version without tombstones loads none and saves", func(t *testing.T) {
		dir := t.TempDir()
		optionsCopy := *defaultOptions
		db, err := bbolt.Open(filepath.Join(dir, fileName), fileMode, &optionsCopy)
		require.NoError(t, err)
		require.NoError(t, db.Update(func(tx *bbolt.Tx) error {
			if _, err := tx.CreateBucket([]byte(bucketName)); err != nil {
				return err
			}

			_, err := tx.CreateBucket([]byte(metaBucketName))
			return err
		}))
		require.NoError(t, db.Close())

		store, err := NewStore(dir)
		require.NoError(t, err)
		defer store.Close()

		tombstones, err := store.Tombstones()
		require.NoError(t, err)
		assert.Empty(t, tombstones)

		deleted := internalpb.CRDTTombstone_builder{Key: codec.EncodeCRDTKey("gone", crdt.GCounterType)}.Build()
		require.NoError(t, store.Save(nil, []*internalpb.CRDTTombstone{deleted}, time.Time{}, time.Time{}))

		tombstones, err = store.Tombstones()
		require.NoError(t, err)
		require.Len(t, tombstones, 1)
	})

	t.Run("a snapshot file without the tombstone bucket reads no tombstones and cannot be saved", func(t *testing.T) {
		store, err := NewStore(t.TempDir())
		require.NoError(t, err)
		defer store.Close()

		require.NoError(t, store.db.Update(func(tx *bbolt.Tx) error {
			return tx.DeleteBucket([]byte(tombstoneBucketName))
		}))

		tombstones, err := store.Tombstones()
		require.NoError(t, err)
		assert.Empty(t, tombstones)

		err = store.Save(nil, nil, time.Time{}, time.Time{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), tombstoneBucketName)
	})

	t.Run("a corrupted tombstone returns error", func(t *testing.T) {
		store, err := NewStore(t.TempDir())
		require.NoError(t, err)
		defer store.Close()

		require.NoError(t, store.db.Update(func(tx *bbolt.Tx) error {
			return tx.Bucket([]byte(tombstoneBucketName)).Put([]byte("bad"), []byte{0xff, 0xff})
		}))

		_, err = store.Tombstones()
		require.Error(t, err)
	})

	t.Run("tombstones after close return error", func(t *testing.T) {
		store, err := NewStore(t.TempDir())
		require.NoError(t, err)
		require.NoError(t, store.Close())

		_, err = store.Tombstones()
		require.ErrorIs(t, err, ErrStoreClosed)
	})

	t.Run("a snapshot file without the metadata bucket reads no contact and cannot be saved", func(t *testing.T) {
		store, err := NewStore(t.TempDir())
		require.NoError(t, err)
		defer store.Close()

		require.NoError(t, store.db.Update(func(tx *bbolt.Tx) error {
			return tx.DeleteBucket([]byte(metaBucketName))
		}))

		lastContact, since, err := store.Contact()
		require.NoError(t, err)
		assert.True(t, lastContact.IsZero())
		assert.True(t, since.IsZero())

		err = store.Save(nil, nil, time.Now(), time.Time{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), metaBucketName)
	})

	t.Run("a malformed contact time returns error", func(t *testing.T) {
		for _, key := range []string{lastContactKey, sinceKey} {
			store, err := NewStore(t.TempDir())
			require.NoError(t, err)

			require.NoError(t, store.db.Update(func(tx *bbolt.Tx) error {
				return tx.Bucket([]byte(metaBucketName)).Put([]byte(key), []byte{0x01})
			}))

			_, _, err = store.Contact()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "malformed time")
			require.NoError(t, store.Close())
		}
	})

	t.Run("contact times after close return error", func(t *testing.T) {
		store, err := NewStore(t.TempDir())
		require.NoError(t, err)
		require.NoError(t, store.Close())

		_, _, err = store.Contact()
		require.ErrorIs(t, err, ErrStoreClosed)
	})

	t.Run("remove already removed file is no-op", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)

		err = store.Close()
		require.NoError(t, err)

		err = store.Remove()
		require.NoError(t, err)

		err = store.Remove()
		require.NoError(t, err)
	})

	t.Run("load with unspecified key type returns raw entry", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)
		defer store.Close()

		entry := internalpb.CRDTSnapshotEntry_builder{
			Key: internalpb.CRDTKey_builder{
				Id:       "bad-key",
				DataType: internalpb.CRDTDataType_CRDT_DATA_TYPE_UNSPECIFIED,
			}.Build(),
			Data:    &internalpb.CRDTData{},
			Version: 1,
		}.Build()
		raw, err := proto.Marshal(entry)
		require.NoError(t, err)

		err = store.db.Update(func(tx *bbolt.Tx) error {
			b := tx.Bucket([]byte(bucketName))
			return b.Put([]byte("bad-key"), raw)
		})
		require.NoError(t, err)

		loaded, err := store.Load()
		require.NoError(t, err)
		assert.Len(t, loaded, 1)
	})

	t.Run("load with nil data returns raw entry", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)
		defer store.Close()

		entry := internalpb.CRDTSnapshotEntry_builder{
			Key: internalpb.CRDTKey_builder{
				Id:       "bad-data",
				DataType: internalpb.CRDTDataType_CRDT_DATA_TYPE_G_COUNTER,
			}.Build(),
			Data:    nil,
			Version: 1,
		}.Build()
		raw, err := proto.Marshal(entry)
		require.NoError(t, err)

		err = store.db.Update(func(tx *bbolt.Tx) error {
			b := tx.Bucket([]byte(bucketName))
			return b.Put([]byte("bad-data"), raw)
		})
		require.NoError(t, err)

		loaded, err := store.Load()
		require.NoError(t, err)
		assert.Len(t, loaded, 1)
	})
}
