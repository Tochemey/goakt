package persistence

import (
	"context"
	"errors"
	"fmt"

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

	commandHandler CommandHandler
	eventHandler   EventHandler
	initHook       InitHook
	shutdownHook   ShutdownHook

	eventsCounter *atomic.Uint64
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

	// run the init hook
	return p.initHook(ctx)
}

// Receive processes any message dropped into the actor mailbox.
func (p *PersistentActor[T]) Receive(ctx actors.ReceiveContext) {
	// grab the command sent
	command := ctx.Message()

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

	// persist the event into the journal
	eventWrapper := &actorspb.Event{
		Event:          marshaledEvent,
		ResultingState: marshaledState,
		Meta:           nil,
	}

	sequenceNumber := p.eventsCounter.Load()
	timestamp := timestamppb.Now()

	// marshal the event wrapper
	payload, _ := proto.Marshal(eventWrapper)
	journals := []*actorspb.Journal{
		{
			PersistenceId:   "",
			SequenceNumber:  sequenceNumber,
			IsDeleted:       false,
			PayloadManifest: string(eventWrapper.ProtoReflect().Descriptor().FullName()),
			Payload:         payload,
			Timestamp:       timestamp,
			WriterId:        "",
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

	// TODO send a command reply with the resulting state
	reply := &actorspb.CommandReply{
		Reply: &actorspb.CommandReply_State{
			State: &actorspb.State{
				State: marshaledState,
				Meta: &actorspb.MetaData{
					PersitenceId:   "",
					RevisionNumber: uint32(sequenceNumber),
					RevisionDate:   timestamp,
				},
			},
		},
	}

	// send the response
	ctx.Response(reply)
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
