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
	"fmt"

	"github.com/tochemey/goakt/v4/crdt"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/remote"
	"google.golang.org/protobuf/proto"
)

// EncodeCRDT converts a crdt.ReplicatedData to its protobuf representation.
// The serializer is used to encode the any-typed values in CRDT types
// (LWWRegister, ORSet, MVRegister, ORMap) into bytes for wire transmission.
func EncodeCRDT(data crdt.ReplicatedData, serializer remote.Serializer) (*internalpb.CRDTData, error) {
	switch v := data.(type) {
	case *crdt.GCounter:
		cRDTData := &internalpb.CRDTData{}
		cRDTData.SetGCounter(proto.ValueOrDefault(encodeGCounter(v)))
		return cRDTData, nil
	case *crdt.PNCounter:
		cRDTData := &internalpb.CRDTData{}
		cRDTData.SetPnCounter(proto.ValueOrDefault(encodePNCounter(v)))
		return cRDTData, nil
	case *crdt.Flag:
		cRDTData := &internalpb.CRDTData{}
		cRDTData.SetFlag(proto.ValueOrDefault(encodeFlag(v)))
		return cRDTData, nil
	case *crdt.LWWRegister:
		lwwData, err := encodeLWWRegister(v, serializer)
		if err != nil {
			return nil, err
		}
		cRDTData := &internalpb.CRDTData{}
		cRDTData.SetLwwRegister(proto.ValueOrDefault(lwwData))
		return cRDTData, nil
	case *crdt.ORSet:
		orSetData, err := encodeORSet(v, serializer)
		if err != nil {
			return nil, err
		}
		cRDTData := &internalpb.CRDTData{}
		cRDTData.SetOrSet(proto.ValueOrDefault(orSetData))
		return cRDTData, nil
	case *crdt.MVRegister:
		mvData, err := encodeMVRegister(v, serializer)
		if err != nil {
			return nil, err
		}
		cRDTData := &internalpb.CRDTData{}
		cRDTData.SetMvRegister(proto.ValueOrDefault(mvData))
		return cRDTData, nil
	case *crdt.ORMap:
		orMapData, err := encodeORMap(v, serializer)
		if err != nil {
			return nil, err
		}
		cRDTData := &internalpb.CRDTData{}
		cRDTData.SetOrMap(proto.ValueOrDefault(orMapData))
		return cRDTData, nil
	default:
		return nil, fmt.Errorf("unsupported CRDT type: %T", data)
	}
}

// DecodeCRDT converts a protobuf CRDTData back to a crdt.ReplicatedData.
// The serializer is used to decode the bytes-typed values back into Go values.
func DecodeCRDT(pb *internalpb.CRDTData, serializer remote.Serializer) (crdt.ReplicatedData, error) {
	if pb == nil {
		return nil, fmt.Errorf("nil CRDTData")
	}
	switch pb.WhichType() {
	case internalpb.CRDTData_GCounter_case:
		return decodeGCounter(pb.GetGCounter()), nil
	case internalpb.CRDTData_PnCounter_case:
		return decodePNCounter(pb.GetPnCounter()), nil
	case internalpb.CRDTData_Flag_case:
		return decodeFlag(pb.GetFlag()), nil
	case internalpb.CRDTData_LwwRegister_case:
		return decodeLWWRegister(pb.GetLwwRegister(), serializer)
	case internalpb.CRDTData_OrSet_case:
		return decodeORSet(pb.GetOrSet(), serializer)
	case internalpb.CRDTData_MvRegister_case:
		return decodeMVRegister(pb.GetMvRegister(), serializer)
	case internalpb.CRDTData_OrMap_case:
		return decodeORMap(pb.GetOrMap(), serializer)
	default:
		return nil, fmt.Errorf("unsupported CRDTData type: %v", pb.WhichType())
	}
}

func encodeGCounter(c *crdt.GCounter) *internalpb.GCounterData {
	gcd := &internalpb.GCounterData{}
	gcd.SetState(c.State())
	return gcd
}

func decodeGCounter(pb *internalpb.GCounterData) *crdt.GCounter {
	return crdt.GCounterFromState(pb.GetState())
}

