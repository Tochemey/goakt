package discovery

import (
	"context"
)

// Discovery helps discover other running actor system in a cloud environment
type Discovery interface {
	// ID returns the discovery name
	ID() string
	// Start the discovery engine
	Start(ctx context.Context, meta Meta) error
	// Nodes returns the list of up and running Nodes at a given time
	Nodes(ctx context.Context) ([]*Node, error)
	// Watch returns event based upon node lifecycle
	Watch(ctx context.Context) (<-chan Event, error)
	// EarliestNode returns the earliest node. This is based upon the node timestamp
	EarliestNode(ctx context.Context) (*Node, error)
	// Stop shutdown the discovery provider
	Stop() error
}
