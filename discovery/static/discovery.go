package static

import (
	"context"
	"sort"
	"sync"

	"github.com/pkg/errors"
	"github.com/tochemey/goakt/discovery"
	"github.com/tochemey/goakt/log"
	"github.com/tochemey/goakt/pkg/telemetry"
	"go.uber.org/atomic"
)

// Discovery represents the static discovery
// With static discovery provider the list of Nodes are known ahead of time
// That means the discovery is not elastic. You cannot at runtime manipulate nodes.
// This is discovery method is great when running Go-Akt in a docker environment
type Discovery struct {
	mu sync.Mutex

	stopChan   chan struct{}
	publicChan chan discovery.Event
	// states whether the actor system has started or not
	isInitialized *atomic.Bool
	logger        log.Logger

	nodes []*discovery.Node
}

// enforce compilation error
var _ discovery.Discovery = &Discovery{}

// NewDiscovery creates an instance of Discovery
func NewDiscovery(nodes []*discovery.Node, logger log.Logger) *Discovery {
	// filter out nodes that are running
	running := make([]*discovery.Node, 0, len(nodes))
	for _, node := range nodes {
		// check whether the node is valid and running
		if !node.IsValid() || !node.IsRunning {
			continue
		}
		// only add running and valid node
		running = append(running, node)
	}
	// create the instance of the Discovery and return it
	return &Discovery{
		mu:            sync.Mutex{},
		publicChan:    make(chan discovery.Event, 2),
		stopChan:      make(chan struct{}, 1),
		isInitialized: atomic.NewBool(false),
		logger:        logger,
		nodes:         running,
	}
}

// ID returns the discovery provider id
func (d *Discovery) ID() string {
	return "static"
}

// Start the discovery engine
// nolint
func (d *Discovery) Start(ctx context.Context, meta discovery.Meta) error {
	// add a span context
	_, span := telemetry.SpanContext(ctx, "Start")
	defer span.End()

	// check whether the list of nodes is not empty
	if len(d.nodes) == 0 {
		return errors.New("no nodes are set")
	}

	// set initialized
	d.isInitialized = atomic.NewBool(true)
	// return no error
	return nil
}

// Nodes returns the list of up and running Nodes at a given time
func (d *Discovery) Nodes(ctx context.Context) ([]*discovery.Node, error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "Nodes")
	defer span.End()

	// first check whether the actor system has started
	if !d.isInitialized.Load() {
		return nil, errors.New("static discovery engine not initialized")
	}

	return d.nodes, nil
}

// Watch returns event based upon node lifecycle
func (d *Discovery) Watch(ctx context.Context) (<-chan discovery.Event, error) {
	// add a span context
	_, span := telemetry.SpanContext(ctx, "Watch")
	defer span.End()
	// first check whether the actor system has started
	if !d.isInitialized.Load() {
		return nil, errors.New("static discovery engine not initialized")
	}

	// no events to publish just return the channel
	return d.publicChan, nil
}

// EarliestNode returns the earliest node. This is based upon the node timestamp
func (d *Discovery) EarliestNode(ctx context.Context) (*discovery.Node, error) {
	// fetch the list of Nodes
	nodes, err := d.Nodes(ctx)
	// handle the error
	if err != nil {
		return nil, errors.Wrap(err, "failed to get the earliest node")
	}

	// check whether the list of nodes is not empty
	if len(nodes) == 0 {
		return nil, errors.New("no nodes are found")
	}

	// let us sort the nodes by their timestamp
	sort.SliceStable(nodes, func(i, j int) bool {
		return nodes[i].StartTime < nodes[j].StartTime
	})
	// return the first element in the sorted list
	return nodes[0], nil
}

// Stop shutdown the discovery engine
func (d *Discovery) Stop() error {
	// first check whether the actor system has started
	if !d.isInitialized.Load() {
		return errors.New("static discovery engine not initialized")
	}
	// acquire the lock
	d.mu.Lock()
	// release the lock
	defer d.mu.Unlock()
	// close the public channel
	close(d.publicChan)
	// stop the watchers
	close(d.stopChan)
	return nil
}
