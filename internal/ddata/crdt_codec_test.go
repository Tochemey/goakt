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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/crdt"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/remote"
)

func crdtTestSerializer() *CRDTValueSerializer {
	return NewCRDTValueSerializer()
}

func TestEncodeCRDTData_GCounter(t *testing.T) {
	s := crdtTestSerializer()
	gc := crdt.NewGCounter().Increment("node-1", 5).Increment("node-2", 3)

	pb, err := EncodeCRDT(gc, s)
	require.NoError(t, err)
	require.NotNil(t, pb.GetGCounter())

	decoded, err := DecodeCRDT(pb, s)
	require.NoError(t, err)
	assert.Equal(t, gc.Value(), decoded.(*crdt.GCounter).Value())
}

func TestEncodeCRDTData_PNCounter(t *testing.T) {
	s := crdtTestSerializer()
	pn := crdt.NewPNCounter().Increment("node-1", 10).Decrement("node-2", 3)

	pb, err := EncodeCRDT(pn, s)
	require.NoError(t, err)
	require.NotNil(t, pb.GetPnCounter())

	decoded, err := DecodeCRDT(pb, s)
	require.NoError(t, err)
	assert.Equal(t, pn.Value(), decoded.(*crdt.PNCounter).Value())
}

func TestEncodeCRDTData_Flag(t *testing.T) {
	s := crdtTestSerializer()

	t.Run("enabled", func(t *testing.T) {
		f := crdt.NewFlag().Enable()
		pb, err := EncodeCRDT(f, s)
		require.NoError(t, err)
		require.NotNil(t, pb.GetFlag())

		decoded, err := DecodeCRDT(pb, s)
		require.NoError(t, err)
		assert.True(t, decoded.(*crdt.Flag).Enabled())
	})

	t.Run("disabled", func(t *testing.T) {
		f := crdt.NewFlag()
		pb, err := EncodeCRDT(f, s)
		require.NoError(t, err)

		decoded, err := DecodeCRDT(pb, s)
		require.NoError(t, err)
		assert.False(t, decoded.(*crdt.Flag).Enabled())
	})
}

func TestEncodeCRDTData_LWWRegister(t *testing.T) {
	s := crdtTestSerializer()

	t.Run("string value", func(t *testing.T) {
		r := crdt.NewLWWRegister().Set("hello", time.Now(), "node-1")
		pb, err := EncodeCRDT(r, s)
		require.NoError(t, err)
		require.NotNil(t, pb.GetLwwRegister())

		decoded, err := DecodeCRDT(pb, s)
		require.NoError(t, err)
		reg := decoded.(*crdt.LWWRegister)
		assert.Equal(t, "hello", reg.Value())
		assert.Equal(t, r.Timestamp(), reg.Timestamp())
		assert.Equal(t, "node-1", reg.NodeID())
	})

	t.Run("int value", func(t *testing.T) {
		r := crdt.NewLWWRegister().Set(int64(42), time.Now(), "node-1")
		pb, err := EncodeCRDT(r, s)
		require.NoError(t, err)

		decoded, err := DecodeCRDT(pb, s)
		require.NoError(t, err)
		assert.Equal(t, int64(42), decoded.(*crdt.LWWRegister).Value())
	})

	t.Run("bool value", func(t *testing.T) {
		r := crdt.NewLWWRegister().Set(true, time.Now(), "node-1")
		pb, err := EncodeCRDT(r, s)
		require.NoError(t, err)

		decoded, err := DecodeCRDT(pb, s)
		require.NoError(t, err)
		assert.Equal(t, true, decoded.(*crdt.LWWRegister).Value())
	})
}

func TestEncodeCRDTData_ORSet(t *testing.T) {
	s := crdtTestSerializer()

	t.Run("string elements", func(t *testing.T) {
		os := crdt.NewORSet().Add("node-1", "a").Add("node-2", "b")
		pb, err := EncodeCRDT(os, s)
		require.NoError(t, err)
		require.NotNil(t, pb.GetOrSet())

		decoded, err := DecodeCRDT(pb, s)
		require.NoError(t, err)
		decodedSet := decoded.(*crdt.ORSet)
		assert.True(t, decodedSet.Contains("a"))
		assert.True(t, decodedSet.Contains("b"))
		assert.Equal(t, 2, decodedSet.Len())
	})

	t.Run("int elements", func(t *testing.T) {
		os := crdt.NewORSet().Add("node-1", int64(42)).Add("node-1", int64(99))
		pb, err := EncodeCRDT(os, s)
		require.NoError(t, err)

		decoded, err := DecodeCRDT(pb, s)
		require.NoError(t, err)
		decodedSet := decoded.(*crdt.ORSet)
		assert.True(t, decodedSet.Contains(int64(42)))
		assert.True(t, decodedSet.Contains(int64(99)))
		assert.Equal(t, 2, decodedSet.Len())
	})

	t.Run("empty set", func(t *testing.T) {
		os := crdt.NewORSet()
		pb, err := EncodeCRDT(os, s)
		require.NoError(t, err)

		decoded, err := DecodeCRDT(pb, s)
		require.NoError(t, err)
		assert.Equal(t, 0, decoded.(*crdt.ORSet).Len())
	})
}

