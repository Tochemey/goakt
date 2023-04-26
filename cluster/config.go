package cluster

import (
	"fmt"

	"github.com/tochemey/goakt/discovery"
)

// NodeConfig define the cluster node config
type NodeConfig struct {
	NodeID   uint64
	NodeHost string
	NodePort int
}

// GetURL returns the config address
func (c NodeConfig) GetURL() string {
	return fmt.Sprintf("https://%s:%d", c.NodeHost, c.NodePort)
}

// Config is the cluster configuration
type Config struct {
	NodeConfig *NodeConfig
	Disco      discovery.Discovery
}
