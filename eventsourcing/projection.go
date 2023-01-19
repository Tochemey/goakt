package eventsourcing

import (
	"context"
	"fmt"
	"sync"

	"github.com/pkg/errors"
	"github.com/tochemey/goakt/actors"
	pb "github.com/tochemey/goakt/pb/goakt/v1"
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

// make sure Projection is a pure Actor
var _ actors.Actor = &Projection{}

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

// PreStart pre-starts the actor. This hook is called during the actor initialization process
func (p *Projection) PreStart(ctx context.Context) error {
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

// Receive  processes any message dropped into the actor mailbox.
func (p *Projection) Receive(ctx actors.ReceiveContext) {
	// add a span context
	goCtx, span := telemetry.SpanContext(ctx.Context(), "Receive")
	defer span.End()
	// acquire the lock
	p.mu.Lock()
	// release lock when done
	defer p.mu.Unlock()
	// grab the command sent
	switch ctx.Message().(type) {
	case *pb.StartProjection:
		// fetch the list of persistence IDs
		_, err := p.journalStore.PersistenceIDs(goCtx)
		// handle the error
		if err != nil {
			// TODO handle error
		}
		// pass
	case *pb.GetCurrentOffset:
	// pass
	case *pb.GetLatestOffset:
		// pass
	}
}

// PostStop is executed during the actor shutdown process
func (p *Projection) PostStop(ctx context.Context) error {
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
