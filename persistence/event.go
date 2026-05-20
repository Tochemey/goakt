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

// Package persistence defines the store interfaces and data types used by
// GoAkt's event-sourced actor infrastructure. Production implementations
// (e.g. PostgreSQL, Cassandra) live in separate packages; the in-memory
// [MemoryEventsStore] and [MemorySnapshotStore] provided here are intended
// for tests only.
package persistence

import "time"

// PersistedEvent is the durable record of a single domain event written to the
// events store by an [EventSourcedActor].
//
// Fields are populated by the actor and must be treated as read-only by store
// implementations; only the store's own persistence layer may add metadata.
type PersistedEvent struct {
	// PersistenceID is the unique identifier of the actor whose event stream
	// this record belongs to. It equals the actor's name at spawn time.
	PersistenceID string

	// SequenceNumber is the monotonically increasing, 1-based ordinal of this
	// event within the actor's event stream. Sequence numbers must not have gaps.
	SequenceNumber uint64

	// Timestamp is the wall-clock time at which the event was serialized and
	// written. It is set by the actor and should be stored verbatim.
	Timestamp time.Time

	// Payload holds the serialized event bytes produced by the serializer
	// identified by SerializerKind. The format is the same self-describing
	// length-prefixed frame used by GoAkt's remote layer.
	Payload []byte

	// Manifest is the lowercased, fully-qualified Go type name of the event
	// (e.g. "myapp.orderplacedevent"). It is stored for auditing and schema
	// evolution; the actor uses SerializerKind plus the type name embedded in
	// the Payload frame for deserialization.
	Manifest string

	// SerializerKind records which serializer produced Payload so that the
	// correct deserializer can be selected on recovery. See [SerializerKind].
	SerializerKind SerializerKind

	// WriterActorID is the persistence ID of the actor that wrote this event.
	// For event-sourced actors it equals PersistenceID; it is stored to support
	// future multi-writer or delegation scenarios.
	WriterActorID string
}
