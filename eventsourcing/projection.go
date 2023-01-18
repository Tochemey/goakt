package eventsourcing

import (
	"context"
	"fmt"
	"sync"

	pb "github.com/tochemey/goakt/pb/goakt/v1"

	"github.com/pkg/errors"
	"github.com/tochemey/goakt/actors"
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
	// specifies the projection unique name
	// TODO: check whether we need it
	projectionName string
}

// make sure Projection is a pure Actor
var _ actors.Actor = &Projection{}

// NewProjection create an instance of Projection given the name of the projection, the handler and the offsets store
func NewProjection(name string, handler ProjectionHandler, offsetsStore persistence.OffsetStore) *Projection {
	return &Projection{
		mu:             sync.RWMutex{},
		handler:        handler,
		offsetsStore:   offsetsStore,
		projectionName: name,
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

	// connect to the various stores
	if p.offsetsStore == nil {
		return errors.New("offsets store is not defined")
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
		// here we just subscribe to the event sourced topic. In case the subscription for some weird reason
		// panic
		if err := ctx.Self().ActorSystem().EventBus().Subscribe(goCtx, Topic, p.subscribeFunc); err != nil {
			panic(errors.Wrapf(err, "failed to subscribe to Topic=%s", Topic))
		}
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

	// disconnect the journal
	if err := p.offsetsStore.Disconnect(ctx); err != nil {
		return fmt.Errorf("failed to disconnect the offsets store: %v", err)
	}

	return nil
}

// subscribeFunc handle event consumed from the Topic, unpack and pass it to the
// projection handler
func (p *Projection) subscribeFunc(event []byte) {

}
