package mdns

import (
	"context"
	"sync"

	"github.com/grandcat/zeroconf"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/discovery"
	"github.com/tochemey/goakt/log"
	"go.uber.org/atomic"
)

// Discovery represents the mDNS discovery provider
type Discovery struct {
	option *Option
	mu     sync.Mutex

	stopChan   chan struct{}
	publicChan chan discovery.Event
	// states whether the actor system has started or not
	isInitialized *atomic.Bool
	logger        log.Logger
	resolver      *zeroconf.Resolver
}

// enforce compilation error
var _ discovery.Discovery = &Discovery{}

// New returns an instance of the kubernetes discovery provider
func New(logger log.Logger) *Discovery {
	// create an instance of
	d := &Discovery{
		mu:            sync.Mutex{},
		publicChan:    make(chan discovery.Event, 2),
		stopChan:      make(chan struct{}, 1),
		isInitialized: atomic.NewBool(false),
		logger:        logger,
	}
	return d
}

// ID returns the discovery id
func (d *Discovery) ID() string {
	return "mdns"
}

// Start the discovery engine
func (d *Discovery) Start(ctx context.Context, meta discovery.Meta) error {
	var err error
	// validate the meta
	// let us make sure we have the required options set
	// assert the present of service instance
	if _, ok := meta[Service]; !ok {
		return errors.New("mDNS service_instance is not provided")
	}
	// assert the service meta
	if _, ok := meta[Domain]; !ok {
		return errors.New("mDNS domain is not provided")
	}
	// set the options
	if err = d.setOptions(meta); err != nil {
		return errors.Wrap(err, "failed to instantiate the mDNS discovery provider")
	}
	// initialize the resolver
	res, err := zeroconf.NewResolver(nil)
	// handle the error
	if err != nil {
		return errors.Wrap(err, "failed to instantiate the mDNS discovery provider")
	}
	d.resolver = res
	// set initialized
	d.isInitialized = atomic.NewBool(true)
	return nil
}

// Nodes returns the list of Nodes at a given time
func (d *Discovery) Nodes(ctx context.Context) ([]*discovery.Node, error) {
	// first check whether the actor system has started
	if !d.isInitialized.Load() {
		return nil, errors.New("mDNS discovery engine not initialized")
	}
	// init entries channel
	entries := make(chan *zeroconf.ServiceEntry)
	var nodes []*discovery.Node
	go func(results <-chan *zeroconf.ServiceEntry) {
		for entry := range results {
			nodes = append(nodes, &discovery.Node{
				Name: entry.ServiceInstanceName(),
				Host: entry.AddrIPv4[0].String(),
				//JoinPort: int32(entry.Port),
			})
		}
	}(entries)
	return nodes, d.resolver.Browse(ctx, d.option.Service, d.option.Domain, entries)
}

// Watch returns event based upon node lifecycle
func (d *Discovery) Watch(ctx context.Context) (<-chan discovery.Event, error) {
	// first check whether the actor system has started
	if !d.isInitialized.Load() {
		return nil, errors.New("mDNS discovery engine not initialized")
	}
	// run the watcher
	go d.watchPods(ctx)
	return d.publicChan, nil
}

// EarliestNode returns the earliest node. This is based upon the node timestamp
func (d *Discovery) EarliestNode(ctx context.Context) (*discovery.Node, error) {
	return nil, errors.New("not yet implemented")
}

// Stop shutdown the discovery engine
func (d *Discovery) Stop() error {
	// first check whether the actor system has started
	if !d.isInitialized.Load() {
		return errors.New("mDNS discovery engine not initialized")
	}
	// stop the watchers
	close(d.stopChan)
	// close the public channel
	close(d.publicChan)
	return nil
}

// setOptions sets the kubernetes option
func (d *Discovery) setOptions(meta discovery.Meta) (err error) {
	// create an instance of Option
	option := new(Option)
	// extract the service instance
	option.Service, err = meta.GetString(Service)
	// handle the error in case the service instance value is not properly set
	if err != nil {
		return err
	}
	// extract the service domain
	option.Domain, err = meta.GetString(Domain)
	// handle the error when the domain is not properly set
	if err != nil {
		return err
	}

	// in case none of the above extraction fails then set the option
	d.option = option
	return nil
}

// watchPods keeps a watch on mDNS pods activities and emit
// respective event when needed
func (d *Discovery) watchPods(ctx context.Context) {
	// create an array of zeroconf service entries
	entries := make(chan *zeroconf.ServiceEntry, 5)
	// create a go routine that will push discovery event to the discovery channel
	go func() {
		for {
			select {
			case <-d.stopChan:
				return
			case entry := <-entries:
				// create a discovery node
				node := &discovery.Node{
					Name: entry.ServiceInstanceName(),
					Host: entry.AddrIPv4[0].String(),
					//JoinPort: int32(entry.Port),
				}
				event := &discovery.NodeAdded{Node: node}
				// add to the channel
				d.publicChan <- event
			}
		}
	}()

	// wrap the context in a cancellation context
	ctx, cancel := context.WithCancel(ctx)
	for {
		select {
		case <-d.stopChan:
			cancel()
			return
		default:
			// browse for all services of a given type in a given domain.
			if err := d.resolver.Browse(ctx, d.option.Service, d.option.Domain, entries); err != nil {
				// log the error
				d.logger.Error(errors.Wrap(err, "failed to lookup"))
			}
		}
	}
}
