package eventsourcing

import (
	"context"

	"google.golang.org/protobuf/types/known/anypb"
)

// ProjectionHandler defines how a projection should handle
// events consumed from an event store
type ProjectionHandler interface {
	Handle(ctx context.Context, persistenceID string, event *anypb.Any, state *anypb.Any) error
}

// Projection defines the core abstraction of
type Projection struct {
}
