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
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/persistence"
	"github.com/tochemey/goakt/v4/remote"
)

// eventSourcedActor implements Actor for event-sourced behaviors.
// The actor's name (set at spawn time) is the persistence ID used for all
// store reads and writes.
type eventSourcedActor struct {
	behavior         EventSourcedBehavior
	snapshotCriteria *persistence.SnapshotCriteria

	logger         log.Logger
	persistenceID  string
	currentState   any
	sequenceNumber uint64
	snapshotWriter *PID
	eventsStore    persistence.EventsStore
	snapshotStore  persistence.SnapshotStore
}

var _ Actor = (*eventSourcedActor)(nil)

// PreStart resolves the behavior and config from the injected dependencies,
// loads the stores from system extensions, and recovers actor state from the
// event log (and optional snapshot). The behavior and config travel with the
// spawn request as dependencies so this path is identical for the initial
// spawn and for a relocated spawn on another node.
func (x *eventSourcedActor) PreStart(ctx *Context) error {
	x.logger = ctx.Logger()
	x.persistenceID = ctx.ActorName()

	for _, dep := range ctx.Dependencies() {
		switch d := dep.(type) {
		case EventSourcedBehavior:
			x.behavior = d
		case *eventSourcedConfig:
			x.snapshotCriteria = d.criteria
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
		if x.snapshotStore != nil {
			snapWriter, err := rctx.Self().SpawnChild(rctx.Context(), snapshotWriterChildName,
				&snapshotWriterActor{store: x.snapshotStore})
			if err != nil {
				rctx.Err(fmt.Errorf("event-sourced actor %s: spawn snapshot writer: %w", x.persistenceID, err))
				return
			}
			x.snapshotWriter = snapWriter
		}

	default:
		x.handleCommand(rctx)
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

	persisted := make([]*persistence.PersistedEvent, 0, len(events))

	for i, event := range events {
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
			WriterActorID:  x.persistenceID,
		})
	}

	if err := x.eventsStore.WriteEvents(rctx.Context(), persisted); err != nil {
		rctx.Err(fmt.Errorf("event-sourced actor %s: write events: %w", x.persistenceID, err))
		return
	}

	for i, event := range events {
		newState, err := x.behavior.HandleEvent(rctx.Context(), event, x.currentState)
		if err != nil {
			rctx.Err(fmt.Errorf("event-sourced actor %s: apply event[%d]: %w", x.persistenceID, i, err))
			return
		}

		x.currentState = newState
		x.sequenceNumber++
	}

	if x.snapshotCriteria != nil && x.snapshotCriteria.SnapshotInterval > 0 &&
		x.sequenceNumber%x.snapshotCriteria.SnapshotInterval == 0 && x.snapshotWriter != nil {
		snap, err := x.buildSnapshot()
		if err != nil {
			x.logger.Warnf("event-sourced actor %s: build snapshot: %v", x.persistenceID, err)
		} else {
			_ = Tell(rctx.Context(), x.snapshotWriter, writeSnapshotCmd{snapshot: snap})
		}
	}

	rctx.Response(x.currentState)
}

// PostStop writes a final snapshot synchronously to speed up the next recovery.
// The write is bounded by [finalSnapshotTimeout] so a slow snapshot store cannot
// hang actor shutdown indefinitely.
func (x *eventSourcedActor) PostStop(ctx *Context) error {
	if x.snapshotStore != nil && x.sequenceNumber > 0 {
		snap, err := x.buildSnapshot()
		if err != nil {
			x.logger.Warnf("event-sourced actor %s: build final snapshot: %v", x.persistenceID, err)
			return nil
		}

		writeCtx, cancel := context.WithTimeout(ctx.Context(), finalSnapshotTimeout)
		defer cancel()
		if err := x.snapshotStore.WriteSnapshot(writeCtx, snap); err != nil {
			x.logger.Warnf("event-sourced actor %s: write final snapshot: %v", x.persistenceID, err)
		}
	}

	return nil
}

// finalSnapshotTimeout bounds the synchronous snapshot write performed in PostStop.
const finalSnapshotTimeout = 5 * time.Second

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
		WriterActorID:  x.persistenceID,
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
