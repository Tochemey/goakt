package cluster

import (
	"net"
	"strconv"

	"github.com/caarlos0/env/v9"
)

// hostNodeConfig helps read the host node settings
type hostNodeConfig struct {
	GossipPort   int    `env:"GOSSIP_PORT"`
	ClusterPort  int    `env:"CLUSTER_PORT"`
	RemotingPort int    `env:"REMOTING_PORT"`
	Name         string `env:"POD_NAME"`
	Host         string `env:"POD_IP"`
}

// node represents a discovered node
type node struct {
	// Name specifies the discovered node's Name
	Name string
	// Host specifies the discovered node's Host
	Host string
	// GossipPort
	GossipPort int
	// ClusterPort
	ClusterPort int
	// RemotingPort
	RemotingPort int
}

// ClusterAddress returns address the node's peers will use to connect to
func (n node) ClusterAddress() string {
	return net.JoinHostPort(n.Host, strconv.Itoa(n.ClusterPort))
}

// GossipAddress returns the node discovery address
func (n node) GossipAddress() string {
	return net.JoinHostPort(n.Host, strconv.Itoa(n.GossipPort))
}

// getHostNode returns the node where the discovery provider is running
func getHostNode() (*node, error) {
	// load the host node configuration
	cfg := &hostNodeConfig{}
	opts := env.Options{RequiredIfNoDef: true, UseFieldNameByDefault: false}
	if err := env.ParseWithOptions(cfg, opts); err != nil {
		return nil, err
	}
	// create the host node
	return &node{
		Name:         cfg.Name,
		Host:         cfg.Host,
		GossipPort:   cfg.GossipPort,
		ClusterPort:  cfg.ClusterPort,
		RemotingPort: cfg.RemotingPort,
	}, nil
}
