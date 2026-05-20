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

package actor

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/eventstream"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/persistence"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/supervisor"
)

// finalSnapshotTimeout bounds the synchronous snapshot write performed in PostStop.
const finalSnapshotTimeout = 5 * time.Second

// eventSourcedActor implements Actor for event-sourced behaviors.
// The actor's name (set at spawn time) is the persistence ID used for all
// store reads and writes.
type eventSourcedActor struct {
	behavior         EventSourcedBehavior
	snapshotCriteria *persistence.SnapshotCriteria

	logger           log.Logger
	persistenceID    string
	writerID         string
	snapshotWriterID string
	currentState     any
	sequenceNumber   uint64
	snapshotWriter   *PID
	eventsStore      persistence.EventsStore
	snapshotStore    persistence.SnapshotStore
	eventsStream     eventstream.Stream
}

var _ Actor = (*eventSourcedActor)(nil)

// PreStart resolves the behavior and snapshot criteria from the injected
// dependencies, loads the events and snapshot stores from system extensions,
// and recovers actor state from the snapshot and event log.
func (x *eventSourcedActor) PreStart(ctx *Context) error {
	x.logger = ctx.Logger()
	x.persistenceID = ctx.ActorName()

	for _, dependency := range ctx.Dependencies() {
		if dependency == nil {
			continue
		}

		switch dependency.ID() {
		case eventSourcedBehaviorDependencyID:
			bd, ok := dependency.(*eventSourcedDependency)
			if !ok || bd.inner == nil {
				return fmt.Errorf("event-sourced actor %s: behavior dependency is malformed", x.persistenceID)
			}
			x.behavior = bd.inner
		case eventSourcedConfigID:
			if cfg, ok := dependency.(*eventSourcedConfig); ok {
				x.snapshotCriteria = cfg.criteria
			}
		}
	}

	if x.behavior == nil {
		return fmt.Errorf("event-sourced actor %s: behavior dependency missing", x.persistenceID)
	}

	x.currentState = x.behavior.InitialState()

	// Retrieve stores from system extensions.
	eventsExt := ctx.Extension(persistence.EventsStoreExtensionID)
	if eventsExt == nil {
		return gerrors.ErrEventsStoreRequired
	}

	x.eventsStore = eventsExt.(*persistence.EventsStoreExtension).Underlying()

	if snapExt := ctx.Extension(persistence.SnapshotStoreExtensionID); snapExt != nil {
		x.snapshotStore = snapExt.(*persistence.SnapshotStoreExtension).Underlying()
	}

	return x.recover(ctx.Context())
}

func (x *eventSourcedActor) recover(ctx context.Context) error {
	if x.snapshotStore != nil {
		snapshot, err := x.snapshotStore.GetLatestSnapshot(ctx, x.persistenceID)
		if err != nil {
			return fmt.Errorf("event-sourced actor %s: get latest snapshot: %w", x.persistenceID, err)
		}

		if snapshot != nil {
			state, err := serializerByKind(snapshot.SerializerKind).Deserialize(snapshot.Payload)
			if err != nil {
				return fmt.Errorf("event-sourced actor %s: deserialize snapshot: %w", x.persistenceID, err)
			}

			x.currentState = state
			x.sequenceNumber = snapshot.SequenceNumber
		}
	}

	events, err := x.eventsStore.ReplayEvents(ctx, x.persistenceID, x.sequenceNumber+1, 0, 0)
	if err != nil {
		return fmt.Errorf("event-sourced actor %s: replay events: %w", x.persistenceID, err)
	}

	for _, e := range events {
		event, err := serializerByKind(e.SerializerKind).Deserialize(e.Payload)
		if err != nil {
			return fmt.Errorf("event-sourced actor %s: deserialize event seq=%d: %w", x.persistenceID, e.SequenceNumber, err)
		}

		x.currentState, err = x.behavior.HandleEvent(ctx, event, x.currentState)
		if err != nil {
			return fmt.Errorf("event-sourced actor %s: apply event seq=%d: %w", x.persistenceID, e.SequenceNumber, err)
		}

		x.sequenceNumber = e.SequenceNumber
	}

	return nil
}

