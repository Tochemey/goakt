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

import (
	"encoding/binary"
	"fmt"
	"reflect"

	"github.com/fxamacker/cbor/v2"
	"github.com/zeebo/xxh3"
	"google.golang.org/protobuf/proto"
)

// Domain tags keep the hashes of different CRDT types, and of the different
// parts of one type, apart: an empty GCounter and an empty ORSet must not
// hash alike, nor may an ORSet entry be mistaken for an MVRegister entry.
const (
	hashTagGCounter uint64 = iota + 1
	hashTagPNCounter
	hashTagFlag
	hashTagLWWRegister
	hashTagMVRegister
	hashTagMVRegisterEntry
	hashTagORSet
	hashTagORSetEntry
	hashTagORMap
	hashTagORMapEntry
	hashTagNilValue
	hashTagStringValue
)

// StateHasher is optionally implemented by CRDTs that can summarize their
// replicated state in a 64-bit content hash. Every CRDT type of this package
// implements it.
//
// The Replicator uses the hash in two places: anti-entropy compares the
// hashes of two replicas of a key to decide whether they differ, and a merge
// that leaves the hash unchanged is known to have changed nothing.
//
// The hash is canonical. Two replicas that hold the same replicated state
// return the same hash whatever the order of the operations and merges that
// produced it, whatever the iteration order of their maps and whichever node
// computes it; two replicas that hold different states return different
// hashes except for a 64-bit collision. Bookkeeping that is not replicated,
// such as the pending delta, does not take part.
//
// The construction is the same for every type. Each independent entry of the
// state (a counter slot, a clock entry, an element with its dots, a map key
// with its value) is hashed on its own into 64 bits, the entry hashes are
// added modulo 2^64, and the sums are hashed once more together with a tag
// naming the type. Addition is commutative and associative, so the result
// cannot depend on the order in which the entries are visited.
type StateHasher interface {
	// StateHash returns the canonical content hash of the replicated state.
	StateHash() uint64
}

// canonicalValueEncoding encodes user values for hashing. Core deterministic
// CBOR sorts map keys and uses the shortest form of every number, so equal
// values encode to equal bytes on every node.
var canonicalValueEncoding, _ = cbor.CoreDetEncOptions().EncMode()

// deterministicProto encodes protobuf user values for hashing with map
// entries in sorted order.
var deterministicProto = proto.MarshalOptions{Deterministic: true}

// hashParts hashes a tag and a fixed list of 64-bit parts, in order. It is
// the final step of every StateHash and of every entry hash: the order of
// the parts is significant, which is what tells the increments of a
// PNCounter from its decrements.
func hashParts(tag uint64, parts ...uint64) uint64 {
	var stack [5 * 8]byte

	buf := stack[:0]
	for _, part := range parts {
		buf = binary.LittleEndian.AppendUint64(buf, part)
	}

	return xxh3.HashSeed(buf, tag)
}

// hashNodeCounters hashes a map from node ID to counter, which is the shape
// of a counter's slots and of a vector clock. Each entry is hashed on its
// own and the hashes are added, so the iteration order of the map does not
// matter. A zero counter is skipped: a slot at zero and a missing slot merge
// identically, so they are the same state.
func hashNodeCounters(counters map[string]uint64) uint64 {
	var sum uint64

	for nodeID, counter := range counters {
		if counter != 0 {
			sum += xxh3.HashStringSeed(nodeID, counter)
		}
	}

	return sum
}

// hashValue hashes a user value held by a CRDT: a register value, a set
// element or a map key.
//
// The bytes hashed are a canonical encoding of the value seeded with the
// name of its type, so the integer 1 and the string "1" hash differently. A
// protobuf message is encoded deterministically; any other value is encoded
// as core deterministic CBOR, which covers every value the Replicator can
// send to a peer. A pointer and the value it points to hash alike, because a
// value may reach a peer as either.
//
// Deterministic protobuf encoding is stable for one build of a message type
// but is not guaranteed across protobuf versions. Two nodes that disagree on
// the encoding of a value see different hashes for the same state; the only
// consequence is an anti-entropy exchange that changes nothing.
func hashValue(value any) uint64 {
	switch v := value.(type) {
	case nil:
		return hashTagNilValue
	case string:
		return xxh3.HashStringSeed(v, hashTagStringValue)
	case proto.Message:
		if encoded, err := deterministicProto.Marshal(v); err == nil {
			return xxh3.HashSeed(encoded, xxh3.HashString(string(v.ProtoReflect().Descriptor().FullName())))
		}
	}

	typ := reflect.TypeOf(value)
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	encoded, err := canonicalValueEncoding.Marshal(value)
	if err != nil {
		// a value CBOR cannot encode cannot be replicated either; its
		// printed form keeps the hash defined
		encoded = fmt.Appendf(nil, "%#v", value)
	}

	return xxh3.HashSeed(encoded, xxh3.HashString(typ.String()))
}

// hashLiveDots hashes the dots that keep one element in an ORSet. Only the
// highest dot of each node counts: a lower dot of the same node on the same
// element is redundant, Compact removes it without changing how the set
// merges, and two replicas that differ only in such dots hold the same
// state. The dot hashes are added, so their order in the slice does not
// matter.
func hashLiveDots(dots []dot) uint64 {
	var sum uint64

	for i, candidate := range dots {
		highest := true

		for j, other := range dots {
			if other.nodeID != candidate.nodeID {
				continue
			}

			// a higher dot of the same node wins; of two equal dots the first counts
			if other.counter > candidate.counter || (other.counter == candidate.counter && j < i) {
				highest = false
				break
			}
		}

		if highest {
			sum += xxh3.HashStringSeed(candidate.nodeID, candidate.counter)
		}
	}

	return sum
}
