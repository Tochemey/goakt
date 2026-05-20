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

package persistence

import "time"

// PersistedSnapshot is the durable record of an actor's state at a given
// sequence number. Snapshots are written by an [EventSourcedActor] to
// accelerate recovery: instead of replaying the full event stream from
// sequence 1, recovery loads the latest snapshot and replays only the events
// that follow it.
//
// Fields are populated by the actor and must be treated as read-only by store
// implementations.
type PersistedSnapshot struct {
	// PersistenceID is the unique identifier of the actor whose state this
	// snapshot captures. It equals the actor's name at spawn time.
	PersistenceID string

	// SequenceNumber is the sequence number of the last event that was applied
	// before this snapshot was taken. Recovery replays events with sequence
	// numbers strictly greater than this value.
	SequenceNumber uint64

	// Timestamp is the wall-clock time at which the snapshot was serialized
	// and written. It is set by the actor and should be stored verbatim.
	Timestamp time.Time

	// Payload holds the serialized state bytes produced by the serializer
	// identified by SerializerKind. The format is the same self-describing
	// length-prefixed frame used by GoAkt's remote layer.
	Payload []byte

	// Manifest is the lowercased, fully-qualified Go type name of the state
	// value (e.g. "myapp.cartstate"). It is stored for auditing and schema
	// evolution.
	Manifest string

	// SerializerKind records which serializer produced Payload so that the
	// correct deserializer can be selected on recovery. See [SerializerKind].
	SerializerKind SerializerKind

	// WriterActorID is the persistence ID of the actor that wrote this
	// snapshot. For event-sourced actors it equals PersistenceID.
	WriterActorID string
}