func encodePNCounter(c *crdt.PNCounter) *internalpb.PNCounterData {
	inc, dec := c.State()
	gcd := &internalpb.GCounterData{}
	gcd.SetState(inc)
	gcd2 := &internalpb.GCounterData{}
	gcd2.SetState(dec)
	pncd := &internalpb.PNCounterData{}
	pncd.SetIncrements(gcd)
	pncd.SetDecrements(gcd2)
	return pncd
}

func decodePNCounter(pb *internalpb.PNCounterData) *crdt.PNCounter {
	return crdt.PNCounterFromState(
		pb.GetIncrements().GetState(),
		pb.GetDecrements().GetState(),
	)
}

func encodeFlag(f *crdt.Flag) *internalpb.FlagData {
	flagData := &internalpb.FlagData{}
	flagData.SetEnabled(f.Enabled())
	return flagData
}

func decodeFlag(pb *internalpb.FlagData) *crdt.Flag {
	if pb.GetEnabled() {
		return crdt.NewFlag().Enable()
	}
	return crdt.NewFlag()
}

func encodeLWWRegister(r *crdt.LWWRegister, serializer remote.Serializer) (*internalpb.LWWRegisterData, error) {
	val, err := serializer.Serialize(r.Value())
	if err != nil {
		return nil, fmt.Errorf("encode LWWRegister value: %w", err)
	}
	lwwrd := &internalpb.LWWRegisterData{}
	lwwrd.SetValue(val)
	lwwrd.SetTimestampNanos(r.Timestamp())
	lwwrd.SetNodeId(r.NodeID())
	return lwwrd, nil
}

func decodeLWWRegister(pb *internalpb.LWWRegisterData, serializer remote.Serializer) (*crdt.LWWRegister, error) {
	val, err := serializer.Deserialize(pb.GetValue())
	if err != nil {
		return nil, fmt.Errorf("decode LWWRegister value: %w", err)
	}
	return crdt.LWWRegisterFromState(
		val,
		pb.GetTimestampNanos(),
		pb.GetNodeId(),
	), nil
}

func encodeORSet(s *crdt.ORSet, serializer remote.Serializer) (*internalpb.ORSetData, error) {
	entries, clock := s.RawState()
	return encodeORSetEntries(entries, clock, serializer)
}

func decodeORSet(pb *internalpb.ORSetData, serializer remote.Serializer) (*crdt.ORSet, error) {
	entries, err := decodeORSetEntries(pb, serializer)
	if err != nil {
		return nil, err
	}
	return crdt.ORSetFromRawState(entries, pb.GetClock()), nil
}

func encodeMVRegister(r *crdt.MVRegister, serializer remote.Serializer) (*internalpb.MVRegisterData, error) {
	entries, clock := r.RawState()
	pbEntries := make([]*internalpb.MVRegisterData_MVRegisterEntry, 0, len(entries))
	for _, e := range entries {
		val, err := serializer.Serialize(e.Value)
		if err != nil {
			return nil, fmt.Errorf("encode MVRegister entry: %w", err)
		}
		mm := &internalpb.MVRegisterData_MVRegisterEntry{}
		mm.SetValue(val)
		mm.SetNodeId(e.Dot.NodeID)
		mm.SetCounter(e.Dot.Counter)
		pbEntries = append(pbEntries, mm)
	}
	mvrd := &internalpb.MVRegisterData{}
	mvrd.SetEntries(pbEntries)
	mvrd.SetClock(clock)
	return mvrd, nil
}

func decodeMVRegister(pb *internalpb.MVRegisterData, serializer remote.Serializer) (*crdt.MVRegister, error) {
	entries := make([]crdt.MVEntry, 0, len(pb.GetEntries()))
	for _, e := range pb.GetEntries() {
		val, err := serializer.Deserialize(e.GetValue())
		if err != nil {
			return nil, fmt.Errorf("decode MVRegister entry: %w", err)
		}
		entries = append(entries, crdt.MVEntry{
			Value: val,
			Dot: crdt.Dot{
				NodeID:  e.GetNodeId(),
				Counter: e.GetCounter(),
			},
		})
	}
	return crdt.MVRegisterFromRawState(entries, pb.GetClock()), nil
}