// Receive handles the PostStart lifecycle message and all user commands.
func (x *eventSourcedActor) Receive(rctx *ReceiveContext) {
	switch rctx.Message().(type) {
	case *PostStart:
		x.eventsStream = rctx.Self().eventsStream
		x.writerID = rctx.Self().ID()
		if x.snapshotStore != nil {
			snapshotWriter, err := rctx.Self().spawnChildLocal(rctx.Context(), snapshotWriterChildName,
				&snapshotWriterActor{
					snapshotStore: x.snapshotStore,
					eventsStore:   x.eventsStore,
					criteria:      x.snapshotCriteria,
					eventsStream:  x.eventsStream,
				}, newSpawnConfig(
					childSpawnOptions()...,
				))
			if err != nil {
				rctx.Err(fmt.Errorf("event-sourced actor %s: spawn snapshot writer: %w", x.persistenceID, err))
				return
			}
			x.snapshotWriter = snapshotWriter
			x.snapshotWriterID = snapshotWriter.ID()
		}

	default:
		x.handleCommand(rctx)
	}
}

// childSpawnOptions returns the shared spawn options for persistence child
// actors. Children are long-lived and supervised with a restart directive so
// they recover automatically on transient failures.
func childSpawnOptions() []SpawnOption {
	return []SpawnOption{
		WithLongLived(),
		WithSupervisor(
			supervisor.NewSupervisor(
				supervisor.WithAnyErrorDirective(supervisor.RestartDirective),
			),
		),
	}
}

func (x *eventSourcedActor) handleCommand(rctx *ReceiveContext) {
	events, err := x.behavior.HandleCommand(rctx.Context(), rctx.Message(), x.currentState)
	if err != nil {
		rctx.Response(err)
		return
	}

	if len(events) == 0 {
		rctx.Response(x.currentState)
		return
	}

	// Apply each event to a tentative state and serialize it in the same pass.
	// An apply failure or serialization failure aborts before any store write,
	// so persisted events are always replayable. An events-store write failure
	// leaves currentState and sequenceNumber untouched.
	tentativeState := x.currentState
	persisted := make([]*persistence.PersistedEvent, 0, len(events))

	for i, event := range events {
		nextState, err := x.behavior.HandleEvent(rctx.Context(), event, tentativeState)
		if err != nil {
			rctx.Err(fmt.Errorf("event-sourced actor %s: apply event[%d]: %w", x.persistenceID, i, err))
			return
		}

		s, kind := serializerFor(event)
		payload, err := s.Serialize(event)
		if err != nil {
			rctx.Err(fmt.Errorf("event-sourced actor %s: serialize event[%d]: %w", x.persistenceID, i, err))
			return
		}

		persisted = append(persisted, &persistence.PersistedEvent{
			PersistenceID:  x.persistenceID,
			SequenceNumber: x.sequenceNumber + uint64(i) + 1,
			Timestamp:      time.Now(),
			Payload:        payload,
			Manifest:       types.Name(event),
			SerializerKind: kind,
			WriterActorID:  x.writerID,
		})
		tentativeState = nextState
	}

	if err := x.eventsStore.WriteEvents(rctx.Context(), persisted); err != nil {
		rctx.Err(fmt.Errorf("event-sourced actor %s: write events: %w", x.persistenceID, err))
		return
	}

	// Commit only after the write succeeds.
	x.currentState = tentativeState
	x.sequenceNumber += uint64(len(events))

	if x.snapshotCriteria != nil && x.snapshotCriteria.SnapshotInterval > 0 &&
		x.sequenceNumber%x.snapshotCriteria.SnapshotInterval == 0 && x.snapshotWriter != nil {
		snap, err := x.buildSnapshot()
		if err != nil {
			x.logger.Warnf("event-sourced actor %s: build snapshot: %v", x.persistenceID, err)
		} else {
			rctx.Tell(x.snapshotWriter, writeSnapshotCmd{snapshot: snap})
		}
	}

	rctx.Response(x.currentState)
}

