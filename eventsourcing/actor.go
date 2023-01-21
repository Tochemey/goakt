package eventsourcing

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/tochemey/goakt/actors"
	pb "github.com/tochemey/goakt/pb/goakt/v1"
	"github.com/tochemey/goakt/persistence"
	"github.com/tochemey/goakt/telemetry"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Command proto.Message
type Event proto.Message
type State proto.Message

// EventSourcedBehavior defines an event sourced behavior when modeling a CQRS Aggregate.
type EventSourcedBehavior[T State] interface {
	persistence.PersistentID
	// InitialState returns the event sourced actor initial state
	InitialState() T
	// HandleCommand helps handle commands received by the event sourced actor. The command handlers define how to handle each incoming command,
	// which validations must be applied, and finally, which events will be persisted if any. When there is no event to be persisted a nil can
	// be returned as a no-op. Command handlers are the meat of the event sourced actor.
	// They encode the business rules of your event sourced actor and act as a guardian of the event sourced actor consistency.
	// The command handler must first validate that the incoming command can be applied to the current model state.
	//  Any decision should be solely based on the data passed in the commands and the state of the Aggregate.
	// In case of successful validation, one or more events expressing the mutations are persisted.
	// Once the events are persisted, they are applied to the state producing a new valid state.
	HandleCommand(ctx context.Context, command Command, priorState T) (event Event, err error)
	// HandleEvent handle events emitted by the command handlers. The event handlers are used to mutate the state of the event sourced actor by applying the events to it.
	// Event handlers must be pure functions as they will be used when instantiating the event sourced actor and replaying the event journal.
	HandleEvent(ctx context.Context, event Event, priorState T) (state T, err error)
}

// eventSourcedActor is an event sourced based actor
type eventSourcedActor[T State] struct {
	EventSourcedBehavior[T]

	journalStore    persistence.JournalStore
	currentState    T
	eventsCounter   *atomic.Uint64
	lastCommandTime time.Time

	mu sync.RWMutex
}

// make sure eventSourcedActor is a pure Actor
var _ actors.Actor = &eventSourcedActor[State]{}

// NewEventSourcedActor returns an instance of event sourced actor
func NewEventSourcedActor[T State](behavior EventSourcedBehavior[T], eventsStore persistence.JournalStore) actors.Actor {
	return &eventSourcedActor[T]{
		EventSourcedBehavior: behavior,
		journalStore:         eventsStore,
		eventsCounter:        atomic.NewUint64(0),
		mu:                   sync.RWMutex{},
	}
}

// PreStart pre-starts the actor
// At this stage we connect to the various stores
func (p *eventSourcedActor[T]) PreStart(ctx context.Context) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "PreStart")
	defer span.End()
	// acquire the lock
	p.mu.Lock()
	// release lock when done
	defer p.mu.Unlock()

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
	return nil
}

// Receive processes any message dropped into the actor mailbox.
func (p *eventSourcedActor[T]) Receive(ctx actors.ReceiveContext) {
	// add a span context
	goCtx, span := telemetry.SpanContext(ctx.Context(), "Receive")
	defer span.End()

	// acquire the lock
	p.mu.Lock()
	// release lock when done
	defer p.mu.Unlock()

	// grab the command sent
	switch command := ctx.Message().(type) {
	case *pb.GetStateCommand:
		// let us fetch the latest journal
		latestEvent, err := p.journalStore.GetLatestEvent(goCtx, p.ID())
		// handle the error
		if err != nil {
			// create a new error reply
			reply := &pb.CommandReply{
				Reply: &pb.CommandReply_ErrorReply{
					ErrorReply: &pb.ErrorReply{
						Message: err.Error(),
					},
				},
			}
			// send the response
			ctx.Response(reply)
			return
		}

		// reply with the state unmarshalled state
		resultingState := latestEvent.GetResultingState()
		reply := &pb.CommandReply{
			Reply: &pb.CommandReply_StateReply{
				StateReply: &pb.StateReply{
					PersistenceId:  p.ID(),
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
		event, err := p.HandleCommand(goCtx, command, p.currentState)
		// handle the command handler error
		if err != nil {
			// create a new error reply
			reply := &pb.CommandReply{
				Reply: &pb.CommandReply_ErrorReply{
					ErrorReply: &pb.ErrorReply{
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
		resultingState, err := p.HandleEvent(goCtx, event, p.currentState)
		// handle the event handler error
		if err != nil {
			// create a new error reply
			reply := &pb.CommandReply{
				Reply: &pb.CommandReply_ErrorReply{
					ErrorReply: &pb.ErrorReply{
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
		p.lastCommandTime = timestamp.AsTime()

		// create the event
		envelope := &pb.Event{
			PersistenceId:  p.ID(),
			SequenceNumber: sequenceNumber,
			IsDeleted:      false,
			Event:          marshaledEvent,
			ResultingState: marshaledState,
			Timestamp:      p.lastCommandTime.Unix(),
		}

		// create a journal list
		journals := []*pb.Event{envelope}

		// TODO persist the event in batch using a child actor
		if err := p.journalStore.WriteEvents(goCtx, journals); err != nil {
			// create a new error reply
			reply := &pb.CommandReply{
				Reply: &pb.CommandReply_ErrorReply{
					ErrorReply: &pb.ErrorReply{
						Message: err.Error(),
					},
				},
			}
			// send the response
			ctx.Response(reply)
			return
		}

		// let us push the envelope to the event stream
		// TODO in its own go-routine
		payload, _ := proto.Marshal(envelope)
		ctx.Self().ActorSystem().EventBus().Publish(goCtx, Topic, payload)

		reply := &pb.CommandReply{
			Reply: &pb.CommandReply_StateReply{
				StateReply: &pb.StateReply{
					PersistenceId:  p.ID(),
					State:          marshaledState,
					SequenceNumber: sequenceNumber,
					Timestamp:      p.lastCommandTime.Unix(),
				},
			},
		}

		// send the response
		ctx.Response(reply)
	}
}

// PostStop prepares the actor to gracefully shutdown
func (p *eventSourcedActor[T]) PostStop(ctx context.Context) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "PostStop")
	defer span.End()

	// acquire the lock
	p.mu.Lock()
	// release lock when done
	defer p.mu.Unlock()

	// disconnect the journal
	if err := p.journalStore.Disconnect(ctx); err != nil {
		return fmt.Errorf("failed to disconnect the journal store: %v", err)
	}

	return nil
}

// recoverFromSnapshot reset the persistent actor to the latest snapshot in case there is one
// this is vital when the persistent actor is restarting.
func (p *eventSourcedActor[T]) recoverFromSnapshot(ctx context.Context) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RecoverFromSnapshot")
	defer span.End()

	// check whether there is a snapshot to recover from
	event, err := p.journalStore.GetLatestEvent(ctx, p.ID())
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

	// in case there is no snapshot
	p.currentState = p.InitialState()

	return nil
}
