package persistence

import (
	"context"
	"fmt"
	"sync"

	"github.com/pkg/errors"
	"github.com/tochemey/goakt/actors"
	pb "github.com/tochemey/goakt/pb/goakt/v1"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// PersistentActor is an event sourced based actor
type PersistentActor[T State] struct {
	journalStore   JournalStore
	commandHandler CommandHandler[T]
	eventHandler   EventHandler[T]
	initHook       InitHook
	shutdownHook   ShutdownHook
	persistentID   string
	eventsCounter  *atomic.Uint64
	mu             sync.Mutex

	currentState T
}

var _ actors.Actor = &PersistentActor[State]{}

// NewPersistentActor returns an instance of PersistentActor
func NewPersistentActor[T State](config *PersistentConfig[T]) *PersistentActor[T] {
	return &PersistentActor[T]{
		journalStore:   config.JournalStore,
		commandHandler: config.CommandHandler,
		eventHandler:   config.EventHandler,
		initHook:       config.InitHook,
		shutdownHook:   config.ShutdownHook,
		eventsCounter:  atomic.NewUint64(0),
		persistentID:   config.PersistentID,
		mu:             sync.Mutex{},
		currentState:   config.InitialState,
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

	// check whether there is a snapshot to recover from
	if err := p.recoverFromSnapshot(ctx); err != nil {
		return errors.Wrap(err, "failed to recover from snapshot")
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
	case *pb.GetStateCommand:
		// first make sure that we do have some events
		if p.eventsCounter.Load() == 0 {
			state, _ := anypb.New(new(emptypb.Empty))
			reply := &pb.CommandReply{
				Reply: &pb.CommandReply_State{
					State: &pb.State{
						PersistenceId:  p.persistentID,
						State:          state,
						SequenceNumber: 0,
						Timestamp:      nil,
					},
				},
			}

			// send the response
			ctx.Response(reply)
		}

		// let us fetch the latest journal
		latestJournal, err := p.journalStore.GetLatestJournal(ctx.Context(), p.persistentID)
		// handle the error
		if err != nil {
			// create a new error reply
			reply := &pb.CommandReply{
				Reply: &pb.CommandReply_Error{
					Error: &pb.ErrorReply{
						Message: err.Error(),
					},
				},
			}
			// send the response
			ctx.Response(reply)
			return
		}

		// reply with the state unmarshaled state
		resultingState := latestJournal.GetResultingState()
		reply := &pb.CommandReply{
			Reply: &pb.CommandReply_State{
				State: &pb.State{
					PersistenceId:  p.persistentID,
					State:          resultingState,
					SequenceNumber: latestJournal.GetSequenceNumber(),
					Timestamp:      latestJournal.GetTimestamp(),
				},
			},
		}

		// send the response
		ctx.Response(reply)
	default:
		// pass the received command to the command handler
		event, err := p.commandHandler(ctx.Context(), command, p.currentState)
		// handle the command handler error
		if err != nil {
			// create a new error reply
			reply := &pb.CommandReply{
				Reply: &pb.CommandReply_Error{
					Error: &pb.ErrorReply{
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
			reply := &pb.CommandReply{
				Reply: &pb.CommandReply_NoReply{
					NoReply: &pb.NoReply{},
				},
			}
			// send the response
			ctx.Response(reply)
			return
		}

		// process the event by calling the event handler
		resultingState, err := p.eventHandler(ctx.Context(), event, p.currentState)
		// handle the event handler error
		if err != nil {
			// create a new error reply
			reply := &pb.CommandReply{
				Reply: &pb.CommandReply_Error{
					Error: &pb.ErrorReply{
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

		// set the current state for the next command
		p.currentState = resultingState

		// marshal the event and the resulting state
		marshaledEvent, _ := anypb.New(event)
		marshaledState, _ := anypb.New(resultingState)

		sequenceNumber := p.eventsCounter.Load()
		timestamp := timestamppb.Now()

		// create a journal list
		journals := []*pb.Journal{
			{
				PersistenceId:  p.persistentID,
				SequenceNumber: sequenceNumber,
				IsDeleted:      false,
				Event:          marshaledEvent,
				ResultingState: marshaledState,
				Timestamp:      timestamp,
			},
		}

		// TODO persist the event in batch using a child actor
		if err := p.journalStore.WriteJournals(ctx.Context(), journals); err != nil {
			// create a new error reply
			reply := &pb.CommandReply{
				Reply: &pb.CommandReply_Error{
					Error: &pb.ErrorReply{
						Message: err.Error(),
					},
				},
			}
			// send the response
			ctx.Response(reply)
			return
		}

		reply := &pb.CommandReply{
			Reply: &pb.CommandReply_State{
				State: &pb.State{
					PersistenceId:  p.persistentID,
					State:          marshaledState,
					SequenceNumber: sequenceNumber,
					Timestamp:      timestamp,
				},
			},
		}

		// send the response
		ctx.Response(reply)
	}
}

// PostStop prepares the actor to gracefully shutdown
func (p *PersistentActor[T]) PostStop(ctx context.Context) error {
	// disconnect the journal
	if err := p.journalStore.Disconnect(ctx); err != nil {
		return fmt.Errorf("failed to disconnect the journal store: %v", err)
	}

	// run the shutdown hook
	return p.shutdownHook(ctx)
}

// recoverFromSnapshot reset the persistent actor to the latest snapshot in case there is one
// this is vital when the persistent actor is restarting.
func (p *PersistentActor[T]) recoverFromSnapshot(ctx context.Context) error {
	// check whether there is a snapshot to recover from
	journal, err := p.journalStore.GetLatestJournal(ctx, p.persistentID)
	// handle the error
	if err != nil {
		return errors.Wrap(err, "failed to recover the latest journal")
	}

	// we do have the latest state just recover from it
	if journal != nil {
		// set the current state
		if err := journal.GetResultingState().UnmarshalTo(p.currentState); err != nil {
			return errors.Wrap(err, "failed unmarshal the latest state")
		}

		// set the event counter
		p.eventsCounter.Store(journal.GetSequenceNumber())
	}

	return nil
}
