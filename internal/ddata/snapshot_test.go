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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/crdt"
)

func TestStore(t *testing.T) {
	t.Run("save and load round trip with GCounter", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)
		defer store.Close()

		data := map[string]crdt.ReplicatedData{
			"counter-1": crdt.NewGCounter().Increment("node-1", 10),
		}
		keyTypes := map[string]crdt.DataType{
			"counter-1": crdt.GCounterType,
		}
		versions := map[string]uint64{
			"counter-1": 5,
		}

		err = store.Save(data, keyTypes, versions)
		require.NoError(t, err)

		loadedData, loadedTypes, loadedVersions, err := store.Load()
		require.NoError(t, err)
		require.Len(t, loadedData, 1)
		assert.Equal(t, crdt.GCounterType, loadedTypes["counter-1"])
		assert.Equal(t, uint64(5), loadedVersions["counter-1"])

		gc := loadedData["counter-1"].(*crdt.GCounter)
		assert.Equal(t, uint64(10), gc.Value())
	})

	t.Run("save and load round trip with PNCounter", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)
		defer store.Close()

		pn := crdt.NewPNCounter().Increment("node-1", 20).Decrement("node-1", 5)
		data := map[string]crdt.ReplicatedData{"pn-1": pn}
		keyTypes := map[string]crdt.DataType{"pn-1": crdt.PNCounterType}
		versions := map[string]uint64{"pn-1": 3}

		err = store.Save(data, keyTypes, versions)
		require.NoError(t, err)

		loadedData, loadedTypes, loadedVersions, err := store.Load()
		require.NoError(t, err)
		assert.Equal(t, crdt.PNCounterType, loadedTypes["pn-1"])
		assert.Equal(t, uint64(3), loadedVersions["pn-1"])
		assert.Equal(t, int64(15), loadedData["pn-1"].(*crdt.PNCounter).Value())
	})

	t.Run("save and load round trip with ORSet", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)
		defer store.Close()

		orset := crdt.NewORSet[string]()
		orset = orset.Add("node-1", "a")
		orset = orset.Add("node-1", "b")

		data := map[string]crdt.ReplicatedData{"set-1": orset}
		keyTypes := map[string]crdt.DataType{"set-1": crdt.ORSetType}
		versions := map[string]uint64{"set-1": 2}

		err = store.Save(data, keyTypes, versions)
		require.NoError(t, err)

		loadedData, _, _, err := store.Load()
		require.NoError(t, err)
		loaded := loadedData["set-1"].(*crdt.ORSet[string])
		assert.True(t, loaded.Contains("a"))
		assert.True(t, loaded.Contains("b"))
	})

	t.Run("save and load round trip with Flag", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)
		defer store.Close()

		flag := crdt.NewFlag().Enable()
		data := map[string]crdt.ReplicatedData{"flag-1": flag}
		keyTypes := map[string]crdt.DataType{"flag-1": crdt.FlagType}
		versions := map[string]uint64{"flag-1": 1}

		err = store.Save(data, keyTypes, versions)
		require.NoError(t, err)

		loadedData, _, _, err := store.Load()
		require.NoError(t, err)
		assert.True(t, loadedData["flag-1"].(*crdt.Flag).Enabled())
	})

	t.Run("save and load round trip with MVRegister", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)
		defer store.Close()

		mv := crdt.NewMVRegister[string]()
		mv = mv.Set("node-1", "hello")

		data := map[string]crdt.ReplicatedData{"mv-1": mv}
		keyTypes := map[string]crdt.DataType{"mv-1": crdt.MVRegisterType}
		versions := map[string]uint64{"mv-1": 1}

		err = store.Save(data, keyTypes, versions)
		require.NoError(t, err)

		loadedData, _, _, err := store.Load()
		require.NoError(t, err)
		loaded := loadedData["mv-1"].(*crdt.MVRegister[string])
		assert.Equal(t, []string{"hello"}, loaded.Values())
	})

	t.Run("load from empty DB returns empty maps", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)
		defer store.Close()

		data, keyTypes, versions, err := store.Load()
		require.NoError(t, err)
		assert.Empty(t, data)
		assert.Empty(t, keyTypes)
		assert.Empty(t, versions)
	})

	t.Run("save overwrites previous data", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)
		defer store.Close()

		// first save
		data1 := map[string]crdt.ReplicatedData{
			"a": crdt.NewGCounter().Increment("n1", 1),
			"b": crdt.NewGCounter().Increment("n1", 2),
		}
		keyTypes1 := map[string]crdt.DataType{"a": crdt.GCounterType, "b": crdt.GCounterType}
		versions1 := map[string]uint64{"a": 1, "b": 1}
		err = store.Save(data1, keyTypes1, versions1)
		require.NoError(t, err)

		// second save with different keys
		data2 := map[string]crdt.ReplicatedData{
			"c": crdt.NewGCounter().Increment("n1", 3),
		}
		keyTypes2 := map[string]crdt.DataType{"c": crdt.GCounterType}
		versions2 := map[string]uint64{"c": 2}
		err = store.Save(data2, keyTypes2, versions2)
		require.NoError(t, err)

		// load should only have "c"
		loaded, _, _, err := store.Load()
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

		err = store.Save(nil, nil, nil)
		assert.ErrorIs(t, err, ErrStoreClosed)
	})

	t.Run("load after close returns error", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)

		err = store.Close()
		require.NoError(t, err)

		_, _, _, err = store.Load()
		assert.ErrorIs(t, err, ErrStoreClosed)
	})

	t.Run("multiple CRDT types in single snapshot", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewStore(dir)
		require.NoError(t, err)
		defer store.Close()

		data := map[string]crdt.ReplicatedData{
			"gc":   crdt.NewGCounter().Increment("n1", 5),
			"pn":   crdt.NewPNCounter().Increment("n1", 10),
			"flag": crdt.NewFlag().Enable(),
		}
		keyTypes := map[string]crdt.DataType{
			"gc":   crdt.GCounterType,
			"pn":   crdt.PNCounterType,
			"flag": crdt.FlagType,
		}
		versions := map[string]uint64{"gc": 1, "pn": 2, "flag": 3}

		err = store.Save(data, keyTypes, versions)
		require.NoError(t, err)

		loaded, loadedTypes, loadedVersions, err := store.Load()
		require.NoError(t, err)
		assert.Len(t, loaded, 3)
		assert.Equal(t, uint64(5), loaded["gc"].(*crdt.GCounter).Value())
		assert.Equal(t, int64(10), loaded["pn"].(*crdt.PNCounter).Value())
		assert.True(t, loaded["flag"].(*crdt.Flag).Enabled())
		assert.Equal(t, crdt.GCounterType, loadedTypes["gc"])
		assert.Equal(t, crdt.PNCounterType, loadedTypes["pn"])
		assert.Equal(t, crdt.FlagType, loadedTypes["flag"])
		assert.Equal(t, uint64(1), loadedVersions["gc"])
		assert.Equal(t, uint64(2), loadedVersions["pn"])
		assert.Equal(t, uint64(3), loadedVersions["flag"])
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
}
