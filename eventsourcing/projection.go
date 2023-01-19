package eventsourcing

import (
	"context"
	"fmt"
	"math"
	"sync"

	pb "github.com/tochemey/goakt/pb/goakt/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/pkg/errors"
	"github.com/tochemey/goakt/persistence"
	"github.com/tochemey/goakt/telemetry"
	"google.golang.org/protobuf/types/known/anypb"
)

// ProjectionHandler is used to handle event and state consumed from the event store
type ProjectionHandler func(ctx context.Context, persistenceID string, event *anypb.Any, state *anypb.Any, offset uint64) error

// Projection defines the projection actor
type Projection struct {
	mu sync.RWMutex
	// specifies the projection handler
	handler ProjectionHandler
	// specifies the offset store
	offsetsStore persistence.OffsetStore
	// specifies the events store
	journalStore persistence.JournalStore
	// specifies the projection unique name
	// TODO: check whether we need it
	projectionName string
}

// NewProjection create an instance of Projection given the name of the projection, the handler and the offsets store
func NewProjection(name string, handler ProjectionHandler, offsetsStore persistence.OffsetStore, eventsStore persistence.JournalStore) *Projection {
	return &Projection{
		mu:             sync.RWMutex{},
		handler:        handler,
		offsetsStore:   offsetsStore,
		projectionName: name,
		journalStore:   eventsStore,
	}
}

// Start starts the projection
func (p *Projection) Start(ctx context.Context) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "PreStart")
	defer span.End()
	// acquire the lock
	p.mu.Lock()
	// release lock when done
	defer p.mu.Unlock()

	// connect to the offset store
	if p.offsetsStore == nil {
		return errors.New("offsets store is not defined")
	}

	// call the connect method of the journal store
	if err := p.offsetsStore.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to the offsets store: %v", err)
	}

	// connect to the events store
	if p.journalStore == nil {
		return errors.New("journal store is not defined")
	}

	// call the connect method of the journal store
	if err := p.journalStore.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to the journal store: %v", err)
	}

	return nil
}

// Stop stops the projection
func (p *Projection) Stop(ctx context.Context) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "PostStop")
	defer span.End()
	// acquire the lock
	p.mu.Lock()
	// release lock when done
	defer p.mu.Unlock()

	// disconnect the events store
	if err := p.journalStore.Disconnect(ctx); err != nil {
		return fmt.Errorf("failed to disconnect the journal store: %v", err)
	}

	// disconnect the offset store
	if err := p.offsetsStore.Disconnect(ctx); err != nil {
		return fmt.Errorf("failed to disconnect the offsets store: %v", err)
	}

	return nil
}

func (p *Projection) runLoop(ctx context.Context) error {
	for {
		// let us fetch all the persistence ids
		ids, err := p.journalStore.PersistenceIDs(ctx)
		if err != nil {
			return err
		}

		// let us replay all the events for each persistence id
		for _, id := range ids {
			// get the latest offset persisted for the persistence id
			offset, err := p.offsetsStore.GetLatestOffset(ctx, persistence.NewProjectionID(p.projectionName, id))
			if err != nil {
				return err
			}

			// fetch events
			events, err := p.journalStore.ReplayEvents(ctx, id, offset.GetCurrentOffset()+1, math.MaxUint64, math.MaxUint64)
			if err != nil {
				return err
			}

			// grab the total number of events fetched
			eventsLen := len(events)
			for i := 0; i < eventsLen; i++ {
				// get the event envelope
				envelope := events[i]
				// grab the data to pass to the projection handler
				state := envelope.GetResultingState()
				event := envelope.GetEvent()
				seqNr := envelope.GetSequenceNumber()
				// pass the data to the projection handler
				if err := p.handler(ctx, id, event, state, seqNr); err != nil {
					// TODO log the error and apply some replay mechanism here
				}

				// here we commit the offset to the offset store and continue the next event
				offset = &pb.Offset{
					PersistenceId:  id,
					ProjectionName: p.projectionName,
					CurrentOffset:  seqNr,
					Timestamp:      timestamppb.Now().AsTime().UnixMilli(),
				}
				if err := p.offsetsStore.WriteOffset(ctx, offset); err != nil {
					// TODO log the error and retry it because the event has been handled successfully
				}
			}
		}
	}
}
