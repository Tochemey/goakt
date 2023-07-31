package cluster

import (
	"context"
	golog "log"

	"github.com/tochemey/goakt/log"

	"github.com/buraksezer/olric/pkg/service_discovery"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/discovery"
)

// discoveryProvider implements the olric ServiceDiscovery interface
type discoveryProvider struct {
	disco  discovery.Discovery
	ctx    context.Context
	logger log.Logger
}

// enforce compilation errors
var _ service_discovery.ServiceDiscovery = &discoveryProvider{}

func newDiscoveryProvider(ctx context.Context, disco discovery.Discovery, logger log.Logger) *discoveryProvider {
	return &discoveryProvider{
		disco:  disco,
		ctx:    ctx,
		logger: logger,
	}
}

// Initialize initializes the plugin: registers some internal data structures, clients etc.
func (d *discoveryProvider) Initialize() error {
	return nil
}

// SetConfig registers the underlying discovery configuration
func (d *discoveryProvider) SetConfig(c map[string]any) error {
	return nil
}

func (d *discoveryProvider) SetLogger(l *golog.Logger) {
	// pass
}

// Register registers this node to a service discovery directory.
func (d *discoveryProvider) Register() error {
	return d.disco.Start(d.ctx)
}

// Deregister removes this node from a service discovery directory.
func (d *discoveryProvider) Deregister() error {
	return d.disco.Stop()
}

// DiscoverPeers returns a list of known nodes.
func (d *discoveryProvider) DiscoverPeers() ([]string, error) {
	var peers []string
	nodes, err := d.disco.Nodes(d.ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to discover nodes")
	}
	for _, node := range nodes {
		if node != nil {
			peers = append(peers, node.PeersAddress())
		}
	}

	// add some debug log
	d.logger.Debugf("%d Nodes discovered", len(peers))
	return peers, nil
}

// Close stops underlying goroutines, if there is any. It should be a blocking call.
func (d *discoveryProvider) Close() error {
	return nil
}
