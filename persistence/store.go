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

import (
	"context"

	"github.com/tochemey/goakt/v4/extension"
)

// SerializerKind identifies which serializer encoded a Payload field.
// It is stored alongside the payload so that the correct deserializer can be
// selected on recovery without inspecting the binary frame itself.
type SerializerKind uint8

const (
	// CBORSerializerKind indicates the payload was encoded with GoAkt's CBOR
	// serializer. The concrete Go type must be registered in the global types
	// registry via [remote.WithSerializers] or [remote.WithClientSerializers].
	CBORSerializerKind SerializerKind = 0

	// ProtobufSerializerKind indicates the payload was encoded with GoAkt's
	// protobuf serializer. The concrete proto message type must be importable
	// so that its descriptor is registered in [protoregistry.GlobalTypes].
	ProtobufSerializerKind SerializerKind = 1

	// EventsStoreExtensionID is the well-known extension ID under which an
	// [EventsStore] is registered with the actor system. Event-sourced actors
	// look up their events store using this identifier.
	EventsStoreExtensionID = "goakt-events-store"

	// SnapshotStoreExtensionID is the well-known extension ID under which a
	// [SnapshotStore] is registered with the actor system. Event-sourced actors
	// look up their snapshot store using this identifier.
	SnapshotStoreExtensionID = "goakt-snapshot-store"
)

// EventsStore is the contract for reading and writing persisted events for
// event-sourced actors. Implementations must be safe for concurrent use.
//
// EventsStore is a pure persistence contract: it has no dependency on
// goakt's extension system. To register an implementation with the actor
// system, wrap it with [NewEventsStoreExtension] and pass the result to
// [WithExtensions].
//
// GoAkt ships an in-memory implementation ([MemoryEventsStore]) suitable for
// tests. Production use cases require a durable backend such as PostgreSQL
// or Cassandra.
type EventsStore interface {
	// WriteEvents atomically appends events to the store. Implementations
	// should write all events or none (transactional where possible).
	WriteEvents(ctx context.Context, events []*PersistedEvent) error

	// ReplayEvents returns events for persistenceID whose sequence numbers
	// fall in the closed interval [fromSequenceNumber, toSequenceNumber].
	// Pass toSequenceNumber=0 to indicate no upper bound.
	// Pass limit=0 to return all matching events without a page cap.
	// Results must be returned in ascending sequence-number order.
	ReplayEvents(ctx context.Context, persistenceID string, fromSequenceNumber, toSequenceNumber uint64, limit uint64) ([]*PersistedEvent, error)

	// GetLatestEvent returns the most recently written event for persistenceID,
	// or nil if no events exist for that ID.
	GetLatestEvent(ctx context.Context, persistenceID string) (*PersistedEvent, error)

	// DeleteEvents removes all events whose sequence number is less than or
	// equal to toSequenceNumber. This is used for event-log compaction after a
	// snapshot is taken.
	DeleteEvents(ctx context.Context, persistenceID string, toSequenceNumber uint64) error
}

// SnapshotStore is the contract for reading and writing actor state snapshots.
// Implementations must be safe for concurrent use.
//
// SnapshotStore is a pure persistence contract: it has no dependency on
// goakt's extension system. To register an implementation with the actor
// system, wrap it with [NewSnapshotStoreExtension] and pass the result to
// [WithExtensions].
//
// GoAkt ships an in-memory implementation ([MemorySnapshotStore]) suitable
// for tests. Production use cases require a durable backend.
type SnapshotStore interface {
	// WriteSnapshot persists snapshot, overwriting any previously stored
	// snapshot for the same persistence ID.
	WriteSnapshot(ctx context.Context, snapshot *PersistedSnapshot) error

	// GetLatestSnapshot returns the stored snapshot for persistenceID, or nil
	// if no snapshot has been written for that ID.
	GetLatestSnapshot(ctx context.Context, persistenceID string) (*PersistedSnapshot, error)

	// DeleteSnapshots removes the snapshot for persistenceID if its sequence
	// number is less than or equal to toSequenceNumber.
	DeleteSnapshots(ctx context.Context, persistenceID string, toSequenceNumber uint64) error
}

// EventsStoreExtension adapts an [EventsStore] to [extension.Extension] so it
// can be registered with the actor system via [WithExtensions]. The wrapped
// store stays free of any goakt-specific identifiers.
type EventsStoreExtension struct {
	underlying EventsStore
}

var _ extension.Extension = (*EventsStoreExtension)(nil)

// NewEventsStoreExtension wraps store so it can be registered as an extension.
func NewEventsStoreExtension(store EventsStore) *EventsStoreExtension {
	return &EventsStoreExtension{underlying: store}
}

// ID returns [EventsStoreExtensionID].
func (x *EventsStoreExtension) ID() string { return EventsStoreExtensionID }

// Underlying returns the wrapped [EventsStore].
func (x *EventsStoreExtension) Underlying() EventsStore { return x.underlying }

// SnapshotStoreExtension adapts a [SnapshotStore] to [extension.Extension] so
// it can be registered with the actor system via [WithExtensions]. The wrapped
// store stays free of any goakt-specific identifiers.
type SnapshotStoreExtension struct {
	underlying SnapshotStore
}

var _ extension.Extension = (*SnapshotStoreExtension)(nil)

// NewSnapshotStoreExtension wraps store so it can be registered as an extension.
func NewSnapshotStoreExtension(store SnapshotStore) *SnapshotStoreExtension {
	return &SnapshotStoreExtension{underlying: store}
}

// ID returns [SnapshotStoreExtensionID].
func (x *SnapshotStoreExtension) ID() string { return SnapshotStoreExtensionID }

// Underlying returns the wrapped [SnapshotStore].
func (x *SnapshotStoreExtension) Underlying() SnapshotStore { return x.underlying }