// PostStop writes a final snapshot synchronously and applies the configured
// retention policy. The write is bounded by [finalSnapshotTimeout].
func (x *eventSourcedActor) PostStop(ctx *Context) error {
	if x.snapshotStore == nil || x.sequenceNumber == 0 {
		return nil
	}

	snap, err := x.buildSnapshot()
	if err != nil {
		x.logger.Warnf("event-sourced actor %s: build final snapshot: %v", x.persistenceID, err)
		return nil
	}

	writeCtx, cancel := context.WithTimeout(ctx.Context(), finalSnapshotTimeout)
	defer cancel()
	if err := x.snapshotStore.WriteSnapshot(writeCtx, snap); err != nil {
		x.logger.Warnf("event-sourced actor %s: write final snapshot: %v", x.persistenceID, err)
		publishFailure(x.eventsStream, NewSnapshotWriteFailed(snap.PersistenceID, snap.SequenceNumber, err))
		return nil
	}

	applyRetentionPolicy(writeCtx, x.logger, x.eventsStream, x.eventsStore, x.snapshotStore, x.snapshotCriteria, snap)
	return nil
}

// applyRetentionPolicy applies criteria's deletion settings after a
// successful snapshot write: it deletes prior events when DeleteEventsOnSnapshot
// is set (honoring EventsRetentionCount) and prior snapshots when
// DeleteSnapshotsOnSnapshot is set. Failures are logged and published as
// [EventsDeleteFailed] or [SnapshotDeleteFailed] events.
func applyRetentionPolicy(
	ctx context.Context,
	logger log.Logger,
	stream eventstream.Stream,
	eventsStore persistence.EventsStore,
	snapshotStore persistence.SnapshotStore,
	criteria *persistence.SnapshotCriteria,
	snapshot *persistence.PersistedSnapshot,
) {
	if criteria == nil || snapshot == nil {
		return
	}

	if criteria.DeleteEventsOnSnapshot && eventsStore != nil {
		// Skip deletion when retention covers or exceeds the snapshot sequence.
		if snapshot.SequenceNumber > criteria.EventsRetentionCount {
			toSeq := snapshot.SequenceNumber - criteria.EventsRetentionCount
			if err := eventsStore.DeleteEvents(ctx, snapshot.PersistenceID, toSeq); err != nil {
				logger.Warnf("event-sourced actor %s: delete events on snapshot (toSeq=%d): %v",
					snapshot.PersistenceID, toSeq, err)
				publishFailure(stream, NewEventsDeleteFailed(snapshot.PersistenceID, toSeq, err))
			}
		}
	}

	if criteria.DeleteSnapshotsOnSnapshot && snapshotStore != nil && snapshot.SequenceNumber > 1 {
		toSeq := snapshot.SequenceNumber - 1
		if err := snapshotStore.DeleteSnapshots(ctx, snapshot.PersistenceID, toSeq); err != nil {
			logger.Warnf("event-sourced actor %s: delete snapshots on snapshot (toSeq=%d): %v",
				snapshot.PersistenceID, toSeq, err)
			publishFailure(stream, NewSnapshotDeleteFailed(snapshot.PersistenceID, toSeq, err))
		}
	}
}

// publishFailure publishes event to the actor system's event stream, or
// no-ops when stream is nil.
func publishFailure(stream eventstream.Stream, event any) {
	if stream == nil {
		return
	}
	stream.Publish(eventsTopic, event)
}

func (x *eventSourcedActor) buildSnapshot() (*persistence.PersistedSnapshot, error) {
	s, kind := serializerFor(x.currentState)

	payload, err := s.Serialize(x.currentState)
	if err != nil {
		return nil, err
	}

	return &persistence.PersistedSnapshot{
		PersistenceID:  x.persistenceID,
		SequenceNumber: x.sequenceNumber,
		Timestamp:      time.Now(),
		Payload:        payload,
		Manifest:       types.Name(x.currentState),
		SerializerKind: kind,
		WriterActorID:  x.snapshotWriterID,
	}, nil
}

// serializerFor returns the serializer and kind for msg.
// proto.Message types use the protobuf serializer; everything else uses CBOR.
func serializerFor(msg any) (remote.Serializer, persistence.SerializerKind) {
	if _, ok := msg.(proto.Message); ok {
		return remote.NewProtoSerializer(), persistence.ProtobufSerializerKind
	}
	return remote.NewCBORSerializer(), persistence.CBORSerializerKind
}

// serializerByKind returns the serializer for a stored SerializerKind.
func serializerByKind(kind persistence.SerializerKind) remote.Serializer {
	if kind == persistence.ProtobufSerializerKind {
		return remote.NewProtoSerializer()
	}
	return remote.NewCBORSerializer()
}