func TestEncodeCRDTData_MVRegister(t *testing.T) {
	s := crdtTestSerializer()

	t.Run("single value", func(t *testing.T) {
		r := crdt.NewMVRegister().Set("node-1", "alice")
		pb, err := EncodeCRDT(r, s)
		require.NoError(t, err)
		require.NotNil(t, pb.GetMvRegister())

		decoded, err := DecodeCRDT(pb, s)
		require.NoError(t, err)
		values := decoded.(*crdt.MVRegister).Values()
		require.Len(t, values, 1)
		assert.Equal(t, "alice", values[0])
	})

	t.Run("concurrent values", func(t *testing.T) {
		r1 := crdt.NewMVRegister().Set("node-1", "alice")
		r2 := crdt.NewMVRegister().Set("node-2", "bob")
		merged := r1.Merge(r2).(*crdt.MVRegister)

		pb, err := EncodeCRDT(merged, s)
		require.NoError(t, err)

		decoded, err := DecodeCRDT(pb, s)
		require.NoError(t, err)
		values := decoded.(*crdt.MVRegister).Values()
		require.Len(t, values, 2)
		assert.Contains(t, values, "alice")
		assert.Contains(t, values, "bob")
	})
}

func TestEncodeCRDTData_ORMap(t *testing.T) {
	s := crdtTestSerializer()

	t.Run("string keys with GCounter values", func(t *testing.T) {
		m := crdt.NewORMap()
		m = m.Set("node-1", "a", crdt.NewGCounter().Increment("node-1", 5))
		m = m.Set("node-2", "b", crdt.NewGCounter().Increment("node-2", 3))

		pb, err := EncodeCRDT(m, s)
		require.NoError(t, err)
		require.NotNil(t, pb.GetOrMap())

		decoded, err := DecodeCRDT(pb, s)
		require.NoError(t, err)
		decodedMap := decoded.(*crdt.ORMap)
		assert.Equal(t, 2, decodedMap.Len())

		va, ok := decodedMap.Get("a")
		require.True(t, ok)
		assert.Equal(t, uint64(5), va.(*crdt.GCounter).Value())

		vb, ok := decodedMap.Get("b")
		require.True(t, ok)
		assert.Equal(t, uint64(3), vb.(*crdt.GCounter).Value())
	})

	t.Run("empty map", func(t *testing.T) {
		m := crdt.NewORMap()
		pb, err := EncodeCRDT(m, s)
		require.NoError(t, err)

		decoded, err := DecodeCRDT(pb, s)
		require.NoError(t, err)
		assert.Equal(t, 0, decoded.(*crdt.ORMap).Len())
	})

	t.Run("nil key set in proto", func(t *testing.T) {
		pb := &internalpb.CRDTData{
			Type: &internalpb.CRDTData_OrMap{
				OrMap: &internalpb.ORMapData{
					KeySet: nil,
				},
			},
		}
		decoded, err := DecodeCRDT(pb, s)
		require.NoError(t, err)
		assert.Equal(t, 0, decoded.(*crdt.ORMap).Len())
	})
}

