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

package crdt

// DataType identifies the CRDT type for serialization and type validation.
type DataType int

const (
	// GCounterType identifies a GCounter CRDT.
	GCounterType DataType = iota
	// PNCounterType identifies a PNCounter CRDT.
	PNCounterType
	// LWWRegisterType identifies a LWWRegister CRDT.
	LWWRegisterType
	// ORSetType identifies an ORSet CRDT.
	ORSetType
	// ORMapType identifies an ORMap CRDT.
	ORMapType
	// FlagType identifies a Flag CRDT.
	FlagType
	// MVRegisterType identifies an MVRegister CRDT.
	MVRegisterType
)

// Key is a serializable CRDT key.
//
// The key's ID is carried inside each delta payload for routing; all deltas
// are published to a single shared topic (goakt.crdt.deltas) via TopicActor.
// The DataType is serialized in anti-entropy and coordination messages
// so that peers can validate type consistency.
//
// Keys are typically defined as package-level variables:
//
//	var requestCount = crdt.PNCounterKey("request-count")
//	var activeSessions = crdt.ORSetKey("active-sessions")
type Key struct {
	id       string
	dataType DataType
}

// ID returns the key's string identifier.
// This is used as the internal store key and is embedded in delta messages
// for routing; replication uses the shared goakt.crdt.deltas topic.
func (k Key) ID() string {
	return k.id
}

// Type returns the CRDT data type this key is associated with.
func (k Key) Type() DataType {
	return k.dataType
}

// GCounterKey creates a key for a GCounter CRDT.
func GCounterKey(id string) Key {
	return Key{id: id, dataType: GCounterType}
}

// PNCounterKey creates a key for a PNCounter CRDT.
func PNCounterKey(id string) Key {
	return Key{id: id, dataType: PNCounterType}
}

// LWWRegisterKey creates a key for a LWWRegister CRDT.
func LWWRegisterKey(id string) Key {
	return Key{id: id, dataType: LWWRegisterType}
}

// ORSetKey creates a key for an ORSet CRDT.
func ORSetKey(id string) Key {
	return Key{id: id, dataType: ORSetType}
}

// ORMapKey creates a key for an ORMap CRDT.
func ORMapKey(id string) Key {
	return Key{id: id, dataType: ORMapType}
}

// FlagKey creates a key for a Flag CRDT.
func FlagKey(id string) Key {
	return Key{id: id, dataType: FlagType}
}

// MVRegisterKey creates a key for an MVRegister CRDT.
func MVRegisterKey(id string) Key {
	return Key{id: id, dataType: MVRegisterType}
}
