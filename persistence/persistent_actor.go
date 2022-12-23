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

// persistentActor is an event sourced based actor
type persistentActor[T State] struct {
	PersistentBehavior[T]

	eventsStore   EventStore
	currentState  T
	eventsCounter *atomic.Uint64

	mu sync.RWMutex
}

// make sure persistentActor is a pure Actor
var _ actors.Actor = &persistentActor[State]{}

// NewPersistentActor returns an instance of persistentActor
func NewPersistentActor[T State](behavior PersistentBehavior[T], eventsStore EventStore) actors.Actor {
	return &persistentActor[T]{
		PersistentBehavior: behavior,
		eventsStore:        eventsStore,
		eventsCounter:      atomic.NewUint64(0),
		mu:                 sync.RWMutex{},
	}
}

// PreStart pre-starts the actor
// At this stage we connect to the various stores
func (p *persistentActor[T]) PreStart(ctx context.Context) error {
	// acquire the lock
	p.mu.Lock()
	// release lock when done
	defer p.mu.Unlock()

	// connect to the various stores
	if p.eventsStore == nil {
		return errors.New("journal store is not defined")
	}

	// call the connect method of the journal store
	if err := p.eventsStore.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to the journal store: %v", err)
	}

	// check whether there is a snapshot to recover from
	if err := p.recoverFromSnapshot(ctx); err != nil {
		return errors.Wrap(err, "failed to recover from snapshot")
	}
	return nil
}

// Receive processes any message dropped into the actor mailbox.
func (p *persistentActor[T]) Receive(ctx actors.ReceiveContext) {
	// acquire the lock
	p.mu.Lock()
	// release lock when done
	defer p.mu.Unlock()

	// grab the command sent
	switch command := ctx.Message().(type) {
	case *pb.GetStateCommand:
		// first make sure that we do have some events
		if p.eventsCounter.Load() == 0 {
			state, _ := anypb.New(new(emptypb.Empty))
			reply := &pb.CommandReply{
				Reply: &pb.CommandReply_State{
					State: &pb.State{
						PersistenceId:  p.PersistenceID(),
						State:          state,
						SequenceNumber: 0,
						Timestamp:      nil,
					},
				},
			}

			// send the response
			ctx.Response(reply)
			return
		}

		// let us fetch the latest journal
		latestEvent, err := p.eventsStore.GetLatestEvent(ctx.Context(), p.PersistenceID())
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
		resultingState := latestEvent.GetResultingState()
		reply := &pb.CommandReply{
			Reply: &pb.CommandReply_State{
				State: &pb.State{
					PersistenceId:  p.PersistenceID(),
					State:          resultingState,
					SequenceNumber: latestEvent.GetSequenceNumber(),
					Timestamp:      latestEvent.GetTimestamp(),
				},
			},
		}

		// send the response
		ctx.Response(reply)
	default:
		// pass the received command to the command handler
		event, err := p.HandleCommand(ctx.Context(), command, p.currentState)
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
		resultingState, err := p.HandleEvent(ctx.Context(), event, p.currentState)
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
		journals := []*pb.Event{
			{
				PersistenceId:  p.PersistenceID(),
				SequenceNumber: sequenceNumber,
				IsDeleted:      false,
				Event:          marshaledEvent,
				ResultingState: marshaledState,
				Timestamp:      timestamp,
			},
		}

		// TODO persist the event in batch using a child actor
		if err := p.eventsStore.WriteEvents(ctx.Context(), journals); err != nil {
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
					PersistenceId:  p.PersistenceID(),
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
func (p *persistentActor[T]) PostStop(ctx context.Context) error {
	// acquire the lock
	p.mu.Lock()
	// release lock when done
	defer p.mu.Unlock()

	// disconnect the journal
	if err := p.eventsStore.Disconnect(ctx); err != nil {
		return fmt.Errorf("failed to disconnect the journal store: %v", err)
	}

	return nil
}

// recoverFromSnapshot reset the persistent actor to the latest snapshot in case there is one
// this is vital when the persistent actor is restarting.
func (p *persistentActor[T]) recoverFromSnapshot(ctx context.Context) error {
	// check whether there is a snapshot to recover from
	event, err := p.eventsStore.GetLatestEvent(ctx, p.PersistenceID())
	// handle the error
	if err != nil {
		return errors.Wrap(err, "failed to recover the latest journal")
	}

	// we do have the latest state just recover from it
	if event != nil {
		// set the current state
		if err := event.GetResultingState().UnmarshalTo(p.currentState); err != nil {
			return errors.Wrap(err, "failed unmarshal the latest state")
		}

		// set the event counter
		p.eventsCounter.Store(event.GetSequenceNumber())
		return nil
	}

	// in case there is no snpashot
	p.currentState = p.InitialState()

	return nil
}
