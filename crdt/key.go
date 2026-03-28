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
)

// Key is a typed, serializable CRDT key.
//
// The generic parameter T binds the key to a specific CRDT type at compile time,
// providing type safety for Update, Get, Subscribe, and Changed messages.
// The key's ID is used as the TopicActor topic name for replication.
// The DataType is serialized in anti-entropy and coordination messages
// so that peers can validate type consistency.
//
// Keys are typically defined as package-level variables:
//
//	var requestCount = crdt.PNCounterKey("request-count")
//	var activeSessions = crdt.ORSetKey[string]("active-sessions")
type Key[T ReplicatedData] struct {
	id       string
	dataType DataType
}

// ID returns the key's string identifier.
// This is used as the TopicActor topic name and the internal store key.
func (k Key[T]) ID() string {
	return k.id
}

// Type returns the CRDT data type this key is associated with.
func (k Key[T]) Type() DataType {
	return k.dataType
}

// GCounterKey creates a typed key for a GCounter CRDT.
func GCounterKey(id string) Key[*GCounter] {
	return Key[*GCounter]{id: id, dataType: GCounterType}
}

// PNCounterKey creates a typed key for a PNCounter CRDT.
func PNCounterKey(id string) Key[*PNCounter] {
	return Key[*PNCounter]{id: id, dataType: PNCounterType}
}

// LWWRegisterKey creates a typed key for a LWWRegister CRDT.
func LWWRegisterKey[T any](id string) Key[*LWWRegister[T]] {
	return Key[*LWWRegister[T]]{id: id, dataType: LWWRegisterType}
}

// ORSetKey creates a typed key for an ORSet CRDT.
func ORSetKey[T comparable](id string) Key[*ORSet[T]] {
	return Key[*ORSet[T]]{id: id, dataType: ORSetType}
}
