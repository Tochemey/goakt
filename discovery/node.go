package discovery

import (
	"fmt"

	"github.com/caarlos0/env/v9"
)

const (
	PortName      = "discovery-port"
	PeersPortName = "peers-port"
)

// hostNodeConfig helps read the host node settings
type hostNodeConfig struct {
	DiscoveryPort int    `env:"DISCOVERY_PORT"`
	PeersPort     int    `env:"PEERS_PORT"`
	Name          string `env:"POD_NAME"`
}

// Node represents a discovered node
type Node struct {
	// Name specifies the discovered node's Name
	Name string
	// Host specifies the discovered node's Host
	Host string
	// Ports specifies the list of Ports
	Ports map[string]int32
}

// PeersAddress returns address the node's peers will use to connect to
func (n Node) PeersAddress() string {
	return fmt.Sprintf("%s:%d", n.Host, n.Ports[PeersPortName])
}

// DiscoveryAddress returns the node discovery address
func (n Node) DiscoveryAddress() string {
	return fmt.Sprintf("%s:%d", n.Host, n.Ports[PortName])
}

// PeersPort returns the node peer port
func (n Node) PeersPort() int32 {
	return n.Ports[PeersPortName]
}

// DiscoveryPort returns the node discovery port
// This port is used by the discovery engine
func (n Node) DiscoveryPort() int32 {
	return n.Ports[PortName]
}

// GetHostNode returns the node where the discovery provider is running
func GetHostNode() (*Node, error) {
	// load the host node configuration
	cfg := &hostNodeConfig{}
	opts := env.Options{RequiredIfNoDef: true, UseFieldNameByDefault: false}
	if err := env.ParseWithOptions(cfg, opts); err != nil {
		return nil, err
	}
	// create the host node
	return &Node{
		Name: cfg.Name,
		Ports: map[string]int32{
			PeersPortName: int32(cfg.PeersPort),
			PortName:      int32(cfg.DiscoveryPort),
		},
	}, nil
}