func encodeORMap(m *crdt.ORMap, serializer remote.Serializer) (*internalpb.ORMapData, error) {
	state := m.RawState()

	keySet, err := encodeORSetEntries(state.KeyEntries, state.KeyClock, serializer)
	if err != nil {
		return nil, fmt.Errorf("failed to encode ORMap key set: %w", err)
	}

	pbEntries := make([]*internalpb.ORMapData_ORMapEntry, 0, len(state.Values))
	for k, v := range state.Values {
		valData, err := EncodeCRDT(v, serializer)
		if err != nil {
			return nil, fmt.Errorf("failed to encode ORMap value for key=%v: %w", k, err)
		}
		keyBytes, err := serializer.Serialize(k)
		if err != nil {
			return nil, fmt.Errorf("failed to encode ORMap key=%v: %w", k, err)
		}
		oo := &internalpb.ORMapData_ORMapEntry{}
		oo.SetKey(keyBytes)
		oo.SetValue(valData)
		pbEntries = append(pbEntries, oo)
	}

	oRMapData := &internalpb.ORMapData{}
	oRMapData.SetEntries(pbEntries)
	oRMapData.SetKeySet(keySet)
	return oRMapData, nil
}

func decodeORMap(pb *internalpb.ORMapData, serializer remote.Serializer) (*crdt.ORMap, error) {
	var keyEntries []crdt.Entry
	var keyClock map[string]uint64
	if ks := pb.GetKeySet(); ks != nil {
		var err error
		keyEntries, err = decodeORSetEntries(ks, serializer)
		if err != nil {
			return nil, fmt.Errorf("failed to decode ORMap key set: %w", err)
		}
		keyClock = ks.GetClock()
	}

	values := make(map[any]crdt.ReplicatedData, len(pb.GetEntries()))
	for _, e := range pb.GetEntries() {
		key, err := serializer.Deserialize(e.GetKey())
		if err != nil {
			return nil, fmt.Errorf("failed to decode ORMap key: %w", err)
		}
		data, err := DecodeCRDT(e.GetValue(), serializer)
		if err != nil {
			return nil, fmt.Errorf("failed to decode ORMap value for key=%v: %w", key, err)
		}
		values[key] = data
	}

	rawState := crdt.ORMapRawState{
		KeyEntries: keyEntries,
		KeyClock:   keyClock,
		Values:     values,
	}
	return crdt.ORMapFromRawState(rawState), nil
}

func encodeORSetEntries(entries []crdt.Entry, clock map[string]uint64, serializer remote.Serializer) (*internalpb.ORSetData, error) {
	pbEntries := make([]*internalpb.ORSetData_ORSetEntry, 0, len(entries))
	for _, e := range entries {
		pbDots := make([]*internalpb.ORSetData_ORSetDot, len(e.Dots))
		for i, d := range e.Dots {
			oo := &internalpb.ORSetData_ORSetDot{}
			oo.SetNodeId(d.NodeID)
			oo.SetCounter(d.Counter)
			pbDots[i] = oo
		}
		elem, err := serializer.Serialize(e.Element)
		if err != nil {
			return nil, fmt.Errorf("encode ORSet element: %w", err)
		}
		oo := &internalpb.ORSetData_ORSetEntry{}
		oo.SetElement(elem)
		oo.SetDots(pbDots)
		pbEntries = append(pbEntries, oo)
	}
	oRSetData := &internalpb.ORSetData{}
	oRSetData.SetEntries(pbEntries)
	oRSetData.SetClock(clock)
	return oRSetData, nil
}

func decodeORSetEntries(pb *internalpb.ORSetData, serializer remote.Serializer) ([]crdt.Entry, error) {
	entries := make([]crdt.Entry, 0, len(pb.GetEntries()))
	for _, e := range pb.GetEntries() {
		dots := make([]crdt.Dot, len(e.GetDots()))
		for i, d := range e.GetDots() {
			dots[i] = crdt.Dot{
				NodeID:  d.GetNodeId(),
				Counter: d.GetCounter(),
			}
		}
		elem, err := serializer.Deserialize(e.GetElement())
		if err != nil {
			return nil, fmt.Errorf("decode ORSet element: %w", err)
		}
		entries = append(entries, crdt.Entry{
			Element: elem,
			Dots:    dots,
		})
	}
	return entries, nil
}
