package discovery

import (
	"context"
	"fmt"

	goaktpb "github.com/tochemey/goakt/internal/goaktpb/v1"
)

// Discovery helps discover other running actor system in a cloud environment
type Discovery interface {
	// Start the discovery engine
	Start(ctx context.Context, meta Meta) error
	// Nodes returns the list of Nodes at a given time
	Nodes(ctx context.Context) ([]*goaktpb.Node, error)
	// Watch returns event based upon node lifecycle
	Watch(ctx context.Context) (chan *goaktpb.Event, error)
	// EarliestNode returns the earliest node
	EarliestNode(ctx context.Context) (*goaktpb.Node, error)
	// Stop shutdown the discovery provider
	Stop() error
}

var (
	ErrMetaKeyNotFound = func(key string) error { return fmt.Errorf("key=%s not found", key) }
)
