package discovery

import (
	"fmt"

	"github.com/caarlos0/env/v9"
)

const (
	GossipPortName  = "gossip-port"
	ClusterPortName = "cluster-port"
)

// hostNodeConfig helps read the host node settings
type hostNodeConfig struct {
	GossipPort  int    `env:"GOSSIP_PORT"`
	ClusterPort int    `env:"CLUSTER_PORT"`
	Name        string `env:"POD_NAME"`
	Host        string `env:"POD_IP"`
}

// Node represents a discovered node
type Node struct {
	// Name specifies the discovered node's Name
	Name string
	// Host specifies the discovered node's Host
	Host string
	// GossipPort
	GossipPort int
	// ClusterPort
	ClusterPort int
}

// ClusterAddress returns address the node's peers will use to connect to
func (n Node) ClusterAddress() string {
	return fmt.Sprintf("%s:%d", n.Host, n.ClusterPort)
}

// GossipAddress returns the node discovery address
func (n Node) GossipAddress() string {
	return fmt.Sprintf("%s:%d", n.Host, n.GossipPort)
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
		Name:        cfg.Name,
		Host:        cfg.Host,
		GossipPort:  cfg.GossipPort,
		ClusterPort: cfg.ClusterPort,
	}, nil
}
