package discovery

import (
	"context"
	"errors"
	"sync"

	goaktpb "github.com/tochemey/goakt/internal/goaktpb/v1"
	"github.com/tochemey/goakt/log"
	"go.uber.org/atomic"
)

const (
	// MDNSDomain specifies the mdns domain meta key. The default value is local
	MDNSDomain = "domain"
	// MDNSService specifies the mdns service key
	MDNSService = "service"
)

// mDNSOption represents the mdns
type mDNSOption struct {
	// Domain specifies the meta domain key value
	// The default value is local
	Domain string
	// Service specifies the meta service key value
	Service string
}

// MDNS represents the mDNS discovery method
type MDNS struct {
	mu         sync.Mutex
	option     *mDNSOption
	stopChan   chan struct{}
	publicChan chan *goaktpb.Event
	// states whether the actor system has started or not
	isInitialized *atomic.Bool
	logger        log.Logger
}

// enforce compilation error when struct does not fully implement the given Discovery interface
var _ Discovery = &MDNS{}

// NewMDNS creates an instance of MDNS
func NewMDNS() *MDNS {
	return &MDNS{
		mu:            sync.Mutex{},
		publicChan:    make(chan *goaktpb.Event, 2),
		stopChan:      make(chan struct{}, 1),
		isInitialized: atomic.NewBool(false),
		logger:        log.DefaultLogger,
	}
}

// Start the mDNS discovery engine
func (d *MDNS) Start(ctx context.Context, meta Meta) error {
	//TODO implement me
	panic("implement me")
}

// Nodes returns the list of Nodes at a given time
func (d *MDNS) Nodes(ctx context.Context) ([]*goaktpb.Node, error) {
	//TODO implement me
	panic("implement me")
}

// Watch returns event based upon nodes lifecycle
func (d *MDNS) Watch(ctx context.Context) (chan *goaktpb.Event, error) {
	//TODO implement me
	panic("implement me")
}

// EarliestNode returns the earliest node
func (d *MDNS) EarliestNode(ctx context.Context) (*goaktpb.Node, error) {
	//TODO implement me
	panic("implement me")
}

// Stop shutdown the mDNS discovery provider
func (d *MDNS) Stop() error {
	// first check whether the actor system has started
	if !d.isInitialized.Load() {
		return errors.New("mDNS discovery engine not initialized")
	}
	// stop the watchers
	close(d.stopChan)
	// close the public channel
	close(d.publicChan)
	// set the initialized to false
	d.isInitialized = atomic.NewBool(false)
	return nil
}

// setOptions sets the kubernetes option
func (d *MDNS) setOptions(meta Meta) (err error) {
	// create an instance of Option
	option := new(mDNSOption)
	// extract the domain
	option.Domain, err = meta.GetString(MDNSDomain)
	// handle the error in case the namespace value is not properly set
	if err != nil {
		// set the default value to local when key not found
		if errors.Is(err, ErrMetaKeyNotFound(MDNSDomain)) {
			// set the default value
			option.Domain = "local"
		} else {
			return err
		}
	}
	// extract the label selector
	option.Service, err = meta.GetString(MDNSService)
	// handle the error in case the label selector value is not properly set
	if err != nil {
		return err
	}
	// in case none of the above extraction fails then set the option
	d.option = option
	return nil
}
