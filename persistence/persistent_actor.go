package persistence

import (
	"context"
	"errors"
	"fmt"
	"sync"

	actorspb "github.com/tochemey/goakt/actorpb/actors/v1"
	"github.com/tochemey/goakt/actors"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// PersistentActor is an event sourced based actor
type PersistentActor[T State] struct {
	initialState  T
	journalStore  JournalStore
	snapshotStore SnapshotStore

	isSnapshotEnabled bool
	snapshotAfter     uint64

	commandHandler CommandHandler
	eventHandler   EventHandler
	initHook       InitHook
	shutdownHook   ShutdownHook

	persistentID  string
	eventsCounter *atomic.Uint64

	mu sync.Mutex
}

var _ actors.Actor = &PersistentActor[State]{}

// NewPersistentActor returns an instance of PersistentActor
func NewPersistentActor[T State](config *PersistentConfig[T]) *PersistentActor[T] {
	return &PersistentActor[T]{
		initialState:      config.InitialState,
		journalStore:      config.JournalStore,
		snapshotStore:     config.SnapshotStore,
		isSnapshotEnabled: false,
		commandHandler:    config.CommandHandler,
		eventHandler:      config.EventHandler,
		initHook:          config.InitHook,
		shutdownHook:      config.ShutdownHook,
		eventsCounter:     atomic.NewUint64(0),
		persistentID:      config.PersistentID,
		mu:                sync.Mutex{},
		snapshotAfter:     config.SnapshotAfter,
	}
}

// PreStart pre-starts the actor
// At this stage we connect to the various stores
func (p *PersistentActor[T]) PreStart(ctx context.Context) error {
	// connect to the various stores
	if p.journalStore == nil {
		return errors.New("journal store is not defined")
	}

	// call the connect method of the journal store
	if err := p.journalStore.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to the journal store: %v", err)
	}

	// connect to the snapshot store iff it is set
	p.isSnapshotEnabled = p.snapshotStore != nil
	if p.isSnapshotEnabled {
		if err := p.snapshotStore.Connect(ctx); err != nil {
			return fmt.Errorf("failed to connect to the snapshot store: %v", err)
		}
	}

	// check whether the snapshot after is greater than zero and set a default value of 50 events
	if p.snapshotAfter == 0 {
		p.snapshotAfter = 50
	}

	// run the init hook
	return p.initHook(ctx)
}

// Receive processes any message dropped into the actor mailbox.
func (p *PersistentActor[T]) Receive(ctx actors.ReceiveContext) {
	// acquire the lock
	p.mu.Lock()
	defer p.mu.Unlock()

	switch command := ctx.Message().(type) {
	case *actorspb.GetStateCommand:
		// TODO we need to make use of the snapshot store
	default:
		// pass the received command to the command handler
		event, err := p.commandHandler(ctx.Context(), command)
		// handle the command handler error
		if err != nil {
			// create a new error reply
			reply := &actorspb.CommandReply{
				Reply: &actorspb.CommandReply_Error{
					Error: &actorspb.ErrorReply{
						Message: err.Error(),
					},
				},
			}
			// send the response
			ctx.Response(reply)
			return
		}

		// if the event is nil nothing is persisted, and we return no reply
		if event == nil {
			// create a new error reply
			reply := &actorspb.CommandReply{
				Reply: &actorspb.CommandReply_NoReply{
					NoReply: &actorspb.NoReply{},
				},
			}
			// send the response
			ctx.Response(reply)
			return
		}

		// process the event by calling the event handler
		resultingState, err := p.eventHandler(ctx.Context(), event)
		// handle the event handler error
		if err != nil {
			// create a new error reply
			reply := &actorspb.CommandReply{
				Reply: &actorspb.CommandReply_Error{
					Error: &actorspb.ErrorReply{
						Message: err.Error(),
					},
				},
			}
			// send the response
			ctx.Response(reply)
			return
		}

		// increment the event counter
		p.eventsCounter.Inc()

		// marshal the event and the resulting state
		marshaledEvent, _ := anypb.New(event)
		marshaledState, _ := anypb.New(resultingState)

		sequenceNumber := p.eventsCounter.Load()
		timestamp := timestamppb.Now()

		// persist the event into the journal
		eventWrapper := &actorspb.Event{
			Event:          marshaledEvent,
			ResultingState: marshaledState,
			Meta: &actorspb.MetaData{
				PersitenceId:   p.persistentID,
				RevisionNumber: uint32(sequenceNumber),
				RevisionDate:   timestamp,
			},
		}

		// marshal the event wrapper
		payload, _ := proto.Marshal(eventWrapper)
		journals := []*actorspb.Journal{
			{
				PersistenceId:   p.persistentID,
				SequenceNumber:  sequenceNumber,
				IsDeleted:       false,
				PayloadManifest: string(eventWrapper.ProtoReflect().Descriptor().FullName()),
				Payload:         payload,
				Timestamp:       timestamp,
			},
		}

		// TODO persist the event in batch using a child actor
		if err := p.journalStore.WriteJournals(ctx.Context(), journals); err != nil {
			// create a new error reply
			reply := &actorspb.CommandReply{
				Reply: &actorspb.CommandReply_Error{
					Error: &actorspb.ErrorReply{
						Message: err.Error(),
					},
				},
			}
			// send the response
			ctx.Response(reply)
			return
		}

		// persist snapshot iff snapshot is enabled
		if p.isSnapshotEnabled {
			// check whether we have reached the snapshot threshold or not
			if p.snapshotAfter >= sequenceNumber {
				// persist a snapshot
				payload, _ := proto.Marshal(resultingState)
				snapshot := &actorspb.Snapshot{
					PersistenceId:   p.persistentID,
					SequenceNumber:  sequenceNumber,
					PayloadManifest: string(resultingState.ProtoReflect().Descriptor().FullName()),
					Payload:         payload,
					Timestamp:       timestamp,
				}
				if err := p.snapshotStore.SaveSnapshot(ctx.Context(), snapshot); err != nil {
					// create a new error reply
					reply := &actorspb.CommandReply{
						Reply: &actorspb.CommandReply_Error{
							Error: &actorspb.ErrorReply{
								Message: err.Error(),
							},
						},
					}
					// send the response
					ctx.Response(reply)
					return
				}
			}
		}

		reply := &actorspb.CommandReply{
			Reply: &actorspb.CommandReply_State{
				State: &actorspb.State{
					State: marshaledState,
					Meta: &actorspb.MetaData{
						PersitenceId:   p.persistentID,
						RevisionNumber: uint32(sequenceNumber),
						RevisionDate:   timestamp,
					},
				},
			},
		}

		// send the response
		ctx.Response(reply)
	}
}

// PostStop prepares the actor to gracefully shutdow  n
func (p *PersistentActor[T]) PostStop(ctx context.Context) error {
	// disconnect the journal
	if err := p.journalStore.Disconnect(ctx); err != nil {
		return fmt.Errorf("failed to disconnect the journal store: %v", err)
	}

	// disconnect the snapshot store iff it is set
	if p.isSnapshotEnabled {
		if err := p.snapshotStore.Disconnect(ctx); err != nil {
			return fmt.Errorf("failed to disconnect the snapshot store: %v", err)
		}
	}
	// run the init hook
	return p.shutdownHook(ctx)
}
