package discovery

import (
	"context"
)

// Discovery helps discover other running actor system in a cloud environment
type Discovery interface {
	// ID returns the discovery name
	ID() string
	// Start the discovery engine
	Start(ctx context.Context) error
	// Nodes returns the list of up and running Nodes at a given time
	Nodes(ctx context.Context) ([]*Node, error)
	// Stop shutdown the discovery provider
	Stop() error
}
