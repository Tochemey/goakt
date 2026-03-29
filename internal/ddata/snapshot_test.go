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

		entry := &internalpb.CRDTSnapshotEntry{
			Key:     codec.EncodeCRDTKey("counter-1", crdt.GCounterType),
			Data:    &internalpb.CRDTData{Type: &internalpb.CRDTData_GCounter{GCounter: &internalpb.GCounterData{State: map[string]uint64{"node-1": 10}}}},
			Version: 5,
		}
		entries := map[string]*internalpb.CRDTSnapshotEntry{"counter-1": entry}

		err = store.Save(entries)
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

		entry := &internalpb.CRDTSnapshotEntry{
			Key: codec.EncodeCRDTKey("pn-1", crdt.PNCounterType),
			Data: &internalpb.CRDTData{Type: &internalpb.CRDTData_PnCounter{PnCounter: &internalpb.PNCounterData{
				Increments: &internalpb.GCounterData{State: map[string]uint64{"node-1": 20}},
				Decrements: &internalpb.GCounterData{State: map[string]uint64{"node-1": 5}},
			}}},
			Version: 3,
		}
		entries := map[string]*internalpb.CRDTSnapshotEntry{"pn-1": entry}

		err = store.Save(entries)
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

		entry := &internalpb.CRDTSnapshotEntry{
			Key:     codec.EncodeCRDTKey("flag-1", crdt.FlagType),
			Data:    &internalpb.CRDTData{Type: &internalpb.CRDTData_Flag{Flag: &internalpb.FlagData{Enabled: true}}},
			Version: 1,
		}
		entries := map[string]*internalpb.CRDTSnapshotEntry{"flag-1": entry}

		err = store.Save(entries)
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
			"a": {
				Key:     codec.EncodeCRDTKey("a", crdt.GCounterType),
				Data:    &internalpb.CRDTData{Type: &internalpb.CRDTData_GCounter{GCounter: &internalpb.GCounterData{State: map[string]uint64{"n1": 1}}}},
				Version: 1,
			},
			"b": {
				Key:     codec.EncodeCRDTKey("b", crdt.GCounterType),
				Data:    &internalpb.CRDTData{Type: &internalpb.CRDTData_GCounter{GCounter: &internalpb.GCounterData{State: map[string]uint64{"n1": 2}}}},
				Version: 1,
			},
		}
		err = store.Save(entries1)
		require.NoError(t, err)

		// second save with different keys
		entries2 := map[string]*internalpb.CRDTSnapshotEntry{
			"c": {
				Key:     codec.EncodeCRDTKey("c", crdt.GCounterType),
				Data:    &internalpb.CRDTData{Type: &internalpb.CRDTData_GCounter{GCounter: &internalpb.GCounterData{State: map[string]uint64{"n1": 3}}}},
				Version: 2,
			},
		}
		err = store.Save(entries2)
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

		err = store.Save(nil)
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
			"gc": {
				Key:     codec.EncodeCRDTKey("gc", crdt.GCounterType),
				Data:    &internalpb.CRDTData{Type: &internalpb.CRDTData_GCounter{GCounter: &internalpb.GCounterData{State: map[string]uint64{"n1": 5}}}},
				Version: 1,
			},
			"pn": {
				Key: codec.EncodeCRDTKey("pn", crdt.PNCounterType),
				Data: &internalpb.CRDTData{Type: &internalpb.CRDTData_PnCounter{PnCounter: &internalpb.PNCounterData{
					Increments: &internalpb.GCounterData{State: map[string]uint64{"n1": 10}},
					Decrements: &internalpb.GCounterData{State: map[string]uint64{}},
				}}},
				Version: 2,
			},
			"flag": {
				Key:     codec.EncodeCRDTKey("flag", crdt.FlagType),
				Data:    &internalpb.CRDTData{Type: &internalpb.CRDTData_Flag{Flag: &internalpb.FlagData{Enabled: true}}},
				Version: 3,
			},
		}

		err = store.Save(entries)
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

		entry := &internalpb.CRDTSnapshotEntry{
			Key: &internalpb.CRDTKey{
				Id:       "bad-key",
				DataType: internalpb.CRDTDataType_CRDT_DATA_TYPE_UNSPECIFIED,
			},
			Data:    &internalpb.CRDTData{},
			Version: 1,
		}
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

		entry := &internalpb.CRDTSnapshotEntry{
			Key: &internalpb.CRDTKey{
				Id:       "bad-data",
				DataType: internalpb.CRDTDataType_CRDT_DATA_TYPE_G_COUNTER,
			},
			Data:    nil,
			Version: 1,
		}
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