func TestDecodeCRDTData_NilInput(t *testing.T) {
	s := crdtTestSerializer()
	_, err := DecodeCRDT(nil, s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil CRDTData")
}

func TestDecodeCRDTData_UnsupportedOneofType(t *testing.T) {
	s := crdtTestSerializer()
	pb := &internalpb.CRDTData{}
	_, err := DecodeCRDT(pb, s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported CRDTData type")
}

func TestEncodeCRDTData_UnsupportedType(t *testing.T) {
	s := crdtTestSerializer()
	_, err := EncodeCRDT(nil, s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported CRDT type")
}

func TestDecodeCRDTData_LWWRegisterBadValue(t *testing.T) {
	s := crdtTestSerializer()
	pb := &internalpb.CRDTData{
		Type: &internalpb.CRDTData_LwwRegister{
			LwwRegister: &internalpb.LWWRegisterData{
				Value: []byte{0xff, 0xfe},
			},
		},
	}
	_, err := DecodeCRDT(pb, s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode LWWRegister value")
}

func TestDecodeCRDTData_ORSetBadElement(t *testing.T) {
	s := crdtTestSerializer()
	pb := &internalpb.CRDTData{
		Type: &internalpb.CRDTData_OrSet{
			OrSet: &internalpb.ORSetData{
				Entries: []*internalpb.ORSetData_ORSetEntry{
					{Element: []byte{0xff, 0xfe}},
				},
			},
		},
	}
	_, err := DecodeCRDT(pb, s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode ORSet element")
}

func TestDecodeCRDTData_MVRegisterBadEntry(t *testing.T) {
	s := crdtTestSerializer()
	pb := &internalpb.CRDTData{
		Type: &internalpb.CRDTData_MvRegister{
			MvRegister: &internalpb.MVRegisterData{
				Entries: []*internalpb.MVRegisterData_MVRegisterEntry{
					{Value: []byte{0xff, 0xfe}, NodeId: "n1", Counter: 1},
				},
			},
		},
	}
	_, err := DecodeCRDT(pb, s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode MVRegister entry")
}

func TestDecodeCRDTData_ORMapBadKey(t *testing.T) {
	s := crdtTestSerializer()
	gcData := &internalpb.CRDTData{
		Type: &internalpb.CRDTData_GCounter{
			GCounter: &internalpb.GCounterData{State: map[string]uint64{"n1": 1}},
		},
	}
	pb := &internalpb.CRDTData{
		Type: &internalpb.CRDTData_OrMap{
			OrMap: &internalpb.ORMapData{
				Entries: []*internalpb.ORMapData_ORMapEntry{
					{Key: []byte{0xff, 0xfe}, Value: gcData},
				},
			},
		},
	}
	_, err := DecodeCRDT(pb, s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode ORMap key")
}

func TestDecodeCRDTData_ORMapBadValue(t *testing.T) {
	s := crdtTestSerializer()
	keyBytes, err := s.Serialize("mykey")
	require.NoError(t, err)

	pb := &internalpb.CRDTData{
		Type: &internalpb.CRDTData_OrMap{
			OrMap: &internalpb.ORMapData{
				Entries: []*internalpb.ORMapData_ORMapEntry{
					{Key: keyBytes, Value: nil},
				},
			},
		},
	}
	_, err = DecodeCRDT(pb, s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode ORMap value")
}

func TestDecodeCRDTData_ORMapBadKeySet(t *testing.T) {
	s := crdtTestSerializer()
	pb := &internalpb.CRDTData{
		Type: &internalpb.CRDTData_OrMap{
			OrMap: &internalpb.ORMapData{
				KeySet: &internalpb.ORSetData{
					Entries: []*internalpb.ORSetData_ORSetEntry{
						{Element: []byte{0xff, 0xfe}},
					},
				},
			},
		},
	}
	_, err := DecodeCRDT(pb, s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode ORMap key set")
}

func TestEncodeCRDTData_LWWRegisterSerializeError(t *testing.T) {
	s := crdtTestSerializer()
	r := crdt.NewLWWRegister()
	_, err := EncodeCRDT(r, s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encode LWWRegister value")
}

func TestEncodeCRDTData_MVRegisterSerializeError(t *testing.T) {
	s := &failingSerializer{}
	r := crdt.NewMVRegister().Set("node-1", "value")
	_, err := EncodeCRDT(r, s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encode MVRegister entry")
}

func TestEncodeCRDTData_ORSetSerializeError(t *testing.T) {
	s := &failingSerializer{}
	os := crdt.NewORSet().Add("node-1", "a")
	_, err := EncodeCRDT(os, s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encode ORSet element")
}

func TestEncodeCRDTData_ORMapKeySerializeError(t *testing.T) {
	s := &failingSerializer{}
	m := crdt.NewORMap()
	m = m.Set("node-1", "key", crdt.NewGCounter().Increment("node-1", 1))
	_, err := EncodeCRDT(m, s)
	require.Error(t, err)
	assert.Error(t, err)
}

func TestEncodeCRDTData_ORMapValueEncodeError(t *testing.T) {
	s := crdtTestSerializer()
	m := crdt.NewORMap()
	m = m.Set("node-1", "key", crdt.NewLWWRegister())
	_, err := EncodeCRDT(m, s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encode ORMap value")
}

func TestEncodeCRDTData_ORMapKeySerializeErrorAfterKeySet(t *testing.T) {
	s := &failAfterNSerializer{allowCount: 1, real: crdtTestSerializer()}
	m := crdt.NewORMap()
	m = m.Set("node-1", "key", crdt.NewGCounter().Increment("node-1", 1))
	_, err := EncodeCRDT(m, s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to encode ORMap key")
}

// failingSerializer always returns errors.
type failingSerializer struct{}

func (f *failingSerializer) Serialize(any) ([]byte, error) {
	return nil, assert.AnError
}

func (f *failingSerializer) Deserialize([]byte) (any, error) {
	return nil, assert.AnError
}

var _ remote.Serializer = (*failingSerializer)(nil)

// failAfterNSerializer delegates to a real serializer for the first N calls,
// then fails all subsequent calls.
type failAfterNSerializer struct {
	allowCount int
	callCount  int
	real       *CRDTValueSerializer
}

func (f *failAfterNSerializer) Serialize(message any) ([]byte, error) {
	f.callCount++
	if f.callCount > f.allowCount {
		return nil, assert.AnError
	}
	return f.real.Serialize(message)
}

func (f *failAfterNSerializer) Deserialize(data []byte) (any, error) {
	return f.real.Deserialize(data)
}

var _ remote.Serializer = (*failAfterNSerializer)(nil)
