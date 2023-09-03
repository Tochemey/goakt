package cluster

import (
	golog "log"
	"strings"

	"github.com/buraksezer/olric/pkg/service_discovery"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/discovery"
)

// discoveryProvider wraps the Cluster engine discovery and implements
// service_discovery.ServiceDiscovery
type discoveryProvider struct {
	provider discovery.Provider
	log      *golog.Logger
}

// enforce compilation error
var _ service_discovery.ServiceDiscovery = &discoveryProvider{}

// Initialize implementation
func (d *discoveryProvider) Initialize() error {
	// check whether the provider is set or not
	if d.provider == nil {
		return errors.New("discovery provider is not set")
	}
	// call the initialize method of the provider
	if err := d.provider.Initialize(); err != nil {
		if !errors.Is(err, discovery.ErrAlreadyInitialized) {
			return err
		}
	}

	return nil
}

// SetConfig implementation
func (d *discoveryProvider) SetConfig(c map[string]any) error {
	// check whether the id is provided or not
	id, ok := c["id"]
	if !ok {
		return errors.New("discovery provider id is not set")
	}
	// validate the id
	idVal := id.(string)
	if !strings.EqualFold(idVal, d.provider.ID()) {
		return errors.New("invalid discovery provider id")
	}
	// let us extract the options
	options, ok := c["options"]
	if !ok {
		return errors.New("discovery provider options is not set")
	}
	// let us cast the options to disco Meta
	meta := options.(discovery.Config)
	// call the underlying provider
	if err := d.provider.SetConfig(meta); err != nil {
		if !errors.Is(err, discovery.ErrAlreadyInitialized) {
			return err
		}
	}
	return nil
}

// SetLogger implementation
func (d *discoveryProvider) SetLogger(l *golog.Logger) {
	d.log = l
}

// Register implementation
func (d *discoveryProvider) Register() error {
	// check whether the provider is set or not
	if d.provider == nil {
		return errors.New("discovery provider is not set")
	}
	// call the provider register
	if err := d.provider.Register(); err != nil {
		if !errors.Is(err, discovery.ErrAlreadyRegistered) {
			return err
		}
	}

	return nil
}

// Deregister implementation
func (d *discoveryProvider) Deregister() error {
	// check whether the provider is set or not
	if d.provider == nil {
		return errors.New("discovery provider is not set")
	}
	// call the provider de-register
	return d.provider.Deregister()
}

// DiscoverPeers implementation
func (d *discoveryProvider) DiscoverPeers() ([]string, error) {
	// check whether the provider is set or not
	if d.provider == nil {
		return nil, errors.New("discovery provider is not set")
	}

	// call the provider discover peers
	return d.provider.DiscoverPeers()
}

// Close implementation
func (d *discoveryProvider) Close() error {
	return nil
}
