package mdns

import (
	"context"
	"net"
	"strconv"
	"sync"
	"time"

	goset "github.com/deckarep/golang-set/v2"
	"github.com/grandcat/zeroconf"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/discovery"
	"go.uber.org/atomic"
)

const (
	ServiceName = "name"
	Service     = "service"
	Domain      = "domain"
	Port        = "port"
	IPv6        = "ipv6"
)

// option represents the mDNS provider option
type option struct {
	// Provider specifies the provider name
	Provider string
	// Service specifies the service name
	ServiceName string
	// Service specifies the service type
	Service string
	// Specifies the service domain
	Domain string
	// Port specifies the port the service is listening to
	Port int
	// IPv6 states whether to fetch ipv6 address instead of ipv4
	IPv6 *bool
}

// Discovery defines the mDNS discovery provider
type Discovery struct {
	option *option
	mu     sync.Mutex

	stopChan chan struct{}
	// states whether the actor system has started or not
	isInitialized *atomic.Bool

	// resolver is used to browse for service discovery
	resolver *zeroconf.Resolver

	server *zeroconf.Server
}

// enforce compilation error
var _ discovery.Provider = &Discovery{}

// NewDiscovery returns an instance of the mDNS discovery provider
func NewDiscovery() *Discovery {
	// create an instance of
	d := &Discovery{
		mu:            sync.Mutex{},
		stopChan:      make(chan struct{}, 1),
		isInitialized: atomic.NewBool(false),
		option:        &option{},
	}

	return d
}

// ID returns the discovery provider identifier
func (d *Discovery) ID() string {
	return "mdns"
}

// Initialize the discovery provider
func (d *Discovery) Initialize() error {
	// acquire the lock
	d.mu.Lock()
	// release the lock
	defer d.mu.Unlock()
	// first check whether the discovery provider is running
	if d.isInitialized.Load() {
		return discovery.ErrAlreadyInitialized
	}

	// check the options
	if d.option.Provider == "" {
		d.option.Provider = d.ID()
	}

	return nil
}

// Register registers this node to a service discovery directory.
func (d *Discovery) Register() error {
	// acquire the lock
	d.mu.Lock()
	// release the lock
	defer d.mu.Unlock()

	// first check whether the discovery provider has started
	// avoid to re-register the discovery
	if d.isInitialized.Load() {
		return discovery.ErrAlreadyRegistered
	}

	// initialize the resolver
	res, err := zeroconf.NewResolver(nil)
	// handle the error
	if err != nil {
		return errors.Wrap(err, "failed to instantiate the mDNS discovery provider")
	}
	// set the resolver
	d.resolver = res

	// register the service
	srv, err := zeroconf.Register(d.option.ServiceName, d.option.Service, d.option.Domain, d.option.Port, []string{"txtv=0", "lo=1", "la=2"}, nil)
	// handle the error
	if err != nil {
		return err
	}

	// set the server
	d.server = srv

	// set initialized
	d.isInitialized = atomic.NewBool(true)
	return nil
}

// Deregister removes this node from a service discovery directory.
func (d *Discovery) Deregister() error {
	// acquire the lock
	d.mu.Lock()
	// release the lock
	defer d.mu.Unlock()

	// first check whether the discovery provider has started
	if !d.isInitialized.Load() {
		return discovery.ErrNotInitialized
	}

	// set the initialized to false
	d.isInitialized = atomic.NewBool(false)

	// shutdown the registered service
	if d.server != nil {
		d.server.Shutdown()
	}

	// stop the watchers
	close(d.stopChan)
	// return
	return nil
}

// SetConfig registers the underlying discovery configuration
func (d *Discovery) SetConfig(config discovery.Config) error {
	// acquire the lock
	d.mu.Lock()
	// release the lock
	defer d.mu.Unlock()

	// first check whether the discovery provider is running
	if d.isInitialized.Load() {
		return discovery.ErrAlreadyInitialized
	}

	var err error
	// validate the meta
	// let us make sure we have the required options set

	// assert the presence of service instance
	if _, ok := config[ServiceName]; !ok {
		return errors.New("mDNS service name is not provided")
	}

	// assert the presence of the service type
	if _, ok := config[Service]; !ok {
		return errors.New("mDNS service type is not provided")
	}

	// assert the presence of the listening port
	if _, ok := config[Port]; !ok {
		return errors.New("mDNS listening port is not provided")
	}

	// assert the service domain
	if _, ok := config[Domain]; !ok {
		return errors.New("mDNS domain is not provided")
	}

	// assert the ipv6 domain
	if _, ok := config[IPv6]; !ok {
		return errors.New("mDNS ipv6 option is not provided")
	}

	// set the options
	if err = d.setOptions(config); err != nil {
		return errors.Wrap(err, "failed to instantiate the mDNS discovery provider")
	}
	return nil
}

// DiscoverPeers returns a list of known nodes.
func (d *Discovery) DiscoverPeers() ([]string, error) {
	// first check whether the discovery provider is running
	if !d.isInitialized.Load() {
		return nil, discovery.ErrNotInitialized
	}
	// init entries channel
	entries := make(chan *zeroconf.ServiceEntry, 100)

	// create a context to browse the services for 5 seconds
	// TODO: make the timeout configurable
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// let us browse the services
	if err := d.resolver.Browse(ctx, d.option.Service, d.option.Domain, entries); err != nil {
		return nil, err
	}
	<-ctx.Done()

	// set ipv6 filter
	v6 := false
	if d.option.IPv6 != nil {
		v6 = *d.option.IPv6
	}

	// define the addresses list
	addresses := goset.NewSet[string]()
	for entry := range entries {
		// lookup for v6 address
		if v6 {
			// iterate the list of ports
			for _, addr := range entry.AddrIPv6 {
				addresses.Add(net.JoinHostPort(addr.String(), strconv.Itoa(entry.Port)))
			}
		}

		// iterate the list of ports
		for _, addr := range entry.AddrIPv4 {
			addresses.Add(net.JoinHostPort(addr.String(), strconv.Itoa(entry.Port)))
		}
	}
	return addresses.ToSlice(), nil
}

// setOptions sets the kubernetes option
func (d *Discovery) setOptions(config discovery.Config) (err error) {
	// create an instance of Option
	option := new(option)
	// extract the service name
	option.ServiceName, err = config.GetString(ServiceName)
	// handle the error in case the service instance value is not properly set
	if err != nil {
		return err
	}
	// extract the service name
	option.Service, err = config.GetString(Service)
	// handle the error in case the service type value is not properly set
	if err != nil {
		return err
	}
	// extract the service domain
	option.Domain, err = config.GetString(Domain)
	// handle the error when the domain is not properly set
	if err != nil {
		return err
	}
	// extract the port the service is listening to
	option.Port, err = config.GetInt(Port)
	// handle the error when the port is not properly set
	if err != nil {
		return err
	}

	// extract the type of ip address to lookup
	option.IPv6, err = config.GetBool(IPv6)
	// handle the error
	if err != nil {
		return err
	}

	// in case none of the above extraction fails then set the option
	d.option = option
	return nil
}
