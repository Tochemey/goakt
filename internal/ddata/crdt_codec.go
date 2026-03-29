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
)

// EncodeCRDT converts a crdt.ReplicatedData to its protobuf representation.
// The serializer is used to encode the any-typed values in CRDT types
// (LWWRegister, ORSet, MVRegister, ORMap) into bytes for wire transmission.
func EncodeCRDT(data crdt.ReplicatedData, serializer remote.Serializer) (*internalpb.CRDTData, error) {
	switch v := data.(type) {
	case *crdt.GCounter:
		return &internalpb.CRDTData{
			Type: &internalpb.CRDTData_GCounter{
				GCounter: encodeGCounter(v),
			},
		}, nil
	case *crdt.PNCounter:
		return &internalpb.CRDTData{
			Type: &internalpb.CRDTData_PnCounter{
				PnCounter: encodePNCounter(v),
			},
		}, nil
	case *crdt.Flag:
		return &internalpb.CRDTData{
			Type: &internalpb.CRDTData_Flag{
				Flag: encodeFlag(v),
			},
		}, nil
	case *crdt.LWWRegister:
		lwwData, err := encodeLWWRegister(v, serializer)
		if err != nil {
			return nil, err
		}
		return &internalpb.CRDTData{
			Type: &internalpb.CRDTData_LwwRegister{
				LwwRegister: lwwData,
			},
		}, nil
	case *crdt.ORSet:
		orSetData, err := encodeORSet(v, serializer)
		if err != nil {
			return nil, err
		}
		return &internalpb.CRDTData{
			Type: &internalpb.CRDTData_OrSet{
				OrSet: orSetData,
			},
		}, nil
	case *crdt.MVRegister:
		mvData, err := encodeMVRegister(v, serializer)
		if err != nil {
			return nil, err
		}
		return &internalpb.CRDTData{
			Type: &internalpb.CRDTData_MvRegister{
				MvRegister: mvData,
			},
		}, nil
	case *crdt.ORMap:
		orMapData, err := encodeORMap(v, serializer)
		if err != nil {
			return nil, err
		}
		return &internalpb.CRDTData{
			Type: &internalpb.CRDTData_OrMap{
				OrMap: orMapData,
			},
		}, nil
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
	switch v := pb.Type.(type) {
	case *internalpb.CRDTData_GCounter:
		return decodeGCounter(v.GCounter), nil
	case *internalpb.CRDTData_PnCounter:
		return decodePNCounter(v.PnCounter), nil
	case *internalpb.CRDTData_Flag:
		return decodeFlag(v.Flag), nil
	case *internalpb.CRDTData_LwwRegister:
		return decodeLWWRegister(v.LwwRegister, serializer)
	case *internalpb.CRDTData_OrSet:
		return decodeORSet(v.OrSet, serializer)
	case *internalpb.CRDTData_MvRegister:
		return decodeMVRegister(v.MvRegister, serializer)
	case *internalpb.CRDTData_OrMap:
		return decodeORMap(v.OrMap, serializer)
	default:
		return nil, fmt.Errorf("unsupported CRDTData type: %T", pb.Type)
	}
}

func encodeGCounter(c *crdt.GCounter) *internalpb.GCounterData {
	return &internalpb.GCounterData{
		State: c.State(),
	}
}

func decodeGCounter(pb *internalpb.GCounterData) *crdt.GCounter {
	return crdt.GCounterFromState(pb.GetState())
}

func encodePNCounter(c *crdt.PNCounter) *internalpb.PNCounterData {
	inc, dec := c.State()
	return &internalpb.PNCounterData{
		Increments: &internalpb.GCounterData{State: inc},
		Decrements: &internalpb.GCounterData{State: dec},
	}
}

func decodePNCounter(pb *internalpb.PNCounterData) *crdt.PNCounter {
	return crdt.PNCounterFromState(
		pb.GetIncrements().GetState(),
		pb.GetDecrements().GetState(),
	)
}

func encodeFlag(f *crdt.Flag) *internalpb.FlagData {
	return &internalpb.FlagData{
		Enabled: f.Enabled(),
	}
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
	return &internalpb.LWWRegisterData{
		Value:          val,
		TimestampNanos: r.Timestamp(),
		NodeId:         r.NodeID(),
	}, nil
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
		pbEntries = append(pbEntries, &internalpb.MVRegisterData_MVRegisterEntry{
			Value:   val,
			NodeId:  e.Dot.NodeID,
			Counter: e.Dot.Counter,
		})
	}
	return &internalpb.MVRegisterData{
		Entries: pbEntries,
		Clock:   clock,
	}, nil
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
		pbEntries = append(pbEntries, &internalpb.ORMapData_ORMapEntry{
			Key:   keyBytes,
			Value: valData,
		})
	}

	return &internalpb.ORMapData{
		Entries: pbEntries,
		KeySet:  keySet,
	}, nil
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
			pbDots[i] = &internalpb.ORSetData_ORSetDot{
				NodeId:  d.NodeID,
				Counter: d.Counter,
			}
		}
		elem, err := serializer.Serialize(e.Element)
		if err != nil {
			return nil, fmt.Errorf("encode ORSet element: %w", err)
		}
		pbEntries = append(pbEntries, &internalpb.ORSetData_ORSetEntry{
			Element: elem,
			Dots:    pbDots,
		})
	}
	return &internalpb.ORSetData{
		Entries: pbEntries,
		Clock:   clock,
	}, nil
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
